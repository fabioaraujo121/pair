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
	default:
		return fmt.Errorf("unknown hook %q (want pre_edit, post_edit, stop, prompt)", name)
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

// PostEdit reloads Neovim and renders the file's diff into the session log.
func (h *Handler) PostEdit(in *Input) error {
	file := h.absPath(in.ToolInput.FilePath)
	ctx := context.Background()
	nvErr := h.Nvim.Reload(ctx)
	if file != "" {
		if diff := h.renderDiff(ctx, file); diff != "" {
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
	if nvErr != nil {
		payload["nvim_error"] = nvErr.Error()
	}
	_ = h.Log.Append("post_edit", payload)
	return nvErr
}

// Stop logs the end of a turn and plays a chime without blocking.
func (h *Handler) Stop(in *Input) error {
	_ = h.Log.Append("stop", map[string]any{"session_id": in.SessionID})
	_ = h.Log.Session("■ turn ended")
	chime()
	return nil
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

// renderDiff returns the working-tree diff of file, through delta when it
// is installed. Untracked files are diffed against /dev/null.
func (h *Handler) renderDiff(ctx context.Context, file string) string {
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
		return ""
	}
	if delta, err := exec.LookPath("delta"); err == nil {
		args := []string{"--paging=never"}
		if w := h.paneWidth(ctx); w != "" {
			args = append(args, "--width", w)
		}
		cmd := exec.CommandContext(ctx, delta, args...)
		cmd.Stdin = bytes.NewReader(out)
		if rendered, err := cmd.Output(); err == nil && len(rendered) > 0 {
			out = rendered
		}
	}
	return string(out)
}

// paneWidth asks tmux how wide the log pane is so delta wraps correctly.
func (h *Handler) paneWidth(ctx context.Context) string {
	out, err := exec.CommandContext(ctx, "tmux", "display", "-p", "-t", h.Cfg.Session+":0.2", "#{pane_width}").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// chime plays a short system sound in its own session so the hook can exit
// immediately.
func chime() {
	cmd := exec.Command("afplay", "/System/Library/Sounds/Glass.aiff")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	_ = cmd.Start()
}
