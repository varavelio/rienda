//go:build e2e

package e2e

import (
	"os"
	"testing"

	"github.com/varavelio/rienda/e2e/harness"
)

// TestMain compiles the binary under test and runs the suite against it.
func TestMain(m *testing.M) {
	os.Exit(harness.Main(m))
}
