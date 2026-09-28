// Package config resolves pair's settings: environment variables with the
// defaults documented in PRD.md, plus the repository root and state
// directories derived from the current working directory.
package config

import (
	"crypto/sha1"
	"encoding/hex"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

const (
	DefaultSession      = "pair"
	DefaultVoice        = "Samantha"
	DefaultRate         = 190
	DefaultLang         = "en"
	DefaultLogWidth     = "36%" // activity log, right column
	DefaultClaudeHeight = "30%" // Claude Code strip under the editor
	DefaultLogDiffLines = 30    // diff lines shown per edit in the activity log
	DefaultCueIdle      = 6     // seconds of quiet after which speech is announced
	DefaultAskWait      = 180   // seconds a question waits for the engineer to open the mic
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
	LogWidth     string // PAIR_LOG_WIDTH
	ClaudeHeight string // PAIR_CLAUDE_HEIGHT
	LogDiffLines int    // PAIR_LOG_DIFF_LINES, 0 = only the summary line
	AskMic       string // PAIR_ASK_MIC: manual (the engineer opens the mic) or auto
	AskWait      int    // PAIR_ASK_WAIT seconds
	Cue          bool   // PAIR_CUE=off disables the attention sound before speech
	CueIdle      int    // PAIR_CUE_IDLE seconds
	TTS          string // PAIR_TTS: text-to-speech adapter (say)
	STT          string // PAIR_STT: speech-to-text adapter (whisper)
	Mute         bool   // PAIR_MUTE: log narrations without speaking or listening
}

// Load resolves the configuration for a process running in cwd. An empty cwd
// means the process's working directory.
//
// Each setting comes from, in order: the environment, <repo>/.pair/config,
// ~/.pair/config, the default. The files hold KEY=VALUE lines with the same
// names as the environment variables.
func Load(cwd string) Config {
	if cwd == "" {
		cwd, _ = os.Getwd()
	}
	root := RepoRoot(cwd)
	home, _ := os.UserHomeDir()
	userDir := filepath.Join(home, ".pair")
	stateDir := filepath.Join(root, ".pair")
	files := []map[string]string{
		readFile(filepath.Join(stateDir, "config")),
		readFile(filepath.Join(userDir, "config")),
	}
	str := func(name, def string) string {
		if v := os.Getenv(name); v != "" {
			return v
		}
		for _, f := range files {
			if v := f[name]; v != "" {
				return v
			}
		}
		return def
	}
	num := func(name string, def int) int {
		if n, err := strconv.Atoi(str(name, "")); err == nil {
			return n
		}
		return def
	}
	return Config{
		Root:         root,
		StateDir:     stateDir,
		UserDir:      userDir,
		Session:      str("PAIR_SESSION", DefaultSession),
		Sock:         str("PAIR_SOCK", DefaultSock(root)),
		HighlightTTL: num("PAIR_HIGHLIGHT_TTL", DefaultHighlightTTL),
		DiffLimit:    num("PAIR_DIFF_LIMIT", DefaultDiffLimit),
		Voice:        str("PAIR_VOICE", DefaultVoice),
		Rate:         num("PAIR_RATE", DefaultRate),
		Lang:         str("PAIR_LANG", DefaultLang),
		WhisperModel: str("PAIR_WHISPER_MODEL", filepath.Join(userDir, "models", "ggml-large-v3-turbo.bin")),
		AskMax:       num("PAIR_ASK_MAX", DefaultAskMax),
		SilenceSecs:  str("PAIR_SILENCE_SECS", DefaultSilenceSecs),
		SilenceLevel: str("PAIR_SILENCE_LEVEL", DefaultSilenceLevel),
		LogWidth:     str("PAIR_LOG_WIDTH", DefaultLogWidth),
		ClaudeHeight: str("PAIR_CLAUDE_HEIGHT", DefaultClaudeHeight),
		LogDiffLines: num("PAIR_LOG_DIFF_LINES", DefaultLogDiffLines),
		AskMic:       str("PAIR_ASK_MIC", "manual"),
		AskWait:      num("PAIR_ASK_WAIT", DefaultAskWait),
		Cue:          str("PAIR_CUE", "on") != "off",
		CueIdle:      num("PAIR_CUE_IDLE", DefaultCueIdle),
		TTS:          str("PAIR_TTS", "say"),
		STT:          str("PAIR_STT", "whisper"),
		Mute:         str("PAIR_MUTE", "") != "",
	}
}

// UserConfigPath is the per-user settings file.
func UserConfigPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".pair", "config")
}

// readFile parses KEY=VALUE lines; blank lines and # comments are skipped,
// surrounding quotes are removed.
func readFile(path string) map[string]string {
	out := map[string]string{}
	data, err := os.ReadFile(path)
	if err != nil {
		return out
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "export "))
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		out[strings.TrimSpace(k)] = strings.Trim(strings.TrimSpace(v), `"'`)
	}
	return out
}

// SaveUser merges values into ~/.pair/config, keeping comments and the
// other keys.
func SaveUser(values map[string]string) error {
	path := UserConfigPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, _ := os.ReadFile(path)
	var lines []string
	if len(data) > 0 {
		lines = strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	} else {
		lines = []string{"# pair settings. Same names as the environment variables; the environment wins."}
	}
	done := map[string]bool{}
	for i, line := range lines {
		k, _, ok := strings.Cut(strings.TrimSpace(line), "=")
		k = strings.TrimSpace(k)
		if v, set := values[k]; ok && set {
			lines[i] = k + "=" + v
			done[k] = true
		}
	}
	keys := make([]string, 0, len(values))
	for k := range values {
		if !done[k] {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	for _, k := range keys {
		lines = append(lines, k+"="+values[k])
	}
	return os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644)
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
