package session

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/varavelio/rienda/internal/id"
	"github.com/varavelio/rienda/internal/llm"
)

// stubGenerator returns scripted identifiers in order, falling back to a
// counter so stores always receive fresh identifiers.
type stubGenerator struct {
	ids  []string
	next int
}

// NewID returns the next identifier.
func (g *stubGenerator) NewID(_ context.Context) string {
	if len(g.ids) > 0 {
		id := g.ids[0]
		g.ids = g.ids[1:]
		return id
	}
	g.next++
	return "generated-" + strconv.Itoa(g.next)
}

// textEntry builds a message entry carrying a single text block.
func textEntry(role llm.Role, text string) Entry {
	return Entry{
		Message: llm.Message{
			Role:   role,
			Blocks: []llm.Block{{Type: llm.BlockText, Text: text}},
		},
	}
}

// newTestStore creates a session in a temporary directory.
func newTestStore(t *testing.T) *Store {
	t.Helper()

	store, err := Create(t.Context(), t.TempDir(), Header{
		Agent:   "coder",
		Model:   "test/model",
		Workdir: t.TempDir(),
	}, &stubGenerator{})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, store.Close()) })
	return store
}

// appendMessage appends a text message to a store.
func appendMessage(t *testing.T, store *Store, role llm.Role, text string) Entry {
	t.Helper()

	entry, err := store.Append(t.Context(), textEntry(role, text))
	require.NoError(t, err)
	return entry
}

// stripMonotonic removes monotonic clock readings so entries loaded from disk
// compare equal to freshly appended ones.
func stripMonotonic(entries []Entry) []Entry {
	for i := range entries {
		entries[i].CreatedAt = entries[i].CreatedAt.Round(0)
	}
	return entries
}

// TestCreate verifies session creation.
func TestCreate(t *testing.T) {
	t.Run("creates a session file", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "nested", "sessions")

		store, err := Create(
			t.Context(),
			dir,
			Header{Agent: " coder ", Model: " test/model "},
			&stubGenerator{},
		)
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, store.Close()) })

		require.NotEmpty(t, store.ID())
		require.Empty(t, store.Leaf())
		require.Empty(t, store.Entries())
		require.Nil(t, store.History())

		info := store.Info()
		require.Equal(t, "coder", info.Agent)
		require.Equal(t, "test/model", info.Model)
		require.Equal(t, info.CreatedAt, info.UpdatedAt)
		require.Empty(t, info.Title)

		name := store.ID() + Extension
		data, err := os.ReadFile(filepath.Join(dir, name)) //nolint:gosec // test path.
		require.NoError(t, err)
		require.Contains(t, string(data), `"kind":"header"`)
		require.Contains(t, string(data), `"version":1`)
	})

	t.Run("uses the shared identifier generator", func(t *testing.T) {
		dir := t.TempDir()

		store, err := Create(
			t.Context(),
			dir,
			Header{Agent: "coder", Model: "test/model"},
			id.NewIDGenerator(),
		)
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, store.Close()) })

		require.NotEmpty(t, store.ID())
		_, err = os.Stat(filepath.Join(dir, store.ID()+Extension))
		require.NoError(t, err)

		entry := appendMessage(t, store, llm.RoleUser, "hello")
		require.NotEmpty(t, entry.ID)

		reloaded, err := Open(dir, store.ID(), id.NewIDGenerator())
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, reloaded.Close()) })
		require.Equal(t, entry.ID, reloaded.Leaf())
	})

	t.Run("rejects invalid generated session ids", func(t *testing.T) {
		for _, generated := range []string{"", "bad/id", ".hidden", "sessions" + Extension} {
			_, err := Create(
				t.Context(),
				t.TempDir(),
				Header{Agent: "a", Model: "m"},
				&stubGenerator{ids: []string{generated}},
			)

			require.ErrorContains(t, err, "invalid session id", "generated %q", generated)
		}
	})

	t.Run("rejects a missing generator", func(t *testing.T) {
		_, err := Create(t.Context(), t.TempDir(), Header{Agent: "a", Model: "m"}, nil)

		require.ErrorContains(t, err, "id generator is required")
	})

	t.Run("reports directory creation failures", func(t *testing.T) {
		file := filepath.Join(t.TempDir(), "file")
		require.NoError(t, os.WriteFile(file, []byte("data"), 0o600))

		_, err := Create(
			t.Context(),
			filepath.Join(file, "sessions"),
			Header{Agent: "a", Model: "m"},
			&stubGenerator{},
		)

		require.ErrorContains(t, err, "create directory")
	})

	t.Run("rejects invalid headers", func(t *testing.T) {
		tests := []struct {
			name    string
			header  Header
			wantErr string
		}{
			{name: "missing agent", header: Header{Model: "m"}, wantErr: "agent is required"},
			{name: "missing model", header: Header{Agent: "a"}, wantErr: "model is required"},
			{
				name:    "relative workdir",
				header:  Header{Agent: "a", Model: "m", Workdir: "relative"},
				wantErr: "workdir must be an absolute path",
			},
		}

		for _, test := range tests {
			t.Run(test.name, func(t *testing.T) {
				_, err := Create(t.Context(), t.TempDir(), test.header, &stubGenerator{})

				require.ErrorContains(t, err, test.wantErr)
			})
		}
	})
}

