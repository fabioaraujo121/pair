package hook

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/fabioaraujo121/pair/internal/events"
	"github.com/fabioaraujo121/pair/internal/tmux"
)

// Hand-edit awareness.
//
// post_edit snapshots every file as Claude leaves it. At the next prompt,
// every file that differs from its snapshot (or, for files Claude never
// touched, from git) changed outside Claude's Edit/Write tools: the
// engineer edited it in Neovim, or Claude went through Bash. Its diff is
// injected as additionalContext, bounded by PAIR_DIFF_LIMIT lines, and the
// snapshot is refreshed so the same change is reported once.

const (
	stateFile = "state.json"
	snapDir   = "snap"
	maxSnap   = 1 << 20 // files above 1 MiB are tracked by hash only
)

// State remembers which content Claude has seen for each path.
type State struct {
	Baselined bool              `json:"baselined"`
	Files     map[string]string `json:"files"` // absolute path -> sha256 of content
}

func loadState(dir string) State {
	st := State{Files: map[string]string{}}
	if data, err := os.ReadFile(filepath.Join(dir, stateFile)); err == nil {
		_ = json.Unmarshal(data, &st)
	}
	if st.Files == nil {
		st.Files = map[string]string{}
	}
	return st
}

func saveState(dir string, st State) error {
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, stateFile), data, 0o644)
}

// ResetState forgets all snapshots; `pair start` calls it so a session
// starts with a clean baseline.
func ResetState(dir string) {
	_ = os.Remove(filepath.Join(dir, stateFile))
	_ = os.RemoveAll(filepath.Join(dir, snapDir))
}

func snapPath(dir, file string) string {
	sum := sha256.Sum256([]byte(file))
	return filepath.Join(dir, snapDir, hex.EncodeToString(sum[:])[:20])
}

func hashBytes(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// Snapshot records file's current content as "seen by Claude".
func Snapshot(dir string, st *State, file string) {
	data, err := os.ReadFile(file)
	if err != nil {
		delete(st.Files, file)
		_ = os.Remove(snapPath(dir, file))
		return
	}
	st.Files[file] = hashBytes(data)
	if len(data) > maxSnap {
		_ = os.Remove(snapPath(dir, file))
		return
	}
	_ = os.MkdirAll(filepath.Join(dir, snapDir), 0o755)
	_ = os.WriteFile(snapPath(dir, file), data, 0o644)
}

// Change is one file that differs from what Claude last saw.
type Change struct {
	Path  string // relative to the repo root
	Diff  string // unified diff, may be empty for deletions or huge files
	Lines int
	Note  string // "deleted", "new file", "too large"
}

// Prompt is the UserPromptSubmit hook: it prints hookSpecificOutput with
// the engineer's changes, or nothing.
func (h *Handler) Prompt(in *Input) error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	st := loadState(h.Cfg.StateDir)
	candidates := h.candidates(ctx, st)

	if !st.Baselined {
		for _, f := range candidates {
			Snapshot(h.Cfg.StateDir, &st, f)
		}
		st.Baselined = true
		_ = saveState(h.Cfg.StateDir, st)
		_ = h.Log.Append("prompt", map[string]any{"baselined": len(candidates), "chars": len(in.Prompt)})
		return nil
	}

	var changes []Change
	for _, f := range candidates {
		if c, ok := h.detectChange(ctx, &st, f); ok {
			changes = append(changes, c)
		}
	}
	_ = saveState(h.Cfg.StateDir, st)
	if len(changes) == 0 {
		_ = h.Log.Append("prompt", map[string]any{"changes": 0, "chars": len(in.Prompt)})
		return nil
	}

	text, truncated := RenderChanges(changes, h.Cfg.DiffLimit)
	names := make([]string, len(changes))
	for i, c := range changes {
		names[i] = c.Path
	}
	_ = h.Log.Append("prompt", map[string]any{"changes": names, "truncated": truncated, "chars": len(in.Prompt)})
	_ = h.Log.Session("✎ engineer changed: " + strings.Join(names, ", "))

	out := map[string]any{
		"hookSpecificOutput": map[string]any{
			"hookEventName":     "UserPromptSubmit",
			"additionalContext": text,
		},
	}
	enc := json.NewEncoder(h.Stdout)
	enc.SetEscapeHTML(false)
	return enc.Encode(out)
}

