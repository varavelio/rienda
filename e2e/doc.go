//go:build e2e

// Package e2e contains the black-box end-to-end suite of Rienda.
//
// Every test drives the compiled rienda executable exclusively through its
// command line and its configuration, agent and session files: no internal
// package is ever imported, and every external contract the suite depends on
// is reproduced independently by the e2e/harness package. Each test file
// covers one product scenario, every test runs against its own isolated
// installation with a dedicated home directory, workspace and fake provider,
// and the binary is compiled once for the whole suite.
//
// The suite is opt-in and compiled only under the e2e build tag:
//
//	go test -tags e2e -count=1 -timeout 10m ./e2e/...
package e2e
