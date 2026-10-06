// Package hook implements the `pair hook <name>` subcommands that the agent
// runs at the points of its work pair cares about: before and after an
// edit, at a prompt, at the end of a turn, when it needs a permission. Each
// handler reads what the agent wrote on stdin through the agent's adapter,
// does its work, logs an event, and never makes the tool call fail: main
// exits 0 for hooks whatever happens here.
package hook

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/fabioaraujo121/pair/internal/agent"
	"github.com/fabioaraujo121/pair/internal/config"
	"github.com/fabioaraujo121/pair/internal/events"
	"github.com/fabioaraujo121/pair/internal/nvim"
	"github.com/fabioaraujo121/pair/internal/session"
	"github.com/fabioaraujo121/pair/internal/status"
	"github.com/fabioaraujo121/pair/internal/tmux"
)

// Nvim is the part of the Neovim client the hooks use.
type Nvim interface {
	Show(ctx context.Context, file string, start, end int) (nvim.Shown, error)
	Diff(ctx context.Context, file, marks string) error
	Reload(ctx context.Context) error
}

// Handler holds the resolved dependencies for one hook invocation.
type Handler struct {
	Cfg    config.Config
	Log    *events.Log
	Nvim   Nvim
	Agent  agent.Agent
	Stdout io.Writer // hook JSON output (prompt)
}

// Run dispatches a hook by name. agentName says which agent wrote the
// input; empty means the configured one. The hooks are registered for the
// whole repository, so they also fire for an agent started outside pair:
// then they do nothing, and that conversation leaves the session alone.
func Run(name, agentName string, stdin io.Reader, stdout io.Writer) error {
	if !config.Active() {
		_, _ = io.Copy(io.Discard, stdin)
		return nil
	}
	data, err := io.ReadAll(stdin)
	if err != nil {
		return err
	}
	cfg := config.Load(agent.PeekCWD(data))
	if agentName == "" {
		agentName = cfg.Agent
	}
	ag, err := agent.For(agentName)
	if err != nil {
		return err
	}
	ev, err := ag.ParseHook(name, data)
	if err != nil {
		return fmt.Errorf("parse hook input: %w", err)
	}
	session.NoteConversation(cfg.StateDir, ev.Conversation)
	h := &Handler{Cfg: cfg, Log: events.Open(cfg.StateDir), Nvim: nvim.New(cfg.Sock), Agent: ag, Stdout: stdout}
	return h.Handle(ev)
}

// Handle runs the handler of the event's hook.
func (h *Handler) Handle(ev *agent.Event) error {
	switch ev.Hook {
	case "pre_edit":
		return h.PreEdit(ev)
	case "post_edit":
		return h.PostEdit(ev)
	case "stop":
		return h.Stop(ev)
	case "prompt":
		return h.Prompt(ev)
	case "notify":
		return h.Notify(ev)
	case "activity":
		return h.Activity(ev)
	default:
		return fmt.Errorf("unknown hook %q (want pre_edit, post_edit, stop, prompt, notify, activity)", ev.Hook)
	}
}

// who is the agent's name in a sentence.
func (h *Handler) who() string {
	if h.Agent == nil {
		return "Claude"
	}
	return h.Agent.Short()
}

