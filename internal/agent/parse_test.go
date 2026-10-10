package agent

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParse(t *testing.T) {
	t.Run("parses a full definition", func(t *testing.T) {
		loaded, err := Parse("coder", []byte(`---
description: Writes and reviews Go code
tools:
  - read
  - edit
---

You are a senior Go engineer.
`))

		require.NoError(t, err)
		require.Equal(t, Agent{
			ID:           "coder",
			Description:  "Writes and reviews Go code",
			Tools:        []string{"read", "edit"},
			SystemPrompt: "You are a senior Go engineer.",
		}, loaded)
	})

	t.Run("parses a minimal definition", func(t *testing.T) {
		loaded, err := Parse(
			"plain",
			[]byte("---\ndescription: A plain agent\n\n---\n"),
		)

		require.NoError(t, err)
		require.Equal(t, Agent{
			ID:          "plain",
			Description: "A plain agent",
		}, loaded)
	})

	t.Run("normalizes whitespace in the description", func(t *testing.T) {
		loaded, err := Parse(
			"spaced",
			[]byte("---\ndescription: '  Spaced  '\n---\n"),
		)

		require.NoError(t, err)
		require.Equal(t, "Spaced", loaded.Description)
	})

	t.Run("trims the system prompt", func(t *testing.T) {
		loaded, err := Parse(
			"trim",
			[]byte("---\ndescription: T\n---\n\n  Hola  \n"),
		)

		require.NoError(t, err)
		require.Equal(t, "Hola", loaded.SystemPrompt)
	})

	t.Run("tolerates a byte order mark and Windows line endings", func(t *testing.T) {
		loaded, err := Parse(
			"bom",
			[]byte("\uFEFF---\r\ndescription: A\r\r\n---\r\n\r\nHola\r\n"),
		)

		require.NoError(t, err)
		require.Equal(t, "Hola", loaded.SystemPrompt)
	})

	t.Run("rejects invalid identifiers", func(t *testing.T) {
		for _, id := range []string{"", ".", "..", ".hidden", "nested/agent", `nested\agent`, "agent.md"} {
			t.Run("id "+id, func(t *testing.T) {
				_, err := Parse(id, []byte("---\ndescription: A\n\n---\n"))

				require.ErrorContains(t, err, "invalid agent id")
			})
		}
	})

	t.Run("rejects invalid definitions", func(t *testing.T) {
		cases := []struct {
			name string
			data string
			want string
		}{
			{"empty file", "", "must start with a --- frontmatter block"},
			{"missing frontmatter", "Just text\n", "must start with a --- frontmatter block"},
			{
				"unterminated frontmatter",
				"---\ndescription: A\n\n",
				"missing its closing ---",
			},
			{"empty frontmatter", "---\n---\nbody\n", "frontmatter is empty"},
			{
				"unknown key",
				"---\ndescription: A\n\nunknown: true\n---\n",
				"invalid frontmatter",
			},
			{"malformed yaml", "---\ndescription: [A\n---\n", "invalid frontmatter"},
			{
				"multiple documents",
				"---\ndescription: A\n\n...\ndescription: B\n---\n",
				"single YAML document",
			},
			{"missing description", "---\ndescription: ''\n---\n", "description is required"},
			{
				"blank description",
				"---\ndescription: '   '\n\n---\n",
				"description is required",
			},
			{
				"forbidden model key",
				"---\ndescription: A\nmodel: a/b\n---\n",
				"model",
			},
			{
				"empty tool name",
				"---\ndescription: A\n\ntools: ['']\n---\n",
				"tools must not contain empty names",
			},
			{
				"removed stop key",
				"---\ndescription: A\n\nstop: [END]\n---\n",
				"invalid frontmatter",
			},
			{
				"removed temperature key",
				"---\ndescription: A\n\ntemperature: 0.5\n---\n",
				"invalid frontmatter",
			},
			{
				"removed thinking key",
				"---\ndescription: A\n\nthinking_level: high\n---\n",
				"invalid frontmatter",
			},
		}

		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				_, err := Parse("coder", []byte(tc.data))

				require.ErrorContains(t, err, tc.want)
			})
		}
	})
}

func TestParseHooksConfig(t *testing.T) {
	t.Run("parses hooks and config", func(t *testing.T) {
		loaded, err := Parse("guarded", []byte(`---
description: A careful agent
tools:
  - shell
hooks:
  - guard
  - audit
config:
  guard:
    level: strict
---

Prompt.
`))

		require.NoError(t, err)
		require.Equal(t, []string{"guard", "audit"}, loaded.Hooks)
		require.Equal(t, map[string]map[string]any{
			"guard": {"level": "strict"},
		}, loaded.Config)
	})

	t.Run("defaults to absent", func(t *testing.T) {
		loaded, err := Parse("plain", []byte("---\ndescription: A\n\n---\n"))
		require.NoError(t, err)
		require.Empty(t, loaded.Hooks)
		require.Nil(t, loaded.Config)
	})

	t.Run("rejects an empty hook name", func(t *testing.T) {
		_, err := Parse(
			"bad",
			[]byte("---\ndescription: A\n\nhooks:\n  - ok\n  - '  '\n---\n"),
		)
		require.ErrorContains(t, err, "hooks must not contain empty names")
	})

	t.Run("rejects a scalar config", func(t *testing.T) {
		_, err := Parse("bad", []byte("---\ndescription: A\n\nconfig: foo\n---\n"))
		require.ErrorContains(t, err, "config")
	})

	t.Run("rejects a list config", func(t *testing.T) {
		_, err := Parse("bad", []byte("---\ndescription: A\n\nconfig:\n  - a\n---\n"))
		require.ErrorContains(t, err, "config")
	})

	t.Run("rejects a scalar config value", func(t *testing.T) {
		_, err := Parse(
			"bad",
			[]byte("---\ndescription: A\n\nconfig:\n  guard: strict\n---\n"),
		)
		require.ErrorContains(t, err, `config "guard"`)
	})
}
