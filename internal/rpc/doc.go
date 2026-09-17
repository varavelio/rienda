// Package rpc implements the protocol server of Rienda for custom clients and
// extensions.
//
// The server speaks newline-delimited JSON over standard input and output, so
// any process can drive the same sessions the other modes use without
// depending on the terminal interface.
package rpc
