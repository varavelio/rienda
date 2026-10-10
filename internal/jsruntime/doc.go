// Package jsruntime owns the embedded JavaScript runtime of Rienda.
//
// A user extension is a directory with an index.js file that assigns
// module.exports. Compile reads and compiles the file once; Invoke runs it in
// a fresh goja runtime per call and hands the exports to its caller, so two
// invocations of the same module share nothing and concurrent invocations are
// safe.
//
// The runtime exposes the extension surface as the ctx object: workspace and
// session identity, files, environment, processes, HTTP, logging, sleeping,
// configuration, human-in-the-loop interaction and, when its caller
// configures one, a shared cache of plain text with a time to live and a
// permanent store without one, its store. Scripts run with the privileges of
// the user who wrote them and are not sandboxed.
//
// The ceilings are deliberate: execution is synchronous with no event loop,
// the runtime imposes no timeout and only honors context cancellation, every
// invocation starts with a fresh runtime so no state survives between calls,
// and there is no module loader.
package jsruntime