// PreEdit points Neovim at the range the agent is about to change. When
// one call changes several files, the first is pointed at and all are
// logged.
func (h *Handler) PreEdit(ev *agent.Event) error {
	var nvErr error
	pointed := false
	for _, f := range ev.Files {
		file := h.absPath(f.Path)
		if file == "" {
			continue
		}
		start, end, found := 1, 1, false
		switch {
		case f.Delete:
			_ = h.Log.Append("pre_edit", map[string]any{"tool": ev.Tool, "file": file, "delete": true})
			_ = h.Log.Session("− " + h.rel(file) + " (removed)")
			continue
		case f.Create || ev.Kind == agent.KindWrite:
			if _, err := os.Stat(file); err != nil {
				_ = h.Log.Append("pre_edit", map[string]any{"tool": ev.Tool, "file": file, "new": true})
				_ = h.Log.Session("+ " + h.rel(file) + " (new file)")
				continue
			}
			found = true // the whole file is being rewritten: point at the top
		default:
			data, err := os.ReadFile(file)
			if err != nil {
				_ = h.Log.Append("pre_edit", map[string]any{"tool": ev.Tool, "file": file, "error": err.Error()})
				continue
			}
			start, end, found = LocateRange(string(data), f.Old)
		}
		if pointed {
			_ = h.Log.Append("pre_edit", map[string]any{"tool": ev.Tool, "file": file, "start": start, "end": end, "found": found})
			_ = h.Log.Session(fmt.Sprintf("→ %s:%d-%d", h.rel(file), start, end))
			continue
		}
		pointed = true
		h.state(tmux.StateWorking, "editing "+h.rel(file))
		shown, err := h.Nvim.Show(context.Background(), file, start, end)
		payload := map[string]any{"tool": ev.Tool, "file": file, "start": start, "end": end, "found": found}
		if err != nil {
			payload["nvim_error"] = err.Error()
			nvErr = err
		} else {
			payload["shown"] = shown
		}
		_ = h.Log.Append("pre_edit", payload)
		_ = h.Log.Session(fmt.Sprintf("→ %s:%d-%d", h.rel(file), start, end))
	}
	return nvErr
}

// PostEdit reloads Neovim, draws the change inside each file, and writes
// its summary to the activity log.
func (h *Handler) PostEdit(ev *agent.Event) error {
	ctx := context.Background()
	nvErr := h.Nvim.Reload(ctx)
	for _, f := range ev.Files {
		file := h.absPath(f.Path)
		if f.NewPath != "" {
			file = h.absPath(f.NewPath)
		}
		if file == "" {
			continue
		}
		if f.Delete {
			h.state(tmux.StateWorking, "removed "+h.rel(file))
			_ = h.Log.Session("− " + h.rel(file) + " (removed)")
			_ = h.Log.Append("post_edit", map[string]any{"tool": ev.Tool, "file": file, "deleted": true})
			continue
		}
		marks := h.marks(ctx, f, file)
		drawn := ""
		if nvErr == nil && !marks.Empty() {
			if data, err := json.Marshal(marks); err == nil {
				if err := h.Nvim.Diff(ctx, file, string(data)); err != nil {
					nvErr = err
				} else {
					drawn = marks.summary()
				}
			}
		}
		h.state(tmux.StateWorking, "edited "+h.rel(file))
		added, removed := marks.counts()
		_ = h.Log.Session(fmt.Sprintf("✎ %s (+%d −%d)", h.rel(file), added, removed))
		// Remember the file as the agent left it, so the next prompt can
		// report only what the engineer changed on top.
		if h.inside(file) {
			st := loadState(h.Cfg.StateDir)
			Snapshot(h.Cfg.StateDir, &st, file, h.withheld(ctx, file))
			_ = saveState(h.Cfg.StateDir, st)
		}
		payload := map[string]any{"tool": ev.Tool, "file": file, "added": added, "removed": removed}
		if drawn != "" {
			payload["drawn"] = drawn
		}
		if nvErr != nil {
			payload["nvim_error"] = nvErr.Error()
		}
		_ = h.Log.Append("post_edit", payload)
	}
	return nvErr
}

// Stop logs the end of a turn, hands the turn to the engineer in the status
// bar, and plays a chime without blocking.
func (h *Handler) Stop(ev *agent.Event) error {
	_ = h.Log.Append("stop", map[string]any{"session_id": ev.Conversation})
	_ = h.Log.Session("■ turn ended, your turn")
	h.state(tmux.StateIdle, "")
	chime("Glass")
	return nil
}

