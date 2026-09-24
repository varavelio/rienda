package tui

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/varavelio/rienda/internal/agent"
	"github.com/varavelio/rienda/internal/session"
)

// writeAgent writes a minimal agent definition into dir.
func writeAgent(t *testing.T, dir, id string) {
	t.Helper()

	definition := "---\n" +
		"description: A test agent\n" +
		"model: fake/test-model\n" +
		"---\n" +
		"You answer briefly.\n"
	require.NoError(
		t,
		os.WriteFile(filepath.Join(dir, id+agent.Extension), []byte(definition), 0o600),
	)
}

// TestLoadAgents verifies agent discovery.
func TestLoadAgents(t *testing.T) {
	t.Run("lists the definitions sorted by id", func(t *testing.T) {
		dir := t.TempDir()
		writeAgent(t, dir, "writer")
		writeAgent(t, dir, "coder")

		definitions, err := loadAgents(dir)

		require.NoError(t, err)
		require.Len(t, definitions, 2)
		require.Equal(t, "coder", definitions[0].ID)
		require.Equal(t, "writer", definitions[1].ID)
	})

	t.Run("requires at least one definition", func(t *testing.T) {
		_, err := loadAgents(t.TempDir())

		require.ErrorContains(t, err, "no agent definitions found")
	})

	t.Run("reports invalid definitions", func(t *testing.T) {
		dir := t.TempDir()
		broken := filepath.Join(dir, "broken"+agent.Extension)
		require.NoError(t, os.WriteFile(broken, []byte("not a definition"), 0o600))

		_, err := loadAgents(dir)

		require.ErrorContains(t, err, "broken")
	})
}

// TestSelectAgent verifies agent selection.
func TestSelectAgent(t *testing.T) {
	definitions := []agent.Agent{{ID: "coder"}, {ID: "writer"}}

	t.Run("picks the only agent", func(t *testing.T) {
		selected, err := selectAgent(definitions[:1], "")

		require.NoError(t, err)
		require.Equal(t, 0, selected)
	})

	t.Run("asks the user to pick among several agents", func(t *testing.T) {
		selected, err := selectAgent(definitions, "")

		require.NoError(t, err)
		require.Equal(t, -1, selected)
	})

	t.Run("honors the requested agent", func(t *testing.T) {
		selected, err := selectAgent(definitions, "writer")

		require.NoError(t, err)
		require.Equal(t, 1, selected)
	})

	t.Run("reports unknown agents with the available ones", func(t *testing.T) {
		_, err := selectAgent(definitions, "ghost")

		require.ErrorContains(t, err, `unknown agent "ghost"`)
		require.ErrorContains(t, err, "coder, writer")
	})
}

// TestResumable verifies which stored sessions the interface offers.
func TestResumable(t *testing.T) {
	infos := []session.Info{
		{ID: "session-1", Agent: "coder", Title: "hello"},
		{ID: "session-2", Agent: "writer", Title: "hello"},
		{ID: "session-3", Agent: "coder"},
	}

	kept := resumable(infos)

	require.Len(t, kept, 2)
	require.Equal(t, "session-1", kept[0].ID)
	require.Equal(t, "session-2", kept[1].ID, "a session whose agent is gone is still offered")
}

// TestRun verifies the entry point of the interface.
func TestRun(t *testing.T) {
	t.Run("prints the usage for help", func(t *testing.T) {
		builder := &strings.Builder{}

		err := Run([]string{"-h"}, strings.NewReader(""), builder)

		require.NoError(t, err)
		require.Contains(t, builder.String(), "Usage:")
	})

	t.Run("fails without agent definitions", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())

		err := Run(nil, strings.NewReader(""), io.Discard)

		require.ErrorContains(t, err, "no agent definitions found")
	})

	t.Run("rejects invalid arguments", func(t *testing.T) {
		err := Run([]string{"coder"}, strings.NewReader(""), io.Discard)

		require.ErrorContains(t, err, "unexpected argument")
	})
}
