# PRD validation — Item 3: Neovim integration

Date: 2026-09-24. Nothing was built into the product; everything below lives under
`/private/tmp/claude-501/-Users-fabioaraujo-Developer/c2b4051e-b6e0-4ea5-b4af-94b87a3a9700/scratchpad/validation/item3/`.

## Verdict per claim

| # | Claim | Verdict | Evidence (one line) |
| --- | --- | --- | --- |
| a | `neovim/go-client` can `Dial` an `nvim --listen <sock>` and `ExecLua` into a plugin function | VERIFIED | go-client v1.2.1 (last tag 2022-01-18, last commit 2022-04-15) against NVIM v0.12.5; `Dial=59µs ExecLua(show)=2.3ms` first call, ~90µs warm |
| b | Plugin `show(file,s,e)` jumps + sets an extmark highlight, verified via API | VERIFIED | `nvim_win_get_cursor=[10,0]`, `nvim_buf_get_extmarks` → `id 1 row 9 col 0, end_row 13, hl_group/line_hl_group PairPointer`, `nvim_buf_get_name` = sample.txt |
| c | `--remote-send` works as fallback | VERIFIED, with caveat | normal mode: cursor moved to 3 in ~4.7ms; in insert mode the command was typed as text (buffer modified); with pending `d` it deleted 15 lines (`d:` motion). `--remote-expr "luaeval(...)"` is mode-safe and returns a value: use it as the fallback |
| d | `autoread` + `:checktime` reloads a file changed by another process without a prompt | VERIFIED | headless and TUI: 40 → 41 lines, `modified=false`, `:messages` empty, `v:warningmsg` empty, `Command("checktime")` returned in 1.2ms / 3.8ms |
| e | Conflict case (unsaved buffer change + disk change) | WRONG for the PRD's assumption | W12 raised; buffer NOT reloaded. Headless: no block. TUI: `[O]K, (L)oad File, ...` dialog and the RPC `Command("checktime")` BLOCKED until Enter (1.51s = until my script pressed it). A `FileChangedShell` autocmd removes the prompt (RPC returns in 155µs) |
| f | Round-trip latency | VERIFIED | in-process Dial+ExecLua 0.2–0.4ms warm (2–10ms when `show` has to `:edit` a new file); whole hook process spawn+Dial+show+6 verify calls 4–6ms; `nvim --remote-expr` 4.5–6ms; `--remote-send` 4.5–5ms |
| g | (extra) Socket path length | WRONG risk in PRD default | `nvim --listen` refused a 127-byte absolute path ("Failed to --listen: invalid argument"); macOS `sun_path` is 104 bytes; Go `net.Dial("unix")` has the same limit |
| h | (extra) `nvim_exec_lua` exists in 0.12 and go-client maps to it | VERIFIED | api-info: `nvim_exec_lua since=7`, api_level 14, not deprecated; go-client `ExecLua` calls `v.call("nvim_exec_lua", ...)` |
| i | (extra) go-client builds on Go 1.27 | VERIFIED | `go vet` + `go build` clean, no warnings; module declares `go 1.15`. 22 of its 151 RPC names are deprecated/removed in 0.12 (list below); none of the ones pair needs |
| j | (extra) swap file held by another running Nvim | VERIFIED no prompt | 0.12 prints `W325: Ignoring swapfile from Nvim process N` and continues; RPC returned in 9ms |

## Environment

```
NVIM v0.12.5
Build type: Release
LuaJIT 2.1.1788856981
go version go1.27.1 darwin/arm64
macOS Darwin 25.6.0 arm64
api_level=14 api_compatible=0
nvim_buf_get_extmarks since=7 params={ "buf", "ns_id", "start", "end", "opts" }
nvim_buf_set_extmark since=7 params={ "buf", "ns_id", "line", "col", "opts" }
nvim_exec_lua since=7 params={ "code", "args" }
```

go-client resolution:

