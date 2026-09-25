# Validation item 5 (tmux): 3-pane layout, send-keys, Escape interrupt, in-flight MCP call

Environment: macOS 26.6.2 arm64, tmux 3.7c, Neovim 0.12.5, Claude Code 2.1.282 (nested via `env -u CLAUDECODE claude --model claude-haiku-4-5-20251001 --permission-mode acceptEdits`), Node v24.16.0 (nvm), @modelcontextprotocol/sdk 1.30.1, zod 4.6.5. Nothing outside the scratch dir was modified except a transient unix socket `/private/tmp/claude-501/val5.sock` (removed at cleanup; see socket-length finding).

## Summary (one line per claim)

| # | Claim | Verdict | Evidence |
| --- | --- | --- | --- |
| a | 3-pane layout scriptable with PRD sizes | VERIFIED | `split-window -h -l 50%` then `split-window -v -l 30% -t :0.1` gave panes 99x50 / 100x34 / 100x15 on a 200x50 window; `-p 50`/`-p 30` are still accepted in 3.7c as shorthand. Idempotent via `tmux has-session`. |
| b | send-keys injects a prompt into interactive Claude Code | VERIFIED | `send-keys "Reply with exactly PONG" Enter` -> `⏺ PONG` in 2.5 s. `-l` with embedded `\n` inserts multi-line text into the input box without submitting; `Enter` submits. Quotes, `$HOME`, `\n` survive with `-l`. Use `-l --` for text starting with `-`. |
| c | Escape via send-keys interrupts a running turn | VERIFIED | Foreground Bash (`./slow.sh` = `exec sleep 90`): Escape -> idle prompt in 0.29 s, UI: `Ran 1 shell command ⎿ Interrupted · What should Claude do instead?`; the `sleep 90` child was killed (pgrep empty 1 s later). Caveat: Claude Code 2.1.x refuses a bare foreground `sleep N` and may auto-background long commands (backgrounded shells survive Escape). |
| d | Fate of an in-flight MCP call on Escape | VERIFIED (server sees cancellation; NOT killed; handler runs to completion unless it honors the signal) | 73 ms after Escape the server received `notifications/cancelled {"requestId":2,"reason":"AbortError: user-cancel"}`; SDK `extra.signal` fired abort; server process (pid 33912) survived; the handler's 90 s timer finished (`sleep done seconds=90 aborted=true`) and its late result was discarded by Claude. Server gets SIGINT only when Claude exits. |
| e | New prompt accepted right after Escape | VERIFIED | `send-keys "Reply with exactly PONG2" Enter` 1.0 s after Escape: reply visible 1.09 s later (2.5 s Escape->reply total). Same after the MCP-call Escape (PONG3 in 1.10 s). |
| bonus | C-c vs Escape | VERIFIED | One C-c mid-turn = same `Interrupted · What should Claude do instead?` (Claude stays alive); two C-c within ~0.3 s exit Claude (`Resume this session with: claude --resume ...`). Escape never exits. |

Extra findings that affect the PRD:

1. macOS unix socket paths are limited to 104 bytes (`sun_path`). `nvim --listen <127-byte path>` failed with `nvim: Failed to --listen: invalid argument`; 104 bytes OK, 105 bytes fails. `$PAIR_SOCK` = `<repo>/.pair/pair.sock` will break in deep repo paths. Use `$TMPDIR`/`/tmp/pair-<hash>.sock` (short) or `--listen 127.0.0.1:<port>` (verified working). A relative `--listen nvim.sock` silently created no reachable socket.
2. Claude Code 2.1.x blocks a plain foreground `sleep N` in Bash and can auto-background long-running shell commands; a backgrounded shell is NOT killed by Escape. Not relevant to `pair` (its long calls are MCP), but relevant to how validation prompts are phrased.
3. MCP cancellation contract for `pair mcp`: on Escape, Claude Code sends `notifications/cancelled` (the Go SDK surfaces this as `ctx` cancellation) but does NOT kill the server or any child it spawned. `narrate`/`ask` MUST watch `ctx.Done()` and kill `say`/`rec`/`whisper-cli` themselves; otherwise `say` keeps speaking and the handler's late result is silently dropped. The server process is only signalled (SIGINT) when Claude exits.
4. `--mcp-config` needs an absolute, existing `command` path: `/opt/homebrew/bin/node` does not exist on this Mac (node comes from nvm at `~/.nvm/versions/node/v24.16.0/bin/node`); the failure surfaced as `ENOENT: no such file or directory, posix_spawn 'stdio'`, which is misleading. `pair init` should resolve the `pair` binary to an absolute path (`os.Executable()`) when registering the server.
5. macOS has no `timeout(1)`; validation scripts must not rely on it.

