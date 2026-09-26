// Package tool defines the Rienda tool interface and the built-in tools.
//
// A tool is a capability the model can invoke during a conversation. Tools
// describe themselves to the model with an llm.Tool definition and run
// invocations through the Tool interface: Execute streams whatever output is
// produced while the invocation is in flight and returns the text the model
// reads back.
//
// The tools an agent may invoke are exactly the names declared in its
// definition file, so an agent without tools has no capabilities and an agent
// with tools is trusted to use them.
//
// The package ships one built-in tool. Shell runs one-shot commands on the
// host; every invocation starts a fresh process, so shell state does not
// persist between calls.
//
// Tools resolve their working directory from the invocation context (see
// WithWorkdir), from their configuration, or from the process working
// directory, in that order.
//
// Users extend the registry without touching Go: a script tool is a directory
// with an index.js file under the global tools directory, exporting a
// description, a JSON Schema of parameters and an execute function (see
// NewScriptTool). Discovery compiles every extension once (see
// DiscoverScripts); the harness registers the user tools over the built-ins,
// so a user tool with the name of a built-in replaces both its definition and
// its execution.
package tool