```
$ go get github.com/neovim/go-client/nvim@latest
go: added github.com/neovim/go-client v1.2.1
$ go list -m -versions github.com/neovim/go-client
github.com/neovim/go-client v1.0.0 v1.0.1 v1.0.2 v1.1.0 v1.1.1 v1.1.2 v1.1.3 v1.1.4 v1.1.5 v1.1.6 v1.1.7 v1.2.0 v1.2.1
$ go list -m -json github.com/neovim/go-client@latest   # "Version": "v1.2.1", "Time": "2022-01-18T22:02:11Z", "GoVersion": "1.15"
$ go list -m -json github.com/neovim/go-client@master   # "Version": "v1.2.2-0.20220415170422-ef8a8cf9c522", "Time": "2022-04-15T17:04:22Z"
```

So the library is unchanged since April 2022. It still works against 0.12 for the small surface pair needs
(`Dial`, `ExecLua`, `Command`, `WindowCursor`, `BufferExtmarks`, `BufferName`, `BufferLines`, `SetBufferLines`,
`Namespaces`, `Mode`, `CurrentBuffer/Window`). go-client's `ExecLua`:

```go
func (v *Nvim) ExecLua(code string, result interface{}, args ...interface{}) error {
	if args == nil {
		args = emptyArgs
	}
	return v.call("nvim_exec_lua", result, code, args)
}
```

## Socket path length (unplanned finding)

First launch with the absolute scratch path (127 bytes) failed:

```
$ nvim --headless --clean --cmd "set rtp+=$S/pair.nvim" --listen "$S/nvim.sock"
nvim: Failed to --listen: invalid argument: "/private/tmp/claude-501/-Users-fabioaraujo-Developer/c2b4051e-b6e0-4ea5-b4af-94b87a3a9700/scratchpad/validation/item3/nvim.sock"
```

macOS limits `sockaddr_un.sun_path` to 104 bytes; Go's `net.Dial("unix", ...)` fails the same way (`invalid argument`).
Workaround used here and recommended for `pair`: bind and dial a path relative to the repo root
(`nvim --listen ./nvim.sock` with cwd = repo; hooks and the MCP server run with cwd = project dir), or put the socket
in `$TMPDIR/pair-<repo-hash>.sock`. `pair doctor` should check `len(abs(PAIR_SOCK)) < 104`.

## Step 1 — plugin `pair.nvim/lua/pair/init.lua` (verbatim)

```lua
-- Minimal pair.nvim used only for PRD validation item 3.
local M = {}

local NS = vim.api.nvim_create_namespace("pair")
local HL = "PairPointer"
local TTL_SECONDS = tonumber(vim.env.PAIR_HIGHLIGHT_TTL) or 5

vim.o.autoread = true
vim.api.nvim_set_hl(0, HL, { bg = "#3b4252", default = true })

local timer -- keep a single active TTL timer

local function clear(buf)
  if buf and vim.api.nvim_buf_is_valid(buf) then
    vim.api.nvim_buf_clear_namespace(buf, NS, 0, -1)
  end
end

--- Jump to `file` line `s` and highlight lines s..e (1-based, inclusive).
--- Returns a small table the caller can use to verify the effect.
function M.show(file, s, e)
  e = e or s
  local abs = vim.fn.fnamemodify(file, ":p")
  local cur = vim.api.nvim_buf_get_name(0)
  if cur ~= abs then
    vim.cmd("edit " .. vim.fn.fnameescape(abs))
  end
  local buf = vim.api.nvim_get_current_buf()
  local nlines = vim.api.nvim_buf_line_count(buf)
  s = math.max(1, math.min(s, nlines))
  e = math.max(s, math.min(e, nlines))

  clear(buf)
  vim.api.nvim_win_set_cursor(0, { s, 0 })
  vim.cmd("normal! zz")

  -- One extmark spanning the whole range: line_hl_group applies per line,
  -- hl_group + end_row covers the text region as well.
  local id = vim.api.nvim_buf_set_extmark(buf, NS, s - 1, 0, {
    end_row = e - 1,
    end_col = #(vim.api.nvim_buf_get_lines(buf, e - 1, e, false)[1] or ""),
    hl_group = HL,
    line_hl_group = HL,
    hl_eol = true,
    priority = 200,
  })

  if timer then
    timer:stop()
    timer:close()
    timer = nil
  end
  timer = vim.uv.new_timer()
  timer:start(TTL_SECONDS * 1000, 0, vim.schedule_wrap(function()
    clear(buf)
    if timer then
      timer:close()
      timer = nil
    end
  end))

  return { buf = buf, file = abs, start_line = s, end_line = e, extmark_id = id }
end

--- Re-check all buffers against disk (autoread reloads unmodified ones).
function M.reload()
  vim.cmd("checktime")
  return true
end

--- Helper for tests: extmarks in our namespace for the current buffer.
function M.marks(buf)
  buf = buf or 0
  return vim.api.nvim_buf_get_extmarks(buf, NS, 0, -1, { details = true })
end

return M
```

