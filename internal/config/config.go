// Package config resolves pair's settings: environment variables with the
// defaults documented in PRD.md, plus the repository root and state
// directories derived from the current working directory.
package config

import (
	"crypto/sha1"
	"encoding/hex"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/fabioaraujo121/pair/internal/agent"
)

const (
	DefaultVoice        = "Samantha"
	DefaultRate         = 190
	DefaultLang         = "en"
	DefaultLogWidth     = "36%" // presence and activity, right column
	DefaultClaudeHeight = "45%" // Claude Code's pane when it is shown; it is hidden by default
	DefaultPresence     = "5"   // lines of the presence animation
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
	RunDir   string // ~/.pair/run/<session>: pids of the processes that speak and record

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
	LogWidth     string  // PAIR_LOG_WIDTH
	Agent        string  // PAIR_AGENT: the coding agent, "claude" or "codex"; from the environment or ~/.pair/config only
	ClaudeHeight string  // PAIR_CLAUDE_HEIGHT
	ClaudeShown  bool    // PAIR_CLAUDE=shown starts with Claude's pane open
	ClaudeArgs   string  // PAIR_CLAUDE_ARGS: extra arguments for the claude command
	Presence     string  // PAIR_PRESENCE_HEIGHT
	AskMic       string  // PAIR_ASK_MIC: manual (the engineer opens the mic) or auto
	AskWait      int     // PAIR_ASK_WAIT seconds
	Cue          bool    // PAIR_CUE=off disables the attention sound before speech
	Ack          bool    // PAIR_ACK=off disables the spoken acknowledgement after you talk
	CueIdle      int     // PAIR_CUE_IDLE seconds
	KokoroVoice  string  // PAIR_KOKORO_VOICE; by language when empty
	KokoroSpeed  float64 // PAIR_KOKORO_SPEED, 1.0 is the natural pace
	KokoroModel  string  // PAIR_KOKORO_MODEL: file name in ~/.pair/kokoro
	TTS          string  // PAIR_TTS: text-to-speech adapter (say, kokoro)
	STT          string  // PAIR_STT: speech-to-text adapter (whisper)
	Mute         bool    // PAIR_MUTE: log narrations without speaking or listening
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
		repoOnly(readFile(filepath.Join(stateDir, "config"))),
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
	session := str("PAIR_SESSION", "")
	if !validName.MatchString(session) {
		session = SessionFor(root)
	}
	num := func(name string, def int) int {
		if n, err := strconv.Atoi(str(name, "")); err == nil {
			return n
		}
		return def
	}
	agentName := strings.ToLower(str("PAIR_AGENT", agent.Default))
	if !agent.Known(agentName) {
		agentName = agent.Default
	}
	return Config{
		Agent:        agentName,
		Root:         root,
		StateDir:     stateDir,
		UserDir:      userDir,
		RunDir:       filepath.Join(userDir, "run", session),
		Session:      session,
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
		ClaudeShown:  str("PAIR_CLAUDE", "hidden") == "shown",
		ClaudeArgs:   strings.TrimSpace(str("PAIR_CLAUDE_ARGS", "")),
		Presence:     str("PAIR_PRESENCE_HEIGHT", DefaultPresence),
		AskMic:       str("PAIR_ASK_MIC", "manual"),
		AskWait:      num("PAIR_ASK_WAIT", DefaultAskWait),
		Cue:          str("PAIR_CUE", "on") != "off",
		Ack:          str("PAIR_ACK", "on") != "off",
		CueIdle:      num("PAIR_CUE_IDLE", DefaultCueIdle),
		KokoroVoice:  str("PAIR_KOKORO_VOICE", ""),
		KokoroSpeed:  float(str("PAIR_KOKORO_SPEED", ""), 1.0),
		KokoroModel:  str("PAIR_KOKORO_MODEL", "kokoro-v1.0.onnx"),
		TTS:          str("PAIR_TTS", "say"),
		STT:          str("PAIR_STT", "whisper"),
		Mute:         str("PAIR_MUTE", "") != "",
	}
}

func float(s string, def float64) float64 {
	if v, err := strconv.ParseFloat(s, 64); err == nil && v > 0 {
		return v
	}
	return def
}

// Active reports whether the process runs inside a pair session. `pair
// start` sets PAIR_ACTIVE for everything it launches; Claude Code started
// any other way in the same repository must not write to the session.
func Active() bool { return os.Getenv("PAIR_ACTIVE") != "" }

// A repository can be someone else's. Its .pair/config may set how the
// session sounds and looks; it may not set what is run, where files are
// written, or whether the microphone opens by itself. Those come from the
// environment and from ~/.pair/config only.
var repoSettings = map[string]bool{
	"PAIR_LANG": true, "PAIR_VOICE": true, "PAIR_RATE": true,
	"PAIR_KOKORO_VOICE": true, "PAIR_KOKORO_SPEED": true,
	"PAIR_HIGHLIGHT_TTL": true, "PAIR_DIFF_LIMIT": true,
	"PAIR_SILENCE_SECS": true, "PAIR_SILENCE_LEVEL": true,
	"PAIR_ASK_MAX": true, "PAIR_ASK_WAIT": true,
	"PAIR_CUE": true, "PAIR_CUE_IDLE": true, "PAIR_ACK": true,
	"PAIR_LOG_WIDTH": true, "PAIR_CLAUDE_HEIGHT": true, "PAIR_PRESENCE_HEIGHT": true,
	"PAIR_CLAUDE": true,
}

// repoOnly keeps the settings a repository is allowed to carry.
func repoOnly(all map[string]string) map[string]string {
	kept := map[string]string{}
	for k, v := range all {
		if repoSettings[k] {
			kept[k] = v
		}
	}
	return kept
}

// validName is what a tmux session name may be: it ends up in tmux targets
// and in a command line run by a key binding.
var validName = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

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
		out[strings.TrimSpace(k)] = unquote(strings.TrimSpace(v))
	}
	return out
}

// unquote removes one pair of quotes around a value. Quotes inside the
// value, or a lone one at either end, belong to it.
func unquote(v string) string {
	if len(v) >= 2 && (v[0] == '"' || v[0] == '\'') && v[len(v)-1] == v[0] {
		return v[1 : len(v)-1]
	}
	return v
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

// SessionFor is the name of the tmux session of a repository. Every
// repository has its own, so a command run in one repository never acts on
// the session of another, and several can be open at once. The name holds
// the directory name, to be recognizable, and a few characters of a hash
// of the path, so two directories called the same do not collide.
func SessionFor(root string) string {
	var b strings.Builder
	for _, r := range filepath.Base(root) {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteRune('-') // tmux gives a meaning to "." and ":" in names
		}
	}
	name := strings.Trim(b.String(), "-")
	if name == "" {
		name = "repo"
	}
	if len(name) > 24 {
		name = name[:24]
	}
	sum := sha1.Sum([]byte(root))
	return "pair-" + name + "-" + hex.EncodeToString(sum[:])[:4]
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
