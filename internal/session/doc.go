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
//	{"kind":"leaf","targetId":"01k5w9wr4t8d2m4xj6q7r5s9za","createdAt":"..."}
//	{"kind":"tag","targetId":"01k5w9wr4t8d2m4xj6q7r5s9za","createdAt":"...","tag":"bug"}
//	{"kind":"title","createdAt":"...","title":"Fix the parser"}
//	{"kind":"compaction","id":"...","parentId":"...","createdAt":"...","summary":"...","keptId":"...","tokensBefore":184203,"responseModel":"...","responseUsage":{...}}
//	{"kind":"agent","id":"...","parentId":"...","createdAt":"...","agentId":"reviewer"}
//
// Files are append-only: branching in place never rewrites them, so every
// branch stays recoverable. Four marker kinds carry the state of the session
// instead of the conversation, and they are the only lines a session writes
// without a user or a model turn behind them: a leaf marker moves the active
// leaf, which is what SetLeaf persists; a tag marker labels the entry it
// targets, which is what SetTag persists; a title marker names the session,
// which is what SetTitle persists; and an agent marker selects the agent of
// the branch, which is what SetAgent persists. A marker always describes the
// state in full, so the last one of each kind wins when the file is read
// again. The title belongs to the whole conversation rather than to a branch,
// so its marker carries no target, and the name of a session without one is
// derived from its first user message.
//
// A compaction entry replaces every entry before its kept one with a summary
// of the conversation, so a session stays inside the context window of its
// model. It hangs from the active leaf like any other appended entry, so it
// belongs to the branch that produced it and to no other: a branch that shares
// the compacted prefix rebuilds the summary, and a branch that rewinds to a
// turn before the checkpoint does not hold it at all and keeps its full
// history. DisplayedBranch applies the newest compaction and History derives
// the messages a provider receives from it, with the summary carried as a
// leading user message. A compaction whose kept entry does not resolve is a
// decode error, so a file that was edited by hand never loses turns in
// silence.
//
// A session runs on the agent its header names until a branch selects another
// one, which ActiveAgent resolves from the newest agent marker of the branch.
// The selection is a property of the branch, like a compaction: a branch that
// returns to a turn before the selection runs on the agent that was in effect
// there, so one conversation can plan with one agent and implement with
// another. The identifier is stored as it was given, so a selection that names
// a missing agent is not an error of the file: the branch simply has nothing
// to run until another selection, or the header, points at an agent that
// exists.
//
// Returning to a turn of the past and writing again is what creates a branch:
// the new messages follow the turn the session returned to, beside the ones
// that were already there, because Append hangs them from the active leaf.
// Returning to a leaf only moves the session, so writing there continues the
// branch instead of opening one. The tool invocations and the reasoning that
// connect two turns travel with the branch, so a branch keeps the context of
// the turn it starts from. Entry kinds written by newer versions are preserved
// on disk and ignored when reading, so old binaries never destroy newer files.
//
// Session and entry identifiers come from an injected IDGenerator, the shared
// id package implementation in production, so every identifier in the project
// follows the same scheme.
//
// The package keeps its own on-disk format and translates to and from the
// canonical llm types, so the file format stays stable when those types
// evolve.
package session
