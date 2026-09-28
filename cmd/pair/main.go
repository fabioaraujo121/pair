// pair turns a Claude Code session into a spoken pairing session. One binary
// serves the CLI, the Claude Code hooks and (from milestone 2) the MCP server.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"golang.org/x/term"

	"github.com/fabioaraujo121/pair/internal/cli"
	"github.com/fabioaraujo121/pair/internal/config"
	"github.com/fabioaraujo121/pair/internal/hook"
	"github.com/fabioaraujo121/pair/internal/logview"
	"github.com/fabioaraujo121/pair/internal/mcpserver"
	"github.com/fabioaraujo121/pair/internal/nvim"
	"github.com/fabioaraujo121/pair/internal/presence"
	"github.com/fabioaraujo121/pair/internal/replay"
	"github.com/fabioaraujo121/pair/internal/speech"
)

// version is set by goreleaser through -ldflags.
var version = "dev"

const usage = `pair — voice-driven pairing for Claude Code in the terminal

Usage:
  pair [start]       start a new session (editor | presence and activity), or go back
                     to the one that is running
       --continue    continue the last session: its log, and Claude's conversation
       --name <name> name the session
  pair sessions      list the sessions of this repository
  pair resume <id or name>   open a past session again, with Claude's conversation
  pair rename [<id or name>] <new name>
  pair claude        show or hide Claude Code's pane (hidden by default; F7 in the session)
  pair init [--lang en] [--voice Samantha] [--rate 190]
                     prepare this repository: hooks, CLAUDE.md block, MCP server, PAIRING.md
  pair doctor        check dependencies and configuration
                     --fix downloads the whisper model, --mic runs the microphone test
  pair stop          kill the session and any running say/rec
  pair hook <name>   Claude Code hooks: pre_edit, post_edit, stop, prompt
  pair hook listen   push-to-talk: record, transcribe, type into the Claude pane
  pair hook interrupt  stop Claude (Escape), then listen
  pair hook point --file f --start n [--end m] [--note t]   point Claude at a range
  pair mcp [--root <repo>]   stdio MCP server with narrate, show, ask (run by Claude Code)
  pair replay [<id or name>] [--speed N] [--mute] [--no-nvim]   play a session back
  pair log [--follow] [<id or name>]   a session as a conversation (the activity column)
  pair presence      the animated state indicator (the panel above the activity column)
  pair version

Environment: PAIR_SESSION, PAIR_SOCK, PAIR_HIGHLIGHT_TTL, PAIR_DIFF_LIMIT,
PAIR_VOICE, PAIR_RATE, PAIR_LANG, PAIR_WHISPER_MODEL, PAIR_ASK_MAX, PAIR_TTS, PAIR_STT,
PAIR_MUTE (see README.md).
`

