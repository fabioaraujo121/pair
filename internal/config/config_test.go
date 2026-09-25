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
