// Package providermod discovers and runs the JavaScript provider modules
// that define the model providers of Rienda.
//
// A provider is a directory holding an index.js that exports one synchronous
// function receiving the standard extension ctx and returning the canonical
// declaration of the provider: the connection settings every request uses
// and the model roster the user may pick from. User providers live under
// "~/.rienda/providers/<name>"; built-in providers ship the same kind of
// module embedded in the binary. A user module whose name matches a built-in
// replaces it entirely: the embedded code is never executed.
//
// Discover executes the winning modules once per process, sequentially in
// ascending name order, and validates every returned declaration. A module
// that throws, is unreadable or returns a malformed declaration is skipped
// with a logged reason: one broken provider never blocks the rest. Modules
// fetch their own model catalogs (typically models.dev) and cache them under
// their own provider directory; this package never touches the network and
// never sees credentials.
//
// The declarations Discover returns are immutable data the rest of the
// program reads: the harness resolves provider/model references against
// them, the front ends list them, and the auth interface stores the
// credentials their auth field asks for.
package providermod
