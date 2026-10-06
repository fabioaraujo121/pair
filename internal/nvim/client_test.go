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
	cmd := exec.Command(nvimBin, "--headless", "--clean", "-n", "--cmd", "set rtp^="+pluginDir, "--listen", sock)
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

func TestDiffIsDrawnInPlace(t *testing.T) {
	sock, stop := startNvim(t)
	defer stop()
	c := &Client{Addr: sock, Timeout: 3 * time.Second, Transport: "rpc"}
	ctx := context.Background()
	file := filepath.Join(t.TempDir(), "sample.txt")
	writeLines(t, file, 20)

	marks := `{"added":[10,11],"removed":[{"at":10,"lines":["old line ten","old line \"eleven\""]}]}`
	if err := c.Diff(ctx, file, marks); err != nil {
		t.Fatalf("diff: %v", err)
	}
	var cursor []int
	if err := c.ExecLua(ctx, "return vim.api.nvim_win_get_cursor(0)", &cursor); err != nil {
		t.Fatal(err)
	}
	if len(cursor) != 2 || cursor[0] != 10 {
		t.Fatalf("cursor = %v, want the first changed line", cursor)
	}
	var summary string
	err := c.ExecLua(ctx, `
		local ns = vim.api.nvim_create_namespace('pair')
		local added, removed = 0, 0
		for _, m in ipairs(vim.api.nvim_buf_get_extmarks(0, ns, 0, -1, { details = true })) do
			local d = m[4]
			if d.line_hl_group == 'PairAdd' then added = added + 1 end
			if d.virt_lines then
				removed = removed + #d.virt_lines
				assert(d.virt_lines_above == true)
				assert(d.virt_lines[1][1][1] == '- old line ten')
			end
		end
		return added .. ' added, ' .. removed .. ' removed'
	`, &summary)
	if err != nil {
		t.Fatal(err)
	}
	if summary != "2 added, 2 removed" {
		t.Fatalf("drawn: %s", summary)
	}

	// The same call through the command-line transport.
	cli := &Client{Addr: sock, Timeout: 5 * time.Second, Transport: "cli"}
	if err := cli.Diff(ctx, file, `{"added":[3],"removed":[]}`); err != nil {
		t.Fatalf("diff over cli: %v", err)
	}
	if err := c.ExecLua(ctx, "return vim.api.nvim_win_get_cursor(0)", &cursor); err != nil || cursor[0] != 3 {
		t.Fatalf("cursor after cli diff = %v %v", cursor, err)
	}
}

// lua runs a chunk and returns what it returns, as a string.
func lua(t *testing.T, c *Client, code string) string {
	t.Helper()
	var out string
	if err := c.ExecLua(context.Background(), code, &out); err != nil {
		t.Fatalf("%s: %v", code, err)
	}
	return out
}

const where = `return vim.fn.fnamemodify(vim.api.nvim_buf_get_name(0), ':t') .. ':' .. vim.api.nvim_win_get_cursor(0)[1] .. ' ' .. vim.api.nvim_get_mode().mode`

// An engineer who is typing keeps the file and the cursor they have. What
// Claude does meanwhile is drawn when it is in the file on screen, and
// never moves anything.
func TestAnEngineerWhoIsTypingIsNotMoved(t *testing.T) {
	sock, stop := startNvim(t)
	defer stop()
	c := &Client{Addr: sock, Timeout: 3 * time.Second, Transport: "rpc"}
	ctx := context.Background()
	dir := t.TempDir()
	mine, other := filepath.Join(dir, "mine.txt"), filepath.Join(dir, "other.txt")
	writeLines(t, mine, 30)
	writeLines(t, other, 30)

	if _, err := c.Show(ctx, mine, 5, 5); err != nil {
		t.Fatal(err)
	}
	if err := c.ExecLua(ctx, "vim.cmd('startinsert')", nil); err != nil {
		t.Fatal(err)
	}
	if got := lua(t, c, where); got != "mine.txt:5 i" {
		t.Fatalf("before: %s", got)
	}

	// Claude points at another file.
	shown, err := c.Show(ctx, other, 12, 14)
	if err != nil {
		t.Fatal(err)
	}
	if shown.Skipped == "" {
		t.Fatalf("the call must say the screen was left alone: %+v", shown)
	}
	if got := lua(t, c, where); got != "mine.txt:5 i" {
		t.Fatalf("the engineer was moved while typing: %s", got)
	}
	// Claude changes another file.
	if err := c.Diff(ctx, other, `{"added":[3],"removed":[]}`); err != nil {
		t.Fatal(err)
	}
	if got := lua(t, c, where); got != "mine.txt:5 i" {
		t.Fatalf("the engineer was moved by a change elsewhere: %s", got)
	}
	// Claude points at the file on screen: it is marked, the cursor stays.
	if shown, err = c.Show(ctx, mine, 20, 22); err != nil || shown.Skipped != "" {
		t.Fatalf("%+v %v", shown, err)
	}
	if got := lua(t, c, where); got != "mine.txt:5 i" {
		t.Fatalf("the cursor moved: %s", got)
	}
	var marks int
	if err := c.ExecLua(ctx, "local ns = vim.api.nvim_create_namespace('pair'); return #vim.api.nvim_buf_get_extmarks(0, ns, 0, -1, {})", &marks); err != nil || marks != 3 {
		t.Fatalf("marks in the file on screen: %d %v", marks, err)
	}
	if err := c.Diff(ctx, mine, `{"added":[25,26],"removed":[]}`); err != nil {
		t.Fatal(err)
	}
	if got := lua(t, c, where); got != "mine.txt:5 i" {
		t.Fatalf("the cursor moved to the change: %s", got)
	}

	// Back in normal mode, everything works as before.
	if err := c.ExecLua(ctx, "vim.cmd('stopinsert')", nil); err != nil {
		t.Fatal(err)
	}
	if shown, err = c.Show(ctx, other, 12, 14); err != nil || shown.Skipped != "" {
		t.Fatalf("%+v %v", shown, err)
	}
	if got := lua(t, c, where); got != "other.txt:12 n" {
		t.Fatalf("after typing: %s", got)
	}
}

