// Package agent loads and validates Rienda agent definitions.
//
// An agent is a Markdown file with YAML frontmatter stored under the global
// agents directory (see DefaultDir). The file name without its extension is
// the agent ID, the frontmatter declares the model the agent runs and the
// tools it may use, and the Markdown body is its system prompt:
//
//	---
//	description: Writes and reviews Go code
//	tools: [read, edit]
//	hooks: [approve, notify]
//	config:
//		foo: true
//	---
//
//	You are a senior Go engineer.
//
// Description is required. Agents never name a model: the model a session
// runs comes from the conversation itself — the user picks it live or a
// deterministic chain seeds a new session — and every generation setting,
// such as the token limit, the sampling parameters or the thinking mode,
// comes from the model declaration of the provider. A frontmatter that
// declares a model key is an error named by Parse.
//
// The frontmatter also declares the extensions of the agent: the ordered hook
// names in Hooks and the per-extension settings in Config, a map of maps that
// scripts read as ctx.agent.config.
//
// Decoding is strict: an unknown frontmatter key is an error, so typos never
// pass unnoticed.
package agent
