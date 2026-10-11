// Package slash loads and expands Rienda custom command definitions.
//
// A command is a Markdown file with YAML frontmatter stored under the global
// commands directory (see DefaultDir). The file name without its extension is
// the command ID, the frontmatter declares the description shown in the
// completion popup, and the Markdown body is the prompt template:
//
//	---
//	description: Run tests with coverage
//	---
//
//	Run the full test suite with coverage.
//
// Commands are literal prompt shortcuts: on submit the TUI expands the
// shortcut to final text and sends it exactly as if the user had typed it by
// hand. Expansion is pure string substitution with no shell, no network and
// no tool execution.
package slash