## Step 2 — headless instance

```
$ cd $S && nvim --headless --clean --cmd "set rtp+=$S/pair.nvim" --listen ./nvim.sock > nvim.log 2>&1 &
srwxr-xr-x@ 1 fabioaraujo  wheel  0 Sep 24 23:07 .../item3/nvim.sock
23678 SN   nvim --headless --clean --cmd set rtp+=.../item3/pair.nvim --listen ./nvim.sock
```

## Step 3 — Go client `goclient/main.go` (verbatim)

```go
// Validation harness for PRD item 3: drive pair.nvim over msgpack-rpc.
package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/neovim/go-client/nvim"
)

func main() {
	sock := flag.String("sock", "../nvim.sock", "nvim --listen socket path")
	file := flag.String("file", "../sample.txt", "file to show")
	s := flag.Int("s", 10, "start line")
	e := flag.Int("e", 14, "end line")
	n := flag.Int("n", 20, "repetitions for latency measurement")
	flag.Parse()
	mode := flag.Arg(0)
	if mode == "" {
		mode = "show"
	}

	t0 := time.Now()
	v, err := nvim.Dial(*sock)
	if err != nil {
		log.Fatalf("Dial(%s): %v", *sock, err)
	}
	defer v.Close()
	tDial := time.Since(t0)

	switch mode {
	case "show":
		doShow(v, *file, *s, *e, *n, tDial, t0)
	case "cursor":
		printState(v)
	case "dirty":
		doDirty(v)
	case "checktime":
		doChecktime(v)
	default:
		log.Fatalf("unknown mode %q", mode)
	}
}

// showVia calls require('pair').show(file, s, e) and returns the Lua result.
func showVia(v *nvim.Nvim, file string, s, e int) (map[string]interface{}, error) {
	var res map[string]interface{}
	err := v.ExecLua("return require('pair').show(...)", &res, file, s, e)
	return res, err
}

func doShow(v *nvim.Nvim, file string, s, e, n int, tDial time.Duration, t0 time.Time) {
	t1 := time.Now()
	res, err := showVia(v, file, s, e)
	if err != nil {
		log.Fatalf("ExecLua show: %v", err)
	}
	tExec := time.Since(t1)
	fmt.Printf("ExecLua result: %v\n", res)
	fmt.Printf("timing: Dial=%s ExecLua(show)=%s Dial+ExecLua=%s\n", tDial, tExec, time.Since(t0))

	printState(v)

	// Latency: repeated show calls (real work) and trivial round trips.
	var min, max, sum time.Duration
	for i := 0; i < n; i++ {
		ts := time.Now()
		if _, err := showVia(v, file, s+i%5, e+i%5); err != nil {
			log.Fatalf("ExecLua show #%d: %v", i, err)
		}
		d := time.Since(ts)
		sum += d
		if min == 0 || d < min {
			min = d
		}
		if d > max {
			max = d
		}
	}
	fmt.Printf("latency ExecLua(show) x%d: avg=%s min=%s max=%s\n", n, sum/time.Duration(n), min, max)
	min, max, sum = 0, 0, 0
	for i := 0; i < n; i++ {
		ts := time.Now()
		var one int
		if err := v.ExecLua("return 1", &one); err != nil {
			log.Fatalf("ExecLua trivial: %v", err)
		}
		d := time.Since(ts)
		sum += d
		if min == 0 || d < min {
			min = d
		}
		if d > max {
			max = d
		}
	}
	fmt.Printf("latency ExecLua(return 1) x%d: avg=%s min=%s max=%s\n", n, sum/time.Duration(n), min, max)
	// Leave the original range highlighted for the remote-send step to overwrite.
	if _, err := showVia(v, file, s, e); err != nil {
		log.Fatal(err)
	}
}

// printState verifies via plain API calls (not via the plugin) what the editor shows.
func printState(v *nvim.Nvim) {
	win, err := v.CurrentWindow()
	if err != nil {
		log.Fatal(err)
	}
	pos, err := v.WindowCursor(win) // nvim_win_get_cursor: [row(1-based), col(0-based)]
	if err != nil {
		log.Fatal(err)
	}
	buf, err := v.CurrentBuffer()
	if err != nil {
		log.Fatal(err)
	}
	name, err := v.BufferName(buf) // nvim_buf_get_name
	if err != nil {
		log.Fatal(err)
	}
	nss, err := v.Namespaces()
	if err != nil {
		log.Fatal(err)
	}
	ns := nss["pair"]
	marks, err := v.BufferExtmarks(buf, ns, 0, -1, map[string]interface{}{}) // nvim_buf_get_extmarks
	if err != nil {
		log.Fatalf("BufferExtmarks: %v", err)
	}
	var details interface{}
	if err := v.ExecLua("return require('pair').marks()", &details); err != nil {
		log.Fatal(err)
	}
	var modified bool
	if err := v.BufferOption(buf, "modified", &modified); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("state: buffer=%d name=%s cursor=[row %d col %d] modified=%v ns(pair)=%d\n", buf, name, pos[0], pos[1], modified, ns)
	fmt.Printf("state: extmarks (typed, id/row/col 0-based) = %+v\n", marks)
	fmt.Printf("state: extmarks (details via ExecLua) = %v\n", details)
}

// doDirty makes an unsaved change through the API so the buffer is 'modified'.
func doDirty(v *nvim.Nvim) {
	buf, err := v.CurrentBuffer()
	if err != nil {
		log.Fatal(err)
	}
	if err := v.SetBufferLines(buf, -1, -1, true, [][]byte{[]byte("UNSAVED EDIT VIA API")}); err != nil {
		log.Fatal(err)
	}
	printLines(v, "after SetBufferLines")
}

// doChecktime runs :checktime over RPC and reports what the buffer looks like afterwards.
func doChecktime(v *nvim.Nvim) {
	printLines(v, "before checktime")
	if err := v.ExecLua("vim.cmd('messages clear'); vim.v.warningmsg = ''; vim.v.errmsg = ''", nil); err != nil {
		log.Fatal(err)
	}
	t := time.Now()
	err := v.Command("checktime")
	fmt.Printf("Command(\"checktime\") err=%v took=%s\n", err, time.Since(t))
	var reload bool
	if err := v.ExecLua("return require('pair').reload()", &reload); err != nil {
		log.Fatalf("ExecLua reload: %v", err)
	}
	fmt.Printf("ExecLua reload() -> %v\n", reload)
	printLines(v, "after checktime")
	var msgs, warn, errmsg string
	if err := v.ExecLua("return vim.fn.execute('messages')", &msgs); err != nil {
		log.Fatal(err)
	}
	if err := v.ExecLua("return vim.v.warningmsg", &warn); err != nil {
		log.Fatal(err)
	}
	if err := v.ExecLua("return vim.v.errmsg", &errmsg); err != nil {
		log.Fatal(err)
	}
	fmt.Printf(":messages -> %q\nv:warningmsg -> %q\nv:errmsg -> %q\n", msgs, warn, errmsg)
	var mode *nvim.Mode
	mode, err = v.Mode()
	fmt.Printf("nvim_get_mode -> %+v err=%v\n", mode, err)
}

func printLines(v *nvim.Nvim, label string) {
	buf, err := v.CurrentBuffer()
	if err != nil {
		log.Fatal(err)
	}
	lines, err := v.BufferLines(buf, 0, -1, true)
	if err != nil {
		log.Fatal(err)
	}
	var modified bool
	if err := v.BufferOption(buf, "modified", &modified); err != nil {
		log.Fatal(err)
	}
	last := ""
	if len(lines) > 0 {
		last = string(lines[len(lines)-1])
	}
	fmt.Printf("%s: lines=%d last=%q modified=%v\n", label, len(lines), last, modified)
	_ = os.Stdout
}
```