// TestAppend verifies message persistence.
func TestAppend(t *testing.T) {
	t.Run("fills the generated fields and links the chain", func(t *testing.T) {
		store := newTestStore(t)

		first := appendMessage(t, store, llm.RoleUser, "hello")
		require.NotEmpty(t, first.ID)
		require.Empty(t, first.ParentID)
		require.Equal(t, KindMessage, first.Kind)
		require.False(t, first.CreatedAt.IsZero())
		require.Equal(t, first.ID, store.Leaf())

		second := appendMessage(t, store, llm.RoleAssistant, "hi")
		require.Equal(t, first.ID, second.ParentID)
		require.Equal(t, second.ID, store.Leaf())
		require.Len(t, store.Entries(), 2)
	})

	t.Run("derives the title from the first user message", func(t *testing.T) {
		store := newTestStore(t)

		appendMessage(t, store, llm.RoleAssistant, "ready when you are")
		require.Empty(t, store.Info().Title)

		appendMessage(t, store, llm.RoleUser, "  Fix\n\n the   bug \n")
		require.Equal(t, "Fix the bug", store.Info().Title)

		appendMessage(t, store, llm.RoleUser, "another message")
		require.Equal(t, "Fix the bug", store.Info().Title)
	})

	t.Run("ignores non-text blocks when deriving the title", func(t *testing.T) {
		store := newTestStore(t)

		_, err := store.Append(t.Context(), Entry{Message: llm.Message{
			Role: llm.RoleUser,
			Blocks: []llm.Block{
				{Type: llm.BlockThinking, Thinking: "hidden"},
				{Type: llm.BlockText, Text: " \n "},
				{Type: llm.BlockText, Text: "real title"},
			},
		}})
		require.NoError(t, err)

		require.Equal(t, "real title", store.Info().Title)
	})

	t.Run("truncates long titles", func(t *testing.T) {
		store := newTestStore(t)

		appendMessage(t, store, llm.RoleUser, strings.Repeat("word ", 30))

		title := store.Info().Title
		require.True(t, strings.HasSuffix(title, "…"))
		require.LessOrEqual(t, len([]rune(title)), maxTitleRunes+1)
	})

	t.Run("updates the update time", func(t *testing.T) {
		store := newTestStore(t)
		created := store.Info().UpdatedAt

		time.Sleep(2 * time.Millisecond)
		entry := appendMessage(t, store, llm.RoleUser, "hello")

		require.Equal(t, entry.CreatedAt, store.Info().UpdatedAt)
		require.True(t, store.Info().UpdatedAt.After(created))
	})

	t.Run("supports explicit parents", func(t *testing.T) {
		store := newTestStore(t)
		root := appendMessage(t, store, llm.RoleUser, "root")
		branch := appendMessage(t, store, llm.RoleAssistant, "branch")

		entry, err := store.Append(t.Context(), Entry{
			ParentID: root.ID,
			Message: llm.Message{
				Role:   llm.RoleAssistant,
				Blocks: []llm.Block{{Type: llm.BlockText, Text: "other branch"}},
			},
		})
		require.NoError(t, err)

		require.Equal(t, root.ID, entry.ParentID)
		require.Equal(t, entry.ID, store.Leaf())

		path, err := store.Path(entry.ID)
		require.NoError(t, err)
		require.Len(t, path, 2)
		require.Equal(t, root.ID, path[0].ID)
		require.Equal(t, entry.ID, path[1].ID)

		require.Equal(t, []llm.Message{root.Message, entry.Message}, store.History())
		require.NotEqual(t, branch.ID, store.Leaf())
	})

	t.Run("rejects invalid entries", func(t *testing.T) {
		store := newTestStore(t)

		tests := []struct {
			name    string
			entry   Entry
			wantErr string
		}{
			{
				name:    "invalid role",
				entry:   textEntry(llm.Role("system"), "hi"),
				wantErr: "invalid message role",
			},
			{
				name:    "no blocks",
				entry:   Entry{Message: llm.Message{Role: llm.RoleUser}},
				wantErr: "must not be empty",
			},
			{
				name: "unknown parent",
				entry: Entry{
					ParentID: "nope",
					Message:  textEntry(llm.RoleUser, "hi").Message,
				},
				wantErr: "unknown parent entry",
			},
			{
				name: "unsupported kind",
				entry: Entry{
					Kind:    KindHeader,
					Message: textEntry(llm.RoleUser, "hi").Message,
				},
				wantErr: "cannot append an entry of kind",
			},
		}

		for _, test := range tests {
			t.Run(test.name, func(t *testing.T) {
				_, err := store.Append(t.Context(), test.entry)

				require.ErrorContains(t, err, test.wantErr)
			})
		}
	})

	t.Run("rejects invalid generated entry ids", func(t *testing.T) {
		header := Header{Agent: "coder", Model: "test/model"}

		t.Run("empty id", func(t *testing.T) {
			store, err := Create(
				t.Context(),
				t.TempDir(),
				header,
				&stubGenerator{ids: []string{"session-1", ""}},
			)
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, store.Close()) })

			_, err = store.Append(t.Context(), textEntry(llm.RoleUser, "hi"))

			require.ErrorContains(t, err, "empty entry id")
		})

		t.Run("duplicate id", func(t *testing.T) {
			store, err := Create(
				t.Context(),
				t.TempDir(),
				header,
				&stubGenerator{ids: []string{"session-1", "entry-1", "entry-1"}},
			)
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, store.Close()) })

			appendMessage(t, store, llm.RoleUser, "hi")
			_, err = store.Append(t.Context(), textEntry(llm.RoleAssistant, "again"))

			require.ErrorContains(t, err, "duplicate entry id")
		})
	})
}