// candidates lists files worth checking: everything git reports as
// modified or untracked, every file Claude touched, and PAIRING.md.
func (h *Handler) candidates(ctx context.Context, st State) []string {
	seen := map[string]bool{}
	var files []string
	add := func(p string) {
		p = filepath.Clean(p)
		if !seen[p] {
			seen[p] = true
			files = append(files, p)
		}
	}
	out, err := exec.CommandContext(ctx, "git", "-C", h.Cfg.Root, "status", "--porcelain", "--untracked-files=all", "--no-renames").Output()
	if err == nil {
		for _, line := range strings.Split(string(out), "\n") {
			if len(line) < 4 {
				continue
			}
			rel := strings.TrimSpace(line[3:])
			if strings.HasPrefix(rel, "\"") { // quoted path with special chars
				rel = strings.Trim(rel, "\"")
			}
			if rel == "" || strings.HasPrefix(rel, ".pair/") {
				continue
			}
			add(filepath.Join(h.Cfg.Root, rel))
		}
	}
	for f := range st.Files {
		add(f)
	}
	add(filepath.Join(h.Cfg.Root, "PAIRING.md"))
	sort.Strings(files)
	return files
}

// detectChange compares file with what Claude saw and refreshes the
// snapshot when it differs.
func (h *Handler) detectChange(ctx context.Context, st *State, file string) (Change, bool) {
	rel := h.rel(file)
	known, wasKnown := st.Files[file]
	data, readErr := os.ReadFile(file)

	if readErr != nil {
		if !wasKnown {
			return Change{}, false // never existed as far as Claude knows
		}
		Snapshot(h.Cfg.StateDir, st, file)
		return Change{Path: rel, Note: "deleted"}, true
	}
	cur := hashBytes(data)
	if wasKnown && cur == known {
		return Change{}, false
	}
	if !wasKnown && !h.gitDirty(ctx, file) {
		// Clean in git and never touched: Claude can read it itself.
		Snapshot(h.Cfg.StateDir, st, file)
		return Change{}, false
	}

	var diff []byte
	switch {
	case len(data) > maxSnap:
		Snapshot(h.Cfg.StateDir, st, file)
		return Change{Path: rel, Note: "too large to inline"}, true
	case wasKnown && fileExists(snapPath(h.Cfg.StateDir, file)):
		diff = h.git(ctx, "diff", "--no-color", "--no-index", "--", snapPath(h.Cfg.StateDir, file), file)
	case h.gitTracked(ctx, file):
		diff = h.git(ctx, "diff", "--no-color", "--", file)
	default:
		diff = h.git(ctx, "diff", "--no-color", "--no-index", "--", "/dev/null", file)
	}
	Snapshot(h.Cfg.StateDir, st, file)
	body := trimDiffHeader(string(diff), rel)
	c := Change{Path: rel, Diff: body, Lines: strings.Count(body, "\n")}
	if body == "" {
		c.Note = "changed"
	}
	return c, true
}

func (h *Handler) git(ctx context.Context, args ...string) []byte {
	out, _ := exec.CommandContext(ctx, "git", append([]string{"-C", h.Cfg.Root}, args...)...).Output()
	return out
}

func (h *Handler) gitTracked(ctx context.Context, file string) bool {
	return len(bytes.TrimSpace(h.git(ctx, "ls-files", "--", file))) > 0
}

