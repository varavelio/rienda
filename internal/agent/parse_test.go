package agent

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParse(t *testing.T) {
	t.Run("parses a full definition", func(t *testing.T) {
		loaded, err := Parse("coder", []byte(`---
description: Writes and reviews Go code
model: openrouter/kimi-k2
tools:
  - read
  - edit
temperature: 0.2
top_p: 0.9
max_tokens: 4096
stop:
  - END
reasoning_effort: High
reasoning_budget_tokens: 2048
---

You are a senior Go engineer.
`))

		require.NoError(t, err)
		require.Equal(t, Agent{
			ID:                    "coder",
			Description:           "Writes and reviews Go code",
			Model:                 "openrouter/kimi-k2",
			Tools:                 []string{"read", "edit"},
			Temperature:           new(0.2),
			TopP:                  new(0.9),
			MaxTokens:             4096,
			Stop:                  []string{"END"},
			ReasoningEffort:       "high",
			ReasoningBudgetTokens: 2048,
			SystemPrompt:          "You are a senior Go engineer.",
		}, loaded)
	})

	t.Run("parses a minimal definition", func(t *testing.T) {
		loaded, err := Parse(
			"plain",
			[]byte("---\ndescription: A plain agent\nmodel: zen-go/glm-5.3\n---\n"),
		)

		require.NoError(t, err)
		require.Equal(t, Agent{
			ID:          "plain",
			Description: "A plain agent",
			Model:       "zen-go/glm-5.3",
		}, loaded)
	})

	t.Run("normalizes whitespace in the description and model", func(t *testing.T) {
		loaded, err := Parse(
			"spaced",
			[]byte("---\ndescription: '  Spaced  '\nmodel: ' openrouter / kimi-k2 '\n---\n"),
		)

		require.NoError(t, err)
		require.Equal(t, "Spaced", loaded.Description)
		require.Equal(t, "openrouter/kimi-k2", loaded.Model)
	})

	t.Run("trims the system prompt", func(t *testing.T) {
		loaded, err := Parse(
			"trim",
			[]byte("---\ndescription: T\nmodel: a/b\n---\n\n\n  Hola  \n\n"),
		)

		require.NoError(t, err)
		require.Equal(t, "Hola", loaded.SystemPrompt)
	})

	t.Run("tolerates a byte order mark and Windows line endings", func(t *testing.T) {
		loaded, err := Parse(
			"bom",
			[]byte("\uFEFF---\r\ndescription: A\r\nmodel: a/b\r\n---\r\n\r\nHola\r\n"),
		)

		require.NoError(t, err)
		require.Equal(t, "Hola", loaded.SystemPrompt)
	})

	t.Run("rejects invalid identifiers", func(t *testing.T) {
		for _, id := range []string{"", ".", "..", ".hidden", "nested/agent", `nested\agent`, "agent.md"} {
			t.Run("id "+id, func(t *testing.T) {
				_, err := Parse(id, []byte("---\ndescription: A\nmodel: a/b\n---\n"))

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
				"---\ndescription: A\nmodel: a/b\n",
				"missing its closing ---",
			},
			{"empty frontmatter", "---\n---\nbody\n", "frontmatter is empty"},
			{
				"unknown key",
				"---\ndescription: A\nmodel: a/b\nunknown: true\n---\n",
				"invalid frontmatter",
			},
			{"malformed yaml", "---\ndescription: [A\nmodel: a/b\n---\n", "invalid frontmatter"},
			{
				"multiple documents",
				"---\ndescription: A\nmodel: a/b\n...\ndescription: B\n---\n",
				"single YAML document",
			},
			{"missing description", "---\nmodel: a/b\n---\n", "description is required"},
			{"missing model", "---\ndescription: A\n---\n", "must have the form provider/model"},
			{
				"model without provider",
				"---\ndescription: A\nmodel: kimi-k2\n---\n",
				"must have the form provider/model",
			},
			{
				"model without name",
				"---\ndescription: A\nmodel: openrouter/\n---\n",
				"must have the form provider/model",
			},
			{
				"empty tool name",
				"---\ndescription: A\nmodel: a/b\ntools: ['']\n---\n",
				"tools must not contain empty names",
			},
			{
				"temperature below range",
				"---\ndescription: A\nmodel: a/b\ntemperature: -0.1\n---\n",
				"temperature",
			},
			{
				"temperature above range",
				"---\ndescription: A\nmodel: a/b\ntemperature: 2.1\n---\n",
				"temperature",
			},
			{"top_p below range", "---\ndescription: A\nmodel: a/b\ntop_p: -0.1\n---\n", "top_p"},
			{"top_p above range", "---\ndescription: A\nmodel: a/b\ntop_p: 1.1\n---\n", "top_p"},
			{
				"negative max_tokens",
				"---\ndescription: A\nmodel: a/b\nmax_tokens: -1\n---\n",
				"max_tokens",
			},
			{
				"empty stop sequence",
				"---\ndescription: A\nmodel: a/b\nstop: ['']\n---\n",
				"stop must not contain empty sequences",
			},
			{
				"negative reasoning budget",
				"---\ndescription: A\nmodel: a/b\nreasoning_budget_tokens: -1\n---\n",
				"reasoning_budget_tokens",
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
