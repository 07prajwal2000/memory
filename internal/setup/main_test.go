package setup

import (
	"os"
	"testing"

	"memory/internal/testenv"
)

// TestMain isolates the whole test binary from the real home and config. Without
// this, a test that passes empty paths makes Run resolve the platform defaults
// and write fake models, a fake llama-server and a config into the real ~/.mem.
func TestMain(m *testing.M) {
	os.Exit(testenv.Main(m))
}