// TestPath verifies branch reconstruction.
func TestPath(t *testing.T) {
	store := newTestStore(t)

	first := appendMessage(t, store, llm.RoleUser, "one")
	second := appendMessage(t, store, llm.RoleAssistant, "two")
	third := appendMessage(t, store, llm.RoleUser, "three")

	t.Run("returns the path to an entry", func(t *testing.T) {
		path, err := store.Path(second.ID)
		require.NoError(t, err)

		require.Len(t, path, 2)
		require.Equal(t, first.ID, path[0].ID)
		require.Equal(t, second.ID, path[1].ID)
	})

	t.Run("reports unknown entries", func(t *testing.T) {
		_, err := store.Path("nope")

		require.ErrorContains(t, err, `unknown entry "nope"`)
	})

	t.Run("follows the leaf history", func(t *testing.T) {
		require.Equal(t, third.ID, store.Leaf())
		require.Equal(
			t,
			[]llm.Message{first.Message, second.Message, third.Message},
			store.History(),
		)
	})
}

// TestOpen verifies session reopening.
func TestOpen(t *testing.T) {
	t.Run("round trips a stored session", func(t *testing.T) {
		dir := t.TempDir()
		store, err := Create(t.Context(), dir, Header{
			Agent:   "coder",
			Model:   "test/model",
			Workdir: "/workspace",
		}, &stubGenerator{})
		require.NoError(t, err)

		appendMessage(t, store, llm.RoleUser, "hello")
		assistant, err := store.Append(t.Context(), Entry{
			ResponseModel:      "kimi-k2",
			ResponseStopReason: llm.StopReasonToolUse,
			ResponseUsage:      llm.Usage{InputTokens: 10, OutputTokens: 5},
			Message: llm.Message{
				Role:   llm.RoleAssistant,
				ItemID: "msg_1",
				Blocks: []llm.Block{
					{
						Type: llm.BlockThinking, Thinking: "plan",
						ThinkingSignature: "sig", ThinkingID: "rs_1",
					},
					{Type: llm.BlockText, Text: "on it"},
				},
			},
		})
		require.NoError(t, err)
		id := store.ID()
		require.NoError(t, store.Close())

		reloaded, err := Open(dir, id, &stubGenerator{})
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, reloaded.Close()) })

		require.Equal(t, id, reloaded.ID())
		require.Equal(t, store.Info().Agent, reloaded.Info().Agent)
		require.Equal(t, store.Info().Model, reloaded.Info().Model)
		require.Equal(t, store.Info().Workdir, reloaded.Info().Workdir)
		require.Equal(t, store.Info().Title, reloaded.Info().Title)
		require.True(t, store.Info().CreatedAt.Equal(reloaded.Info().CreatedAt))
		require.True(t, store.Info().UpdatedAt.Equal(reloaded.Info().UpdatedAt))
		require.Equal(t, assistant.ID, reloaded.Leaf())
		require.Equal(t, stripMonotonic(store.Entries()), stripMonotonic(reloaded.Entries()))
		require.Equal(t, store.History(), reloaded.History())

		continued := appendMessage(t, reloaded, llm.RoleUser, "more")
		require.Equal(t, assistant.ID, continued.ParentID)
	})

	t.Run("rejects invalid identifiers", func(t *testing.T) {
		for _, id := range []string{"", ".", "..", ".hidden", "with/slash", "with\\slash", "file.jsonl"} {
			_, err := Open(t.TempDir(), id, &stubGenerator{})

			require.ErrorContains(t, err, "invalid session id", "id %q", id)
		}
	})

	t.Run("rejects a missing generator", func(t *testing.T) {
		_, err := Open(t.TempDir(), "s1", nil)

		require.ErrorContains(t, err, "id generator is required")
	})

	t.Run("reports missing sessions", func(t *testing.T) {
		_, err := Open(t.TempDir(), "20260916T101530Z-ab12cd", &stubGenerator{})

		require.ErrorContains(t, err, "session: open")
	})

	t.Run("honors a stored leaf marker", func(t *testing.T) {
		dir := t.TempDir()
		content := validHeaderLine + "\n" + userMessageLine + "\n" +
			assistantMessageLine + "\n" +
			`{"kind":"leaf","id":"l1","parentId":"m1","createdAt":"2026-09-16T10:15:33Z"}` + "\n"
		require.NoError(t, os.WriteFile(filepath.Join(dir, "s1"+Extension), []byte(content), 0o600))

		store, err := Open(dir, "s1", &stubGenerator{})
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, store.Close()) })

		require.Equal(t, "m1", store.Leaf())
		require.Len(t, store.History(), 1)
	})
}

