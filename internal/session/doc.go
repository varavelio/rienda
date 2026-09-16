// Package session stores conversations as trees of entries.
//
// A session lives in a JSONL file inside a directory chosen by the caller, so
// the package itself never ties a session to a project. Local runs group
// sessions by project: ProjectID turns the project path or any other logical
// identifier injected by the caller into a directory-safe identifier and
// ProjectDir composes the project directory, so one project never mixes with
// another:
//
//	~/.rienda/sessions/home-eduardo-projects-myproject-3f9a1c2d/01k5w9wr4t....jsonl
//
// Consumers that group sessions differently, like an RPC server serving
// clients that are not directories, inject their own directory to Create,
// Open and List.
//
// The first line of a session file is a header with the session metadata and
// every following line is one tree entry. Entries reference their predecessor
// through ParentID, so the conversation history is the path from the root of
// the tree down to the active leaf:
//
//	{"kind":"header","version":1,"id":"01k5w9wr4t3xk8c9b7pv3q2fme","createdAt":"...","agent":"coder","model":"openrouter/kimi-k2"}
//	{"kind":"message","id":"01k5w9wr4t8d2m4xj6q7r5s9za","createdAt":"...","role":"user","blocks":[{"type":"text","text":"Fix the bug"}]}
//	{"kind":"message","id":"01k5w9wr4ta9e5n6yk8r7s6t0b","parentId":"01k5w9wr4t8d2m4xj6q7r5s9za","createdAt":"...","role":"assistant","responseModel":"kimi-k2","responseStopReason":"tool_use","blocks":[...]}
//
// Files are append-only: branching in place never rewrites them, so every
// branch stays recoverable. A leaf marker entry moves the active leaf without
// carrying conversation content, which is reserved for future branching
// commands. Entry kinds written by newer versions are preserved on disk and
// ignored when reading, so old binaries never destroy newer files.
//
// Session and entry identifiers come from an injected IDGenerator, the shared
// id package implementation in production, so every identifier in the project
// follows the same scheme.
//
// The package keeps its own on-disk format and translates to and from the
// canonical llm types, so the file format stays stable when those types
// evolve.
package session
