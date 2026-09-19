package tui

import (
	"errors"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"

	"github.com/varavelio/rienda/internal/filecomplete"
)

// pressTab completes the highlighted suggestion of a mention.
var pressTab = tea.KeyPressMsg{Code: tea.KeyTab}

// typeText feeds every rune of text to the model as a key press, the way a
// user types it.
func typeText(t *testing.T, m *model, text string) {
	t.Helper()

	for _, char := range text {
		if char == ' ' {
			update(t, m, tea.KeyPressMsg{Code: tea.KeySpace, Text: " "})
			continue
		}
		update(t, m, tea.KeyPressMsg{Code: char, Text: string(char)})
	}
}

// completerFunc adapts a function to the fileCompleter interface.
type completerFunc func(query string, limit int) ([]filecomplete.Suggestion, error)

// Complete calls the function the completer was built from.
func (f completerFunc) Complete(query string, limit int) ([]filecomplete.Suggestion, error) {
	return f(query, limit)
}

// recordingCompleter answers with a fixed set of suggestions and records the
// queries it receives, so a test can assert when the model asks for them.
type recordingCompleter struct {
	suggestions []filecomplete.Suggestion
	queries     []string
}

// Complete records the query and returns the scripted suggestions.
func (c *recordingCompleter) Complete(query string, limit int) ([]filecomplete.Suggestion, error) {
	c.queries = append(c.queries, query)
	return c.suggestions, nil
}

// mentionSuggestions returns the paths a completion offers in the tests.
func mentionSuggestions() []filecomplete.Suggestion {
	return []filecomplete.Suggestion{
		{Path: "internal/tui/model.go"},
		{Path: "internal/tui/view.go"},
	}
}

// mentionModel returns a chatting model that completes mentions with the given
// completer.
func mentionModel(t *testing.T, complete fileCompleter) (*model, *fakeSession) {
	t.Helper()

	m, scripted := chatModel(t)
	m.completeFiles = complete
	return m, scripted
}

// TestMentionToken verifies the detection of the mention that ends at the
// cursor.
func TestMentionToken(t *testing.T) {
	t.Run("reads the text written after the mention", func(t *testing.T) {
		query, ok := mentionToken("@src/mai", 0, 8)

		require.True(t, ok)
		require.Equal(t, "src/mai", query)
	})

	t.Run("reads an empty mention", func(t *testing.T) {
		query, ok := mentionToken("@", 0, 1)

		require.True(t, ok)
		require.Empty(t, query)
	})

	t.Run("opens only on an at sign that starts a word", func(t *testing.T) {
		_, ok := mentionToken("user@example.com", 0, 16)
		require.False(t, ok, "an at sign inside a word is left alone")

		query, ok := mentionToken("read @ma", 0, 8)
		require.True(t, ok, "a mention opens after a space")
		require.Equal(t, "ma", query)
	})

	t.Run("reads a mention that ends at the cursor of a later line", func(t *testing.T) {
		query, ok := mentionToken("one\n@mo", 1, 3)

		require.True(t, ok)
		require.Equal(t, "mo", query)
	})

	t.Run("reports no mention on a line without one", func(t *testing.T) {
		_, ok := mentionToken("one\nmo", 1, 2)
		require.False(t, ok)
	})

	t.Run("reports no mention before the at sign", func(t *testing.T) {
		_, ok := mentionToken("@src", 0, 0)
		require.False(t, ok)

		_, ok = mentionToken("read @src", 0, 4)
		require.False(t, ok)
	})

	t.Run("reports no mention outside the value", func(t *testing.T) {
		_, ok := mentionToken("", 0, 0)
		require.False(t, ok)

		_, ok = mentionToken("only one line", 3, 0)
		require.False(t, ok)
	})
}

