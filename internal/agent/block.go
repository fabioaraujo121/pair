package agent

import (
	"os"
	"strings"

	pair "github.com/fabioaraujo121/pair"
)

// The pairing-mode instructions go into the file the agent reads at the
// start of every conversation (CLAUDE.md, AGENTS.md), between markers, so
// the rest of the file stays the engineer's.

const (
	blockStart = "<!-- pair:start -->"
	blockEnd   = "<!-- pair:end -->"
)

var languageNames = map[string]string{
	"pt": "Portuguese", "en": "English", "es": "Spanish", "fr": "French",
	"de": "German", "it": "Italian", "ja": "Japanese", "nl": "Dutch",
}

// PairingBlock renders the pairing-mode block for a language code.
func PairingBlock(lang string) string {
	name := languageNames[strings.ToLower(lang)]
	if name == "" {
		name = lang
	}
	body := strings.TrimSpace(strings.ReplaceAll(pair.InstructionsTemplate, "{{LANGUAGE}}", name))
	return blockStart + "\n" + body + "\n" + blockEnd + "\n"
}

// UpsertBlock returns content with the marked block replaced, or appended
// after a blank line when there is none. Calling it twice yields the same
// result.
func UpsertBlock(content, block string) string {
	start := strings.Index(content, blockStart)
	end := strings.Index(content, blockEnd)
	if start >= 0 && end > start {
		end += len(blockEnd)
		if end < len(content) && content[end] == '\n' {
			end++
		}
		return content[:start] + block + content[end:]
	}
	if content == "" {
		return block
	}
	if !strings.HasSuffix(content, "\n") {
		content += "\n"
	}
	return content + "\n" + block
}

// writeBlock puts the block into path. It reports whether the file changed.
func writeBlock(path, lang string) (bool, error) {
	current, _ := os.ReadFile(path)
	updated := UpsertBlock(string(current), PairingBlock(lang))
	if updated == string(current) {
		return false, nil
	}
	return true, os.WriteFile(path, []byte(updated), 0o644)
}