Build: `go mod tidy && go vet ./... && go build -o pairtest .` → clean, no warnings (Go 1.27.1).

Run (absolute file path; note: `show` resolves relative paths against **nvim's** cwd, so hooks must pass absolute
paths — the first run with `../sample.txt` opened an empty buffer):

```
$ ./goclient/pairtest -sock ./nvim.sock -file "$S/sample.txt" -s 10 -e 14 show
ExecLua result: map[buf:2 end_line:14 extmark_id:1 file:.../item3/sample.txt start_line:10]
timing: Dial=59.042µs ExecLua(show)=2.297292ms Dial+ExecLua=2.382042ms
state: buffer=2 name=.../item3/sample.txt cursor=[row 10 col 0] modified=false ns(pair)=3
state: extmarks (typed, id/row/col 0-based) = [{ID:1 Row:9 Col:0}]
state: extmarks (details via ExecLua) = [[1 9 0 map[end_col:7 end_right_gravity:false end_row:13 hl_eol:true hl_group:PairPointer line_hl_group:PairPointer ns_id:3 priority:200 right_gravity:true]]]
latency ExecLua(show) x20: avg=94.579µs min=41.709µs max=170.167µs
latency ExecLua(return 1) x20: avg=30.179µs min=24.042µs max=67.375µs
```

