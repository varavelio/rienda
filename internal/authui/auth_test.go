package authui

import (
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/stretchr/testify/require"

	"github.com/varavelio/rienda/internal/credentials"
	"github.com/varavelio/rienda/internal/providermod"
)

// oneProvider returns a declaration of one api_key provider with a roster.
func oneProvider(models int) []providermod.Provider {
	return []providermod.Provider{{
		Name: "opencode-go",
		Decl: providermod.Declaration{
			Auth:   providermod.AuthAPIKey,
			Models: make([]providermod.ModelDeclaration, models),
		},
	}}
}

// key builds one key press out of its text.
func key(text string) tea.KeyMsg {
	return tea.KeyPressMsg{Text: text}
}

// typing sends a key through the typing phase and returns the screen it
// produced, asserting every conversion the trip carries: a screen that
// returns another model is a bug the test refuses to hide.
func typing(t *testing.T, m *authModel, text string) *authModel {
	next, _ := m.updateTyping(key(text))
	typed, ok := next.(*authModel)
	require.True(t, ok, "updateTyping returned %T", next)
	return typed
}

// listing sends a key through the list phase and returns the screen it
// produced, with the same assertions.
func listing(t *testing.T, m *authModel, text string) *authModel {
	next, _ := m.updateList(key(text))
	typed, ok := next.(*authModel)
	require.True(t, ok, "updateList returned %T", next)
	return typed
}

// TestListMarksTheRoster verifies the state the list shows: a provider with a
// stored key is authenticated, one that needs none is not actionable, and an
// unauthenticated api_key provider is marked missing.
func TestListMarksTheRoster(t *testing.T) {
	path := filepath.Join(t.TempDir(), "credentials.json")
	store, err := credentials.Load(path)
	require.NoError(t, err)
	require.NoError(t, store.Set("opencode-go", "sk-test"))

	m := newAuthModel(oneProvider(3), store, path)
	require.Len(t, m.providers, 1)
	require.True(t, m.providers[0].authed)
	require.Equal(t, 3, m.providers[0].models)
}

// TestTypingAcceptsPrintableKeys verifies the typing contract: every printable
// key contributes its text, backspace drops the last one, enter stores it once
// fresh-read, and the prompt closes.
func TestTypingAcceptsPrintableKeys(t *testing.T) {
	path := filepath.Join(t.TempDir(), "credentials.json")
	store, err := credentials.Load(path)
	require.NoError(t, err)

	m := typing(t, newAuthModel(oneProvider(1), store, path).startTyping(), "")
	for _, text := range []string{"s", "k", "-", "1", "2", "3"} {
		m = typing(t, m, text)
	}
	require.Equal(t, []rune("sk-123"), m.input)

	m = typing(t, m, "backspace")
	require.Equal(t, []rune("sk-12"), m.input)

	m = typing(t, m, "enter")
	require.False(t, m.typing, "enter leaves the prompt")
	require.Equal(t, "sk-12", store.APIKey("opencode-go"))
}

// TestTypingEscapeNeverWrites verifies the documented contract: escaping out
// of the prompt discards the entry and the file stays untouched.
func TestTypingEscapeNeverWrites(t *testing.T) {
	path := filepath.Join(t.TempDir(), "credentials.json")
	store, err := credentials.Load(path)
	require.NoError(t, err)

	m := typing(t, newAuthModel(oneProvider(1), store, path).startTyping(), "")
	for _, text := range []string{"a", "b"} {
		m = typing(t, m, text)
	}

	m = typing(t, m, "esc")
	require.False(t, m.typing)
	require.Empty(t, m.input)
	require.False(t, store.Authenticated("opencode-go"))
}

// TestBlankKeyIsRefused verifies an empty entry stores nothing and the screen
// says so instead of pretending.
func TestBlankKeyIsRefused(t *testing.T) {
	path := filepath.Join(t.TempDir(), "credentials.json")
	store, err := credentials.Load(path)
	require.NoError(t, err)

	m := typing(t, newAuthModel(oneProvider(1), store, path).startTyping(), "enter")
	require.Contains(t, m.notice, "nothing written")
	require.False(t, store.Authenticated("opencode-go"))
}

// TestNoneProviderIsNotSelectable verifies a provider that needs no credential
// cannot be configured.
func TestNoneProviderIsNotSelectable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "credentials.json")
	store, err := credentials.Load(path)
	require.NoError(t, err)

	providers := []providermod.Provider{{
		Name: "local",
		Decl: providermod.Declaration{Auth: providermod.AuthNone},
	}}
	m := listing(t, newAuthModel(providers, store, path), "enter")
	require.False(t, m.typing)
	require.Contains(t, m.notice, "needs no credential")
}

// TestPasteLandsInThePrompt verifies the clipboard: a paste on the prompt
// appends its content, and one on the list is dropped, because the list
// names nothing to paste into.
func TestPasteLandsInThePrompt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "credentials.json")
	store, err := credentials.Load(path)
	require.NoError(t, err)

	// On the prompt the paste lands whole, whatever it carries.
	m := typing(t, newAuthModel(oneProvider(1), store, path).startTyping(), "")
	pasted, _ := m.Update(tea.PasteMsg{Content: "sk-paste-9"})
	typed, ok := pasted.(*authModel)
	require.True(t, ok, "Update returned %T", pasted)
	require.Equal(t, []rune("sk-paste-9"), typed.input)

	// On the list the paste is dropped: nothing to paste into.
	next, _ := newAuthModel(oneProvider(1), store, path).Update(
		tea.PasteMsg{Content: "forgotten"},
	)
	dropped, ok := next.(*authModel)
	require.True(t, ok, "Update returned %T", next)
	require.Empty(t, dropped.input)
}

// TestWriteSurvivesAConcurrentWriter verifies the fresh-read write: another
// process's entries survive the store of the screen.
func TestWriteSurvivesAConcurrentWriter(t *testing.T) {
	path := filepath.Join(t.TempDir(), "credentials.json")
	first, err := credentials.Load(path)
	require.NoError(t, err)
	other, err := credentials.Load(path)
	require.NoError(t, err)

	require.NoError(t, other.Set("other", "remote"))

	m := typing(t, newAuthModel(oneProvider(1), first, path).startTyping(), "")
	for _, text := range []string{"s", "k"} {
		m = typing(t, m, text)
	}
	typing(t, m, "enter")

	fresh, err := credentials.Load(path)
	require.NoError(t, err)
	require.True(t, fresh.Authenticated("opencode-go"))
	require.True(t, fresh.Authenticated("other"), "the concurrent entry survived")
}

// TestUsageWithoutATerminal verifies a run whose stdin carries no terminal
// refuses with the code a script detects, without hanging.
func TestUsageWithoutATerminal(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)

	stdout, stderr := &strings.Builder{}, &strings.Builder{}
	err := Auth([]string{}, strings.NewReader(""), stdout, stderr)
	require.Error(t, err)
	require.Contains(t, stderr.String(), "interactive terminal")
	require.Empty(t, stdout.String())
}
