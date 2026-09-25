// Package config resolves pair's settings: environment variables with the
// defaults documented in PRD.md, plus the repository root and state
// directories derived from the current working directory.
package config

import (
	"crypto/sha1"
	"encoding/hex"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const (
	DefaultSession      = "pair"
	DefaultVoice        = "Luciana"
	DefaultRate         = 190
	DefaultLang         = "pt"
	DefaultHighlightTTL = 20
	DefaultDiffLimit    = 200
	DefaultAskMax       = 60
	DefaultSilenceSecs  = "1.5" // trailing silence that ends a recording
	DefaultSilenceLevel = "1%"  // amplitude below which sox counts silence

	// MaxSockPath is the sun_path limit on macOS: nvim --listen and Go's
	// net.Dial both refuse longer unix socket paths.
	MaxSockPath = 104
)

// Config is the fully resolved configuration for one invocation.
type Config struct {
	Root     string // repository root (directory containing .git) or the cwd
	StateDir string // <Root>/.pair
	UserDir  string // ~/.pair

	Session      string
	Sock         string
	HighlightTTL int
	DiffLimit    int
	Voice        string
	Rate         int
	Lang         string
	WhisperModel string
	AskMax       int
	SilenceSecs  string
	SilenceLevel string
	Mute         bool // PAIR_MUTE: log narrations without speaking or listening
}

// Load resolves the configuration for a process running in cwd. An empty cwd
// means the process's working directory.
func Load(cwd string) Config {
	if cwd == "" {
		cwd, _ = os.Getwd()
	}
	root := RepoRoot(cwd)
	home, _ := os.UserHomeDir()
	userDir := filepath.Join(home, ".pair")
	return Config{
		Root:         root,
		StateDir:     filepath.Join(root, ".pair"),
		UserDir:      userDir,
		Session:      envStr("PAIR_SESSION", DefaultSession),
		Sock:         envStr("PAIR_SOCK", DefaultSock(root)),
		HighlightTTL: envInt("PAIR_HIGHLIGHT_TTL", DefaultHighlightTTL),
		DiffLimit:    envInt("PAIR_DIFF_LIMIT", DefaultDiffLimit),
		Voice:        envStr("PAIR_VOICE", DefaultVoice),
		Rate:         envInt("PAIR_RATE", DefaultRate),
		Lang:         envStr("PAIR_LANG", DefaultLang),
		WhisperModel: envStr("PAIR_WHISPER_MODEL", filepath.Join(userDir, "models", "ggml-large-v3-turbo.bin")),
		AskMax:       envInt("PAIR_ASK_MAX", DefaultAskMax),
		SilenceSecs:  envStr("PAIR_SILENCE_SECS", DefaultSilenceSecs),
		SilenceLevel: envStr("PAIR_SILENCE_LEVEL", DefaultSilenceLevel),
		Mute:         os.Getenv("PAIR_MUTE") != "",
	}
}

// RepoRoot walks up from dir looking for a .git entry (directory or file, so
// worktrees count). It returns dir itself when none is found. No git
// subprocess: hooks call this on every edit.
func RepoRoot(dir string) string {
	cur := dir
	for {
		if _, err := os.Stat(filepath.Join(cur, ".git")); err == nil {
			return cur
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return dir
		}
		cur = parent
	}
}

// DefaultSock is the Neovim listen address for a repository: a short path in
// $TMPDIR keyed by a hash of the root, so it stays under MaxSockPath however
// deep the repository lives.
func DefaultSock(root string) string {
	sum := sha1.Sum([]byte(root))
	name := "pair-" + hex.EncodeToString(sum[:])[:8] + ".sock"
	dir := strings.TrimRight(os.Getenv("TMPDIR"), "/")
	if dir == "" {
		dir = "/tmp"
	}
	return filepath.Join(dir, name)
}

// IsTCP reports whether addr is a host:port listen address rather than a
// unix socket path.
func IsTCP(addr string) bool { return strings.Contains(addr, ":") }

// SockTooLong reports whether a unix socket path exceeds the macOS limit.
func SockTooLong(addr string) bool { return !IsTCP(addr) && len(addr) > MaxSockPath }

func envStr(name, def string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return def
}

func envInt(name string, def int) int {
	if v := os.Getenv(name); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}
