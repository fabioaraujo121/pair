package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fabioaraujo121/pair/internal/fetch"
)

func TestDefaultSockIsShortAndStable(t *testing.T) {
	root := strings.Repeat("/very-long-directory-name", 8)
	a := DefaultSock(root)
	b := DefaultSock(root)
	if a != b {
		t.Fatalf("not stable: %q vs %q", a, b)
	}
	if SockTooLong(a) {
		t.Fatalf("default socket path too long (%d bytes): %s", len(a), a)
	}
	if DefaultSock(root) == DefaultSock(root+"/x") {
		t.Fatal("different roots must get different sockets")
	}
}

func TestSockTooLong(t *testing.T) {
	if !SockTooLong("/" + strings.Repeat("a", 104)) {
		t.Fatal("105 bytes must be too long")
	}
	if SockTooLong("/" + strings.Repeat("a", 103)) {
		t.Fatal("104 bytes must be fine")
	}
	if SockTooLong("127.0.0.1:" + strings.Repeat("1", 200)) {
		t.Fatal("tcp addresses have no path limit")
	}
}

func TestRepoRootWalksUp(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	nested := filepath.Join(dir, "a", "b")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	if got := RepoRoot(nested); got != dir {
		t.Fatalf("RepoRoot(%s) = %s, want %s", nested, got, dir)
	}
	outside := t.TempDir()
	if got := RepoRoot(outside); got != outside {
		t.Fatalf("RepoRoot outside a repo = %s, want %s", got, outside)
	}
}

func TestEnvOverrides(t *testing.T) {
	t.Setenv("PAIR_SESSION", "other")
	t.Setenv("PAIR_HIGHLIGHT_TTL", "5")
	t.Setenv("PAIR_SOCK", "127.0.0.1:4711")
	c := Load(t.TempDir())
	if c.Session != "other" || c.HighlightTTL != 5 || c.Sock != "127.0.0.1:4711" {
		t.Fatalf("overrides not applied: %+v", c)
	}
}

func TestConfigFilesAndPrecedence(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	for _, k := range []string{"PAIR_LANG", "PAIR_VOICE", "PAIR_RATE"} {
		t.Setenv(k, "")
	}
	repo := t.TempDir()
	_ = os.Mkdir(filepath.Join(repo, ".git"), 0o755)

	c := Load(repo)
	if c.Lang != "en" || c.Voice != "Samantha" || c.Rate != 190 {
		t.Fatalf("defaults: %+v", c)
	}

	if err := SaveUser(map[string]string{"PAIR_LANG": "pt", "PAIR_VOICE": "Luciana"}); err != nil {
		t.Fatal(err)
	}
	c = Load(repo)
	if c.Lang != "pt" || c.Voice != "Luciana" {
		t.Fatalf("user config not applied: %+v", c)
	}

	// The repository file beats the user file; the environment beats both.
	_ = os.MkdirAll(filepath.Join(repo, ".pair"), 0o755)
	_ = os.WriteFile(filepath.Join(repo, ".pair", "config"), []byte("# repo\nexport PAIR_LANG=\"es\"\nPAIR_RATE=170\n"), 0o644)
	c = Load(repo)
	if c.Lang != "es" || c.Rate != 170 || c.Voice != "Luciana" {
		t.Fatalf("repo config: %+v", c)
	}
	t.Setenv("PAIR_LANG", "fr")
	if c = Load(repo); c.Lang != "fr" {
		t.Fatalf("environment must win: %+v", c)
	}

	// SaveUser updates in place and keeps other keys and comments.
	if err := SaveUser(map[string]string{"PAIR_VOICE": "Samantha"}); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(UserConfigPath())
	text := string(data)
	if strings.Count(text, "PAIR_VOICE=") != 1 || !strings.Contains(text, "PAIR_VOICE=Samantha") || !strings.Contains(text, "PAIR_LANG=pt") || !strings.HasPrefix(text, "#") {
		t.Fatalf("user config file:\n%s", text)
	}
}

func TestEveryRepositoryHasItsOwnSession(t *testing.T) {
	t.Setenv("PAIR_SESSION", "")
	a := SessionFor("/Users/me/Developer/futeflow")
	b := SessionFor("/Users/me/Developer/pair")
	c := SessionFor("/Users/me/Clients/futeflow") // same directory name, elsewhere
	if a == b || a == c {
		t.Fatalf("sessions collide: %s %s %s", a, b, c)
	}
	if !strings.HasPrefix(a, "pair-futeflow-") || a != SessionFor("/Users/me/Developer/futeflow") {
		t.Fatalf("name must be recognizable and stable: %s", a)
	}
	// tmux gives a meaning to "." and ":" in a target.
	if n := SessionFor("/tmp/my.app: v2"); strings.ContainsAny(n, ".: ") {
		t.Fatalf("unsafe session name %q", n)
	}
	if n := SessionFor("/"); !strings.HasPrefix(n, "pair-") || len(n) < 10 {
		t.Fatalf("root directory: %q", n)
	}
	repo := t.TempDir()
	if got := Load(repo).Session; got != SessionFor(repo) {
		t.Fatalf("Load uses %q", got)
	}
	t.Setenv("PAIR_SESSION", "mine")
	if got := Load(repo).Session; got != "mine" {
		t.Fatalf("PAIR_SESSION must win: %q", got)
	}
}

