// Package shell quotes words for the interactive shell that pair types into.
package shell

import (
	"regexp"
	"strings"
)

// Quote wraps s in single quotes for a POSIX shell, unless it is a plain
// word that needs none.
func Quote(s string) string {
	if plainWord.MatchString(s) {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'"'"'`) + "'"
}

// plainWord is a word no shell gives a meaning to. Anything else is
// quoted: the command lines pair types are read by an interactive shell,
// where even "!" and "{" mean something.
var plainWord = regexp.MustCompile(`^[A-Za-z0-9_./:@%+,=-]+$`)

// Join quotes every word and joins them with spaces.
func Join(words []string) string {
	quoted := make([]string, len(words))
	for i, w := range words {
		quoted[i] = Quote(w)
	}
	return strings.Join(quoted, " ")
}
