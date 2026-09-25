// pair turns a Claude Code session into a spoken pairing session. One binary
// serves the CLI, the Claude Code hooks and (from milestone 2) the MCP server.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/fabioaraujo121/pair/internal/cli"
	"github.com/fabioaraujo121/pair/internal/config"
	"github.com/fabioaraujo121/pair/internal/hook"
	"github.com/fabioaraujo121/pair/internal/mcpserver"
)

// version is set by goreleaser through -ldflags.
var version = "dev"

const usage = `pair — voice-driven pairing for Claude Code in the terminal

Usage:
  pair [start]       create the tmux session (Claude Code | Neovim / log) or attach to it
  pair init          write hooks and permissions to .claude/settings.json, create PAIRING.md
  pair doctor        check dependencies and configuration
  pair stop          kill the session and any running say/rec
  pair hook <name>   hook handler run by Claude Code: pre_edit, post_edit, stop, prompt
  pair mcp [--root <repo>]   stdio MCP server with narrate, show, ask (run by Claude Code)
  pair version

Environment: PAIR_SESSION, PAIR_SOCK, PAIR_HIGHLIGHT_TTL, PAIR_DIFF_LIMIT,
PAIR_VOICE, PAIR_RATE, PAIR_LANG, PAIR_WHISPER_MODEL, PAIR_ASK_MAX, PAIR_MUTE (see PRD.md).
`

func main() {
	args := os.Args[1:]
	cmd := "start"
	if len(args) > 0 {
		cmd, args = args[0], args[1:]
	}

	var err error
	switch cmd {
	case "start":
		err = cli.Start()
	case "init":
		err = cli.Init()
	case "doctor":
		err = cli.Doctor(os.Stdout)
	case "stop":
		err = cli.Stop()
	case "hook":
		if len(args) == 0 {
			err = fmt.Errorf("usage: pair hook <pre_edit|post_edit|stop|prompt>")
		} else {
			err = hook.Run(args[0], os.Stdin)
		}
		// Hooks must never fail the tool call: report and exit 0.
		if err != nil && os.Getenv("PAIR_DEBUG") != "" {
			fmt.Fprintln(os.Stderr, "pair hook:", err)
		}
		return
	case "mcp":
		err = serveMCP(args)
	case "version", "-v", "--version":
		fmt.Println("pair", version)
	case "help", "-h", "--help":
		fmt.Print(usage)
	default:
		fmt.Fprintf(os.Stderr, "pair: unknown command %q\n\n%s", cmd, usage)
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "pair:", err)
		os.Exit(1)
	}
}

// serveMCP runs the stdio MCP server until stdin closes or a signal arrives.
// --root names the repository; it defaults to the working directory, which
// is where Claude Code starts a local-scope server.
func serveMCP(args []string) error {
	root := ""
	for i := 0; i < len(args); i++ {
		if args[i] == "--root" && i+1 < len(args) {
			root = args[i+1]
			i++
		}
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	return mcpserver.New(config.Load(root), version).Run(ctx, &mcp.StdioTransport{})
}
