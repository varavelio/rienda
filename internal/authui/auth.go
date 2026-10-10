package authui

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/varavelio/rienda/internal/credentials"
	"github.com/varavelio/rienda/internal/harness"
	"github.com/varavelio/rienda/internal/providermod"
)

// authDirName is the directory of the user where the providers, the
// credentials and the state live.
const authDirName = ".rienda"

// Auth drives the auth screen: it lists every provider the installation
// discovered, marks the authenticated ones and stores a key for the one the
// user picks. It is interactive only; a run without a terminal attached
// prints the usage on standard error and exits with a non-zero code, which is
// how scripts detect it before hanging on a read that will not come.
func Auth(args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	if len(args) > 0 {
		_, _ = fmt.Fprintln(stderr, "usage: rienda auth")
		return fmt.Errorf("auth: unexpected argument %q", args[0])
	}
	if !isTerminal(stdin) {
		_, _ = fmt.Fprintln(stderr, "usage: rienda auth")
		_, _ = fmt.Fprintln(stderr, "rienda: the auth screen needs an interactive terminal")
		return errors.New("auth: no interactive terminal")
	}

	providers, err := discoverProvidersForAuth()
	if err != nil {
		return fmt.Errorf("auth: %w", err)
	}
	credentialsPath, err := credentialsPath()
	if err != nil {
		return fmt.Errorf("auth: %w", err)
	}
	store, err := credentials.Load(credentialsPath)
	if errors.Is(err, credentials.ErrMalformed) {
		_, _ = fmt.Fprintf(
			stderr,
			"rienda: credentials %s is malformed; storing a key starts a new file\n",
			credentialsPath,
		)
		store = &credentials.Store{}
	} else if err != nil {
		return fmt.Errorf("auth: %w", err)
	}

	app := newAuthModel(providers, store, credentialsPath)
	program := tea.NewProgram(app, tea.WithInput(stdin), tea.WithOutput(stdout))
	if _, err := program.Run(); err != nil {
		return fmt.Errorf("auth: %w", err)
	}
	return nil
}

// discoverProvidersForAuth runs the provider discovery of an installation,
// the same pass a session runs, so the screen lists what the product offers.
func discoverProvidersForAuth() ([]providermod.Provider, error) {
	providers, err := harness.Discover()
	if err != nil {
		return nil, fmt.Errorf("harness: %w", err)
	}
	return providers, nil
}

// isTerminal reports whether the reader of stdin is an interactive terminal.
func isTerminal(stdin io.Reader) bool {
	if file, ok := stdin.(interface{ Stat() (os.FileInfo, error) }); ok {
		info, err := file.Stat()
		if err != nil {
			return false
		}
		return info.Mode()&os.ModeCharDevice != 0
	}
	return false
}

// credentialsPath returns the credentials file of the user, the one the auth
// screen writes.
func credentialsPath() (string, error) {
	if path := strings.TrimSpace(os.Getenv("RIENDA_CREDENTIALS")); path != "" {
		return path, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("locate home directory: %w", err)
	}
	return filepath.Join(home, authDirName, "credentials.json"), nil
}

// Chords the screen answers to, shared by both phases.
const (
	// keyQuit leaves the screen through the session chord.
	keyQuit = "ctrl+c"
	// keyEscape leaves also what it is editing.
	keyEscape = "esc"
	// keyBackspace drops the last typed character.
	keyBackspace = "backspace"
	// keyStore confirms the entry.
	keyEnter = "enter"
)

// authState is the screen's state: reading, typing or done.
type authState int

const (
	// authList shows the providers and moves among them.
	authList authState = iota
	// authTyping reads the key the user types.
	authTyping
)

// providerRow is one line of the provider list.
type providerRow struct {
	name   string
	models int
	authed bool
	none   bool
}

// authModel is the bubbletea model of the auth screen.
type authModel struct {
	providers []providerRow
	row       int
	store     *credentials.Store
	path      string
	input     []rune
	typing    bool
	written   bool
	notice    string
}

