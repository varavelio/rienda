package session

import (
	"context"
	"os"
	"path/filepath"
	"slices"
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

// readLines returns the lines a store wrote into its file, which lets a test
// assert that a change wrote nothing.
func readLines(t *testing.T, store *Store) []string {
	t.Helper()

	data, err := os.ReadFile(store.path)
	require.NoError(t, err)
	return strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
}

// messagesOf returns the messages of a chain of entries.
func messagesOf(entries []Entry) []llm.Message {
	messages := make([]llm.Message, 0, len(entries))
	for _, entry := range entries {
		messages = append(messages, entry.Message)
	}
	return messages
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

// TestBranch verifies the entries of the active branch: the whole chain of
// messages in conversation order, and its projection into messages.
func TestBranch(t *testing.T) {
	t.Run("returns nothing for a session without messages", func(t *testing.T) {
		store := newTestStore(t)

		require.Empty(t, store.Branch())
		require.Nil(t, store.History())
	})

	t.Run("follows the active branch in conversation order", func(t *testing.T) {
		store := newTestStore(t)
		first := appendMessage(t, store, llm.RoleUser, "one")
		second := appendMessage(t, store, llm.RoleAssistant, "two")

		branch := store.Branch()

		require.Len(t, branch, 2)
		require.Equal(t, first.ID, branch[0].ID)
		require.Equal(t, second.ID, branch[1].ID)
		require.Equal(t, []llm.Message{first.Message, second.Message}, store.History())
	})

	t.Run("follows the branch the leaf selects", func(t *testing.T) {
		store := newTestStore(t)
		root := appendMessage(t, store, llm.RoleUser, "root")
		branch := appendMessage(t, store, llm.RoleAssistant, "branch")

		// A new turn continues from the root instead of the last answer, which
		// moves the active branch away from the one just written.
		other, err := store.Append(t.Context(), Entry{
			ParentID: root.ID,
			Message: llm.Message{
				Role:   llm.RoleAssistant,
				Blocks: []llm.Block{{Type: llm.BlockText, Text: "other branch"}},
			},
		})
		require.NoError(t, err)

		active := store.Branch()

		require.Len(t, active, 2)
		require.Equal(t, root.ID, active[0].ID)
		require.Equal(t, other.ID, active[1].ID)
		require.Equal(t, []llm.Message{root.Message, other.Message}, store.History())
		require.NotContains(t, active, branch)
	})
}

// TestSetLeaf verifies moving the active leaf of the tree, which is what lets
// a session return to an earlier turn and continue beside it.
func TestSetLeaf(t *testing.T) {
	t.Run("continues from the entry it moves to", func(t *testing.T) {
		store := newTestStore(t)
		root := appendMessage(t, store, llm.RoleUser, "root")
		answered := appendMessage(t, store, llm.RoleAssistant, "answered")

		require.NoError(t, store.SetLeaf(root.ID))

		require.Equal(t, root.ID, store.Leaf())
		require.Equal(t, []llm.Message{root.Message}, store.History())

		// Writing from the turn the session returned to opens a branch beside
		// the one it left, and the branch it left stays stored.
		other := appendMessage(t, store, llm.RoleAssistant, "other")

		require.Equal(t, root.ID, other.ParentID)
		require.Equal(t, []llm.Message{root.Message, other.Message}, store.History())
		require.Len(t, store.Entries(), 3)

		left, err := store.Path(answered.ID)
		require.NoError(t, err)
		require.Equal(t, []llm.Message{root.Message, answered.Message}, messagesOf(left))
	})

	t.Run("moves the leaf before the first message", func(t *testing.T) {
		store := newTestStore(t)
		root := appendMessage(t, store, llm.RoleUser, "root")
		appendMessage(t, store, llm.RoleAssistant, "answered")

		require.NoError(t, store.SetLeaf(""))

		require.Empty(t, store.Leaf())
		require.Nil(t, store.Branch())
		require.Nil(t, store.History())

		written := appendMessage(t, store, llm.RoleUser, "again")

		require.Empty(t, written.ParentID)
		require.Len(t, store.Entries(), 3)
		require.Equal(t, []llm.Message{written.Message}, store.History())
		require.NotEqual(t, root.ID, written.ID)
	})

	t.Run("keeps the move when the session is reopened", func(t *testing.T) {
		dir := t.TempDir()
		store, err := Create(t.Context(), dir, Header{
			Agent: "coder",
			Model: "test/model",
		}, &stubGenerator{})
		require.NoError(t, err)

		root := appendMessage(t, store, llm.RoleUser, "root")
		appendMessage(t, store, llm.RoleAssistant, "answered")
		require.NoError(t, store.SetLeaf(root.ID))

		id := store.ID()
		require.NoError(t, store.Close())

		reloaded, err := Open(dir, id, &stubGenerator{})
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, reloaded.Close()) })

		require.Equal(t, root.ID, reloaded.Leaf())
		require.True(
			t,
			reloaded.Info().UpdatedAt.Equal(store.Info().UpdatedAt),
			"moving the leaf is not conversation activity",
		)

		continued := appendMessage(t, reloaded, llm.RoleAssistant, "other")
		require.Equal(t, root.ID, continued.ParentID)
	})

	t.Run("writes nothing when the leaf already holds the entry", func(t *testing.T) {
		dir := t.TempDir()
		store, err := Create(t.Context(), dir, Header{
			Agent: "coder",
			Model: "test/model",
		}, &stubGenerator{})
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, store.Close()) })

		entry := appendMessage(t, store, llm.RoleUser, "root")
		before := readLines(t, store)

		require.NoError(t, store.SetLeaf(entry.ID))

		require.Equal(t, before, readLines(t, store))
	})

	t.Run("rejects entries it does not hold", func(t *testing.T) {
		store := newTestStore(t)
		appendMessage(t, store, llm.RoleUser, "root")

		require.ErrorContains(t, store.SetLeaf("nope"), `unknown entry "nope"`)
	})

	t.Run("rejects a closed store", func(t *testing.T) {
		store := newTestStore(t)
		require.NoError(t, store.Close())

		require.ErrorContains(t, store.SetLeaf(""), "store is closed")
	})
}

