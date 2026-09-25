package nvim

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestVimQuoting(t *testing.T) {
	if got := vimString("it's"); got != "'it''s'" {
		t.Fatalf("vimString = %s", got)
	}
	got := vimList([]any{`/tmp/a "b"\c.go`, 3, 7})
	want := `["/tmp/a \"b\"\\c.go", 3, 7]`
	if got != want {
		t.Fatalf("vimList = %s, want %s", got, want)
	}
}

// startNvim launches a headless Neovim with only pair.nvim on the runtime
// path, listening on a short socket, and returns the socket and a stop func.
func startNvim(t *testing.T) (string, func()) {
	t.Helper()
	nvimBin, err := exec.LookPath("nvim")
	if err != nil {
		t.Skip("nvim not on PATH")
	}
	_, thisFile, _, _ := runtime.Caller(0)
	pluginDir := filepath.Join(filepath.Dir(thisFile), "..", "..", "nvim")
	dir, err := os.MkdirTemp("", "pairnv")
	if err != nil {
		t.Fatal(err)
	}
	sock := filepath.Join(dir, "nvim.sock")
	cmd := exec.Command(nvimBin, "--headless", "--clean", "--cmd", "set rtp^="+pluginDir, "--listen", sock)
	cmd.Stdout, cmd.Stderr = os.Stderr, os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(sock); err == nil {
			break
		}
		if time.Now().After(deadline) {
			_ = cmd.Process.Kill()
			t.Fatal("nvim socket did not appear")
		}
		time.Sleep(50 * time.Millisecond)
	}
	return sock, func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
		_ = os.RemoveAll(dir)
	}
}

func writeLines(t *testing.T, path string, n int) {
	t.Helper()
	var b strings.Builder
	for i := 1; i <= n; i++ {
		fmt.Fprintf(&b, "line %d\n", i)
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestShowAndReloadOverRPC(t *testing.T) {
	sock, stop := startNvim(t)
	defer stop()
	testShowAndReload(t, &Client{Addr: sock, Timeout: 3 * time.Second, Transport: "rpc"})
}

func TestShowAndReloadOverCLI(t *testing.T) {
	sock, stop := startNvim(t)
	defer stop()
	testShowAndReload(t, &Client{Addr: sock, Timeout: 5 * time.Second, Transport: "cli"})
}

func testShowAndReload(t *testing.T, c *Client) {
	t.Helper()
	ctx := context.Background()
	file := filepath.Join(t.TempDir(), "sample.txt")
	writeLines(t, file, 20)

	if v, err := c.Ping(ctx); err != nil || !strings.Contains(v, "pair.nvim loaded") {
		t.Fatalf("ping: %q, %v", v, err)
	}

	start := time.Now()
	if _, err := c.Show(ctx, file, 5, 7); err != nil {
		t.Fatalf("show: %v", err)
	}
	t.Logf("show took %s", time.Since(start))

	// Verify through the API, not through the return value.
	rpc := &Client{Addr: c.Addr, Timeout: 3 * time.Second, Transport: "rpc"}
	var cursor []int
	if err := rpc.ExecLua(ctx, "return vim.api.nvim_win_get_cursor(0)", &cursor); err != nil {
		t.Fatal(err)
	}
	if len(cursor) != 2 || cursor[0] != 5 {
		t.Fatalf("cursor = %v, want line 5", cursor)
	}
	var marks int
	if err := rpc.ExecLua(ctx, "local ns = vim.api.nvim_create_namespace('pair'); return #vim.api.nvim_buf_get_extmarks(0, ns, 0, -1, {})", &marks); err != nil {
		t.Fatal(err)
	}
	if marks != 3 {
		t.Fatalf("extmarks = %d, want 3 (lines 5..7)", marks)
	}
	var name string
	if err := rpc.ExecLua(ctx, "return vim.api.nvim_buf_get_name(0)", &name); err != nil {
		t.Fatal(err)
	}
	wantName, _ := filepath.EvalSymlinks(file) // Neovim resolves /var -> /private/var
	if name != wantName {
		t.Fatalf("buffer name = %s, want %s", name, wantName)
	}

	// Change the file from outside and reload: no prompt, new line count.
	f, err := os.OpenFile(file, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.WriteString("line 21\n")
	_ = f.Close()
	if err := c.Reload(ctx); err != nil {
		t.Fatalf("reload: %v", err)
	}
	var lines int
	if err := rpc.ExecLua(ctx, "return vim.api.nvim_buf_line_count(0)", &lines); err != nil {
		t.Fatal(err)
	}
	if lines != 21 {
		t.Fatalf("line count after reload = %d, want 21", lines)
	}

	// A dirty buffer must NOT be reloaded, and must not block.
	if err := rpc.ExecLua(ctx, "vim.api.nvim_buf_set_lines(0, 0, 1, false, {'edited locally'})", nil); err != nil {
		t.Fatal(err)
	}
	f, _ = os.OpenFile(file, os.O_APPEND|os.O_WRONLY, 0o644)
	_, _ = f.WriteString("line 22\n")
	_ = f.Close()
	start = time.Now()
	if err := c.Reload(ctx); err != nil {
		t.Fatalf("reload (dirty): %v", err)
	}
	if time.Since(start) > time.Second {
		t.Fatalf("reload on a dirty buffer blocked for %s", time.Since(start))
	}
	var first string
	_ = rpc.ExecLua(ctx, "return vim.api.nvim_buf_get_lines(0, 0, 1, false)[1]", &first)
	if first != "edited locally" {
		t.Fatalf("dirty buffer was reloaded; first line = %q", first)
	}
}