func main() {
	args := os.Args[1:]
	cmd := "start"
	if len(args) > 0 {
		cmd, args = args[0], args[1:]
	}
	switch cmd {
	case "-h", "--help", "-v", "--version":
	default:
		if len(cmd) > 1 && cmd[0] == '-' { // `pair --continue`, `pair --name x`
			cmd, args = "start", os.Args[1:]
		}
	}

	var err error
	switch cmd {
	case "start":
		err = cli.Start(args)
	case "resume": // open a past session again: pair resume <id or name>
		key := "last"
		if len(args) > 0 {
			key = args[0]
		}
		err = cli.Start([]string{"--resume", key})
	case "sessions", "list", "ls":
		err = cli.Sessions(os.Stdout)
	case "rename":
		err = cli.Rename(args, os.Stdout)
	case "init":
		err = cli.Init(args)
	case "doctor":
		opts := cli.DoctorOptions{}
		for _, a := range args {
			switch a {
			case "--fix":
				opts.DownloadModel = true
			case "--mic":
				opts.MicTest = true
			}
		}
		err = cli.Doctor(os.Stdout, opts)
	case "stop":
		err = cli.Stop()
	case "hook":
		if len(args) == 0 {
			err = fmt.Errorf("usage: pair hook <pre_edit|post_edit|stop|prompt|listen|interrupt|point>")
			break
		}
		switch args[0] {
		case "listen": // voice commands read no stdin and report failures
			err = hook.Listen(args[1:], os.Stdout)
		case "interrupt":
			err = hook.Interrupt(args[1:], os.Stdout)
		case "point":
			err = hook.Point(args[1:], os.Stdout)
		case "click": // a click on a status bar button
			err = hook.Click(args[1:], os.Stdout)
		default:
			err = hook.Run(args[0], os.Stdin, os.Stdout)
			// Claude Code hooks must never fail the tool call: report and exit 0.
			if err != nil && os.Getenv("PAIR_DEBUG") != "" {
				fmt.Fprintln(os.Stderr, "pair hook:", err)
			}
			return
		}
	case "mcp":
		err = serveMCP(args)
	case "replay":
		err = runReplay(args)
	case "log":
		err = runLog(args)
	case "presence":
		err = runPresence()
	case "claude": // show or hide Claude Code's pane
		err = hook.ToggleClaude(args, os.Stdout)
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
	srv, err := mcpserver.New(config.Load(root), version)
	if err != nil {
		return err
	}
	return srv.Run(ctx, &mcp.StdioTransport{})
}

// runReplay plays a session log back: `pair replay [events.jsonl] [--speed N]
// [--mute] [--no-nvim]`.
func runReplay(args []string) error {
	cfg := config.Load("")
	path := filepath.Join(cfg.StateDir, "events.jsonl")
	opts := replay.Options{Speed: 1, Out: os.Stdout}
	mute, noNvim := cfg.Mute, false
	var err error
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--speed":
			if i+1 < len(args) {
				i++
				fmt.Sscan(args[i], &opts.Speed)
			}
		case "--mute":
			mute = true
		case "--no-nvim":
			noNvim = true
		default:
			if path, err = sessionFile(args[i]); err != nil {
				return err
			}
		}
	}
	events, err := replay.Load(path)
	if err != nil {
		return err
	}
	if !mute {
		if opts.Speaker, err = speech.NewSpeaker(cfg); err != nil {
			return err
		}
	}
	if !noNvim {
		client := nvim.New(cfg.Sock)
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		if _, err := client.Ping(ctx); err == nil {
			opts.Nvim = client
		} else {
			fmt.Fprintf(os.Stderr, "pair replay: Neovim not reachable at %s, printing jumps only\n", cfg.Sock)
		}
		cancel()
	}
	fmt.Fprintf(os.Stderr, "replaying %d events from %s at %gx\n", len(events), path, opts.Speed)
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	return replay.Run(ctx, events, opts)
}

// runLog shows the session as a conversation: `pair log [--follow] [file]`.
func runLog(args []string) error {
	root := os.Getenv("PAIR_ROOT")
	cfg := config.Load(root)
	path := filepath.Join(cfg.StateDir, "events.jsonl")
	follow := false
	for _, a := range args {
		switch a {
		case "--follow", "-f":
			follow = true
		default:
			p, err := sessionFile(a)
			if err != nil {
				return err
			}
			path = p
		}
	}
	if follow {
		ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
		defer stop()
		return logview.Follow(ctx, path, cfg.Root, os.Stdout)
	}
	width := 80
	if w, _, err := term.GetSize(int(os.Stdout.Fd())); err == nil && w > 0 {
		width = w
	}
	return logview.Render(path, cfg.Root, os.Stdout, width, term.IsTerminal(int(os.Stdout.Fd())))
}

// runPresence animates the state of the session until interrupted.
func runPresence() error {
	cfg := config.Load(os.Getenv("PAIR_ROOT"))
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	return presence.Run(ctx, cfg.StateDir, os.Stdout)
}

// sessionFile resolves an argument of `pair log` or `pair replay`: a file
// that exists, or the id or name of a session.
func sessionFile(arg string) (string, error) {
	if _, err := os.Stat(arg); err == nil {
		return arg, nil
	}
	return cli.SessionEvents(arg)
}
