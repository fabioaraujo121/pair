// Package testenv cuts a test binary off from the machine it runs on.
//
// pair reads the environment, ~/.pair/config and the repository's own
// config, talks to a tmux server, plays and records sound, and signals
// processes. A test that inherits the developer's environment can change
// outcome with their settings, and, worse, can reach their live pairing
// session: type into it, change its status bar, stop it. Every test
// package calls Isolate from TestMain before anything else runs.
package testenv

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/fabioaraujo121/pair/internal/tmux"
)

var (
	// RealHome is the developer's home directory, for the tests that are
	// asked to run the real engines installed there.
	RealHome string
	// RealAudio is set by PAIR_REAL_AUDIO=1: run the tests that load the
	// real speech models. They are slow and depend on what is installed.
	RealAudio bool
)

// Isolate gives the process a home directory of its own, removes every
// PAIR_ variable, points tmux at a server that belongs to this process,
// and keeps git away from the developer's configuration. It returns what
// undoes the parts that leave something behind.
func Isolate() (cleanup func()) {
	RealHome, _ = os.UserHomeDir()
	RealAudio = os.Getenv("PAIR_REAL_AUDIO") != ""

	// The Go tool finds its caches through HOME: name them before HOME
	// changes, or a test that builds the binary compiles everything anew.
	if out, err := exec.Command("go", "env", "-json", "GOCACHE", "GOMODCACHE", "GOPATH").Output(); err == nil {
		var env map[string]string
		if json.Unmarshal(out, &env) == nil {
			for k, v := range env {
				if v != "" {
					_ = os.Setenv(k, v)
				}
			}
		}
	}

	for _, kv := range os.Environ() {
		if name, _, ok := strings.Cut(kv, "="); ok && strings.HasPrefix(name, "PAIR_") {
			_ = os.Unsetenv(name)
		}
	}
	_ = os.Unsetenv("TMUX")

	home, err := os.MkdirTemp("", "pairhome")
	if err != nil {
		panic(err)
	}
	_ = os.Setenv("HOME", home)
	_ = os.Setenv("XDG_CONFIG_HOME", home+"/.config")
	_ = os.Setenv("XDG_STATE_HOME", home+"/.local/state")
	_ = os.Setenv("XDG_DATA_HOME", home+"/.local/share")
	_ = os.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	_ = os.Setenv("GIT_CONFIG_NOSYSTEM", "1")

	socket := fmt.Sprintf("pairtest-%d", os.Getpid())
	_ = os.Setenv("PAIR_TMUX_SOCKET", socket)
	tmux.Socket = socket

	return func() {
		_ = exec.Command("tmux", "-L", socket, "kill-server").Run()
		_ = os.RemoveAll(home)
	}
}
