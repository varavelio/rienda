package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestLoad verifies the reading of the configuration file: every block the
// file may declare, the documented defaults, and the refusal of the removed
// providers block.
func TestLoad(t *testing.T) {
	t.Run("missing file holds the defaults", func(t *testing.T) {
		cfg, err := Load(filepath.Join(t.TempDir(), "nope.yaml"))
		require.NoError(t, err)
		require.Equal(t, Compaction{
			Enabled:          DefaultCompactionEnabled,
			ReserveTokens:    DefaultCompactionReserveTokens,
			KeepRecentTokens: DefaultCompactionKeepRecentTokens,
		}, cfg.Compaction)
	})

	t.Run("empty file holds the defaults", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "config.yaml")
		require.NoError(t, os.WriteFile(path, []byte(""), 0o600))
		cfg, err := Load(path)
		require.NoError(t, err)
		require.True(t, cfg.Compaction.Enabled)
	})

	t.Run("a full file round-trips", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "config.yaml")
		require.NoError(t, os.WriteFile(path, []byte(
			"compaction:\n  enabled: false\n  reserve_tokens: 4096\n"+
				"config:\n  guard:\n    route: strict\n",
		), 0o600))
		cfg, err := Load(path)
		require.NoError(t, err)
		require.False(t, cfg.Compaction.Enabled)
		require.Equal(t, 4096, cfg.Compaction.ReserveTokens)
		require.Equal(t, DefaultCompactionKeepRecentTokens, cfg.Compaction.KeepRecentTokens)
		require.Equal(t, "strict", cfg.Config["guard"]["route"])
	})

	t.Run("the removed providers block is refused with the pointer", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "config.yaml")
		require.NoError(t, os.WriteFile(path, []byte(
			"providers:\n  fake:\n    protocol: openai_chat_completions\n",
		), 0o600))
		_, err := Load(path)
		require.ErrorContains(t, err, "providers are no longer declared here")
		require.ErrorContains(t, err, "provider modules")
	})

	t.Run("the removed compaction model is refused by the strict decoder", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "config.yaml")
		require.NoError(t, os.WriteFile(path, []byte(
			"compaction:\n  model: fake/summarizer\n",
		), 0o600))
		_, err := Load(path)
		require.ErrorContains(t, err, "field model not found")
	})

	t.Run("an unknown key is refused", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "config.yaml")
		require.NoError(t, os.WriteFile(path, []byte("unknown: true\n"), 0o600))
		_, err := Load(path)
		require.ErrorContains(t, err, "field unknown not found")
	})

	t.Run("a malformed file names its path", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "config.yaml")
		require.NoError(t, os.WriteFile(path, []byte("compaction: ["), 0o600))
		_, err := Load(path)
		require.ErrorContains(t, err, path)
	})
}

// TestData verifies the document view the extension runtime reads.
func TestData(t *testing.T) {
	t.Run("returns the declared snake_case document", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "config.yaml")
		require.NoError(t, os.WriteFile(path, []byte(
			"config:\n  guard:\n    route: strict\n",
		), 0o600))
		cfg, err := Load(path)
		require.NoError(t, err)
		data := cfg.Data()
		outer, ok := data["config"].(map[string]any)
		require.True(t, ok)
		inner, ok := outer["guard"].(map[string]any)
		require.True(t, ok)
		require.Equal(t, "strict", inner["route"])
	})

	t.Run("an absent document is empty, never nil", func(t *testing.T) {
		cfg, err := Load(filepath.Join(t.TempDir(), "nope.yaml"))
		require.NoError(t, err)
		require.NotNil(t, cfg.Data())
		require.Empty(t, cfg.Data())
	})
}