## Step 1: layout script

`layout.sh` (verbatim, final version):

```zsh
#!/bin/zsh
# layout.sh - create (or reuse) the 3-pane `pair` layout in tmux session val5.
# Pane 0 (left, 50% width): claude placeholder (zsh for now)
# Pane 1 (top right, 70% height): nvim --listen $SOCK --clean
# Pane 2 (bottom right, 30% height): tail -f log.txt
export PATH=/opt/homebrew/bin:$PATH
SESSION=val5
S=/private/tmp/claude-501/-Users-fabioaraujo-Developer/c2b4051e-b6e0-4ea5-b4af-94b87a3a9700/scratchpad/validation/item5
# NOTE: macOS unix socket paths are limited to 104 bytes (sun_path); the scratch path is 127 bytes, so use a short path.
SOCK=/private/tmp/claude-501/val5.sock
LOG=$S/log.txt

if tmux has-session -t "$SESSION" 2>/dev/null; then
  echo "session $SESSION exists; reusing (would 'tmux attach -t $SESSION' if interactive)"
  [ -t 0 ] && [ -z "$TMUX" ] && exec tmux attach -t "$SESSION"
  exit 0
fi

touch "$LOG"
# Detached session, fixed geometry so the layout is deterministic without a client.
tmux new-session -d -s "$SESSION" -n main -x 200 -y 50 -c "$S" zsh
# Pane 1: split pane 0 horizontally; new pane is 50% of the width, on the right.
tmux split-window -h -l 50% -t "$SESSION:0.0" -c "$S" zsh
# Pane 2: split pane 1 vertically; new pane is 30% of the height, at the bottom.
tmux split-window -v -l 30% -t "$SESSION:0.1" -c "$S" zsh
tmux select-pane -t "$SESSION:0.0"

tmux send-keys -t "$SESSION:0.1" "nvim --listen $SOCK --clean" Enter
tmux send-keys -t "$SESSION:0.2" "tail -f $LOG" Enter
echo "session $SESSION created"
```

Flags used: `tmux new-session -d -s val5 -n main -x 200 -y 50`, `tmux split-window -h -l 50% -t val5:0.0`, `tmux split-window -v -l 30% -t val5:0.1`. tmux 3.7c man page: "-l size ... may be followed by '%' to specify a percentage of the available space. -p is a shorthand option for this", so `-p 50` / `-p 30` also work.

First run: `session val5 created`, exit 0. Second run: `session val5 exists; reusing ...`, exit 0 (checks `tmux has-session -t val5`; would `exec tmux attach` when run from a terminal outside tmux).

`tmux list-panes -t val5 -F '#{pane_index} #{pane_width}x#{pane_height} #{pane_current_command}'` (after nvim/tail started, claude running in pane 0):

```
0 99x50 2.1.282
1 100x34 nvim
2 100x15 tail
```

`tmux display -p -t val5 '#{window_layout}'`:

```
e951,200x50,0,0{99x50,0,0,0,100x50,100,0[100x34,100,0,1,100x15,100,35,2]}
```

Socket: first attempt with `$S/nvim.sock` (127 bytes) failed:

```
nvim: Failed to --listen: invalid argument: "/private/tmp/claude-501/-Users-fabioaraujo-Developer/c2b4051e-.../item5/nvim.sock"
```

Length probe with `nvim --headless --listen <path> -c 'qa!'`: len=100 rc=0, len=103 rc=0, len=104 rc=0, len=105 rc=1 (`Failed to --listen: invalid argument`), len=110 rc=1.
Workarounds verified: `nvim --listen 127.0.0.1:47555 --clean` -> `nvim --server 127.0.0.1:47555 --remote-expr 'v:servername'` = `127.0.0.1:47555`; `nvim --listen /private/tmp/claude-501/val5.sock` (33 bytes) -> socket file created (`srwxr-xr-x ... val5.sock`) and `--remote-expr` answered.

## Step 2: send-keys into the Claude pane

Start: `tmux send-keys -t val5:0.0 "cd $S/proj && env -u CLAUDECODE claude --model claude-haiku-4-5-20251001 --permission-mode acceptEdits" Enter`. `env -u CLAUDECODE` was sufficient (no need to unset `CLAUDE_CODE_ENTRYPOINT`). A workspace-trust dialog appeared with "No, exit" preselected, so it must be answered with `send-keys Down Enter` (a bare `Enter` would exit). Prompt was ready ~12 s after launch.

