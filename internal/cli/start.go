package cli

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	pair "github.com/fabioaraujo121/pair"
	"github.com/fabioaraujo121/pair/internal/agent"
	"github.com/fabioaraujo121/pair/internal/config"
	"github.com/fabioaraujo121/pair/internal/events"
	"github.com/fabioaraujo121/pair/internal/hook"
	"github.com/fabioaraujo121/pair/internal/session"
	"github.com/fabioaraujo121/pair/internal/speech"
	"github.com/fabioaraujo121/pair/internal/status"
	"github.com/fabioaraujo121/pair/internal/tmux"
)

// Start creates the tmux session or attaches to it when it already exists.
//
// The screen answers three questions: is the agent running (the presence
// animation, the status bar and the colour of the borders), which files is
// it changing (Neovim, the main area) and why (the activity column). The
// agent itself runs in a background window: F7 or the button with its
// name brings its pane under the editor and sends it away again.
//
//	┌──────────────────────────────┬──────────────────┐
//	│ neovim: tree | editor        │ presence         │
//	│              | .pair/PAIRING ├──────────────────┤
//	│                              │ activity         │
//	│ (the agent, when shown)      │                  │
//	└──────────────────────────────┴──────────────────┘
func Start(args []string) error {
	opts, err := parseStart(args)
	if err != nil {
		return err
	}
	cfg := config.Load("")
	if opts.agent != "" {
		cfg.Agent = opts.agent
	}
	ag, err := agent.For(cfg.Agent)
	if err != nil {
		return err
	}
	for _, bin := range []string{"tmux", "nvim", ag.Binary()} {
		if _, err := exec.LookPath(bin); err != nil {
			return fmt.Errorf("%s is not on PATH; run `pair doctor`", bin)
		}
	}
	// The first versions wrote hooks and instructions into the repository.
	// Left there, the hooks would run twice and the instructions would
	// reach every session, with or without pair.
	if left := ag.Leftovers(cfg.Root); len(left) > 0 {
		return fmt.Errorf("an earlier version of pair wrote into %s: %s. Run `pair uninstall` once, then `pair`", tildePath(cfg.Root), strings.Join(left, ", "))
	}
	if config.SockTooLong(cfg.Sock) {
		return fmt.Errorf("PAIR_SOCK is %d bytes; macOS allows %d. Use a shorter path or 127.0.0.1:<port>", len(cfg.Sock), config.MaxSockPath)
	}
	pluginDir, err := InstallPlugin(cfg.UserDir)
	if err != nil {
		return err
	}

	if hasSession(cfg.Session) {
		if opts.resume != "" || opts.name != "" {
			return fmt.Errorf("a session is already running here; `pair stop` it first, or run `pair` to go back to it")
		}
		return attach(cfg.Session)
	}

	meta, continued, err := openSession(cfg, opts)
	if err != nil {
		return err
	}
	// A continued session goes on with the agent that held its
	// conversation, unless asked otherwise.
	if continued && meta.Agent != "" && opts.agent == "" && meta.Agent != cfg.Agent {
		if a, err := agent.For(meta.Agent); err == nil {
			cfg.Agent, ag = meta.Agent, a
		}
	}
	if meta.Agent != ag.Name() {
		meta.Agent = ag.Name()
		_ = session.Save(cfg.StateDir, meta)
	}
	log := events.Open(cfg.StateDir)
	if f, err := os.OpenFile(log.SessionLogPath(), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600); err == nil {
		_ = f.Close()
	}
	if !continued {
		hook.ResetState(cfg.StateDir) // fresh baseline for hand-edit detection
	}
	if err := createSession(cfg, ag, pluginDir, log, meta, continued); err != nil {
		return err
	}
	kind, word := "session_start", "started"
	if continued {
		kind, word = "session_continue", "continued"
	}
	_ = log.Append(kind, map[string]any{"root": cfg.Root, "id": meta.ID, "name": meta.Label(), "agent": ag.Name()})
	_ = log.Session(fmt.Sprintf("● pair session %s %s in %s", meta.Label(), word, tildePath(cfg.Root)))
	return attach(cfg.Session)
}