// TestMention verifies the file completion of the prompt.
func TestMention(t *testing.T) {
	t.Run("opens the completion when the prompt holds a mention", func(t *testing.T) {
		completer := &recordingCompleter{suggestions: mentionSuggestions()}
		m, _ := mentionModel(t, completer)

		typeText(t, m, "@mo")

		require.True(t, m.mention.active)
		require.Equal(t, "mo", m.mention.query)
		require.Equal(t, mentionSuggestions(), m.mention.items)
		require.Equal(t, []string{"", "m", "mo"}, completer.queries, "one query per change")
		require.Contains(t, plain(m.render()), "internal/tui/model.go")
	})

	t.Run("leaves the prompt alone without a mention", func(t *testing.T) {
		completer := &recordingCompleter{suggestions: mentionSuggestions()}
		m, _ := mentionModel(t, completer)

		typeText(t, m, "read user@example.com")

		require.False(t, m.mention.active)
		require.Empty(t, completer.queries)
	})

	t.Run("runs without a completion", func(t *testing.T) {
		m, _ := chatModel(t)

		typeText(t, m, "@mo")

		require.False(t, m.mention.active)
		require.Equal(t, "@mo", m.input.Value())
	})

	t.Run("cycles the highlight through the suggestions", func(t *testing.T) {
		m, _ := mentionModel(t, &recordingCompleter{suggestions: mentionSuggestions()})
		typeText(t, m, "@mo")

		update(t, m, pressUp)
		require.Equal(t, 1, m.mention.cursor, "stepping up from the first suggestion wraps")

		update(t, m, pressDown)
		require.Equal(t, 0, m.mention.cursor, "stepping down from the last suggestion wraps")
	})

	t.Run("completes the highlighted suggestion with enter", func(t *testing.T) {
		m, scripted := mentionModel(t, &recordingCompleter{suggestions: mentionSuggestions()})
		typeText(t, m, "@vi")

		update(t, m, pressDown)
		update(t, m, pressEnter)

		require.Equal(t, "@internal/tui/view.go ", m.input.Value())
		require.False(t, m.mention.active)
		require.False(t, m.running, "enter completes instead of sending the prompt")
		require.Empty(t, scripted.prompts)
	})

	t.Run("completes the highlighted suggestion with tab", func(t *testing.T) {
		m, scripted := mentionModel(t, &recordingCompleter{suggestions: mentionSuggestions()})
		typeText(t, m, "@mo")

		update(t, m, pressTab)

		require.Equal(t, "@internal/tui/model.go ", m.input.Value())
		require.Empty(t, scripted.prompts)
	})

	t.Run("keeps the characters written around the mention", func(t *testing.T) {
		m, _ := mentionModel(t, &recordingCompleter{suggestions: mentionSuggestions()})
		m.input.SetValue("read @mo now")
		m.input.SetCursorColumn(8)
		m.syncPrompt()

		update(t, m, pressEnter)

		require.Equal(t, "read @internal/tui/model.go  now", m.input.Value())
	})

	t.Run("cancels the completion with escape", func(t *testing.T) {
		m, _ := mentionModel(t, &recordingCompleter{suggestions: mentionSuggestions()})
		typeText(t, m, "@mo")

		update(t, m, pressEscape)

		require.False(t, m.mention.active)
		require.False(t, m.confirmInterrupt, "escape cancels the completion, not the run")
		require.Equal(t, "@mo", m.input.Value())
	})

	t.Run("keeps the completion closed until the mention changes", func(t *testing.T) {
		completer := &recordingCompleter{suggestions: mentionSuggestions()}
		m, _ := mentionModel(t, completer)
		typeText(t, m, "@mo")
		update(t, m, pressEscape)

		m.syncPrompt()
		require.False(
			t,
			m.mention.active,
			"a refresh that does not change the mention keeps it closed",
		)
		require.Len(t, completer.queries, 3, "the canceled mention is not queried again")

		typeText(t, m, "d")
		require.True(t, m.mention.active)
		require.Equal(t, "mod", m.mention.query)
	})

	t.Run("keeps the completion open when the accepted path is a directory", func(t *testing.T) {
		directory := []filecomplete.Suggestion{{Path: "internal/tui/", IsDir: true}}
		m, _ := mentionModel(t, &recordingCompleter{suggestions: directory})
		typeText(t, m, "@tui")

		update(t, m, pressEnter)

		require.Equal(t, "@internal/tui/", m.input.Value())
		require.True(t, m.mention.active)
		require.Equal(t, "internal/tui/", m.mention.query)
	})

	t.Run("engages the mention without suggestions", func(t *testing.T) {
		m, _ := mentionModel(t, completerFunc(
			func(string, int) ([]filecomplete.Suggestion, error) { return nil, nil },
		))

		typeText(t, m, "@zzz")

		require.True(t, m.mention.active)
		require.Empty(t, m.mention.items)
		require.Zero(t, m.mentionHeight(), "the popup takes no row without suggestions")
	})

	t.Run("closes the completion when the project cannot be listed", func(t *testing.T) {
		m, _ := mentionModel(t, completerFunc(
			func(string, int) ([]filecomplete.Suggestion, error) {
				return nil, errors.New("files: list: permission denied")
			},
		))

		typeText(t, m, "@mo")

		require.False(t, m.mention.active)
		require.Zero(t, m.mentionHeight())
	})

	t.Run("shares the rows of the conversation with the popup", func(t *testing.T) {
		m, _ := mentionModel(t, &recordingCompleter{suggestions: mentionSuggestions()})
		update(t, m, windowMsg(80, 30))
		typeText(t, m, "@mo")

		lines := strings.Split(ansi.Strip(m.render()), "\n")
		border := slices.IndexFunc(lines, func(line string) bool {
			return strings.Contains(line, "╭")
		})

		require.Len(t, lines, 30)
		require.GreaterOrEqual(t, border, 2, "the popup sits above the input box")
		require.Contains(t, lines[border-2], "internal/tui/model.go")
		require.Contains(t, lines[border-1], "internal/tui/view.go")
	})
}
