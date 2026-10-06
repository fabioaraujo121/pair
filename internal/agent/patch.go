package agent

import "strings"

// Codex edits files with one tool, apply_patch, whose argument is a patch
// in its own grammar:
//
//	*** Begin Patch
//	*** Add File: path
//	+line
//	*** Delete File: path
//	*** Update File: path
//	*** Move to: new path
//	@@ context
//	 kept line
//	-old line
//	+new line
//	*** End of File
//	*** End Patch
//
// There are no line numbers: a hunk is found by its text. ParseApplyPatch
// reads the files a patch touches and, for an update, the text of the
// first hunk as it stands before the change, so the editor can point at
// it.

// ParseApplyPatch reads the files an apply_patch command touches.
func ParseApplyPatch(patch string) []File {
	var files []File
	var cur *File
	var before []string // the lines of the first hunk that exist before the change
	var after []string  // its lines after the change
	inFirst := false    // reading the first hunk of the current file
	seenHunk := false

	flush := func() {
		if cur == nil {
			return
		}
		if cur.Create {
			cur.Content = strings.Join(after, "\n")
			if len(after) > 0 {
				cur.Content += "\n"
			}
		} else if !cur.Delete {
			cur.Old = strings.Join(before, "\n")
			cur.New = strings.Join(after, "\n")
		}
		files = append(files, *cur)
		cur, before, after, inFirst, seenHunk = nil, nil, nil, false, false
	}
	start := func(f File) {
		flush()
		cur = &f
		inFirst = f.Create // an added file has no hunk header: its lines follow at once
	}

	for _, line := range strings.Split(patch, "\n") {
		switch {
		case strings.HasPrefix(line, "*** Begin Patch"):
		case strings.HasPrefix(line, "*** End Patch"):
			flush()
		case strings.HasPrefix(line, "*** Add File: "):
			start(File{Path: strings.TrimSpace(strings.TrimPrefix(line, "*** Add File: ")), Create: true})
		case strings.HasPrefix(line, "*** Delete File: "):
			start(File{Path: strings.TrimSpace(strings.TrimPrefix(line, "*** Delete File: ")), Delete: true})
		case strings.HasPrefix(line, "*** Update File: "):
			start(File{Path: strings.TrimSpace(strings.TrimPrefix(line, "*** Update File: "))})
		case strings.HasPrefix(line, "*** Move to: "):
			if cur != nil {
				cur.NewPath = strings.TrimSpace(strings.TrimPrefix(line, "*** Move to: "))
			}
		case strings.HasPrefix(line, "*** End of File"):
		case strings.HasPrefix(line, "@@"):
			if cur != nil && !cur.Create {
				inFirst, seenHunk = !seenHunk, true // only the first hunk is kept
			}
		case cur == nil || !inFirst:
		case strings.HasPrefix(line, "+"):
			after = append(after, line[1:])
		case strings.HasPrefix(line, "-"):
			before = append(before, line[1:])
		default: // context: " line", or a line whose leading space was dropped
			text := strings.TrimPrefix(line, " ")
			before = append(before, text)
			after = append(after, text)
		}
	}
	flush()
	return files
}