// startOptions are the flags of `pair start`.
type startOptions struct {
	name      string // --name: the name of the new session
	resume    string // --resume <id or name>, or "last" for --continue
	continue_ bool
	agent     string // --agent: the coding agent for this session
}

func parseStart(args []string) (startOptions, error) {
	var o startOptions
	for i := 0; i < len(args); i++ {
		value := func(flag string) (string, error) {
			if i+1 >= len(args) || strings.HasPrefix(args[i+1], "-") {
				return "", fmt.Errorf("%s needs a value", flag)
			}
			i++
			return args[i], nil
		}
		var err error
		switch a := args[i]; a {
		case "-c", "--continue":
			o.continue_, o.resume = true, "last"
		case "-n", "--name":
			o.name, err = value(a)
		case "-r", "--resume":
			o.resume, err = value(a)
		case "-a", "--agent":
			if o.agent, err = value(a); err == nil && !agent.Known(o.agent) {
				err = fmt.Errorf("unknown agent %q (one of %s)", o.agent, strings.Join(agent.Names(), ", "))
			}
		default:
			err = fmt.Errorf("unknown option %q (usage: pair [--continue] [--name <name>] [--resume <id or name>] [--agent claude|codex])", a)
		}
		if err != nil {
			return o, err
		}
	}
	return o, nil
}

// openSession decides which session the screen is about to show: a new
// one, the last one continued, or a past one opened again.
func openSession(cfg config.Config, o startOptions) (session.Meta, bool, error) {
	if o.resume == "" {
		m, err := session.New(cfg.StateDir, o.name, time.Now())
		return m, false, err
	}
	if len(session.List(cfg.StateDir)) == 0 {
		if err := session.Archive(cfg.StateDir); err != nil { // a log from before sessions were kept apart
			return session.Meta{}, false, err
		}
	}
	m, err := session.Restore(cfg.StateDir, o.resume)
	if errors.Is(err, session.ErrNone) {
		return m, false, fmt.Errorf("there is no earlier session in this repository to continue; start one with `pair`")
	}
	if err != nil {
		return m, false, err
	}
	if o.name != "" {
		m.Name = o.name
		_ = session.Save(cfg.StateDir, m)
	}
	return m, true, nil
}

// agentArgs builds the arguments of the agent's command: the session's
// hooks and MCP server, handed to this process only; the conversation to
// resume when the session is continued; the session's name; and what the
// engineer configured.
func agentArgs(cfg config.Config, ag agent.Agent, exe string, m session.Meta, continued bool) (string, error) {
	o := agent.Launch{Name: m.Name, Exe: exe, Root: cfg.Root, RunDir: cfg.RunDir, AskTimeout: cfg.AskWait + cfg.AskMax + 120}
	if continued {
		// The id comes from a file in the repository: it is used only
		// when it has the shape of one.
		if ag.ConversationID(m.Conversation) {
			o.Resume = m.Conversation
		} else {
			o.Continue = true
		}
	}
	args, err := ag.Launch(o)
	if err != nil {
		return "", err
	}
	parts := make([]string, 0, len(args)+1)
	for _, a := range args {
		parts = append(parts, shellQuote(a))
	}
	if cfg.ClaudeArgs != "" {
		parts = append(parts, cfg.ClaudeArgs) // the engineer's own, from the environment or ~/.pair/config
	}
	if len(parts) == 0 {
		return "", nil
	}
	return " " + strings.Join(parts, " "), nil
}

// scratchpad makes sure the shared scratchpad exists for the session. It
// lives in .pair, which is private to the session and ignored by git.
func scratchpad(cfg config.Config) {
	path := filepath.Join(cfg.StateDir, "PAIRING.md")
	if _, err := os.Stat(path); os.IsNotExist(err) {
		_ = os.WriteFile(path, []byte(pair.PairingTemplate), 0o600)
	}
}

