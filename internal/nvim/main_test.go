package nvim

import (
	"os"
	"testing"

	"github.com/fabioaraujo121/pair/internal/testenv"
)

// TestMain cuts the tests off from the developer's settings, home
// directory and live pairing session before any of them runs.
func TestMain(m *testing.M) {
	cleanup := testenv.Isolate()
	code := m.Run()
	cleanup()
	os.Exit(code)
}