// Notify handles the agent's notifications. A permission prompt is easy
// to miss when nobody watches the agent's pane, so it raises the status
// bar alert and a sound; an idle reminder just marks the turn as the
// engineer's.
func (h *Handler) Notify(ev *agent.Event) error {
	msg := strings.TrimSpace(ev.Message)
	_ = h.Log.Append("notify", map[string]any{"message": msg, "idle": ev.Idle})
	if ev.Idle {
		h.state(tmux.StateIdle, "")
		return nil
	}
	_ = h.Log.Session("⚠ " + h.who() + " needs you: " + msg)
	status.Set(h.Cfg, tmux.StateAttention, msg)
	// The agent's pane is hidden: bring it in so the prompt can be read
	// and answered. It goes away again on the next sign of activity,
	// unless the engineer had opened it themselves.
	if h.Cfg.Session != "" && !tmux.AgentVisible(h.Cfg.Session) {
		if err := tmux.ShowAgent(h.Cfg.Session, h.Cfg.ClaudeHeight); err == nil {
			_ = os.WriteFile(h.autoShown(), nil, 0o600)
		}
	}
	chime("Funk")
	return nil
}

// Activity runs before every tool call and keeps the status bar saying
// what the agent is doing. It writes nothing to the log.
func (h *Handler) Activity(ev *agent.Event) error {
	detail := ""
	switch ev.Kind {
	case agent.KindPair:
		return nil // narrate, show and ask report their own state
	case agent.KindRead:
		detail = "reading " + h.rel(h.absPath(ev.Detail))
	case agent.KindEdit, agent.KindWrite:
		names := make([]string, 0, len(ev.Files))
		for _, f := range ev.Files {
			names = append(names, h.rel(h.absPath(f.Path)))
		}
		detail = "editing " + strings.Join(names, ", ")
	case agent.KindShell:
		detail = "running " + ev.Detail
	case agent.KindSearch:
		detail = "searching " + ev.Detail
	case agent.KindWeb:
		detail = "looking up " + ev.Detail
	case agent.KindDelegate:
		detail = "delegating " + ev.Detail
	case agent.KindMCP:
		detail = "using " + ev.Detail
	default:
		detail = firstNonEmpty(ev.Detail, strings.ToLower(ev.Tool))
	}
	h.state(tmux.StateWorking, strings.TrimSpace(detail))
	return nil
}

func firstNonEmpty(a, b string) string {
	if strings.TrimSpace(a) != "" {
		return a
	}
	return b
}

// state records what is happening and gives the screen back to the editor
// once the agent no longer waits on the engineer.
func (h *Handler) state(kind, detail string) {
	status.Set(h.Cfg, kind, detail)
	h.hideIfAutoShown()
}

// startTurn is state(working) at the beginning of a turn of the agent's.
func (h *Handler) startTurn() {
	status.StartTurn(h.Cfg)
	h.hideIfAutoShown()
}

// hideIfAutoShown hides the agent's pane when pair itself had opened it
// for a permission prompt.
func (h *Handler) hideIfAutoShown() {
	if _, err := os.Stat(h.autoShown()); err == nil {
		_ = os.Remove(h.autoShown())
		_ = tmux.HideAgent(h.Cfg.Session)
	}
}

// autoShown marks that the agent's pane was opened by pair, not by the
// engineer.
func (h *Handler) autoShown() string { return filepath.Join(h.Cfg.StateDir, "claude_autoshown") }

// LocateRange returns the 1-based first and last line of the first
// occurrence of old in content. found is false when old is empty or absent,
// in which case the range is line 1.
func LocateRange(content, old string) (start, end int, found bool) {
	if old == "" {
		return 1, 1, false
	}
	idx := strings.Index(content, old)
	if idx < 0 {
		return 1, 1, false
	}
	start = 1 + strings.Count(content[:idx], "\n")
	end = start + strings.Count(strings.TrimSuffix(old, "\n"), "\n")
	return start, end, true
}

func (h *Handler) absPath(p string) string {
	if p == "" {
		return ""
	}
	if !filepath.IsAbs(p) {
		p = filepath.Join(h.Cfg.Root, p)
	}
	return filepath.Clean(p)
}

func (h *Handler) rel(p string) string {
	if r, err := filepath.Rel(h.Cfg.Root, p); err == nil && !strings.HasPrefix(r, "..") {
		return r
	}
	return p
}

// chime plays a short system sound in its own session so the hook can exit
// immediately.
func chime(name string) {
	cmd := exec.Command("afplay", "/System/Library/Sounds/"+name+".aiff")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	_ = cmd.Start()
}