func createSession(cfg config.Config, ag agent.Agent, pluginDir string, log *events.Log, meta session.Meta, continued bool) error {
	removeSocket(cfg.Sock) // stale, from a previous session
	_ = os.RemoveAll(cfg.RunDir)
	s := cfg.Session
	exe, _ := os.Executable()
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	events.Private(cfg.StateDir)
	scratchpad(cfg)
	agentArguments, err := agentArgs(cfg, ag, exe, meta, continued)
	if err != nil {
		return err
	}
	cols, lines := terminalSize()
	fail := func(err error) error {
		_, _ = tmuxRun("kill-session", "-t", "="+s)
		return err
	}

	// The session environment reaches every pane, so the agent is started
	// by name and any shell wrapper the user has around it still applies.
	newSession := []string{"new-session", "-d", "-P", "-F", "#{pane_id}", "-s", s, "-n", tmux.MainWindow,
		"-x", cols, "-y", lines, "-c", cfg.Root,
		"-e", "PAIR_SOCK=" + cfg.Sock, "-e", "PAIR_ROOT=" + cfg.Root, "-e", "PAIR_SESSION=" + s, "-e", "PAIR_ACTIVE=1",
		"-e", "PAIR_TMUX_SOCKET=" + tmux.Socket, "-e", "PAIR_AGENT=" + ag.Name()}
	for _, kv := range ag.Env() {
		newSession = append(newSession, "-e", kv)
	}
	editor, err := tmuxRun(newSession...)
	if err != nil {
		return err
	}
	logPane, err := tmuxRun("split-window", "-h", "-P", "-F", "#{pane_id}", "-l", cfg.LogWidth, "-t", editor, "-c", cfg.Root)
	if err != nil {
		return fail(err)
	}
	// -b: the new pane goes above the activity column.
	presencePane, err := tmuxRun("split-window", "-v", "-b", "-P", "-F", "#{pane_id}", "-l", cfg.Presence, "-t", logPane, "-c", cfg.Root)
	if err != nil {
		return fail(err)
	}
	// The agent lives in a window of its own, in the background.
	agentPane, err := tmuxRun("new-window", "-d", "-P", "-F", "#{pane_id}", "-n", tmux.AgentWindow, "-t", "="+s+":", "-c", cfg.Root)
	if err != nil {
		return fail(err)
	}

	who := ag.Short()
	steps := [][]string{
		{"set-environment", "-t", "=" + s, "PAIR_PANE_NVIM", editor},
		{"set-environment", "-t", "=" + s, "PAIR_PANE_LOG", logPane},
		{"set-environment", "-t", "=" + s, "PAIR_PANE_PRESENCE", presencePane},
		{"set-environment", "-t", "=" + s, "PAIR_PANE_" + tmux.RoleAgent, agentPane},
		{"set-option", "-p", "-t", editor, "@pair_name", "files: what " + who + " reads and changes"},
		{"set-option", "-p", "-t", presencePane, "@pair_name", strings.ToLower(who)},
		{"set-option", "-p", "-t", logPane, "@pair_name", "activity: what " + who + " does and why"},
		{"set-option", "-p", "-t", agentPane, "@pair_name", strings.ToLower(ag.Title()) + ": type here · " + cfg.Keys.Agent + " hides this pane"},
	}
	steps = append(steps, sessionChrome(exe, who, cfg.Keys)...)
	for _, args := range steps {
		if _, err := tmuxRun(args...); err != nil {
			return fail(err)
		}
	}
	status.Set(cfg, tmux.StateIdle, "")

	nvimCmd := "clear; nvim --cmd " + shellQuote("set rtp^="+vimOption(pluginDir)) +
		" --cmd " + shellQuote("let g:pair_bin="+vimString(exe)+" | let g:pair_root="+vimString(cfg.Root)) +
		" --listen " + shellQuote(cfg.Sock) +
		" -c " + shellQuote("lua require('pair').layout()")
	tailCmd := "clear; " + shellQuote(exe) + " log --follow"
	presenceCmd := "clear; " + shellQuote(exe) + " presence"
	agentCmd := "clear; " + ag.Binary() + agentArguments

	keys := [][]string{
		{"send-keys", "-t", editor, nvimCmd, "Enter"},
		{"send-keys", "-t", logPane, tailCmd, "Enter"},
		{"send-keys", "-t", presencePane, presenceCmd, "Enter"},
		{"send-keys", "-t", agentPane, agentCmd, "Enter"},
		{"select-pane", "-t", editor},
	}
	for _, args := range keys {
		if _, err := tmuxRun(args...); err != nil {
			return err
		}
	}
	greet(cfg, exe)
	if cfg.ClaudeShown {
		return tmux.ShowAgent(s, cfg.ClaudeHeight)
	}
	// A repository opened for the first time makes the agent ask whether
	// the folder is trusted, before any hook runs. Look once it has had
	// time to start, and open its pane if it is waiting.
	_, _ = tmuxRun("run-shell", "-b", "-d", "5", shellQuote(exe)+" agent --if-waiting >/dev/null 2>&1")
	return nil
}

