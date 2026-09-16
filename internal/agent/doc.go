// Package agent loads and validates Rienda agent definitions.
//
// An agent is a Markdown file with YAML frontmatter stored under the global
// agents directory (see DefaultDir). The file name without its extension is
// the agent ID, the frontmatter describes how the agent runs and the Markdown
// body is its system prompt.
//
// Decoding is strict: an unknown frontmatter key is an error, so typos never
// pass unnoticed.
package agent
