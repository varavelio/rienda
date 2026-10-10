// Package native carries the provider modules built into the binary.
//
// Every built-in provider is the same index.js a user would write, compiled
// into the binary through go:embed and loaded by the ordinary provider
// discovery. A user module named like a built-in replaces it completely.
package native

import (
	_ "embed"
)

//go:embed opencode-go/index.js
var opencodeGo string

//go:embed zai-coding-plan/index.js
var zaiCodingPlan string

// CatalogBudget is the payload cap the provider modules need: the models.dev
// database is measured in megabytes, and the default cap of the extension
// runtime suits shell output but starves a roster fetch.
const CatalogBudget = 32 << 20

// Builtin returns every embedded provider module, keyed by provider name.
// The map is a fresh copy every call, so callers cannot damage the binaries'
// source of truth.
func Builtin() map[string]string {
	return map[string]string{
		"opencode-go":     opencodeGo,
		"zai-coding-plan": zaiCodingPlan,
	}
}
