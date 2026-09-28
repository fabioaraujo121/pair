// Package hook implements the `pair hook <name>` subcommands that Claude Code
// runs through .claude/settings.json. Each handler reads the hook's JSON on
// stdin, does its work, logs an event, and never makes the tool call fail:
// main exits 0 for hooks whatever happens here.
package hook

import (
	"bytes"
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
	"time"

	"github.com/fabioaraujo121/pair/internal/config"
	"github.com/fabioaraujo121/pair/internal/events"
	"github.com/fabioaraujo121/pair/internal/nvim"
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
	FilePath   string `json:"file_path"`
	OldString  string `json:"old_string"`
	NewString  string `json:"new_string"`
	ReplaceAll bool   `json:"replace_all"`
	Content    string `json:"content"`
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
	default:
		return fmt.Errorf("unknown hook %q (want pre_edit, post_edit, stop, prompt, notify)", name)
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
	if file != "" && nvErr == nil {
		if m := h.marks(ctx, in, file); !m.Empty() {
			if data, err := json.Marshal(m); err == nil {
				if err := h.Nvim.Diff(ctx, file, string(data)); err != nil {
					nvErr = err
				} else {
					drawn = m.summary()
				}
			}
		}
	}
	if file != "" {
		h.state(tmux.StateWorking, "edited "+h.rel(file))
		summary, diff := h.renderDiff(ctx, file)
		if summary != "" {
			_ = h.Log.Session(summary)
		}
		if diff != "" {
			_ = h.Log.SessionRaw([]byte(diff))
		}
	}
	if file != "" {
		// Remember the file as Claude left it, so the next prompt can report
		// only what the engineer changed on top.
		st := loadState(h.Cfg.StateDir)
		Snapshot(h.Cfg.StateDir, &st, file)
		_ = saveState(h.Cfg.StateDir, st)
	}
	payload := map[string]any{"tool": in.ToolName, "file": file}
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
	_ = h.Log.Session("⚠ Claude needs you: " + msg + " (F7 shows Claude's pane)")
	h.state(tmux.StateAttention, msg)
	chime("Funk")
	return nil
}

// state updates the status bar indicator of the running session.
func (h *Handler) state(kind, detail string) {
	tmux.SetState(h.Cfg.Session, kind, detail)
}

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

// renderDiff returns a one-line summary ("✎ path (+3 −1)") and the
// working-tree diff of file, through delta when it is installed and capped
// at LogDiffLines lines so narrations stay visible in the activity log.
// Untracked files are diffed against /dev/null.
func (h *Handler) renderDiff(ctx context.Context, file string) (summary, diff string) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	git := func(args ...string) []byte {
		out, _ := exec.CommandContext(ctx, "git", append([]string{"-C", h.Cfg.Root}, args...)...).Output()
		return out
	}
	out := git("diff", "--no-color", "--", file)
	if len(bytes.TrimSpace(out)) == 0 {
		if len(bytes.TrimSpace(git("ls-files", "--", file))) == 0 {
			// untracked: git exits 1 when there are differences, Output still has them
			out = git("diff", "--no-color", "--no-index", "--", "/dev/null", file)
		}
	}
	if len(bytes.TrimSpace(out)) == 0 {
		return "✎ " + h.rel(file) + " (no change against git)", ""
	}
	added, removed := 0, 0
	for _, line := range strings.Split(string(out), "\n") {
		switch {
		case strings.HasPrefix(line, "+++"), strings.HasPrefix(line, "---"):
		case strings.HasPrefix(line, "+"):
			added++
		case strings.HasPrefix(line, "-"):
			removed++
		}
	}
	summary = fmt.Sprintf("✎ %s (+%d −%d)", h.rel(file), added, removed)
	limit := h.Cfg.LogDiffLines
	if limit <= 0 || filepath.Base(file) == "PAIRING.md" {
		return summary, "" // the scratchpad is already on screen
	}
	if delta, err := exec.LookPath("delta"); err == nil {
		args := []string{"--paging=never", "--file-style=omit", "--hunk-header-style=omit", "--keep-plus-minus-markers"}
		if w := h.paneWidth(ctx); w != "" {
			args = append(args, "--width", w)
		}
		cmd := exec.CommandContext(ctx, delta, args...)
		cmd.Stdin = bytes.NewReader(out)
		if rendered, err := cmd.Output(); err == nil && len(rendered) > 0 {
			out = rendered
		}
	}
	lines := strings.Split(strings.Trim(string(out), "\n"), "\n")
	if len(lines) > limit {
		more := len(lines) - limit
		lines = append(lines[:limit], fmt.Sprintf("   … %d more lines, see the file in the editor", more))
	}
	return summary, strings.Join(lines, "\n") + "\n"
}

// paneWidth asks tmux how wide the log pane is so delta wraps correctly.
func (h *Handler) paneWidth(ctx context.Context) string {
	out, err := exec.CommandContext(ctx, "tmux", tmux.Args("display", "-p", "-t", tmux.Pane(h.Cfg.Session, tmux.RoleLog), "#{pane_width}")...).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// chime plays a short system sound in its own session so the hook can exit
// immediately.
func chime(name string) {
	cmd := exec.Command("afplay", "/System/Library/Sounds/"+name+".aiff")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	_ = cmd.Start()
}
