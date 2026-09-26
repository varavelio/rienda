// Package hook discovers user hook extensions and runs them at the fixed
// points of a run.
//
// A hook is a directory with an index.js file that exports one function per
// hook point it implements. Discovery compiles every extension once; Resolve
// binds the extensions an agent declares, in order, into the Hooks the engine
// calls. The engine declares the contract and this package implements it, so
// the engine never imports this package.
//
// A hook never fails a run: a throw or an unusable return value is reported
// as a notice and treated as no opinion.
package hook
