// Package speech is the voice layer: a Speaker interface with the macOS
// `say` implementation, and the text normalizer that turns Claude's prose
// into something a TTS engine pronounces well.
//
// Cancelling the context passed to Speak interrupts the speech: `say` gets
// SIGTERM. This is how an Escape in Claude Code (which cancels the MCP
// handler's context) silences the narration; Claude Code itself never kills
// the server's children.
package speech

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// Speaker speaks text and blocks until it has been spoken or ctx is done.
type Speaker interface {
	Speak(ctx context.Context, text string) error
}

// Say speaks through macOS `say`.
type Say struct {
	Voice string
	Rate  int
	Bin   string // defaults to "say" on PATH
}

// NewSay returns a Say speaker for the given voice and words-per-minute rate.
func NewSay(voice string, rate int) *Say {
	return &Say{Voice: voice, Rate: rate}
}

// Speak runs `say` with the text on stdin and waits for it to finish. When
// ctx is cancelled the process receives SIGTERM and Speak returns ctx.Err().
func (s *Say) Speak(ctx context.Context, text string) error {
	if strings.TrimSpace(text) == "" {
		return nil
	}
	bin := s.Bin
	if bin == "" {
		bin = "say"
	}
	args := []string{}
	if s.Voice != "" {
		args = append(args, "-v", s.Voice)
	}
	if s.Rate > 0 {
		args = append(args, "-r", strconv.Itoa(s.Rate))
	}
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Stdin = strings.NewReader(text)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	cmd.Cancel = func() error { return cmd.Process.Signal(syscall.SIGTERM) }
	cmd.WaitDelay = time.Second
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("%s: %v: %s", bin, err, strings.TrimSpace(stderr.String()))
	}
	return nil
}

// Silent is a Speaker that speaks nothing; used when PAIR_MUTE is set and
// in tests.
type Silent struct{}

// Speak returns immediately.
func (Silent) Speak(context.Context, string) error { return nil }

// DefaultVocabulary expands abbreviations that TTS engines otherwise spell
// letter by letter or mangle. Keys match whole words, case-sensitively.
var DefaultVocabulary = map[string]string{
	"PR":   "pull request",
	"PRs":  "pull requests",
	"DB":   "database",
	"DBs":  "databases",
	"env":  "environment",
	"repo": "repositório",
	"i.e.": "ou seja",
	"e.g.": "por exemplo",
	"vs":   "versus",
	"vs.":  "versus",
}

var (
	fencedCode  = regexp.MustCompile("(?s)```.*?```")
	inlineCode  = regexp.MustCompile("`([^`]*)`")
	mdLink      = regexp.MustCompile(`\[([^\]]*)\]\([^)]*\)`)
	mdHeader    = regexp.MustCompile(`(?m)^\s{0,3}#{1,6}\s+`)
	mdBullet    = regexp.MustCompile(`(?m)^\s*(?:[-*+]|\d+[.)])\s+`)
	mdQuote     = regexp.MustCompile(`(?m)^\s*>\s?`)
	mdEmphasis  = regexp.MustCompile(`(\*\*|__|\*|_)(\S[^*_]*?)(\*\*|__|\*|_)`)
	camelCase   = regexp.MustCompile(`([a-z0-9])([A-Z])`)
	snakeCase   = regexp.MustCompile(`([A-Za-z])_([A-Za-z])`)
	whitespace  = regexp.MustCompile(`\s+`)
	wordPattern = regexp.MustCompile(`[A-Za-z][A-Za-z0-9.]*`)
)

// Normalize prepares text for speech: code blocks are dropped, markdown
// markers removed, identifiers split into words, and vocabulary expanded.
// extra vocabulary entries override the defaults.
func Normalize(text string, extra map[string]string) string {
	t := fencedCode.ReplaceAllString(text, " ")
	t = inlineCode.ReplaceAllString(t, "$1")
	t = mdLink.ReplaceAllString(t, "$1")
	t = mdHeader.ReplaceAllString(t, "")
	t = mdBullet.ReplaceAllString(t, "")
	t = mdQuote.ReplaceAllString(t, "")
	for i := 0; i < 3; i++ { // nested emphasis
		t = mdEmphasis.ReplaceAllString(t, "$2")
	}
	t = snakeCase.ReplaceAllString(t, "$1 $2")
	t = camelCase.ReplaceAllString(t, "$1 $2")

	vocab := DefaultVocabulary
	if len(extra) > 0 {
		vocab = make(map[string]string, len(DefaultVocabulary)+len(extra))
		for k, v := range DefaultVocabulary {
			vocab[k] = v
		}
		for k, v := range extra {
			vocab[k] = v
		}
	}
	t = wordPattern.ReplaceAllStringFunc(t, func(w string) string {
		if r, ok := vocab[w]; ok { // dotted entries such as "e.g."
			return r
		}
		bare := strings.TrimRight(w, ".")
		if r, ok := vocab[bare]; ok { // "PR." at the end of a sentence
			return r + w[len(bare):]
		}
		return w
	})
	return strings.TrimSpace(whitespace.ReplaceAllString(t, " "))
}