// newAuthModel builds the screen over the discovered providers and the
// credentials of the user.
func newAuthModel(
	providers []providermod.Provider,
	store *credentials.Store,
	path string,
) *authModel {
	rows := make([]providerRow, 0, len(providers))
	for _, provider := range providers {
		rows = append(rows, providerRow{
			name:   provider.Name,
			models: len(provider.Decl.Models),
			authed: provider.Decl.Auth != providermod.AuthAPIKey ||
				store.Authenticated(provider.Name),
			none: provider.Decl.Auth == providermod.AuthNone,
		})
	}
	return &authModel{providers: rows, store: store, path: path}
}

// Init starts the screen.
func (m *authModel) Init() tea.Cmd { return nil }

// Update carries one message through the screen. A paste lands in the prompt
// the phase is working on, so a key that came in the clipboard reaches the
// entry instead of being dropped the way an unknown chord is.
func (m *authModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if paste, ok := msg.(tea.PasteMsg); ok {
		if m.typing {
			m.input = append(m.input, []rune(paste.Content)...)
		}
		return m, nil
	}
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	switch m.state() {
	case authList:
		return m.updateList(key)
	case authTyping:
		return m.updateTyping(key)
	}
	return m, nil
}

// state reports what the screen shows.
func (m *authModel) state() authState {
	if m.typing {
		return authTyping
	}
	return authList
}

// updateList moves over the list and enters the selected provider.
func (m *authModel) updateList(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch key.String() {
	case keyQuit:
		return m, tea.Quit
	case "up", "k":
		if m.row > 0 {
			m.row--
		}
	case "down", "j":
		if m.row < len(m.providers)-1 {
			m.row++
		}
	case keyEnter:
		row := m.providers[m.row]
		switch {
		case row.none:
			m.notice = row.name + " needs no credential"
		case row.name == "":
			m.notice = "the row carries no provider"
		default:
			m.typing = true
			m.input = nil
		}
	}
	return m, nil
}

// startTyping puts the screen on the prompt of the selected provider, which
// is what enter on the list does.
func (m *authModel) startTyping() *authModel {
	m.typing = true
	m.input = nil
	return m
}

// updateTyping accepts the key and stores it, then enters on enter.
func (m *authModel) updateTyping(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.Key()
	switch {
	case msg.String() == keyQuit || msg.String() == keyEscape:
		// Escaping never writes anything.
		m.typing = false
		m.input = nil
	case msg.String() == keyBackspace:
		if len(m.input) > 0 {
			m.input = m.input[:len(m.input)-1]
		}
	case msg.String() == keyEnter:
		value := strings.TrimSpace(string(m.input))
		m.typing = false
		m.input = nil
		if value == "" {
			m.notice = "the key is empty, nothing written"
			return m, nil
		}
		name := m.providers[m.row].name
		if err := m.store.Set(name, value); err != nil {
			m.notice = err.Error()
			return m, nil
		}
		m.providers[m.row].authed = true
		m.written = true
		m.notice = name + " authenticated"
		return m, nil
	default:
		// A printable key contributes its text; combination chords and
		// special keys carry no text and fall out untouched.
		if text := key.Text; text != "" {
			m.input = append(m.input, []rune(text)...)
		}
	}
	return m, nil
}

// View renders the list or the prompt.
func (m *authModel) View() tea.View {
	rows := []string{"rienda auth", ""}
	if m.typing {
		row := m.providers[m.row]
		masked := strings.Repeat("*", len(m.input))
		rows = append(rows, "API key for "+row.name+": "+masked, "", "enter store · esc cancel")
	} else {
		for i, provider := range m.providers {
			marker := "—"
			if provider.authed {
				marker = "✓"
			}
			rows = append(rows, m.line(i == m.row, marker, provider))
		}
		if len(m.providers) == 0 {
			rows = append(rows, "no providers discovered")
		}
		rows = append(rows, "")
		if m.notice != "" {
			rows = append(rows, m.notice, "")
		}
		rows = append(rows, "enter configure · ↑/↓ move · ctrl+c quit")
	}
	return tea.NewView(strings.Join(rows, "\n"))
}

// line renders one row of the list: the cursor, the auth state and the count.
func (m *authModel) line(cursor bool, marker string, provider providerRow) string {
	cursorMark := ""
	if cursor {
		cursorMark = "> "
	}
	return cursorMark + marker + " " + provider.name + " · " + fmt.Sprintf(
		"%d models",
		provider.models,
	)
}