// TestList verifies session listing.
func TestList(t *testing.T) {
	t.Run("returns nothing for a missing directory", func(t *testing.T) {
		infos, err := List(filepath.Join(t.TempDir(), "missing"))

		require.NoError(t, err)
		require.Nil(t, infos)
	})

	t.Run("lists sessions most recently updated first", func(t *testing.T) {
		dir := t.TempDir()
		generator := &stubGenerator{}

		first, err := Create(
			t.Context(),
			dir,
			Header{Agent: "coder", Model: "test/model"},
			generator,
		)
		require.NoError(t, err)
		appendMessage(t, first, llm.RoleUser, "first session")
		require.NoError(t, first.Close())

		time.Sleep(2 * time.Millisecond)

		second, err := Create(
			t.Context(),
			dir,
			Header{Agent: "coder", Model: "test/model"},
			generator,
		)
		require.NoError(t, err)
		appendMessage(t, second, llm.RoleUser, "second session")
		require.NoError(t, second.Close())

		infos, err := List(dir)
		require.NoError(t, err)
		require.Len(t, infos, 2)
		require.Equal(t, second.ID(), infos[0].ID)
		require.Equal(t, "second session", infos[0].Title)
		require.Equal(t, first.ID(), infos[1].ID)
		require.Equal(t, "first session", infos[1].Title)
	})

	t.Run("ignores unrelated files", func(t *testing.T) {
		dir := t.TempDir()
		store, err := Create(
			t.Context(),
			dir,
			Header{Agent: "coder", Model: "test/model"},
			&stubGenerator{},
		)
		require.NoError(t, err)
		require.NoError(t, store.Close())

		require.NoError(t, os.WriteFile(filepath.Join(dir, ".hidden.jsonl"), []byte("{}\n"), 0o600))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("hi"), 0o600))
		require.NoError(t, os.Mkdir(filepath.Join(dir, "folder"+Extension), 0o750))

		infos, err := List(dir)
		require.NoError(t, err)
		require.Len(t, infos, 1)
		require.Equal(t, store.ID(), infos[0].ID)
		require.Empty(t, infos[0].Title)
		require.Equal(t, infos[0].CreatedAt, infos[0].UpdatedAt)
	})

	t.Run("collects corrupt sessions without hiding the rest", func(t *testing.T) {
		dir := t.TempDir()
		store, err := Create(
			t.Context(),
			dir,
			Header{Agent: "coder", Model: "test/model"},
			&stubGenerator{},
		)
		require.NoError(t, err)
		require.NoError(t, store.Close())

		require.NoError(
			t,
			os.WriteFile(filepath.Join(dir, "broken"+Extension), []byte("not json\n"), 0o600),
		)

		infos, err := List(dir)

		require.ErrorContains(t, err, "broken"+Extension)
		require.Len(t, infos, 1)
		require.Equal(t, store.ID(), infos[0].ID)
	})
}

// TestClose verifies store closing.
func TestClose(t *testing.T) {
	store := newTestStore(t)

	t.Run("is idempotent", func(t *testing.T) {
		require.NoError(t, store.Close())
		require.NoError(t, store.Close())
	})

	t.Run("rejects appends after closing", func(t *testing.T) {
		_, err := store.Append(t.Context(), textEntry(llm.RoleUser, "hi"))

		require.ErrorContains(t, err, "store is closed")
	})
}

// TestDefaultDir verifies the default sessions directory.
func TestDefaultDir(t *testing.T) {
	dir, err := DefaultDir()
	require.NoError(t, err)

	require.True(t, strings.HasSuffix(filepath.ToSlash(dir), "/.rienda/sessions"))
}

// TestTruncateTitle verifies title truncation.
func TestTruncateTitle(t *testing.T) {
	t.Run("keeps short texts untouched", func(t *testing.T) {
		require.Equal(t, "hello", truncateTitle("hello", 10))
	})

	t.Run("cuts long texts with an ellipsis", func(t *testing.T) {
		require.Equal(t, "hello…", truncateTitle("hello world", 5))
	})
}
