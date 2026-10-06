package agent

import (
	"os"
	"testing"

	"github.com/fabioaraujo121/pair/internal/testenv"
)

func TestMain(m *testing.M) {
	cleanup := testenv.Isolate()
	code := m.Run()
	cleanup()
	os.Exit(code)
}
