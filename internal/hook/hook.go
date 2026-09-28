// Package hook implements the `pair hook <name>` subcommands that Claude Code
// runs through .claude/settings.json. Each handler reads the hook's JSON on
// stdin, does its work, logs an event, and never makes the tool call fail:
// main exits 0 for hooks whatever happens here.
package hook

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/fabioaraujo121/pair/internal/config"
	"github.com/fabioaraujo121/pair/internal/events"
	"github.com/fabioaraujo121/pair/internal/nvim"
	"github.com/fabioaraujo121/pair/internal/status"
	"github.com/fabioaraujo121/pair/internal/tmux"
)

// Input is the JSON Claude Code writes to a hook's stdin (fields observed on
// 2.1.282; unknown fields are ignored).
type Input struct {
	SessionID      string          `json:"session_id"`
	TranscriptPath string          `json:"transcript_path"`
	CWD            string          `json:"cwd"`
	PermissionMode string          `json:"permission_mode"`
	HookEventName  string          `json:"hook_event_name"`
	ToolName       string          `json:"tool_name"`
	ToolUseID      string          `json:"tool_use_id"`
	ToolInput      ToolInput       `json:"tool_input"`
	ToolResponse   json.RawMessage `json:"tool_response"`
	Prompt         string          `json:"prompt"`
	Message        string          `json:"message"`           // Notification
	Notification   string          `json:"notification_type"` // Notification
}

// ToolInput covers the Edit and Write tools.
type ToolInput struct {
	Command     string `json:"command"`     // Bash
	Description string `json:"description"` // Bash, Agent
	Pattern     string `json:"pattern"`     // Grep, Glob
	URL         string `json:"url"`         // WebFetch
	Query       string `json:"query"`       // WebSearch
	FilePath    string `json:"file_path"`
	OldString   string `json:"old_string"`
	NewString   string `json:"new_string"`
	ReplaceAll  bool   `json:"replace_all"`
	Content     string `json:"content"`
}

// Parse decodes hook input. An empty stdin yields an empty Input, not an
// error, so hooks can be run by hand.
func Parse(r io.Reader) (*Input, error) {
	var in Input
	err := json.NewDecoder(r).Decode(&in)
	if errors.Is(err, io.EOF) {
		return &in, nil
	}
	if err != nil {
		return nil, err
	}
	return &in, nil
}

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
	Stdout io.Writer // hook JSON output (UserPromptSubmit)
}

// Run dispatches a hook by name.
func Run(name string, stdin io.Reader, stdout io.Writer) error {
	in, err := Parse(stdin)
	if err != nil {
		return fmt.Errorf("parse hook input: %w", err)
	}
	cfg := config.Load(in.CWD)
	h := &Handler{Cfg: cfg, Log: events.Open(cfg.StateDir), Nvim: nvim.New(cfg.Sock), Stdout: stdout}
	switch name {
	case "pre_edit":
		return h.PreEdit(in)
	case "post_edit":
		return h.PostEdit(in)
	case "stop":
		return h.Stop(in)
	case "prompt":
		return h.Prompt(in)
	case "notify":
		return h.Notify(in)
	case "activity":
		return h.Activity(in)
	default:
		return fmt.Errorf("unknown hook %q (want pre_edit, post_edit, stop, prompt, notify, activity)", name)
	}
}

// PreEdit points Neovim at the range Claude is about to change.
func (h *Handler) PreEdit(in *Input) error {
	file := h.absPath(in.ToolInput.FilePath)
	if file == "" {
		return nil
	}
	start, end, found := 1, 1, false
	switch in.ToolName {
	case "Edit":
		data, err := os.ReadFile(file)
		if err != nil {
			_ = h.Log.Append("pre_edit", map[string]any{"tool": in.ToolName, "file": file, "error": err.Error()})
			return nil
		}
		start, end, found = LocateRange(string(data), in.ToolInput.OldString)
	case "Write":
		if _, err := os.Stat(file); err != nil {
			_ = h.Log.Append("pre_edit", map[string]any{"tool": in.ToolName, "file": file, "new": true})
			_ = h.Log.Session("+ " + h.rel(file) + " (new file)")
			return nil
		}
		found = true // the whole file is being rewritten: point at the top
	default:
		return nil
	}

	h.state(tmux.StateWorking, "editing "+h.rel(file))
	ctx := context.Background()
	shown, nvErr := h.Nvim.Show(ctx, file, start, end)
	payload := map[string]any{"tool": in.ToolName, "file": file, "start": start, "end": end, "found": found}
	if nvErr != nil {
		payload["nvim_error"] = nvErr.Error()
	} else {
		payload["shown"] = shown
	}
	_ = h.Log.Append("pre_edit", payload)
	_ = h.Log.Session(fmt.Sprintf("→ %s:%d-%d", h.rel(file), start, end))
	return nvErr
}