Cursor (`nvim_win_get_cursor`), extmark (`nvim_buf_get_extmarks`, plus details) and buffer name (`nvim_buf_get_name`)
all match the requested range. The go-client `ExtMark` struct is `[id,row,col]` only; details must be fetched via
`ExecLua` or a raw call with `details=true`.

## Step 4 — `--remote-send` and `--remote-expr` fallbacks

```
===== STEP 4a: --remote-send fallback (normal mode)
nvim --server ./nvim.sock --remote-send   0.00s user 0.00s system 85% cpu 0.006 total
line('.') -> 3   bufname -> .../item3/sample.txt
state: buffer=2 name=.../item3/sample.txt cursor=[row 3 col 0] modified=false ns(pair)=3
state: extmarks (typed, id/row/col 0-based) = [{ID:1 Row:2 Col:0}]
state: extmarks (details via ExecLua) = [[1 2 0 map[end_col:6 ... end_row:4 hl_eol:true hl_group:PairPointer line_hl_group:PairPointer ...]]]

===== STEP 4b: --remote-expr luaeval fallback
20      <- returned value (start_line)
line('.') -> 20
state: ... cursor=[row 20 col 0] ... extmarks = [{ID:1 Row:19 Col:0}] end_row:21

===== STEP 4c: caveat - nvim in INSERT mode, then --remote-send the same command
mode(1) -> i
after remote-send: mode(1) -> i  line('.') -> 21  modified -> 1
line 20 text now -> :lua require('pair').show('.../item3/sample.txt', 30, 32)
--- --remote-expr while still in insert mode:
30      <- worked
line('.') -> 30  mode(1) -> i
after <Esc>u: mode(1) -> n  modified -> 0  line 20 -> line 20

===== STEP 4d: caveat - pending operator 'd', then --remote-send the command
after: line('.') -> 20  line('$') -> 25  modified -> 1      <- "d:lua ...<CR>" became a motion: 15 lines deleted
after u: line('$') -> 40  modified -> 0
```

Conclusion: `--remote-send` is keystroke injection (`nvim_input`), so it is only correct when the target is idle in
normal mode. With the engineer typing in insert mode it corrupts the buffer; with a pending operator it is destructive.
`--remote-expr` uses `nvim_eval`, is mode-independent, returns a value and errors, and costs the same (~5 ms).
Recommend the PRD name `nvim --server $PAIR_SOCK --remote-expr "luaeval(\"require('pair').show(...)\")"` as the
fallback, not `--remote-send`.

## Step 5 — autoread + checktime

Headless instance, clean buffer, file appended from the shell:

```
modified before -> 0  autoread -> 1
$ echo "line 41 appended from shell" >> sample.txt
before checktime: lines=40 last="line 40" modified=false
Command("checktime") err=<nil> took=1.243041ms
ExecLua reload() -> true
after checktime: lines=41 last="line 41 appended from shell" modified=false
:messages -> ""
v:warningmsg -> ""
v:errmsg -> ""
nvim_get_mode -> &{Mode:n Blocking:false} err=<nil>
```

Headless instance, conflict (unsaved API edit + disk change):