// TestActiveAgent verifies resolving the agent a branch runs.
func TestActiveAgent(t *testing.T) {
	t.Run("reports the agent of the header when the branch selects none", func(t *testing.T) {
		store := newTestStore(t)
		appendMessage(t, store, llm.RoleUser, "one")

		require.Equal(t, "coder", store.ActiveAgent())
	})

	t.Run("reports the selection of the branch", func(t *testing.T) {
		store := newTestStore(t)
		appendMessage(t, store, llm.RoleUser, "one")

		require.NoError(t, store.SetAgent(t.Context(), "reviewer"))

		require.Equal(t, "reviewer", store.ActiveAgent())
	})

	t.Run("reports the newest selection", func(t *testing.T) {
		store := newTestStore(t)
		appendMessage(t, store, llm.RoleUser, "one")
		require.NoError(t, store.SetAgent(t.Context(), "reviewer"))
		require.NoError(t, store.SetAgent(t.Context(), "writer"))

		require.Equal(t, "writer", store.ActiveAgent())
	})

	t.Run("ignores a selection the branch left behind", func(t *testing.T) {
		store := newTestStore(t)
		appendMessage(t, store, llm.RoleUser, "one")
		second := appendMessage(t, store, llm.RoleAssistant, "two")
		require.NoError(t, store.SetAgent(t.Context(), "reviewer"))

		// Returning to a turn before the selection leaves it on the branch
		// that wrote it, so the conversation runs on the agent of the header.
		require.NoError(t, store.SetLeaf(second.ID))

		require.Equal(t, "coder", store.ActiveAgent())
	})
}

// TestActiveModel verifies resolving the model a branch runs.
func TestActiveModel(t *testing.T) {
	t.Run("reports the model of the header when the branch selects none", func(t *testing.T) {
		store := newTestStore(t)
		appendMessage(t, store, llm.RoleUser, "one")

		require.Equal(t, "test/model", store.ActiveModel())
	})

	t.Run("reports the selection of the branch", func(t *testing.T) {
		store := newTestStore(t)
		appendMessage(t, store, llm.RoleUser, "one")

		require.NoError(t, store.SetModel(t.Context(), "fake/other-model"))

		require.Equal(t, "fake/other-model", store.ActiveModel())
	})

	t.Run("reports the newest selection", func(t *testing.T) {
		store := newTestStore(t)
		appendMessage(t, store, llm.RoleUser, "one")
		require.NoError(t, store.SetModel(t.Context(), "fake/one"))
		require.NoError(t, store.SetModel(t.Context(), "fake/two"))

		require.Equal(t, "fake/two", store.ActiveModel())
	})

	t.Run("ignores a selection the branch left behind", func(t *testing.T) {
		store := newTestStore(t)
		appendMessage(t, store, llm.RoleUser, "one")
		second := appendMessage(t, store, llm.RoleAssistant, "two")
		require.NoError(t, store.SetModel(t.Context(), "fake/other-model"))

		require.NoError(t, store.SetLeaf(second.ID))

		require.Equal(t, "test/model", store.ActiveModel())
	})

	t.Run("keeps the agents and the models of a branch apart", func(t *testing.T) {
		store := newTestStore(t)
		appendMessage(t, store, llm.RoleUser, "one")

		require.NoError(t, store.SetAgent(t.Context(), "reviewer"))
		require.Equal(t, "reviewer", store.ActiveAgent())
		// The agent selection is not a model selection, so it leaves the model
		// of the session exactly where it was.
		require.Equal(t, "test/model", store.ActiveModel())

		require.NoError(t, store.SetModel(t.Context(), "fake/other-model"))
		require.Equal(t, "reviewer", store.ActiveAgent(), "the model selection is not an agent one")
		require.Equal(t, "fake/other-model", store.ActiveModel())
	})
}