// PostEdit reloads Neovim, draws the change inside the file, and writes
// its summary and diff to the activity log.
func (h *Handler) PostEdit(in *Input) error {
	file := h.absPath(in.ToolInput.FilePath)
	ctx := context.Background()
	nvErr := h.Nvim.Reload(ctx)
	drawn := ""
	var marks Marks
	if file != "" {
		marks = h.marks(ctx, in, file)
	}
	if file != "" && nvErr == nil && !marks.Empty() {
		if data, err := json.Marshal(marks); err == nil {
			if err := h.Nvim.Diff(ctx, file, string(data)); err != nil {
				nvErr = err
			} else {
				drawn = marks.summary()
			}
		}
	}
	added, removed := 0, 0
	if file != "" {
		h.state(tmux.StateWorking, "edited "+h.rel(file))
		added, removed = marks.counts()
		_ = h.Log.Session(fmt.Sprintf("✎ %s (+%d −%d)", h.rel(file), added, removed))
	}
	if file != "" {
		// Remember the file as Claude left it, so the next prompt can report
		// only what the engineer changed on top.
		st := loadState(h.Cfg.StateDir)
		Snapshot(h.Cfg.StateDir, &st, file)
		_ = saveState(h.Cfg.StateDir, st)
	}
	payload := map[string]any{"tool": in.ToolName, "file": file, "added": added, "removed": removed}
	if drawn != "" {
		payload["drawn"] = drawn
	}
	if nvErr != nil {
		payload["nvim_error"] = nvErr.Error()
	}
	_ = h.Log.Append("post_edit", payload)
	return nvErr
}

// Stop logs the end of a turn, hands the turn to the engineer in the status
// bar, and plays a chime without blocking.
func (h *Handler) Stop(in *Input) error {
	_ = h.Log.Append("stop", map[string]any{"session_id": in.SessionID})
	_ = h.Log.Session("■ turn ended, your turn")
	h.state(tmux.StateIdle, "")
	chime("Glass")
	return nil
}

// Notify handles Claude Code's notifications. A permission prompt is easy
// to miss when nobody watches Claude's pane, so it raises the status bar
// alert and a sound; an idle reminder just marks the turn as the
// engineer's.
func (h *Handler) Notify(in *Input) error {
	msg := strings.TrimSpace(in.Message)
	idle := in.Notification == "idle_prompt" || strings.Contains(strings.ToLower(msg), "waiting for your input")
	_ = h.Log.Append("notify", map[string]any{"message": msg, "kind": in.Notification})
	if idle {
		h.state(tmux.StateIdle, "")
		return nil
	}
	_ = h.Log.Session("⚠ Claude needs you: " + msg)
	status.Set(h.Cfg, tmux.StateAttention, msg)
	// Claude's pane is hidden: bring it in so the prompt can be read and
	// answered. It goes away again on the next sign of activity, unless the
	// engineer had opened it themselves.
	if h.Cfg.Session != "" && !tmux.ClaudeVisible(h.Cfg.Session) {
		if err := tmux.ShowClaude(h.Cfg.Session, h.Cfg.ClaudeHeight); err == nil {
			_ = os.WriteFile(h.autoShown(), nil, 0o644)
		}
	}
	chime("Funk")
	return nil
}

// Activity runs before every tool call and keeps the status bar saying
// what Claude is doing. It writes nothing to the log.
func (h *Handler) Activity(in *Input) error {
	detail := ""
	ti := in.ToolInput
	switch {
	case strings.HasPrefix(in.ToolName, "mcp__pair__"):
		return nil // narrate, show and ask report their own state
	case in.ToolName == "Read":
		detail = "reading " + h.rel(h.absPath(ti.FilePath))
	case in.ToolName == "Edit" || in.ToolName == "Write":
		detail = "editing " + h.rel(h.absPath(ti.FilePath))
	case in.ToolName == "Bash":
		detail = "running " + firstNonEmpty(ti.Description, ti.Command)
	case in.ToolName == "Grep" || in.ToolName == "Glob":
		detail = "searching " + ti.Pattern
	case in.ToolName == "WebFetch" || in.ToolName == "WebSearch":
		detail = "looking up " + firstNonEmpty(ti.URL, ti.Query)
	case in.ToolName == "Agent" || in.ToolName == "Task":
		detail = "delegating " + ti.Description
	case strings.HasPrefix(in.ToolName, "mcp__"):
		detail = "using " + strings.TrimPrefix(in.ToolName, "mcp__")
	default:
		detail = strings.ToLower(in.ToolName)
	}
	h.state(tmux.StateWorking, detail)
	return nil
}

func firstNonEmpty(a, b string) string {
	if strings.TrimSpace(a) != "" {
		return a
	}
	return b
}

// state records what is happening and gives the screen back to the editor
// once Claude no longer waits on the engineer.
func (h *Handler) state(kind, detail string) {
	status.Set(h.Cfg, kind, detail)
	h.hideIfAutoShown()
}

// startTurn is state(working) at the beginning of a turn of Claude's.
func (h *Handler) startTurn() {
	status.StartTurn(h.Cfg)
	h.hideIfAutoShown()
}

// hideIfAutoShown hides Claude's pane when pair itself had opened it for a
// permission prompt.
func (h *Handler) hideIfAutoShown() {
	if _, err := os.Stat(h.autoShown()); err == nil {
		_ = os.Remove(h.autoShown())
		_ = tmux.HideClaude(h.Cfg.Session)
	}
}

// autoShown marks that Claude's pane was opened by pair, not by the
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