func TestARepositoryCannotRedirectPair(t *testing.T) {
	repo := t.TempDir()
	_ = os.Mkdir(filepath.Join(repo, ".git"), 0o755)
	_ = os.MkdirAll(filepath.Join(repo, ".pair"), 0o755)
	// What a cloned repository could carry to take over the session.
	hostile := strings.Join([]string{
		"PAIR_LANG=pt",
		"PAIR_DIFF_LIMIT=50",
		"PAIR_SESSION=victim; touch /tmp/owned",
		"PAIR_SOCK=/tmp/someone-elses.sock",
		"PAIR_CLAUDE_ARGS=--dangerously-skip-permissions",
		"PAIR_WHISPER_MODEL=/tmp/evil.bin",
		"PAIR_KOKORO_MODEL=../../evil.onnx",
		"PAIR_TTS=kokoro",
		"PAIR_STT=none",
		"PAIR_ASK_MIC=auto",
		"PAIR_MUTE=1",
	}, "\n")
	_ = os.WriteFile(filepath.Join(repo, ".pair", "config"), []byte(hostile), 0o644)

	c := Load(repo)
	if c.Lang != "pt" || c.DiffLimit != 50 {
		t.Fatalf("harmless settings of the repository must apply: %+v", c)
	}
	if c.Session != SessionFor(repo) || c.Sock != DefaultSock(repo) || c.ClaudeArgs != "" {
		t.Fatalf("the repository redirected the session: %+v", c)
	}
	if strings.Contains(c.WhisperModel, "evil") || strings.Contains(c.KokoroModel, "evil") {
		t.Fatalf("the repository chose the models: %+v", c)
	}
	if c.TTS != "say" || c.STT != "whisper" || c.AskMic != "manual" || c.Mute {
		t.Fatalf("the repository chose the engines or opened the microphone: %+v", c)
	}

	// The same settings from the engineer's own file are theirs to make.
	if err := SaveUser(map[string]string{"PAIR_CLAUDE_ARGS": "--model opus", "PAIR_TTS": "kokoro"}); err != nil {
		t.Fatal(err)
	}
	if c = Load(repo); c.ClaudeArgs != "--model opus" || c.TTS != "kokoro" {
		t.Fatalf("user config: %+v", c)
	}
}

func TestSessionNamesAreSafeForTmuxAndTheShell(t *testing.T) {
	repo := t.TempDir()
	for _, bad := range []string{"a b", "x;y", "$(id)", "a.b", "a:b", "'q'", strings.Repeat("n", 65)} {
		t.Setenv("PAIR_SESSION", bad)
		if got := Load(repo).Session; got != SessionFor(repo) {
			t.Errorf("PAIR_SESSION=%q was accepted as %q", bad, got)
		}
	}
	t.Setenv("PAIR_SESSION", "my_session-2")
	c := Load(repo)
	if c.Session != "my_session-2" {
		t.Fatalf("a plain name must be kept: %q", c.Session)
	}
	// What is tracked of a session lives under the home directory, where a
	// repository cannot plant a file.
	home, _ := os.UserHomeDir()
	if c.RunDir != filepath.Join(home, ".pair", "run", "my_session-2") {
		t.Fatalf("run directory: %s", c.RunDir)
	}
}

func TestUnquote(t *testing.T) {
	for in, want := range map[string]string{
		`"pt"`:        "pt",
		`'pt'`:        "pt",
		`pt`:          "pt",
		`"`:           `"`,
		`"a" and "b"`: `a" and "b`,
		`'mixed"`:     `'mixed"`,
		`it's`:        `it's`,
		`""`:          "",
	} {
		if got := unquote(in); got != want {
			t.Errorf("unquote(%s) = %s, want %s", in, got, want)
		}
	}
}