// TestSetModel verifies selecting the model of a branch, which is what lets one
// conversation change model without losing its branches.
func TestSetModel(t *testing.T) {
	t.Run("selects the model of the branch and survives reopening", func(t *testing.T) {
		dir := t.TempDir()
		store, err := Create(t.Context(), dir, Header{
			Agent: "coder",
			Model: "test/model",
		}, &stubGenerator{})
		require.NoError(t, err)

		first := appendMessage(t, store, llm.RoleUser, "one")
		second := appendMessage(t, store, llm.RoleAssistant, "two")

		require.NoError(t, store.SetModel(t.Context(), "fake/other-model"))

		branch := store.Branch()
		require.Len(t, branch, 3)
		selection := branch[2]
		require.Equal(t, KindModel, selection.Kind)
		require.Equal(t, second.ID, selection.ParentID)
		require.Equal(t, "fake/other-model", selection.ModelRef)
		require.Equal(t, selection.ID, store.Leaf())

		// The selection is not a message, so it never reaches the provider.
		require.Equal(t, []llm.Message{first.Message, second.Message}, store.History())

		id := store.ID()
		require.NoError(t, store.Close())

		reloaded, err := Open(dir, id, &stubGenerator{})
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, reloaded.Close()) })

		require.Equal(t, "fake/other-model", reloaded.ActiveModel())
		require.Equal(t, selection.ID, reloaded.Leaf())
		require.Equal(t, KindModel, reloaded.Entries()[2].Kind)
		require.Equal(t, "fake/other-model", reloaded.Entries()[2].ModelRef)

		// The header keeps the model the session was created with, which is
		// what pins the provider the session talks to.
		require.Equal(t, "test/model", reloaded.Info().Model)
	})

	t.Run("binds the selection to the branch that wrote it", func(t *testing.T) {
		store := newTestStore(t)
		appendMessage(t, store, llm.RoleUser, "one")
		second := appendMessage(t, store, llm.RoleAssistant, "two")
		require.NoError(t, store.SetModel(t.Context(), "fake/other-model"))

		appendMessage(t, store, llm.RoleAssistant, "a")
		require.Equal(t, "fake/other-model", store.ActiveModel())

		require.NoError(t, store.SetLeaf(second.ID))
		appendMessage(t, store, llm.RoleAssistant, "b")
		require.Equal(t, "test/model", store.ActiveModel())

		require.NoError(t, store.SetLeaf(store.Entries()[2].ID))
		require.Equal(t, "fake/other-model", store.ActiveModel())
	})

	t.Run("writes nothing when the model does not change", func(t *testing.T) {
		dir := t.TempDir()
		store, err := Create(t.Context(), dir, Header{
			Agent: "coder",
			Model: "test/model",
		}, &stubGenerator{})
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, store.Close()) })

		appendMessage(t, store, llm.RoleUser, "one")
		before := readLines(t, store)

		require.NoError(t, store.SetModel(t.Context(), "test/model"))

		require.Equal(t, before, readLines(t, store))
	})

	t.Run("rejects an empty reference", func(t *testing.T) {
		store := newTestStore(t)

		require.ErrorContains(t, store.SetModel(t.Context(), "   "), "must not be empty")
	})

	t.Run("rejects a closed store", func(t *testing.T) {
		store := newTestStore(t)
		require.NoError(t, store.Close())

		require.ErrorContains(t, store.SetModel(t.Context(), "fake/other"), "store is closed")
	})
}