```
after SetBufferLines: lines=42 last="UNSAVED EDIT VIA API" modified=true
$ echo "line 42 appended from shell (conflict)" >> sample.txt
before checktime: lines=42 last="UNSAVED EDIT VIA API" modified=true
Command("checktime") err=<nil> took=280.25µs
after checktime: lines=42 last="UNSAVED EDIT VIA API" modified=true        <- not reloaded
:messages -> ""
v:warningmsg -> "W12: Warning: File \".../item3/sample.txt\" has changed and the buffer was changed in Vim as well"
nvim_get_mode -> &{Mode:n Blocking:false}
```

Real TUI in tmux (`tmux new -s val3 "nvim --clean --cmd 'set rtp+=...' --listen ./nvim-tui.sock sample-tui.txt"`),
the same two cases plus a mitigation:

```
== TUI 1: clean buffer, disk change, checktime
checktime RPC returned within 1.5s
before checktime: lines=40 last="tui 40" modified=false
Command("checktime") err=<nil> took=3.819458ms
after checktime: lines=41 last="tui disk change 1" modified=false
:messages -> ""   v:warningmsg -> ""

== TUI 2: conflict (modified buffer + disk change), checktime
--- screen after 1.5s:
W12: Warning: File "sample-tui.txt" has changed and the buffer was changed in Vim as well
See ":help W12" for more info.
[O]K, (L)oad File, Load File (a)nd Options:
!!! checktime RPC still blocked after 1.5s -> sending Enter to TUI
Command("checktime") err=<nil> took=1.509702125s                          <- blocked until Enter
after checktime: lines=42 last="UNSAVED EDIT VIA API" modified=true

== TUI 3: mitigation - FileChangedShell autocmd that notifies instead of W12
  vim.api.nvim_create_autocmd('FileChangedShell', { callback = function(ev)
    if vim.v.fcs_reason == 'conflict' then
      vim.v.fcs_choice = ''
      vim.notify('pair: ' .. vim.fn.fnamemodify(ev.file, ':t') .. ' changed on disk while buffer modified; :e! to reload')
    else
      vim.v.fcs_choice = 'reload'
    end
  end })
--- screen after 1.5s:
sample-tui.txt [+]                                                                1,1            Top
pair: sample-tui.txt changed on disk while buffer modified; :e! to reload
checktime RPC returned within 1.5s
Command("checktime") err=<nil> took=155.625µs
after checktime: lines=42 last="UNSAVED EDIT VIA API" modified=true
:messages -> "\npair: sample-tui.txt changed on disk while buffer modified; :e! to reload"
v:warningmsg -> ""

== TUI 4: mitigation with clean buffer (fcs_choice=reload path)
Command("checktime") err=<nil> took=2.421667ms
before checktime: lines=43 last="tui disk change 3" modified=false
after checktime: lines=44 last="tui disk change 4" modified=false
```

So: the PRD's `post_edit` hook assumption ("`:checktime` in Neovim", no prompt) holds only while the engineer has no
unsaved change in the buffer Claude edits. In the TUI the conflict raises a modal dialog and the hook's RPC call hangs
until the engineer answers (it would otherwise run into the hook timeout). `pair.nvim` must ship a `FileChangedShell`
autocmd (as above) so a conflict becomes a one-line notification and the RPC returns immediately; the Go side should
additionally guard every RPC with a deadline (go-client has no per-call context; run the call in a goroutine and
`Close()` the connection on timeout).

## Step 6 — bonus checks

- `nvim_exec_lua` is present in 0.12 (`since=7`, api_level 14) and not deprecated; go-client's `ExecLua` calls it by name.
- Go 1.27.1: `go vet` and `go build` clean for go-client v1.2.1 (module `go 1.15`), no deprecation warnings.
- go-client calls 151 RPC names; against 0.12 api-info, these are deprecated/missing (avoid these wrappers):

