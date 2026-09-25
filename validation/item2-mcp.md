# Validation item 2 (MCP): Go SDK stdio server, blocking calls, permissions, cancellation

Environment: macOS 26.6.2 arm64, Claude Code 2.1.282 (nested via `env -u CLAUDECODE claude --model claude-haiku-4-5-20251001`), Go 1.27.1, `github.com/modelcontextprotocol/go-sdk` v1.8.0, tmux 3.7c. The MCP server was registered only through `--mcp-config <abs>/mcp.json --strict-mcp-config`; `permissions.allow` lived in the scratch project's `.claude/settings.json`. Nothing under `~/.claude` was touched.

Note: the subagent that ran this item stalled while grepping the fetched docs and never wrote its own report. This file was assembled from the raw evidence it left on disk (server source, `tool.log.*`, timestamped `tmux capture-pane` snapshots, fetched docs); those files are in `item2-evidence/`.

## Summary

| # | Claim | Verdict | Evidence |
| --- | --- | --- | --- |
| a | go-sdk API is `mcp.NewServer` / `mcp.AddTool` / `mcp.StdioTransport` | VERIFIED | `server.go` builds against v1.8.0; standalone `initialize` / `tools/list` / `tools/call` exchange answered (protocol 2025-06-18). Handler signature is `func(ctx, *mcp.CallToolRequest, In) (*mcp.CallToolResult, any, error)`. |
| b | With `CLAUDE_CODE_MCP_AUTO_BACKGROUND_MS=0` a 150 s call stays blocking | VERIFIED | Run A: `start 150` 23:08:21.892, `done` 23:10:51.895. UI at 23:10:43 still `Calling pair · 2m 21s…`, then `Called pair` / `DONE` / `Baked for 2m 36s`. No background message. |
| c | Without the env var the call is auto-backgrounded at ~2 min | VERIFIED | Run B: at 2m 2s still `Calling pair · 1m 57s…`; at 23:14:33 `Called pair` / `Waiting for sleep to complete...` / `done · 1 MCP task still running`; later `MCP task km95pk3r (pair/sleep) completed.` then `DONE`. The mitigation is necessary. |
| d | `permissions.allow` for the tool suppresses the prompt | VERIFIED | Session ran in `manual mode` (no `acceptEdits`); no permission prompt in any snapshot; trust dialog noted `This folder pre-approves 1 tool permission in .claude/settings.json`. |
| e | Escape cancels the handler's `ctx` | VERIFIED | Escape sent 23:11:37.110; `tool.log`: `cancelled: context canceled` at 23:11:37.182 (72 ms). UI: `Called pair` / `Interrupted · What should Claude do instead?`. Item 5 saw the wire message: `notifications/cancelled {"requestId":2,"reason":"AbortError: user-cancel"}`; the server process is not killed. |
| f | The env var and the timeouts are documented | VERIFIED | MCP docs: "…environment variable in milliseconds to change the threshold, or set it to `0` to turn automatic backgrounding off. Setting `CLAUDE_CODE_DISABLE_BACKGROUND_TASKS` to `1` also turns it off, along with all other background task features." `MCP_TOOL_TIMEOUT` default "about 28 hours when that variable is unset"; idle timeout "300000 (5 minutes) for network servers and 1800000 (30 minutes) for stdio servers" — a call whose server sends nothing for that long aborts (idle, not total). |
| g | `-p` mode | observed | `claude -p` call with seconds=150 ran 23:15:56 → 23:18:26 and printed `DONE` (blocking). |

## server.go (verbatim)

```go
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const logPath = ".../item2/tool.log"

type SleepInput struct {
	Seconds int `json:"seconds" jsonschema:"number of seconds to sleep"`
}

func logLine(format string, a ...any) { /* append "HH:MM:SS.mmm pid=N msg" */ }

func sleepTool(ctx context.Context, req *mcp.CallToolRequest, in SleepInput) (*mcp.CallToolResult, any, error) {
	logLine("start %d", in.Seconds)
	select {
	case <-time.After(time.Duration(in.Seconds) * time.Second):
		logLine("done")
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf("slept %ds", in.Seconds)}}}, nil, nil
	case <-ctx.Done():
		logLine("cancelled: %v", ctx.Err())
		return nil, nil, ctx.Err()
	}
}

func main() {
	server := mcp.NewServer(&mcp.Implementation{Name: "pair", Version: "0.0.1"}, nil)
	mcp.AddTool(server, &mcp.Tool{Name: "sleep", Description: "Sleep for N seconds and return"}, sleepTool)
	if err := server.Run(context.Background(), &mcp.StdioTransport{}); err != nil {
		log.Fatal(err)
	}
}
```

Full source: `item2-evidence/server.go`.

## Tool logs

```
# Run A (env var = 0) and run C (Escape), same server process
23:08:04.717 pid=25542 server starting
23:08:21.892 pid=25542 start 150
23:10:51.895 pid=25542 done
23:11:18.743 pid=25542 start 90
23:11:37.182 pid=25542 cancelled: context canceled     # Escape sent at 23:11:37.110

# Run B (control, no env var)
23:11:56.016 pid=31030 server starting
23:12:15.592 pid=31030 start 150
23:14:45.595 pid=31030 done

# -p run
23:15:50.065 pid=34156 server starting
23:15:56.159 pid=34156 start 150
23:18:26.160 pid=34156 done
```

## UI snapshots

Run A at 2m 21s (`item2-evidence/A-08-2m21s-still-blocking.txt`):

```
❯ Call the sleep tool with seconds=150, then reply exactly DONE.
⏺ Calling pair · 2m 21s…
✻ Hyperspacing… (2m 26s · ↓ 262 tokens)
  ⏸ manual mode on · esc to interrupt · ← for agents
```

Run B at 2m 7s (`item2-evidence/B-07-auto-backgrounded.txt`):

```
❯ Call the sleep tool with seconds=150, then reply exactly DONE.
  Called pair
⏺ Waiting for sleep to complete...
✻ Sautéed for 2m 7s · done 11:14 PM · 1 MCP task still running
  ⏸ manual mode on · 1 MCP task · ← for agents · ↓ to manage
```

Run B end (`item2-evidence/B-08-task-completed.txt`):

```
⏺ MCP task km95pk3r (pair/sleep) completed.
⏺ DONE
```

Run C after Escape (`item2-evidence/C-02-after-escape.txt`):

```
❯ Call the sleep tool with seconds=90, then reply exactly DONE.
  Called pair
  ⎿  Interrupted · What should Claude do instead?
```

## Consequences for the PRD

1. Keep `CLAUDE_CODE_MCP_AUTO_BACKGROUND_MS=0` exported by `pair start`; it is documented and it is needed (run B). `CLAUDE_CODE_DISABLE_BACKGROUND_TASKS=1` is a documented alternative but disables all background features, so prefer the narrower one.
2. Spell out the cancellation contract in Component 2: Escape delivers `ctx` cancellation within ~100 ms, the server and any child process survive, late results are discarded. `narrate` / `ask` must select on `ctx.Done()` and kill `say` / `rec` / `whisper-cli` themselves.
3. The 30-minute figure in the Timeouts paragraph is an idle timeout (no server notification for 30 min aborts the call), not a total. Irrelevant at `PAIR_ASK_MAX=60` but the wording should match.
4. The nested session's workspace-trust dialog defaults to "No, exit"; `pair start` must not assume Claude Code boots straight to the prompt in a fresh repo.
