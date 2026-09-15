// Package llm defines the provider-neutral core types used across Rienda.
//
// Providers in internal/provider implement Client by translating between
// these canonical types and each vendor wire format.
//
// Field naming follows the role each field plays, so a flat struct stays
// self-describing: fields of a tool call are prefixed ToolCall, fields of a
// tool result are prefixed ToolResult, fields of a thinking block are prefixed
// Thinking, and fields describing a tool definition are prefixed Tool only
// (for example Tool.Name).
package llm
