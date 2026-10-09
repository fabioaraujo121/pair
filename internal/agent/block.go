package agent

import (
	"os"
	"strings"

	pair "github.com/fabioaraujo121/pair"
)

// The pairing instructions reach the agent at the start of every session,
// through its SessionStart hook, and only while a pair session runs. The
// first versions wrote them into CLAUDE.md or AGENTS.md between markers,
// where every session in the repository read them; Uninstall takes those
// blocks out.

const (
	blockStart = "<!-- pair:start -->"
	blockEnd   = "<!-- pair:end -->"
)

var languageNames = map[string]string{
	"pt": "Portuguese", "en": "English", "es": "Spanish", "fr": "French",
	"de": "German", "it": "Italian", "ja": "Japanese", "nl": "Dutch",
}

// Instructions renders the pairing instructions for a language code.
func Instructions(lang string) string {
	name := languageNames[strings.ToLower(lang)]
	if name == "" {
		name = lang
	}
	return strings.TrimSpace(strings.ReplaceAll(pair.InstructionsTemplate, "{{LANGUAGE}}", name)) + "\n"
}

// RemoveBlock returns content without the marked block, and whether there
// was one.
func RemoveBlock(content string) (string, bool) {
	start := strings.Index(content, blockStart)
	end := strings.Index(content, blockEnd)
	if start < 0 || end < start {
		return content, false
	}
	end += len(blockEnd)
	if end < len(content) && content[end] == '\n' {
		end++
	}
	out := content[:start] + content[end:]
	if strings.TrimSpace(out) == "" {
		return "", true
	}
	return strings.TrimRight(out, "\n") + "\n", true
}

// hasBlock reports whether the file holds the marked block.
func hasBlock(path string) bool {
	data, err := os.ReadFile(path)
	return err == nil && strings.Contains(string(data), blockStart)
}

// removeBlockFile takes the block out of the file; an emptied file is
// removed. It reports whether anything changed.
func removeBlockFile(path string) (bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return false, nil
	}
	out, had := RemoveBlock(string(data))
	if !had {
		return false, nil
	}
	if out == "" {
		return true, os.Remove(path)
	}
	return true, os.WriteFile(path, []byte(out), 0o644)
}