T1 simple prompt (`send-keys "Reply with exactly PONG" Enter`), reply after 2.56 s:

```
  Get to finished work sooner with Opus 5.5. Switch anytime with /model.
❯ Reply with exactly PONG
⏺ PONG
✻ Churned for 1s · done 11:10 PM
───────────────────────────────────────────────────────────────────────────────────────────────────
❯ 
───────────────────────────────────────────────────────────────────────────────────────────────────
  ⏵⏵ accept edits on (shift+tab to cycle) · ← for agents
```

T2 multi-line via `tmux send-keys -t val5:0.0 -l $'line1\nline two here\nline three END'` then `send-keys Enter`. Input box before Enter showed three lines (LF is inserted as a newline, not a submit):

```
✻ Churned for 1s · done 11:10 PM
❯ Reply with exactly the number of lines in this message, then the last word.
  line two here
  line three END
⏺ 3 END
✻ Worked for 1s · done 11:10 PM
───────────────────────────────────────────────────────────────────────────────────────────────────
❯ 
───────────────────────────────────────────────────────────────────────────────────────────────────
  ⏵⏵ accept edits on (shift+tab to cycle) · ← for agents
```

T3 quotes/special chars via `-l` with `He said "hi" and it's $HOME `date` \n done`:

```
✻ Worked for 1s · done 11:10 PM
❯ Reply with exactly this string, verbatim, nothing else: He said "hi" and it's $HOME date \n done
⏺ He said "hi" and it's $HOME date \n done
✻ Cogitated for 1s · done 11:10 PM
───────────────────────────────────────────────────────────────────────────────────────────────────
❯ 
───────────────────────────────────────────────────────────────────────────────────────────────────
  ⏵⏵ accept edits on (shift+tab to cycle) · ← for agents
```

