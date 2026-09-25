package cli

import (
	"strings"

	pair "github.com/fabioaraujo121/pair"
)

const (
	blockStart = "<!-- pair:start -->"
	blockEnd   = "<!-- pair:end -->"
)

var languageNames = map[string]string{
	"pt": "Portuguese", "en": "English", "es": "Spanish", "fr": "French",
	"de": "German", "it": "Italian", "ja": "Japanese", "nl": "Dutch",
}

// PairingBlock renders the CLAUDE.md pairing-mode block for a whisper
// language code.
func PairingBlock(lang string) string {
	name := languageNames[strings.ToLower(lang)]
	if name == "" {
		name = lang
	}
	body := strings.TrimSpace(strings.ReplaceAll(pair.ClaudeBlockTemplate, "{{LANGUAGE}}", name))
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