// TestSetAgent verifies selecting the agent of a branch, which is what lets
// one conversation change agent without losing its branches.
func TestSetAgent(t *testing.T) {
	t.Run("selects the agent of the branch and survives reopening", func(t *testing.T) {
		dir := t.TempDir()
		store, err := Create(t.Context(), dir, Header{
			Agent: "coder",
			Model: "test/model",
		}, &stubGenerator{})
		require.NoError(t, err)

		first := appendMessage(t, store, llm.RoleUser, "one")
		second := appendMessage(t, store, llm.RoleAssistant, "two")

		require.NoError(t, store.SetAgent(t.Context(), "reviewer"))

		branch := store.Branch()
		require.Len(t, branch, 3)
		selection := branch[2]
		require.Equal(t, KindAgent, selection.Kind)
		require.Equal(t, second.ID, selection.ParentID)
		require.Equal(t, "reviewer", selection.AgentID)
		require.Equal(t, selection.ID, store.Leaf())

		// The selection is not a message, so it never reaches the provider.
		require.Equal(t, []llm.Message{first.Message, second.Message}, store.History())

		id := store.ID()
		require.NoError(t, store.Close())

		reloaded, err := Open(dir, id, &stubGenerator{})
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, reloaded.Close()) })

		require.Equal(t, "reviewer", reloaded.ActiveAgent())
		require.Equal(t, selection.ID, reloaded.Leaf())
		require.Equal(t, KindAgent, reloaded.Entries()[2].Kind)
		require.Equal(t, "reviewer", reloaded.Entries()[2].AgentID)
	})

	t.Run("selects the agent before the first message", func(t *testing.T) {
		store := newTestStore(t)

		require.NoError(t, store.SetAgent(t.Context(), "reviewer"))

		branch := store.Branch()
		require.Len(t, branch, 1)
		require.Empty(t, branch[0].ParentID)
		require.Equal(t, "reviewer", store.ActiveAgent())

		// The message that follows hangs from the selection, so the whole
		// conversation runs on the agent it selected.
		written := appendMessage(t, store, llm.RoleUser, "one")
		require.Equal(t, branch[0].ID, written.ParentID)
		require.Equal(t, "reviewer", store.ActiveAgent())
	})

	t.Run("binds the selection to the branch that wrote it", func(t *testing.T) {
		store := newTestStore(t)
		appendMessage(t, store, llm.RoleUser, "one")
		second := appendMessage(t, store, llm.RoleAssistant, "two")
		require.NoError(t, store.SetAgent(t.Context(), "reviewer"))

		appendMessage(t, store, llm.RoleAssistant, "a")
		require.Equal(t, "reviewer", store.ActiveAgent())

		// The branch that returns to a turn before the selection runs on the
		// agent that was in effect there.
		require.NoError(t, store.SetLeaf(second.ID))
		appendMessage(t, store, llm.RoleAssistant, "b")
		require.Equal(t, "coder", store.ActiveAgent())

		// The branch that holds the selection keeps it.
		require.NoError(t, store.SetLeaf(store.Entries()[2].ID))
		require.Equal(t, "reviewer", store.ActiveAgent())
	})

	t.Run("writes nothing when the agent does not change", func(t *testing.T) {
		dir := t.TempDir()
		store, err := Create(t.Context(), dir, Header{
			Agent: "coder",
			Model: "test/model",
		}, &stubGenerator{})
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, store.Close()) })

		appendMessage(t, store, llm.RoleUser, "one")
		before := readLines(t, store)

		// The session already runs on the agent of its header.
		require.NoError(t, store.SetAgent(t.Context(), "coder"))

		require.Equal(t, before, readLines(t, store))
	})

	t.Run("rejects an empty agent", func(t *testing.T) {
		store := newTestStore(t)

		require.ErrorContains(t, store.SetAgent(t.Context(), "   "), "must not be empty")
	})

	t.Run("rejects a closed store", func(t *testing.T) {
		store := newTestStore(t)
		require.NoError(t, store.Close())

		require.ErrorContains(t, store.SetAgent(t.Context(), "reviewer"), "store is closed")
	})
}

