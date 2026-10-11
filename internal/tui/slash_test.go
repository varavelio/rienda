package tui

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/varavelio/rienda/internal/agent"
	"github.com/varavelio/rienda/internal/slash"
)

// slashCommands returns the commands the slash tests offer.
func slashCommands() []slash.Command {
	return []slash.Command{
		{ID: "test", Description: "Run tests", Template: "Run tests: $*"},
		{ID: "doc", Description: "Write docs", Template: "Write docs for $1"},
	}
}

// slashModel returns a chatting model offering the test commands.
func slashModel(t *testing.T) (*model, *fakeSession) {
	t.Helper()

	m, scripted := chatModel(t)
	m.slashCommands = slashCommands()
	return m, scripted
}

// TestSlashToken verifies the detection of the command that ends at the cursor.
func TestSlashToken(t *testing.T) {
	t.Run("reads the text after the leading slash", func(t *testing.T) {
		query, ok := slashToken("/te", 0, 3)
		require.True(t, ok)
		require.Equal(t, "te", query)
	})
	t.Run("reads an empty command", func(t *testing.T) {
		query, ok := slashToken("/", 0, 1)
		require.True(t, ok)
		require.Empty(t, query)
	})
	t.Run("leaves a slash outside position zero alone", func(t *testing.T) {
		_, ok := slashToken("run /te", 0, 7)
		require.False(t, ok)
	})
	t.Run("closes the token on whitespace", func(t *testing.T) {
		_, ok := slashToken("/test foo", 0, 6)
		require.False(t, ok)
	})
	t.Run("reports no command on later lines", func(t *testing.T) {
		_, ok := slashToken("/test\nfoo", 1, 3)
		require.False(t, ok)
	})
	t.Run("reports no command before the slash", func(t *testing.T) {
		_, ok := slashToken("/test", 0, 0)
		require.False(t, ok)
	})
}

// TestSlash verifies the command completion of the prompt.
func TestSlash(t *testing.T) {
	t.Run("opens on a leading slash", func(t *testing.T) {
		m, _ := slashModel(t)
		typeText(t, m, "/")
		require.True(t, m.slash.active)
		require.Empty(t, m.slash.query)
		require.Len(t, m.slash.items, 2)
	})

	t.Run("narrows as the query grows", func(t *testing.T) {
		m, _ := slashModel(t)
		typeText(t, m, "/tes")
		require.Equal(t, "tes", m.slash.query)
		require.Len(t, m.slash.items, 1)
		require.Equal(t, "test", m.slash.items[0].ID)
	})

	t.Run("leaves a mid prompt slash alone", func(t *testing.T) {
		m, _ := slashModel(t)
		typeText(t, m, "run /te")
		require.False(t, m.slash.active)
	})

	t.Run("closes the token on space for mention handoff", func(t *testing.T) {
		m, _ := slashModel(t)
		typeText(t, m, "/test ")
		require.False(t, m.slash.active)
		require.Equal(t, "/test ", m.input.Value())
	})

	t.Run("dismisses on escape until the query changes", func(t *testing.T) {
		m, _ := slashModel(t)
		typeText(t, m, "/te")
		require.True(t, m.slash.active)
		update(t, m, pressEscape)
		require.False(t, m.slash.active)
	})

	t.Run("cycles the highlight", func(t *testing.T) {
		m, _ := slashModel(t)
		typeText(t, m, "/")
		update(t, m, pressUp)
		require.Equal(t, 1, m.slash.cursor)
		update(t, m, pressDown)
		require.Equal(t, 0, m.slash.cursor)
	})

	t.Run("completes without expanding", func(t *testing.T) {
		m, scripted := slashModel(t)
		typeText(t, m, "/tes")
		update(t, m, pressEnter)
		require.Equal(t, "/test ", m.input.Value())
		require.False(t, m.slash.active)
		require.False(t, m.running)
		require.Empty(t, scripted.prompts)
	})
}

