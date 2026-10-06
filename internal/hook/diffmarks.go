package hook

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"

	"github.com/fabioaraujo121/pair/internal/agent"
)

// Drawing the change in place.
//
// After an edit, Neovim shows the change inside the real file: added lines
// are highlighted, removed lines appear where they used to be. The data
// comes from the agent itself when it reports the change it made (Claude
// Code's tool_response.structuredPatch), so it is exactly the edit that was
// made, whatever git thinks of the file; otherwise from git.

// Hunk is one block of a patch, as the agent reports it or as git prints it.
type Hunk = agent.Hunk

// Removed is a block of deleted lines, drawn above line At of the new file.
type Removed struct {
	At    int      `json:"at"`
	Lines []string `json:"lines"`
}

// Marks is what pair.nvim needs to draw a change: 1-based line numbers of
// the added lines in the new file, and the removed blocks.
type Marks struct {
	Added   []int     `json:"added"`
	Removed []Removed `json:"removed"`
}

// Empty reports whether there is nothing to draw.
func (m Marks) Empty() bool { return len(m.Added) == 0 && len(m.Removed) == 0 }

// maxMarks bounds what is sent to the editor for very large changes.
const maxMarks = 800

// MarksFromHunks converts hunks into editor marks.
func MarksFromHunks(hunks []Hunk) Marks {
	m := Marks{Added: []int{}, Removed: []Removed{}}
	for _, h := range hunks {
		line := h.NewStart
		if line < 1 {
			line = 1
		}
		var gone []string
		flush := func() {
			if len(gone) > 0 {
				m.Removed = append(m.Removed, Removed{At: line, Lines: gone})
				gone = nil
			}
		}
		for _, l := range h.Lines {
			if l == "" {
				flush()
				line++
				continue
			}
			switch l[0] {
			case '+':
				flush()
				if len(m.Added) < maxMarks {
					m.Added = append(m.Added, line)
				}
				line++
			case '-':
				if len(gone) < maxMarks {
					gone = append(gone, l[1:])
				}
			case '\\': // "\ No newline at end of file"
			default:
				flush()
				line++
			}
		}
		flush()
	}
	return m
}

var hunkHeader = regexp.MustCompile(`^@@ -(\d+)(?:,(\d+))? \+(\d+)(?:,(\d+))? @@`)

// ParseUnified reads the hunks of a unified diff (git diff output).
func ParseUnified(diff string) []Hunk {
	var hunks []Hunk
	var cur *Hunk
	sc := bufio.NewScanner(strings.NewReader(diff))
	sc.Buffer(make([]byte, 1<<20), 1<<24)
	for sc.Scan() {
		line := sc.Text()
		if m := hunkHeader.FindStringSubmatch(line); m != nil {
			hunks = append(hunks, Hunk{OldStart: atoi(m[1], 1), OldLines: atoi(m[2], 1), NewStart: atoi(m[3], 1), NewLines: atoi(m[4], 1)})
			cur = &hunks[len(hunks)-1]
			continue
		}
		if cur == nil {
			continue // file headers
		}
		if line == "" || strings.ContainsRune(" +-\\", rune(line[0])) {
			cur.Lines = append(cur.Lines, line)
		}
	}
	return hunks
}

func atoi(s string, def int) int {
	if n, err := strconv.Atoi(s); err == nil {
		return n
	}
	return def
}

// marks works out what to draw for an edit: the agent's own patch first,
// then "everything is new" for a created file, then git.
func (h *Handler) marks(ctx context.Context, f agent.File, file string) Marks {
	if len(f.Hunks) > 0 {
		return MarksFromHunks(f.Hunks)
	}
	if f.Create || (f.Content != "" && !h.gitTracked(ctx, file)) {
		return allAdded(file)
	}
	if out := h.git(ctx, "diff", "--no-color", "-U0", "--", file); len(out) > 0 {
		return MarksFromHunks(ParseUnified(string(out)))
	}
	return Marks{}
}

// allAdded marks every line of a new file.
func allAdded(file string) Marks {
	m := Marks{Added: []int{}, Removed: []Removed{}}
	data, err := os.ReadFile(file)
	if err != nil {
		return m
	}
	n := strings.Count(string(data), "\n")
	if len(data) > 0 && data[len(data)-1] != '\n' {
		n++
	}
	if n > maxMarks {
		n = maxMarks
	}
	for i := 1; i <= n; i++ {
		m.Added = append(m.Added, i)
	}
	return m
}

// summary is the one-line description of what was drawn.
func (m Marks) summary() string {
	removed := 0
	for _, r := range m.Removed {
		removed += len(r.Lines)
	}
	return fmt.Sprintf("+%d −%d", len(m.Added), removed)
}

// counts returns the number of added and removed lines.
func (m Marks) counts() (added, removed int) {
	for _, r := range m.Removed {
		removed += len(r.Lines)
	}
	return len(m.Added), removed
}