// TestSetTag verifies labeling the entries of the tree, which is what lets
// the user find a turn again.
func TestSetTag(t *testing.T) {
	t.Run("labels an entry and survives reopening", func(t *testing.T) {
		dir := t.TempDir()
		store, err := Create(t.Context(), dir, Header{
			Agent: "coder",
			Model: "test/model",
		}, &stubGenerator{})
		require.NoError(t, err)

		entry := appendMessage(t, store, llm.RoleUser, "root")
		require.NoError(t, store.SetTag(entry.ID, "bug"))
		require.Equal(t, "bug", store.Entries()[0].Tag)

		id := store.ID()
		updated := store.Info().UpdatedAt
		require.NoError(t, store.Close())

		reloaded, err := Open(dir, id, &stubGenerator{})
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, reloaded.Close()) })

		require.Equal(t, "bug", reloaded.Entries()[0].Tag)
		require.True(t, reloaded.Info().UpdatedAt.Equal(updated))
	})

	t.Run("replaces the tag of an entry", func(t *testing.T) {
		store := newTestStore(t)
		entry := appendMessage(t, store, llm.RoleUser, "root")

		require.NoError(t, store.SetTag(entry.ID, "bug"))
		require.NoError(t, store.SetTag(entry.ID, "review"))
		require.Equal(t, "review", store.Entries()[0].Tag)
	})

	t.Run("removes the tag of an entry", func(t *testing.T) {
		dir := t.TempDir()
		store, err := Create(t.Context(), dir, Header{
			Agent: "coder",
			Model: "test/model",
		}, &stubGenerator{})
		require.NoError(t, err)

		entry := appendMessage(t, store, llm.RoleUser, "root")
		require.NoError(t, store.SetTag(entry.ID, "bug"))
		require.NoError(t, store.SetTag(entry.ID, "  "))

		id := store.ID()
		require.NoError(t, store.Close())

		reloaded, err := Open(dir, id, &stubGenerator{})
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, reloaded.Close()) })

		require.Empty(t, reloaded.Entries()[0].Tag)
	})

	t.Run("trims the tag it stores", func(t *testing.T) {
		store := newTestStore(t)
		entry := appendMessage(t, store, llm.RoleUser, "root")

		require.NoError(t, store.SetTag(entry.ID, "  bug  "))

		require.Equal(t, "bug", store.Entries()[0].Tag)
	})

	t.Run("writes nothing when the tag does not change", func(t *testing.T) {
		store := newTestStore(t)
		entry := appendMessage(t, store, llm.RoleUser, "root")
		require.NoError(t, store.SetTag(entry.ID, "bug"))
		before := readLines(t, store)

		require.NoError(t, store.SetTag(entry.ID, "bug"))

		require.Equal(t, before, readLines(t, store))
	})

	t.Run("rejects entries it does not hold", func(t *testing.T) {
		store := newTestStore(t)
		appendMessage(t, store, llm.RoleUser, "root")

		require.ErrorContains(t, store.SetTag("nope", "bug"), `unknown entry "nope"`)
	})

	t.Run("rejects a closed store", func(t *testing.T) {
		store := newTestStore(t)
		require.NoError(t, store.Close())

		require.ErrorContains(t, store.SetTag("", "bug"), "store is closed")
	})
}

