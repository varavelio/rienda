// Package tui implements the interactive terminal user interface of Rienda.
//
// The interface is the default mode of the binary: when the command line
// entry point receives no command it delegates here. It drives the same
// sessions as the other modes, through the harness package, and renders the
// events of every run as they arrive.
package tui