// TestSlashKeepsTheFooter verifies the popup steals rows from the
// conversation, never from the chrome: the footer hints stay rendered while
// the menu is open, even on a short terminal.
func TestSlashKeepsTheFooter(t *testing.T) {
	t.Run("footer survives the popup", func(t *testing.T) {
		m, scripted := chatModel(t)
		m.slashCommands = manySlashCommands()
		_ = scripted
		update(t, m, windowMsg(80, 24))
		before := m.transcriptHeight()
		typeText(t, m, "/")
		require.True(t, m.slash.active)
		require.Positive(t, m.slashHeight(), "the popup takes rows on a roomy terminal")
		require.Less(t, m.transcriptHeight(), before, "the popup shares conversation rows")
		require.Equal(t, len(m.fullHintLines()), m.hintLines(), "the chrome keeps every hint row")
		rendered := plain(m.render())
		require.Contains(t, rendered, "enter send")
		require.Contains(t, rendered, "/test", "the popup lists commands above the intact footer")
	})

	t.Run("footer survives a mention too", func(t *testing.T) {
		m, _ := mentionModel(t, testProject())
		update(t, m, windowMsg(80, 14))
		typeText(t, m, "@")
		require.True(t, m.mention.active)
		require.Contains(t, plain(m.render()), "enter send")
	})

	t.Run("dims the description", func(t *testing.T) {
		m, _ := slashModel(t)
		typeText(t, m, "/tes")
		listed := m.slashList(2)
		require.Contains(t, listed, "/test")
		require.Contains(t, listed, "Run tests")
		require.Contains(t, listed, "\x1b[2m", "the description reads faint next to the plain name")
	})
}

// manySlashCommands returns enough commands to fill the popup on a short
// terminal, so a layout that steals chrome rows visibly trims the footer.
func manySlashCommands() []slash.Command {
	names := []string{"alpha", "beta", "build", "check", "deploy", "doc", "fix", "test"}
	commands := make([]slash.Command, 0, len(names))
	for _, name := range names {
		commands = append(commands, slash.Command{
			ID:          name,
			Description: "Does " + name,
			Template:    "Do " + name,
		})
	}
	return commands
}

// TestSlashSubmit verifies command resolution on submit.
func TestSlashSubmit(t *testing.T) {
	t.Run("expands a known command", func(t *testing.T) {
		m, scripted := slashModel(t)
		m.input.SetValue("/test --coverage")
		update(t, m, pressEnter)
		require.Len(t, scripted.prompts, 1)
		require.Equal(t, "Run tests: --coverage", scripted.prompts[0])
	})

	t.Run("expands positionals", func(t *testing.T) {
		m, scripted := slashModel(t)
		m.input.SetValue("/doc foo")
		update(t, m, pressEnter)
		require.Len(t, scripted.prompts, 1)
		require.Equal(t, "Write docs for foo", scripted.prompts[0])
	})

	t.Run("notices unknown without a run", func(t *testing.T) {
		m, scripted := slashModel(t)
		before := len(scripted.prompts)
		m.input.SetValue("/nope x")
		update(t, m, pressEnter)
		require.Len(t, scripted.prompts, before)
		require.Contains(t, plain(m.render()), "unknown command")
	})

	t.Run("notices a lone slash without a run", func(t *testing.T) {
		m, scripted := slashModel(t)
		before := len(scripted.prompts)
		m.input.SetValue("/")
		update(t, m, pressEnter)
		require.Len(t, scripted.prompts, before)
	})

	t.Run("leaves normal prompts alone", func(t *testing.T) {
		m, scripted := slashModel(t)
		m.input.SetValue("hello /test")
		update(t, m, pressEnter)
		require.Len(t, scripted.prompts, 1)
		require.Equal(t, "hello /test", scripted.prompts[0])
	})
}

// TestSlashDiagnostics verifies broken files surface as notices without blocking.
func TestSlashDiagnostics(t *testing.T) {
	t.Run("seeds diagnostics as notices", func(t *testing.T) {
		m := newTestModelWith(t, modelConfig{
			agents:           []agent.Agent{{ID: "coder", Description: "A test agent"}},
			selected:         0,
			slashDiagnostics: []string{"slash: broken file"},
		})
		run(t, m, m.Init())
		require.Equal(t, phaseChat, m.phase)
		update(t, m, windowMsg(80, 24))
		require.Contains(t, plain(m.render()), "slash: broken file")
	})
}
