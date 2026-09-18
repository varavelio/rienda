//go:build e2e

// Package harness drives black-box rienda instances for the end-to-end suite
// in the parent directory.
//
// A Harness owns one isolated installation of the application: a home
// directory with its own configuration and agent definitions, a workspace
// directory, and a fake model provider that answers the requests of the
// compiled binary with the turns a test scripts. Tests reach the instance
// only through its command line, its configuration files, the files it
// stores, and the network, so the suite validates the product exactly as a
// user experiences it.
//
// The package never imports github.com/varavelio/rienda/internal. The file
// formats of the configuration, the agent definitions and the session files,
// and the provider payloads of the three wire protocols, are reproduced here
// independently, so a change in the implementation only passes the suite when
// it preserves its external contracts.
//
// Main compiles the executable once for the whole suite. Setting
// RIENDA_E2E_BINARY to a compiled binary skips the compilation, which shortens
// the loop while developing the suite.
//
// The suite is opt-in and compiled only under the e2e build tag:
//
//	go test -tags e2e -count=1 -timeout 10m ./e2e/...
package harness
