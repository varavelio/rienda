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
// The package ships two built-in tools. Shell runs one-shot commands on the
// host and DevcontainerShell runs one-shot commands inside the project's dev
// container through the devcontainer CLI. Both run each invocation in a fresh
// process; Shell accepts a host working directory, while DevcontainerShell
// always runs in the workspace folder of the container and keeps host paths
// out of its interface.
//
// Tools resolve their working directory from the invocation context (see
// WithWorkdir), from their configuration, or from the process working
// directory, in that order.
package tool
