// Package cli implements the programmatic, non-interactive command line
// interface of Rienda.
//
// Its commands run one agent over one prompt, either in a new session or in a
// stored one they continue, and stream the result to the caller, which makes
// them suitable for scripts and automation. A compaction is reported on
// standard error, so a non-interactive run is not silent about it. The command
// line entry point delegates here after selecting a command, and the
// interactive front ends live in their own packages.
package cli