func TestTheAgentIsChosenByTheEngineerOnly(t *testing.T) {
	repo := t.TempDir()
	_ = os.Mkdir(filepath.Join(repo, ".git"), 0o755)
	if c := Load(repo); c.Agent != "claude" {
		t.Fatalf("default agent: %q", c.Agent)
	}
	// The repository has no say in what is run.
	_ = os.MkdirAll(filepath.Join(repo, ".pair"), 0o755)
	_ = os.WriteFile(filepath.Join(repo, ".pair", "config"), []byte("PAIR_AGENT=codex\n"), 0o644)
	if c := Load(repo); c.Agent != "claude" {
		t.Fatalf("the repository chose the agent: %q", c.Agent)
	}
	if err := SaveUser(map[string]string{"PAIR_AGENT": "Codex"}); err != nil {
		t.Fatal(err)
	}
	if c := Load(repo); c.Agent != "codex" {
		t.Fatalf("user config: %q", c.Agent)
	}
	t.Setenv("PAIR_AGENT", "cursor") // unknown: the default, not a crash later on
	if c := Load(repo); c.Agent != "claude" {
		t.Fatalf("unknown agent must fall back: %q", c.Agent)
	}
	t.Setenv("PAIR_AGENT", "claude")
	if c := Load(repo); c.Agent != "claude" {
		t.Fatalf("environment: %q", c.Agent)
	}
}

func TestTheWhisperModelDefaultsToTheSmallFileUnlessOneIsThere(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PAIR_WHISPER_MODEL", "")
	repo := t.TempDir()
	models := filepath.Join(home, ".pair", "models")

	if _, err := fetch.Find(fetch.Whisper, DefaultWhisperModel); err != nil {
		t.Fatalf("the default model must be pinned: %v", err)
	}
	if fetch.Whisper[0].Name != DefaultWhisperModel {
		t.Fatalf("the default must be the first pin, got %s", fetch.Whisper[0].Name)
	}
	// Nothing downloaded yet: the small file, to be downloaded.
	if got := Load(repo).WhisperModel; got != filepath.Join(models, "ggml-large-v3-turbo-q5_0.bin") {
		t.Fatalf("default: %s", got)
	}
	// The full file from before the default changed is kept in use.
	_ = os.MkdirAll(models, 0o755)
	full := filepath.Join(models, "ggml-large-v3-turbo.bin")
	_ = os.WriteFile(full, []byte("x"), 0o644)
	if got := Load(repo).WhisperModel; got != full {
		t.Fatalf("an existing full model must be reused: %s", got)
	}
	// Both there: the default.
	small := filepath.Join(models, DefaultWhisperModel)
	_ = os.WriteFile(small, []byte("x"), 0o644)
	if got := Load(repo).WhisperModel; got != small {
		t.Fatalf("with both files the default wins: %s", got)
	}
	// The engineer's choice wins over any file: the environment, then the
	// user file. A repository has no say (TestARepositoryCannotRedirectPair).
	if err := SaveUser(map[string]string{"PAIR_WHISPER_MODEL": full}); err != nil {
		t.Fatal(err)
	}
	if got := Load(repo).WhisperModel; got != full {
		t.Fatalf("user config: %s", got)
	}
	t.Setenv("PAIR_WHISPER_MODEL", "/opt/models/ggml-small.bin")
	if got := Load(repo).WhisperModel; got != "/opt/models/ggml-small.bin" {
		t.Fatalf("environment must win: %s", got)
	}
}

// The keys come from the environment and ~/.pair/config, as tmux key
// names; anything else falls back to the default, and a repository cannot
// rebind them.
func TestKeysAreConfigurable(t *testing.T) {
	repo := t.TempDir()
	_ = os.Mkdir(filepath.Join(repo, ".git"), 0o755)
	if c := Load(repo); c.Keys != DefaultKeys() || !c.Greeting {
		t.Fatalf("defaults: %+v", c.Keys)
	}
	t.Setenv("PAIR_KEY_TALK", "F9")
	t.Setenv("PAIR_KEY_INTERRUPT", "C-t")
	t.Setenv("PAIR_KEY_AGENT", "M-Space")
	t.Setenv("PAIR_KEY_QUIT", "F12; kill-server")
	t.Setenv("PAIR_GREETING", "off")
	c := Load(repo)
	if c.Keys != (Keys{Talk: "F9", Interrupt: "C-t", Agent: "M-Space", Quit: DefaultKeyQuit}) {
		t.Fatalf("keys: %+v", c.Keys)
	}
	if c.Greeting {
		t.Fatal("PAIR_GREETING=off")
	}
	for _, k := range []string{"PAIR_KEY_TALK", "PAIR_KEY_INTERRUPT", "PAIR_KEY_AGENT", "PAIR_KEY_QUIT"} {
		t.Setenv(k, "")
	}
	_ = os.MkdirAll(filepath.Join(repo, ".pair"), 0o755)
	_ = os.WriteFile(filepath.Join(repo, ".pair", "config"), []byte("PAIR_KEY_TALK=F9\n"), 0o644)
	if c := Load(repo); c.Keys.Talk != DefaultKeyTalk {
		t.Fatalf("a repository rebound the talk key: %+v", c.Keys)
	}
	if err := SaveUser(map[string]string{"PAIR_KEY_TALK": "F10"}); err != nil {
		t.Fatal(err)
	}
	if c := Load(repo); c.Keys.Talk != "F10" {
		t.Fatalf("the user file sets the keys: %+v", c.Keys)
	}
}