// greet has the first-session sentence spoken, the first time pair runs on
// this machine, by a process of the tmux server's: the start goes on to
// attach meanwhile, and the sentence comes once the screen is up.
func greet(cfg config.Config, exe string) {
	if !hook.WantsGreeting(cfg) {
		return
	}
	_, _ = tmuxRun("run-shell", "-b", "-d", "2", shellQuote(exe)+" hook greet --root "+shellQuote(cfg.Root)+" >/dev/null 2>&1")
}

// sessionChrome returns the options and key bindings of pair's tmux server:
// mouse, labelled panes, a status line with the current state and the keys.
// They are global to the server, which is pair's own.
func sessionChrome(exe, who string, keys config.Keys) [][]string {
	// The bindings belong to the server, which may hold the sessions of
	// several repositories: each command is told which session the key was
	// pressed in. tmux expands #{session_name} when the key is pressed.
	in := "PAIR_SESSION=#{session_name} " + shellQuote(exe)
	listen := in + " hook listen >/dev/null 2>&1"
	interrupt := in + " hook interrupt >/dev/null 2>&1"
	toggle := in + " agent >/dev/null 2>&1"
	quit := in + " hook click quit >/dev/null 2>&1"
	return [][]string{
		{"set-option", "-g", "mouse", "on"},
		// Full colour for Neovim's syntax highlighting.
		{"set-option", "-g", "default-terminal", "tmux-256color"},
		{"set-option", "-as", "terminal-features", ",*:RGB"},
		{"set-option", "-g", "escape-time", "10"},
		{"set-option", "-g", "focus-events", "on"},
		{"set-option", "-g", "pane-border-status", "top"},
		{"set-option", "-g", "pane-border-format", " #{@pair_name} "},
		{"set-option", "-g", "status-interval", "1"},
		{"set-option", "-g", "status-left-length", "90"},
		{"set-option", "-g", "status-left", " pair #{@pair_state} "},
		{"set-option", "-g", "status-right-length", "90"},
		// Buttons: clicking one runs `pair hook click <name>`. Each is
		// labelled with its key, the configured one.
		{"set-option", "-g", "status-right", "#[range=user|talk]#{?#{==:#{@pair_mic},on},#[fg=white#,bg=red#,bold] ■ stop " + keys.Talk + " ,#[fg=black#,bg=white] 🎙 talk " + keys.Talk + " }#[default]#[norange] " +
			"#[range=user|stop]#[fg=black,bg=white] ✋ interrupt " + keys.Interrupt + " #[default]#[norange] " +
			"#[range=user|agent]#[fg=black,bg=white] ⌨ " + strings.ToLower(who) + " " + keys.Agent + " #[default]#[norange] " +
			"#[range=user|quit]#[fg=black,bg=white] ⏻ quit " + keys.Quit + " #[default]#[norange] "},
		{"bind-key", "-n", "MouseDown1Status", "run-shell", "-b", in + " hook click '#{mouse_status_range}' >/dev/null 2>&1"},
		{"set-option", "-g", "window-status-current-format", ""},
		{"set-option", "-g", "window-status-format", ""},
		// Voice keys work from any pane, with and without the prefix.
		{"bind-key", "-n", keys.Talk, "run-shell", "-b", listen},
		{"bind-key", "-n", keys.Interrupt, "run-shell", "-b", interrupt},
		{"bind-key", "t", "run-shell", "-b", listen},
		{"bind-key", "i", "run-shell", "-b", interrupt},
		// The agent key shows its pane under the editor and hides it again.
		{"bind-key", "-n", keys.Agent, "run-shell", "-b", toggle},
		{"bind-key", "c", "run-shell", "-b", toggle},
		// The quit key ends the session: the agent, the panes, the voice.
		// Pressed twice, so a stray key does not end it.
		{"bind-key", "-n", keys.Quit, "run-shell", "-b", quit},
		{"bind-key", "q", "run-shell", "-b", quit},
	}
}

