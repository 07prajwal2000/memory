package store

import (
	"os"
	"testing"

	"memory/internal/testenv"
)

// TestMain keeps the whole test binary away from the real home and config.
func TestMain(m *testing.M) {
	os.Exit(testenv.Main(m))
}
