// Package agent loads and validates Rienda agent definitions.
//
// An agent is a Markdown file with YAML frontmatter stored under the global
// agents directory (see DefaultDir). The file name without its extension is
// the agent ID, the frontmatter declares the model the agent runs and the
// tools it may use, and the Markdown body is its system prompt:
//
//	---
//	description: Writes and reviews Go code
//	model: openrouter/kimi-k2
//	tools: [read, edit]
//	---
//
//	You are a senior Go engineer.
//
// Description and Model are required. Model is a provider/model reference and
// every generation setting of the run, such as the token limit, the sampling
// parameters or the thinking level, comes from that model in the
// configuration; agents never declare them.
//
// Decoding is strict: an unknown frontmatter key is an error, so typos never
// pass unnoticed.
package agent
