package logview

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWrapBreaksAtWords(t *testing.T) {
	lines := Wrap("ideas on how can we improve these secrets and where can we store them.", 24)
	for _, l := range lines {
		if len(l) > 24 {
			t.Fatalf("line too long: %q", l)
		}
		if strings.HasPrefix(l, " ") || strings.HasSuffix(l, " ") {
			t.Fatalf("stray space: %q", l)
		}
	}
	if strings.Join(lines, " ") != "ideas on how can we improve these secrets and where can we store them." {
		t.Fatalf("words lost: %q", lines)
	}
	// A path longer than the line is split, not dropped.
	long := Wrap("see app/views/layouts/_analytics.html.erb now", 16)
	if strings.ReplaceAll(strings.Join(long, ""), " ", "") != "seeapp/views/layouts/_analytics.html.erbnow" {
		t.Fatalf("long word: %q", long)
	}
	for _, l := range long {
		if len(l) > 16 {
			t.Fatalf("line too long: %q", l)
		}
	}
}

const session = `{"ts":"2026-09-28T11:46:00-03:00","type":"session_start","payload":{"root":"/repo","lang":"en","voice":"Samantha"}}
{"ts":"2026-09-28T11:46:06-03:00","type":"listen","payload":{"heard":{"text":"ideas on how can we improve these secrets and where can we store them."}}}
{"ts":"2026-09-28T11:46:06.5-03:00","type":"prompt","payload":{"text":"ideas on how can we improve these secrets and where can we store them.","chars":70}}
{"ts":"2026-09-28T11:47:23-03:00","type":"narrate","payload":{"text":"I'm adding the ideas to PAIRING.md. The Google IDs are public, so they are config, not secrets."}}
{"ts":"2026-09-28T11:47:38-03:00","type":"pre_edit","payload":{"file":"/repo/PAIRING.md","start":89,"end":91}}
{"ts":"2026-09-28T11:47:43-03:00","type":"post_edit","payload":{"file":"/repo/PAIRING.md","added":159,"removed":0}}
{"ts":"2026-09-28T11:47:54-03:00","type":"show","payload":{"file":"/repo/app/controllers/admin/base_controller.rb","start":22,"end":29,"note":"This is the real secret."}}
{"ts":"2026-09-28T11:48:02-03:00","type":"question","payload":{"text":"Should I move the public Google IDs into a versioned settings file?"}}
{"ts":"2026-09-28T11:48:32-03:00","type":"listen","payload":{"answers":true,"heard":{"text":"Don't worry about it."}}}
{"ts":"2026-09-28T11:48:33-03:00","type":"ask","payload":{"text":"Should I move the public Google IDs into a versioned settings file?","answer":{"text":"Don't worry about it."}}}
{"ts":"2026-09-28T11:49:04-03:00","type":"stop","payload":{}}
{"ts":"2026-09-28T11:50:00-03:00","type":"prompt","payload":{"text":"typed by hand","changes":["lib/x.rb"]}}
`

func TestConversationIsGroupedWrappedAndNotRepeated(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.jsonl")
	_ = os.WriteFile(path, []byte(session), 0o644)
	var out bytes.Buffer
	if err := Render(path, "/repo", &out, 44, false); err != nil {
		t.Fatal(err)
	}
	text := out.String()
	t.Log("\n" + text)

	for _, l := range strings.Split(text, "\n") {
		if len([]rune(l)) > 44 {
			t.Fatalf("line wider than the pane: %q", l)
		}
	}
	// What you said is shown once, although both the microphone and
	// Claude Code logged it; the answer is shown once too.
	if n := strings.Count(text, "improve these"); n != 1 {
		t.Fatalf("spoken prompt shown %d times", n)
	}
	if n := strings.Count(text, "worry about it"); n != 1 {
		t.Fatalf("answer shown %d times", n)
	}
	if n := strings.Count(text, "Should I move"); n != 1 {
		t.Fatalf("question shown %d times", n)
	}
	for _, want := range []string{"you  11:46", "claude  11:47", "claude asks  11:48", "you answer  11:48",
		"✎ PAIRING.md  +159 −0", "base_controller.rb:22-29", "This is the real secret.", "· your turn", "typed by hand", "✎ you edited lib/x.rb"} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q", want)
		}
	}
	if strings.Contains(text, "89-91") {
		t.Fatal("the pointer before an edit is noise next to the edit card")
	}
	// The question comes before its answer.
	if strings.Index(text, "Should I move") > strings.Index(text, "worry about it") {
		t.Fatal("answer printed before the question")
	}
	// One header for consecutive entries of the same speaker.
	if n := strings.Count(text, "claude  11:4"); n != 1 {
		t.Fatalf("claude header repeated %d times", n)
	}
}

func TestOldLogsWithoutQuestionEvents(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.jsonl")
	_ = os.WriteFile(path, []byte(`{"ts":"2026-09-28T10:40:01-03:00","type":"ask","payload":{"text":"Where do we start?","answer":{"text":"Visual changes."}}}
{"ts":"2026-09-28T10:41:01-03:00","type":"ask","payload":{"text":"Continue?","answer":{"text":"","dropped":"nobody answered within 3m0s"}}}
`), 0o644)
	var out bytes.Buffer
	_ = Render(path, "", &out, 60, false)
	text := out.String()
	for _, want := range []string{"Where do we start?", "Visual changes.", "Continue?", "no answer: nobody answered"} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q in:\n%s", want, text)
		}
	}
}
