// Package cli implements the programmatic, non-interactive command line
// interface of Rienda.
//
// Its commands run one agent over one prompt and stream the result to the
// caller, which makes them suitable for scripts and automation. The command
// line entry point delegates here after selecting a command, and the
// interactive front ends live in their own packages.
package cli