func (h *Handler) gitDirty(ctx context.Context, file string) bool {
	return len(bytes.TrimSpace(h.git(ctx, "status", "--porcelain", "--untracked-files=all", "--", file))) > 0
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// trimDiffHeader drops the "diff --git/index/---/+++" preamble and keeps
// hunks, so snapshot paths never leak into the context.
func trimDiffHeader(diff, rel string) string {
	var hunks []string
	for _, line := range strings.Split(diff, "\n") {
		switch {
		case strings.HasPrefix(line, "diff --git"), strings.HasPrefix(line, "index "),
			strings.HasPrefix(line, "--- "), strings.HasPrefix(line, "+++ "),
			strings.HasPrefix(line, "new file mode"), strings.HasPrefix(line, "deleted file mode"):
			continue
		}
		hunks = append(hunks, line)
	}
	body := strings.TrimSpace(strings.Join(hunks, "\n"))
	if body == "" {
		return ""
	}
	return "--- a/" + rel + "\n+++ b/" + rel + "\n" + body + "\n"
}

// RenderChanges formats the context block, keeping at most limit diff
// lines in total.
func RenderChanges(changes []Change, limit int) (string, bool) {
	if limit <= 0 {
		limit = 200
	}
	var b strings.Builder
	b.WriteString("Engineer changed since last turn (edits made outside your Edit/Write tools; read the files before changing them):\n")
	budget := limit
	truncated := false
	for _, c := range changes {
		fmt.Fprintf(&b, "\n### %s", c.Path)
		if c.Note != "" {
			fmt.Fprintf(&b, " (%s)", c.Note)
		}
		b.WriteString("\n")
		if c.Diff == "" {
			continue
		}
		lines := strings.Split(strings.TrimRight(c.Diff, "\n"), "\n")
		if len(lines) > budget {
			if budget <= 0 {
				fmt.Fprintf(&b, "(%d diff lines omitted, limit reached)\n", len(lines))
				truncated = true
				continue
			}
			lines = lines[:budget]
			truncated = true
		}
		b.WriteString("```diff\n")
		b.WriteString(strings.Join(lines, "\n"))
		b.WriteString("\n```\n")
		budget -= len(lines)
		if truncated {
			fmt.Fprintf(&b, "(diff truncated at %d lines; read the file for the rest)\n", limit)
		}
	}
	return b.String(), truncated
}

// Point is `pair hook point`: the engineer selected a range in Neovim and
// wants Claude to look at it. The reference (and note, if any) is typed
// into the Claude pane; with a note it is submitted, without one it is
// left in the input for the engineer to complete.
func Point(args []string, out io.Writer) error {
	var file, note string
	start, end := 0, 0
	for i := 0; i < len(args); i++ {
		next := func() string {
			if i+1 < len(args) {
				i++
				return args[i]
			}
			return ""
		}
		switch args[i] {
		case "--file":
			file = next()
		case "--start":
			fmt.Sscan(next(), &start)
		case "--end":
			fmt.Sscan(next(), &end)
		case "--note":
			note = next()
		}
	}
	if file == "" || start < 1 {
		return fmt.Errorf("usage: pair hook point --file <path> --start <line> [--end <line>] [--note <text>]")
	}
	if end < start {
		end = start
	}
	cfg := voiceConfig(args)
	if abs, err := filepath.Abs(file); err == nil {
		if r, err := filepath.Rel(cfg.Root, abs); err == nil && !strings.HasPrefix(r, "..") {
			file = r
		}
	}
	ref := fmt.Sprintf("%s:%d-%d", file, start, end)
	if start == end {
		ref = fmt.Sprintf("%s:%d", file, start)
	}
	text := "Look at " + ref
	if note != "" {
		text += ": " + strings.TrimSpace(note)
	}
	log := events.Open(cfg.StateDir)
	_ = log.Append("point", map[string]any{"file": file, "start": start, "end": end, "note": note})
	_ = log.Session("👉 " + ref + " " + note)
	if tmux.HasSession(cfg.Session) {
		if err := tmux.SendText(cfg.Session, text, note != ""); err != nil {
			return err
		}
	}
	fmt.Fprintln(out, text)
	return nil
}