// attach hands the terminal to tmux: switch-client from inside pair's
// server, exec attach from a plain terminal, and print instructions
// otherwise.
func attach(session string) error {
	manual := fmt.Sprintf("tmux -L %s attach -t %s", tmux.Socket, session)
	if os.Getenv("TMUX") != "" {
		if tmux.InsideServer() {
			_, err := tmuxRun("switch-client", "-t", "="+session)
			return err
		}
		fmt.Printf("session %q is running on pair's own tmux server. You are inside another tmux; open a plain terminal and run `pair`, or nest with: TMUX= %s\n", session, manual)
		return nil
	}
	if !stdinIsTerminal() {
		fmt.Printf("session %q is running; attach with: %s\n", session, manual)
		return nil
	}
	tmuxBin, err := exec.LookPath("tmux")
	if err != nil {
		return err
	}
	argv := append([]string{"tmux"}, tmux.Args("attach", "-t", "="+session)...)
	return syscall.Exec(tmuxBin, argv, os.Environ())
}

// Stop ends the session of this repository, and only that one: its tmux
// session, and the speech or recording it may have in progress.
func Stop() error {
	cfg := config.Load("")
	switch {
	case hasSession(cfg.Session):
		if _, err := tmuxRun("kill-session", "-t", "="+cfg.Session); err != nil {
			return err
		}
		speech.KillSpeech(cfg.RunDir)
		speech.KillRecording(cfg.RunDir)
	case stopLegacySession("pair"):
		fmt.Println("stopped a session created by an earlier version of pair")
	default:
		fmt.Printf("no pair session is running for %s\n", tildePath(cfg.Root))
		for name, root := range tmux.Sessions() {
			fmt.Printf("  %s is running for %s: stop it from there (cd %s && pair stop)\n", name, tildePath(root), tildePath(root))
		}
		return nil
	}
	removeSocket(cfg.Sock)
	session.End(cfg.StateDir, time.Now())
	_ = events.Open(cfg.StateDir).Session("○ pair session stopped")
	if m, ok := session.Current(cfg.StateDir); ok {
		fmt.Printf("session %q stopped. Continue it with `pair --continue`, see all with `pair sessions`.\n", m.Label())
	}
	return nil
}

// stopLegacySession kills a session that an earlier version created on the
// user's default tmux server. It only touches a session that carries
// pair's PAIR_SOCK variable.
func stopLegacySession(session string) bool {
	out, err := exec.Command("tmux", "show-environment", "-t", "="+session, "PAIR_SOCK").Output()
	if err != nil || !strings.HasPrefix(strings.TrimSpace(string(out)), "PAIR_SOCK=") {
		return false
	}
	return exec.Command("tmux", "kill-session", "-t", "="+session).Run() == nil
}

// removeSocket removes a unix socket, and nothing that is not one.
func removeSocket(path string) {
	if config.IsTCP(path) {
		return
	}
	if fi, err := os.Lstat(path); err == nil && fi.Mode()&os.ModeSocket != 0 {
		_ = os.Remove(path)
	}
}

// vimString quotes s as a Vim single-quoted string.
func vimString(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

// vimOption escapes a path for a comma-separated Vim option given to :set.
func vimOption(path string) string {
	return strings.NewReplacer(`\`, `\\`, " ", `\ `, ",", `\,`, "|", `\|`).Replace(path)
}

// tildePath abbreviates the home directory for display.
func tildePath(p string) string {
	if home, err := os.UserHomeDir(); err == nil && strings.HasPrefix(p, home+"/") {
		return "~" + p[len(home):]
	}
	return p
}
