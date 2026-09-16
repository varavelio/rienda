// Package agent loads and validates Rienda agent definitions.
//
// An agent is a Markdown file with YAML frontmatter stored under the global
// agents directory (see DefaultDir). The file name without its extension is
// the agent ID, the frontmatter describes how the agent runs and the Markdown
// body is its system prompt:
//
//	---
//	description: Writes and reviews Go code
//	model: openrouter/kimi-k2
//	tools: [read, edit]
//	reasoning_effort: high
//	---
//
//	You are a senior Go engineer.
//
// Description and Model are required. Every other field is optional and, when
// absent, leaves the corresponding setting to the model defaults.
//
// Decoding is strict: an unknown frontmatter key is an error, so typos never
// pass unnoticed.
package agent
