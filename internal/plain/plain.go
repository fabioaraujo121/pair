// Package plain makes text that pair did not write safe to print on a
// terminal: a transcript, a narration, a command, a file name, the name of
// a session. Control characters, the escape character among them, are
// removed, so what someone said or wrote cannot clear the screen, move the
// cursor or rename the terminal.
package plain

import (
	"strings"
	"unicode"
)

// Text removes control characters and keeps line breaks.
func Text(s string) string { return clean(s, true) }

// Line removes control characters and puts everything on one line.
func Line(s string) string { return clean(s, false) }

func clean(s string, keepLines bool) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r == '\n' && keepLines:
			return r
		case r == '\n', r == '\t', r == '\r':
			return ' '
		case unicode.IsControl(r):
			return -1
		}
		return r
	}, s)
}
