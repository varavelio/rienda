package tool

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// writeDevcontainerShim writes an executable script that replaces the
// devcontainer binary during tests and returns its path.
func writeDevcontainerShim(t *testing.T, body string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "devcontainer")
	content := []byte("#!/bin/sh\n" + body + "\n")
	// The shim stands in for the devcontainer binary, so it must be executable.
	err := os.WriteFile(path, content, 0o755) //nolint:gosec // must be executable.
	require.NoError(t, err)
	return path
}

// newDevcontainerWorkspace builds a workspace folder with a devcontainer
// configuration file.
func newDevcontainerWorkspace(t *testing.T, configRelativePath string) string {
	t.Helper()

	workspace := t.TempDir()
	config := filepath.Join(workspace, configRelativePath)
	require.NoError(t, os.MkdirAll(filepath.Dir(config), 0o750))
	require.NoError(t, os.WriteFile(config, []byte("{}"), 0o600))
	return workspace
}

// TestDevcontainerShellExecute verifies command execution through the
// devcontainer CLI.
func TestDevcontainerShellExecute(t *testing.T) {
	t.Run("wraps the command with devcontainer exec", func(t *testing.T) {
		shim := writeDevcontainerShim(t, `printf 'ARGS:%s\n' "$*"`)
		workspace := newDevcontainerWorkspace(
			t,
			filepath.Join(".devcontainer", "devcontainer.json"),
		)
		nested := filepath.Join(workspace, "pkg")
		require.NoError(t, os.Mkdir(nested, 0o750))

		shell, err := NewDevcontainerShell(DevcontainerShellOptions{Binary: shim})
		require.NoError(t, err)

		sink := &recordingSink{}
		result, err := shell.Execute(
			WithWorkdir(t.Context(), nested),
			Call{Arguments: json.RawMessage(`{"command":"echo hi"}`)},
			sink,
		)
		require.NoError(t, err)

		require.False(t, result.IsError)
		require.Equal(
			t,
			"ARGS:exec --workspace-folder "+workspace+" sh -c echo hi",
			resultText(t, result),
		)
	})

	t.Run("finds a workspace root devcontainer.json", func(t *testing.T) {
		shim := writeDevcontainerShim(t, `printf 'ARGS:%s\n' "$*"`)
		workspace := newDevcontainerWorkspace(t, ".devcontainer.json")

		shell, err := NewDevcontainerShell(DevcontainerShellOptions{
			Binary:  shim,
			Workdir: workspace,
		})
		require.NoError(t, err)

		sink := &recordingSink{}
		result, err := shell.Execute(
			t.Context(),
			Call{Arguments: json.RawMessage(`{"command":"echo hi"}`)},
			sink,
		)
		require.NoError(t, err)

		require.Contains(t, resultText(t, result), "--workspace-folder "+workspace)
	})

	t.Run("rejects a workdir argument", func(t *testing.T) {
		shim := writeDevcontainerShim(t, `printf 'ARGS:%s\n' "$*"`)
		workspace := newDevcontainerWorkspace(
			t,
			filepath.Join(".devcontainer", "devcontainer.json"),
		)

		shell, err := NewDevcontainerShell(DevcontainerShellOptions{
			Binary:  shim,
			Workdir: workspace,
		})
		require.NoError(t, err)

		sink := &recordingSink{}
		_, err = shell.Execute(
			t.Context(),
			Call{Arguments: json.RawMessage(`{"command":"echo hi","workdir":"/tmp"}`)},
			sink,
		)

		require.ErrorContains(t, err, "does not accept a workdir argument")
	})

	t.Run("reports a non-zero exit code", func(t *testing.T) {
		shim := writeDevcontainerShim(t, `exit 7`)
		workspace := newDevcontainerWorkspace(
			t,
			filepath.Join(".devcontainer", "devcontainer.json"),
		)

		shell, err := NewDevcontainerShell(DevcontainerShellOptions{
			Binary:  shim,
			Workdir: workspace,
		})
		require.NoError(t, err)

		sink := &recordingSink{}
		result, err := shell.Execute(
			t.Context(),
			Call{Arguments: json.RawMessage(`{"command":"echo hi"}`)},
			sink,
		)
		require.NoError(t, err)

		require.True(t, result.IsError)
		require.Contains(t, resultText(t, result), "command exited with code 7")
	})

	t.Run("fails when no devcontainer is found", func(t *testing.T) {
		workdir := t.TempDir()
		shell, err := NewDevcontainerShell(DevcontainerShellOptions{
			Binary:  writeDevcontainerShim(t, "true"),
			Workdir: workdir,
		})
		require.NoError(t, err)

		sink := &recordingSink{}
		_, err = shell.Execute(
			t.Context(),
			Call{Arguments: json.RawMessage(`{"command":"echo hi"}`)},
			sink,
		)

		require.ErrorContains(t, err, "not inside a dev container")
		require.NotContains(t, err.Error(), workdir)
	})
}

// TestNewDevcontainerShell verifies option handling.
func TestNewDevcontainerShell(t *testing.T) {
	t.Run("applies defaults", func(t *testing.T) {
		shell, err := NewDevcontainerShell(DevcontainerShellOptions{})
		require.NoError(t, err)

		definition := shell.Definition()
		require.Equal(t, "dc-shell", definition.Name)
		require.Contains(t, definition.Description, "working directory")
		require.True(t, json.Valid(definition.Parameters))
		require.Contains(t, string(definition.Parameters), `"command"`)
		require.NotContains(t, string(definition.Parameters), `"workdir"`)
	})

	t.Run("rejects invalid options", func(t *testing.T) {
		_, err := NewDevcontainerShell(DevcontainerShellOptions{Name: "bad name"})

		require.ErrorContains(t, err, "invalid tool name")
	})
}