// A message longer than the screen makes Neovim wait for a key, and while
// it waits nothing pair asks for is done.
func TestMessagesFitInOneLine(t *testing.T) {
	sock, stop := startNvim(t)
	defer stop()
	c := &Client{Addr: sock, Timeout: 3 * time.Second, Transport: "rpc"}
	ctx := context.Background()
	dir := filepath.Join(t.TempDir(), strings.Repeat("deep/", 12), strings.Repeat("long-name-", 8))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, "sample.txt")
	writeLines(t, file, 5)

	if err := c.ExecLua(ctx, `_G.notes = {}; vim.notify = function(m) table.insert(_G.notes, m) end`, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Show(ctx, file, 1, 1); err != nil {
		t.Fatal(err)
	}
	// Unsaved edits, and the file changes on disk.
	if err := c.ExecLua(ctx, "vim.api.nvim_buf_set_lines(0, 0, 1, false, {'edited locally'})", nil); err != nil {
		t.Fatal(err)
	}
	time.Sleep(1100 * time.Millisecond) // the change is seen by its time, in seconds
	writeLines(t, file, 9)
	if err := c.Reload(ctx); err != nil {
		t.Fatal(err)
	}
	got := lua(t, c, `return #_G.notes .. '|' .. (_G.notes[1] or '') .. '|' .. vim.o.columns`)
	parts := strings.SplitN(got, "|", 3)
	if len(parts) != 3 || parts[0] == "0" {
		t.Fatalf("the engineer must be told about the file left as it was: %q", got)
	}
	var columns int
	fmt.Sscan(parts[2], &columns)
	if n := len([]rune(parts[1])); n > columns-14 || strings.ContainsAny(parts[1], "\n\r") {
		t.Fatalf("a message of %d characters on a screen of %d columns: %q", n, columns, parts[1])
	}
	if !strings.HasPrefix(parts[1], "pair: ") || !strings.HasSuffix(parts[1], "…") {
		t.Fatalf("message: %q", parts[1])
	}
}

func TestPointUsesTheLinesAsked(t *testing.T) {
	sock, stop := startNvim(t)
	defer stop()
	c := &Client{Addr: sock, Timeout: 3 * time.Second, Transport: "rpc"}
	ctx := context.Background()
	file := filepath.Join(t.TempDir(), "sample.txt")
	writeLines(t, file, 30)
	if _, err := c.Show(ctx, file, 1, 1); err != nil {
		t.Fatal(err)
	}
	// Stand-ins: no question asked, no command run, the arguments kept.
	setup := `
		vim.notify = function() end
		vim.ui.input = function(_, done) done('') end
		vim.system = function(cmd) _G.ran = table.concat(cmd, ' ', 2); return {} end
	`
	if err := c.ExecLua(ctx, setup, nil); err != nil {
		t.Fatal(err)
	}
	asked := func(do string) string {
		t.Helper()
		if err := c.ExecLua(ctx, "_G.ran = ''; "+do, nil); err != nil {
			t.Fatal(err)
		}
		return lua(t, c, "return _G.ran")
	}

	if got := asked("vim.cmd('4,6PairPoint')"); !strings.Contains(got, "--start 4 --end 6") {
		t.Fatalf(":4,6PairPoint ran %q", got)
	}
	// A selection made earlier is not what the engineer points at now.
	if got := asked("vim.cmd('normal! 10GVjj'); vim.cmd('normal! \\27'); vim.cmd('normal! 20G'); require('pair').point()"); !strings.Contains(got, "--start 20 --end 20") {
		t.Fatalf("with an old selection, pointing from line 20 ran %q", got)
	}
	if got := asked("vim.cmd('PairPoint')"); !strings.Contains(got, "--start 20 --end 20") {
		t.Fatalf(":PairPoint ran %q", got)
	}
}

// Unsaved work must survive a crash, of Neovim or of the machine.
func TestThePluginKeepsRecoveryFiles(t *testing.T) {
	_, thisFile, _, _ := runtime.Caller(0)
	src, err := os.ReadFile(filepath.Join(filepath.Dir(thisFile), "..", "..", "nvim", "lua", "pair", "init.lua"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(src), "swapfile = false") || strings.Contains(string(src), "noswapfile") {
		t.Fatal("the plugin turns recovery files off")
	}
	if !strings.Contains(string(src), "shortmess:append('A')") {
		t.Fatal("with recovery files on, their prompt must be off: it would block every call")
	}
}