```
nvim__set_hl_ns MISSING_IN_0.12
nvim_add_user_command MISSING_IN_0.12            (renamed nvim_create_user_command in 0.7)
nvim_buf_add_highlight deprecated_since=13
nvim_buf_add_user_command MISSING_IN_0.12
nvim_buf_changedtick_event MISSING_IN_0.12       (notification name, false positive)
nvim_buf_get_option deprecated_since=11          (v.BufferOption — used in the harness, still works)
nvim_buf_lines_event MISSING_IN_0.12             (notification name, false positive)
nvim_buf_set_option deprecated_since=11
nvim_call_atomic deprecated_since=12             (v.NewBatch — do not rely on it)
nvim_err_write / nvim_err_writeln deprecated_since=13
nvim_exec deprecated_since=11                    (v.Exec — use ExecLua + vim.fn.execute instead)
nvim_get_hl_by_id / nvim_get_hl_by_name deprecated_since=9
nvim_get_option / nvim_get_option_info / nvim_set_option deprecated_since=11
nvim_out_write deprecated_since=13
nvim_subscribe / nvim_unsubscribe deprecated_since=13
nvim_win_get_option / nvim_win_set_option deprecated_since=11
```

  `Dial`, `ExecLua`, `Command`, `WindowCursor`, `BufferExtmarks`, `BufferName`, `BufferLines`, `SetBufferLines`,
  `Namespaces`, `Mode`, `CurrentBuffer`, `CurrentWindow`, `Close` are all current. Keep the Go side on `ExecLua` only.
- Swap file held by another running Nvim: `show()` in the TUI printed `W325: Ignoring swapfile from Nvim process 34452`
  and returned in 9 ms; no ATTENTION prompt in 0.12 for a live process. (A stale swapfile from a crashed instance was
  not tested; a `SwapExists` autocmd setting `v:swapchoice` covers it and was verified to not break the happy path.)
- Neovim resolves the `file` argument of `show()` against its own cwd, so hooks must pass absolute paths (the first
  Go run with `../sample.txt` silently opened an empty new buffer).

## Timings (end-to-end, zsh EPOCHREALTIME, 5 runs each, warm)

```
go binary: spawn+Dial+ExecLua show+6 verify calls      6.41 / 4.72 / 4.50 / 4.13 / 4.05 ms
go binary: spawn+Dial+Command(checktime)               3.67 / 3.61 / 3.47 / 3.36 / 2.83 ms
nvim CLI: --remote-expr luaeval(show)                  6.06 / 5.23 / 4.85 / 4.55 / 4.52 ms
nvim CLI: --remote-send :lua show<CR>                  4.52 / 4.98 / 4.74 / 4.63 / 4.66 ms
in-process: Dial 47–200 µs; ExecLua(show) 140–290 µs warm, 2.3–10 ms when it has to :edit the file; ExecLua(return 1) ~30 µs
```

The msgpack path and the CLI path are equally fast at the hook level (~5 ms, dominated by process spawn); the msgpack
path's real advantages are typed results/errors and no dependence on the target's mode.

## Concrete PRD changes

1. Data flow bullet: "drive Neovim with `--remote-send`" → "drive Neovim over msgpack-rpc (`nvim_exec_lua` via
   go-client); fallback `nvim --server $PAIR_SOCK --remote-expr 'luaeval(...)'`, never `--remote-send` (keystrokes are
   interpreted in insert/operator-pending mode: verified buffer corruption and a 15-line deletion)".
2. Component 4 / hook `post_edit`: add a `FileChangedShell` autocmd in `pair.nvim` (reload when unmodified, one-line
   `vim.notify` on conflict, never the W12 dialog) — without it the `post_edit` RPC blocks on a modal prompt in the TUI
   when the engineer has unsaved edits. Document the caveat and add a per-RPC deadline in `internal/nvim`.
3. `PAIR_SOCK` default `.pair/nvim.sock`: keep it relative to the repo root (bind and dial with cwd = repo) or use
   `$TMPDIR/pair-<hash>.sock`; `pair doctor` checks the absolute length < 104 bytes (macOS `sun_path`).
4. Hooks pass absolute `file_path` to `show()` (Neovim resolves relative to its own cwd).
5. Risk table: `neovim/go-client` last commit 2022-04-15, v1.2.1; 22 of its wrappers hit deprecated/removed API in 0.12
   — mitigation: restrict usage to `Dial`/`ExecLua`/`Command`/`Close` (all current), everything else inside Lua.
6. `show()` first-call cost is 2–10 ms when it must `:edit` the file; warm calls ~0.1 ms; whole hook ~5 ms. Fine.