// TestSetTitle verifies naming a session, which is what lets the user find it
// again in the list of stored sessions.
func TestSetTitle(t *testing.T) {
	t.Run("names a session and survives reopening", func(t *testing.T) {
		dir := t.TempDir()
		store, err := Create(t.Context(), dir, Header{
			Agent: "coder",
			Model: "test/model",
		}, &stubGenerator{})
		require.NoError(t, err)

		appendMessage(t, store, llm.RoleUser, "hello")
		require.NoError(t, store.SetTitle("Fix the parser"))
		require.Equal(t, "Fix the parser", store.Info().Title)
		require.True(t, store.Info().Named)

		sessionID := store.ID()
		require.NoError(t, store.Close())

		reloaded, err := Open(dir, sessionID, &stubGenerator{})
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, reloaded.Close()) })

		require.Equal(t, "Fix the parser", reloaded.Info().Title)
		require.True(t, reloaded.Info().Named)
	})

	t.Run("names a session that holds no message", func(t *testing.T) {
		store := newTestStore(t)

		require.NoError(t, store.SetTitle("Fix the parser"))

		require.Equal(t, "Fix the parser", store.Info().Title)
		require.True(t, store.Info().Named)
	})

	t.Run("leaves the derived title unnamed", func(t *testing.T) {
		store := newTestStore(t)
		appendMessage(t, store, llm.RoleUser, "hello")

		require.Equal(t, "hello", store.Info().Title)
		require.False(t, store.Info().Named)
	})

	t.Run("keeps the name when the session is branched", func(t *testing.T) {
		store := newTestStore(t)
		root := appendMessage(t, store, llm.RoleUser, "root")
		appendMessage(t, store, llm.RoleAssistant, "answered")
		require.NoError(t, store.SetTitle("Fix the parser"))

		require.NoError(t, store.SetLeaf(root.ID))
		appendMessage(t, store, llm.RoleAssistant, "other")

		require.Equal(t, "Fix the parser", store.Info().Title, "the name belongs to the session")
	})

	t.Run("replaces the name of a session", func(t *testing.T) {
		store := newTestStore(t)
		appendMessage(t, store, llm.RoleUser, "hello")

		require.NoError(t, store.SetTitle("first"))
		require.NoError(t, store.SetTitle("second"))

		require.Equal(t, "second", store.Info().Title)
	})

	t.Run("falls back to the derived title when the name is removed", func(t *testing.T) {
		store := newTestStore(t)
		appendMessage(t, store, llm.RoleUser, "Fix the bug")
		require.NoError(t, store.SetTitle("named"))

		require.NoError(t, store.SetTitle(""))

		require.Equal(t, "Fix the bug", store.Info().Title)
	})

	t.Run("keeps the name over the derived title", func(t *testing.T) {
		store := newTestStore(t)
		require.NoError(t, store.SetTitle("named"))
		appendMessage(t, store, llm.RoleUser, "Fix the bug")

		require.Equal(t, "named", store.Info().Title)
	})

	t.Run("folds the name into a single line", func(t *testing.T) {
		store := newTestStore(t)

		require.NoError(t, store.SetTitle("  Fix\n\n the   parser  "))

		require.Equal(t, "Fix the parser", store.Info().Title)
	})

	t.Run("writes nothing when the name does not change", func(t *testing.T) {
		store := newTestStore(t)
		require.NoError(t, store.SetTitle("named"))
		before := readLines(t, store)

		require.NoError(t, store.SetTitle(" named "))

		require.Equal(t, before, readLines(t, store))
	})

	t.Run("leaves the update time untouched", func(t *testing.T) {
		store := newTestStore(t)
		appendMessage(t, store, llm.RoleUser, "hello")
		updated := store.Info().UpdatedAt

		require.NoError(t, store.SetTitle("named"))

		require.Equal(t, updated, store.Info().UpdatedAt)
	})

	t.Run("rejects a closed store", func(t *testing.T) {
		store := newTestStore(t)
		require.NoError(t, store.Close())

		require.ErrorContains(t, store.SetTitle("named"), "store is closed")
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
			assistantMessageLine + "\n" + leafMarkerLine("m1") + "\n"
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

	t.Run("names the sessions the user renamed", func(t *testing.T) {
		dir := t.TempDir()
		store, err := Create(
			t.Context(),
			dir,
			Header{Agent: "coder", Model: "test/model"},
			&stubGenerator{},
		)
		require.NoError(t, err)
		appendMessage(t, store, llm.RoleUser, "first message")
		require.NoError(t, store.SetTitle("Fix the parser"))
		require.NoError(t, store.Close())

		infos, err := List(dir)
		require.NoError(t, err)
		require.Len(t, infos, 1)
		require.Equal(t, "Fix the parser", infos[0].Title)
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

// TestOneLine verifies folding a text into a single line of at most maxRunes.
func TestOneLine(t *testing.T) {
	t.Run("keeps short texts untouched", func(t *testing.T) {
		require.Equal(t, "hello", oneLine("hello", 10))
	})

	t.Run("folds whitespace and newlines into single spaces", func(t *testing.T) {
		require.Equal(t, "Fix the bug", oneLine("  Fix\n\n the\tbug \n", 40))
	})

	t.Run("cuts long texts with an ellipsis", func(t *testing.T) {
		require.Equal(t, "hello…", oneLine("hello world", 5))
	})

	t.Run("keeps an empty text empty", func(t *testing.T) {
		require.Empty(t, oneLine("   \n ", 10))
	})
}

// appendCompaction appends a checkpoint replacing everything before keptID.
func appendCompaction(t *testing.T, store *Store, summary, keptID string) Entry {
	t.Helper()

	entry, err := store.AppendCompaction(
		t.Context(),
		summary,
		keptID,
		123,
		"kimi-k2",
		llm.Usage{InputTokens: 7, OutputTokens: 3},
	)
	require.NoError(t, err)
	return entry
}

// TestAppendCompaction verifies persisting a checkpoint.
func TestAppendCompaction(t *testing.T) {
	t.Run("appends the entry after the active leaf and advances it", func(t *testing.T) {
		store := newTestStore(t)
		first := appendMessage(t, store, llm.RoleUser, "one")
		second := appendMessage(t, store, llm.RoleAssistant, "two")

		compaction := appendCompaction(t, store, "the summary", first.ID)

		require.Equal(t, KindCompaction, compaction.Kind)
		require.Equal(t, second.ID, compaction.ParentID)
		require.Equal(t, compaction.ID, store.Leaf())
		require.Equal(t, "the summary", compaction.CompactionSummary)
		require.Equal(t, first.ID, compaction.CompactionKeptID)
		require.Equal(t, 123, compaction.CompactionTokensBefore)
		require.Equal(t, "kimi-k2", compaction.ResponseModel)
		require.Equal(t, llm.Usage{InputTokens: 7, OutputTokens: 3}, compaction.ResponseUsage)

		// The entry is written after the previous leaf, so reopening the
		// session reads it back exactly as it was appended.
		reopened, err := Open(filepath.Dir(store.path), store.ID(), &stubGenerator{})
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, reopened.Close()) })

		entries := reopened.Entries()
		require.Len(t, entries, 3)
		require.Equal(t, KindCompaction, entries[2].Kind)
		require.Equal(t, compaction.ID, entries[2].ID)
		require.Equal(t, compaction.CompactionSummary, entries[2].CompactionSummary)
		require.Equal(t, compaction.ResponseUsage, entries[2].ResponseUsage)
		require.Equal(t, compaction.ID, reopened.Leaf())
	})

	t.Run("refuses an unknown kept entry", func(t *testing.T) {
		store := newTestStore(t)
		appendMessage(t, store, llm.RoleUser, "one")

		_, err := store.AppendCompaction(t.Context(), "summary", "nope", 1, "m", llm.Usage{})

		require.ErrorContains(t, err, `unknown kept entry "nope"`)
	})

	t.Run("refuses an empty summary", func(t *testing.T) {
		store := newTestStore(t)
		first := appendMessage(t, store, llm.RoleUser, "one")

		_, err := store.AppendCompaction(t.Context(), "   ", first.ID, 1, "m", llm.Usage{})

		require.ErrorContains(t, err, "summary must not be empty")
	})

	t.Run("refuses a closed store", func(t *testing.T) {
		store := newTestStore(t)
		first := appendMessage(t, store, llm.RoleUser, "one")
		require.NoError(t, store.Close())

		_, err := store.AppendCompaction(t.Context(), "summary", first.ID, 1, "m", llm.Usage{})

		require.ErrorContains(t, err, "store is closed")
	})
}

// TestDisplayedBranch verifies the entries a front end reads once a compaction
// is applied.
func TestDisplayedBranch(t *testing.T) {
	t.Run("returns the branch when it holds no compaction", func(t *testing.T) {
		store := newTestStore(t)
		first := appendMessage(t, store, llm.RoleUser, "one")
		second := appendMessage(t, store, llm.RoleAssistant, "two")

		displayed := store.DisplayedBranch()

		require.Len(t, displayed, 2)
		require.Equal(t, first.ID, displayed[0].ID)
		require.Equal(t, second.ID, displayed[1].ID)
		require.Equal(t, []llm.Message{first.Message, second.Message}, store.History())
	})

	t.Run("starts at the kept entry and holds the checkpoint", func(t *testing.T) {
		store := newTestStore(t)
		appendMessage(t, store, llm.RoleUser, "one")
		appendMessage(t, store, llm.RoleAssistant, "two")
		third := appendMessage(t, store, llm.RoleUser, "three")
		compaction := appendCompaction(t, store, "the summary", third.ID)

		displayed := store.DisplayedBranch()

		require.Len(t, displayed, 2)
		require.Equal(t, third.ID, displayed[0].ID)
		require.Equal(t, compaction.ID, displayed[1].ID)

		history := store.History()
		require.Len(t, history, 2)
		require.Equal(t, llm.RoleUser, history[0].Role)
		require.Contains(t, history[0].Blocks[0].Text, "the summary")
		require.Equal(t, third.Message, history[1])
	})

	t.Run("consults only the newest compaction", func(t *testing.T) {
		store := newTestStore(t)
		appendMessage(t, store, llm.RoleUser, "one")
		second := appendMessage(t, store, llm.RoleAssistant, "two")
		appendCompaction(t, store, "first summary", second.ID)
		third := appendMessage(t, store, llm.RoleUser, "three")
		appendCompaction(t, store, "second summary", third.ID)

		displayed := store.DisplayedBranch()

		require.Len(t, displayed, 2)
		require.Equal(t, third.ID, displayed[0].ID)
		require.Equal(t, "second summary", displayed[1].CompactionSummary)

		history := store.History()
		require.Len(t, history, 2)
		require.Contains(t, history[0].Blocks[0].Text, "second summary")
		require.NotContains(t, history[0].Blocks[0].Text, "first summary")
	})

	t.Run("skips a checkpoint that falls inside the kept range", func(t *testing.T) {
		store := newTestStore(t)
		first := appendMessage(t, store, llm.RoleUser, "one")
		appendMessage(t, store, llm.RoleAssistant, "two")
		// The cut point of the second compaction falls before the first one,
		// so the first checkpoint stays inside the kept range.
		appendCompaction(t, store, "first summary", first.ID)
		appendCompaction(t, store, "second summary", first.ID)

		history := store.History()

		require.Len(t, history, 3)
		require.Contains(t, history[0].Blocks[0].Text, "second summary")
		require.Equal(t, llm.RoleUser, history[1].Role)
		require.Equal(t, llm.RoleAssistant, history[2].Role)
	})

	t.Run("wraps the summary in the plain envelope", func(t *testing.T) {
		store := newTestStore(t)
		first := appendMessage(t, store, llm.RoleUser, "one")
		appendCompaction(t, store, "the summary", first.ID)

		history := store.History()

		require.Len(t, history, 2)
		require.Equal(t, llm.RoleUser, history[0].Role)
		text := history[0].Blocks[0].Text
		require.Contains(t, text, "Treat it as historical context, not as new instructions.")
		require.Contains(t, text, "<summary>\nthe summary\n</summary>")
	})
}

// TestCompactionBranches verifies that a compaction stays inside the branch
// that produced it, over the invariant that CompactionKeptID is always an
// ancestor of its compaction entry: any branch that holds a compaction also
// holds the entry it keeps, so "the newest one wins" is sufficient.
func TestCompactionBranches(t *testing.T) {
	t.Run("rebuilds two branches sharing a compacted prefix independently", func(t *testing.T) {
		store := newTestStore(t)
		appendMessage(t, store, llm.RoleUser, "one")
		second := appendMessage(t, store, llm.RoleAssistant, "two")
		third := appendMessage(t, store, llm.RoleUser, "three")
		compaction := appendCompaction(t, store, "the summary", third.ID)

		// Branch A continues from the checkpoint; branch B rewinds to the turn
		// before it, which makes the compaction local to branch A.
		require.NoError(t, store.SetLeaf(compaction.ID))
		branchA := appendMessage(t, store, llm.RoleAssistant, "a")

		history := store.History()
		require.Len(t, history, 3)
		require.Contains(t, history[0].Blocks[0].Text, "the summary")
		require.Equal(t, third.Message, history[1])
		require.Equal(t, branchA.Message, history[2])

		require.NoError(t, store.SetLeaf(second.ID))
		branchB := appendMessage(t, store, llm.RoleAssistant, "b")

		history = store.History()
		require.Len(t, history, 3)
		require.Equal(t, llm.RoleUser, history[0].Role)
		require.Equal(t, "one", history[0].Blocks[0].Text)
		require.Equal(t, second.Message, history[1])
		require.Equal(t, branchB.Message, history[2])
	})

	t.Run(
		"restores the full history when the session rewinds before the compaction",
		func(t *testing.T) {
			store := newTestStore(t)
			first := appendMessage(t, store, llm.RoleUser, "one")
			second := appendMessage(t, store, llm.RoleAssistant, "two")
			appendCompaction(t, store, "the summary", first.ID)

			require.NoError(t, store.SetLeaf(second.ID))

			displayed := store.DisplayedBranch()
			require.Len(t, displayed, 2)
			require.Equal(t, first.ID, displayed[0].ID)
			require.Equal(t, second.ID, displayed[1].ID)

			history := store.History()
			require.Len(t, history, 2)
			require.Equal(t, "one", history[0].Blocks[0].Text)
			require.Equal(t, "two", history[1].Blocks[0].Text)
		},
	)

	t.Run(
		"falls back to the entries after the compaction when the kept one is absent",
		func(t *testing.T) {
			store := newTestStore(t)
			first := appendMessage(t, store, llm.RoleUser, "one")
			second := appendMessage(t, store, llm.RoleAssistant, "two")
			compaction := appendCompaction(t, store, "the summary", first.ID)

			// A defensive case: the kept entry is missing from the branch, which a
			// hand-edited file can produce. The rebuild falls back to the entries
			// after the compaction instead of showing a conversation with a hole.
			branch := store.Branch()
			branch = slices.DeleteFunc(
				branch,
				func(entry Entry) bool { return entry.ID == first.ID },
			)

			kept, resolved := keptFrom(branch, compaction.CompactionKeptID)

			require.False(t, resolved)
			require.Nil(t, kept)
			require.Equal(t, second.ID, branch[0].ID)

			// The displayed branch of the whole store is not affected: the kept
			// entry is present, so it starts at it and holds the checkpoint.
			displayed := store.DisplayedBranch()
			require.Equal(t, first.ID, displayed[0].ID)
			require.Equal(t, compaction.ID, displayed[len(displayed)-1].ID)
		},
	)
}