Double quotes, apostrophe, `$HOME`, and a literal `\n` were delivered verbatim (tmux does no shell expansion). The backticks around `date` were not visible in the echoed prompt (Claude Code's input renders inline code without the backticks, or strips them); the model's reply also omitted them. Leading dash: `tmux send-keys -t val5:0.0 -l -- '-leading dash text: reply with exactly DASH'` -> `⏺ DASH`.

Quoting rules for the push-to-talk injector: always `tmux send-keys -t pair:0.0 -l -- "$transcript"` (literal mode, so words like `Enter`, `Space`, `C-c`, `Escape` in a transcript are not parsed as key names and a leading `-` is not parsed as an option), then a separate `tmux send-keys -t pair:0.0 Enter` to submit. Embedded newlines in the transcript become newlines in the input box. No shell escaping needed since the argument is passed directly, not through a shell.

## Step 3: Escape interrupts a Bash turn

Attempt 1, prompt `Run the shell command: sleep 90; then reply DONE`: the model ran `sleep 90` as a background shell and finished its turn in 5 s (`The sleep command is running in the background. I'll reply DONE once it completes.` + status `1 shell still running`), so Escape at ~10 s landed on an idle prompt and the `sleep 90` (pids 29723 zsh -c ..., 29726 sleep) survived. Attempt 2 (explicit foreground `sleep 90`): the model reported `I cannot execute a plain foreground sleep 90 as requested` - Claude Code 2.1.x's Bash tool blocks foreground `sleep`. Attempt 3 used `./slow.sh` (`#!/bin/zsh` / `exec sleep 90`) with `Bash(./slow.sh)` allowed in `proj/.claude/settings.json`.

Pane while running (attempt 3):

```
     (ctrl+b ctrl+b (twice) to run in background)
✻ Ideating… (7s · ↓ 174 tokens)
  ⎿  Tip: Run /install-slack-app to use Claude in Slack
───────────────────────────────────────────────────────────────────────────────────────────────────
❯ 
───────────────────────────────────────────────────────────────────────────────────────────────────
  ⏵⏵ accept edits on (shift+tab to cycle) · esc to interrupt · ← for agents
```

`pgrep -f "sleep 90"` before Escape: `34934` (`sleep 90`, ppid 34932 = the Bash tool's zsh). `tmux send-keys -t val5:0.0 Escape` -> idle prompt in 0.295 s. Pane after Escape:

```
❯ Use the Bash tool to run exactly ./slow.sh in the foreground (run_in_background false, timeout
  120000) and wait for it to finish, then reply DONE. Do not use any other tool.
  Ran 1 shell command
  ⎿  Interrupted · What should Claude do instead?
───────────────────────────────────────────────────────────────────────────────────────────────────
❯ 
───────────────────────────────────────────────────────────────────────────────────────────────────
  ⏵⏵ accept edits on (shift+tab to cycle) · ← for agents
```

`pgrep -f "sleep 90"` at +1 s and +4 s: empty. The foreground child was killed by the interrupt.

## Step 4: in-flight MCP call on Escape

Server: `mcp/server.mjs` (McpServer + StdioServerTransport, tool `sleep(seconds)`; logs every inbound JSON-RPC method, the `extra.signal` abort event, SIGTERM/SIGINT/SIGHUP, stdin end, and exit). Registered only via `--mcp-config $S/mcp.json --strict-mcp-config`:

```
{"mcpServers":{"pair":{"type":"stdio","command":"/Users/fabioaraujo/.nvm/versions/node/v24.16.0/bin/node","args":["/private/tmp/claude-501/-Users-fabioaraujo-Developer/c2b4051e-b6e0-4ea5-b4af-94b87a3a9700/scratchpad/validation/item5/mcp/server.mjs"]}}}
```

`proj/.claude/settings.json`:

```
{"permissions":{"allow":["mcp__pair__sleep","Bash(sleep:*)","Bash(./slow.sh)","Bash(./slow.sh:*)","Bash(zsh:*)","Bash(zsh slow.sh)"]}}
```

First attempt failed because the config pointed at `/opt/homebrew/bin/node`, which does not exist here; Claude reported `The pair MCP server failed to connect this session with the error: ENOENT: no such file or directory, posix_spawn 'stdio'`. Fixed by using `$(command -v node)` (nvm path). A `claude -p` probe then showed the full lifecycle including SIGINT on exit.

Interactive run: prompt `Call the MCP tool mcp__pair__sleep with seconds=90 and wait for its result...`. Server logged `sleep start` 2 s later. Pane while in flight:

```
  Calling pair…
· Actualizing… (4s · ↓ 135 tokens · thought for 1s)
  tmux focus-events off · add 'set -g focus-events on' to ~/.tmux.conf and reattach for focus tr…
───────────────────────────────────────────────────────────────────────────────────────────────────
❯ 
───────────────────────────────────────────────────────────────────────────────────────────────────
  ⏵⏵ accept edits on (shift+tab to cycle) · esc to interrupt · ← for agents
```

Escape sent at 2026-09-25T02:15:56.225Z, ~14.3 s into the call. Idle prompt after 0.305 s. Pane after Escape:

```
⚠ Your login expires in 1 day · run /login to renew
  Get to finished work sooner with Opus 5.5. Switch anytime with /model.
❯ Call the MCP tool mcp__pair__sleep with seconds=90 and wait for its result. Do not use any other
  tool. When it returns, reply DONE.
  Called pair
  ⎿  Interrupted · What should Claude do instead?
───────────────────────────────────────────────────────────────────────────────────────────────────
❯ 
───────────────────────────────────────────────────────────────────────────────────────────────────
  ⏵⏵ accept edits on (shift+tab to cycle) · ← for agents
```

`mcp.log` (complete, for the interactive server pid 33912, through Claude's exit):

```
2026-09-25T02:15:14.521Z pid=33912 server boot
2026-09-25T02:15:14.524Z pid=33912 connected (stdio)
2026-09-25T02:15:14.524Z pid=33912 rx method=initialize id=0
2026-09-25T02:15:14.528Z pid=33912 rx method=notifications/initialized id=-
2026-09-25T02:15:14.528Z pid=33912 rx method=tools/list id=1
2026-09-25T02:15:41.902Z pid=33912 rx method=tools/call id=2
2026-09-25T02:15:41.905Z pid=33912 sleep start seconds=90 requestId=2 aborted=false
2026-09-25T02:15:56.298Z pid=33912 rx method=notifications/cancelled id=- params={"requestId":2,"reason":"AbortError: user-cancel"}
2026-09-25T02:15:56.300Z pid=33912 sleep ABORT signal fired reason=AbortError: user-cancel
2026-09-25T02:17:11.909Z pid=33912 sleep done seconds=90 aborted=true
2026-09-25T02:17:41.085Z pid=33912 signal SIGINT
2026-09-25T02:17:41.085Z pid=33912 exit code=0
```

Reading: `notifications/cancelled` with `reason: "AbortError: user-cancel"` arrived 73 ms after Escape; the SDK fired the tool handler's `AbortSignal`; the process was NOT signalled or killed (`pgrep -f server.mjs` = 33912 before and after; no SIGTERM/SIGINT/stdin-end lines); the handler's `setTimeout` kept running and completed at 02:17:11.9Z (`aborted=true`); Claude ignored the late result (no UI change). SIGINT arrived only when Claude was exited with C-c C-c at 02:17:41Z. Conclusion for `narrate`/`ask`: they must select on `ctx.Done()` and kill `say`/`rec`/`whisper-cli` themselves; Claude will not do it.

## Step 5: prompt accepted right after Escape

After the Bash-turn Escape: `send-keys "Reply with exactly PONG2" Enter` sent ~1.0 s after Escape; `⏺ PONG2` visible 1.09 s later (2.50 s Escape -> reply). After the MCP-call Escape: `⏺ PONG3` visible 1.10 s after sending. Pane:

```
✻ Worked for 5s · done 11:10 PM · 1 shell still running
❯ Reply with exactly PONG2
⏺ PONG2
✽ Scampering… (1s · thinking)
───────────────────────────────────────────────────────────────────────────────────────────────────
❯ 
───────────────────────────────────────────────────────────────────────────────────────────────────
  ⏵⏵ accept edits on · 1 shell · esc to interrupt · ← for agents · ↓ to manage
```

Interrupt flow for the PRD (kill say -> Escape -> listen -> send transcript) works; the prompt is accepted as soon as `Interrupted · What should Claude do instead?` is shown (~0.3 s), so `pair hook interrupt` can send Escape and immediately start recording.

## Bonus: C-c vs Escape

Single C-c 1.75 s into a streaming answer:

```
  an old friend beneath his weathered boots. The tower stood on a rocky promontory where the sea
  crashed with ancient fury, and it was here, in this sol
  ⎿  Interrupted · What should Claude do instead?
───────────────────────────────────────────────────────────────────────────────────────────────────
❯ 
───────────────────────────────────────────────────────────────────────────────────────────────────
  ⏵⏵ accept edits on (shift+tab to cycle) · ← for agents
```

Claude stayed alive (`pane_current_command` = 2.1.282). Two C-c within ~0.3 s exited Claude:

```
odel claude-haiku-4-5-20251001 --permission-mode acceptEdits
Resume this session with:
claude --resume 67444430-1737-4ef4-9b78-4acf5a165725
fabioaraujo@MacBook-Pro-de-Fabio proj %
```

So a single C-c behaves like Escape (interrupt turn); Escape is the safer key for `pair hook interrupt` because a double send can never exit the session.

## Timings

| Event | Time |
| --- | --- |
| Claude launch -> trust dialog | ~8 s |
| trust accepted -> idle prompt | ~4 s |
| simple prompt -> reply (haiku) | 2.5 s |
| Escape -> idle prompt (Bash turn) | 0.29 s |
| Escape -> idle prompt (MCP call) | 0.31 s |
| Escape -> server receives notifications/cancelled | 73 ms |
| Escape -> next prompt's reply visible | 2.5 s |
| C-c C-c -> shell prompt back | < 2.5 s |

## Concrete PRD changes

1. Architecture / Neovim integration: `$PAIR_SOCK` must be <= 104 bytes on macOS. Put it in `$TMPDIR` or `/tmp/pair-<repohash>.sock`, or use `--listen 127.0.0.1:<port>`; do not default to `<repo>/.pair/pair.sock`. `pair doctor` should check the length.
2. Component 2 (`pair mcp`): state explicitly that Escape delivers `notifications/cancelled` (Go SDK: ctx cancelled) within ~100 ms, that the server and its children are not killed, and that `narrate`/`ask` must kill `say`/`rec`/`whisper-cli` on `ctx.Done()` and return promptly (late results are discarded).
3. Component 6 / `pair hook interrupt`: use `tmux send-keys -t pair:0.0 Escape` (not C-c); after ~0.3 s the input is ready; inject the transcript with `tmux send-keys -t pair:0.0 -l -- "$text"` followed by `tmux send-keys -t pair:0.0 Enter`.
4. Component 1 (`pair start`): use `split-window -h -l 50%` and `split-window -v -l 30% -t pair:0.1` (or `-p`), `new-session -d` with `-x/-y` when not attached, and `has-session` for idempotence; answer the workspace-trust dialog only if the user has not trusted the repo (the dialog's default is "No, exit"). `pair init` should register the MCP server with an absolute binary path (`os.Executable()`).
5. Validation plan item 2/5: note that Claude Code blocks foreground `sleep` in Bash and may auto-background shells; use an MCP tool or a wrapper script for blocking-call tests.
