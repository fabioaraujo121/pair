package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
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
