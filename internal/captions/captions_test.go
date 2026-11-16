package captions

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fabioaraujo121/pair/internal/replay"
	"github.com/fabioaraujo121/pair/internal/session"
)

// A session of one turn: the task spoken, a narration, a pointer with a
// note, a question answered by push-to-talk, a hand edit pointed at, and a
// second push-to-talk whose recording length was not kept.
const sample = `{"ts":"2026-11-16T10:00:00.000-03:00","type":"session_start","payload":{"root":"/repo","agent":"claude"}}
{"ts":"2026-11-16T10:00:04.400-03:00","type":"listen","payload":{"heard":{"text":"Hide scheduled posts from the feed until published_at passes.","seconds":3.2}}}
{"ts":"2026-11-16T10:00:05.000-03:00","type":"prompt","payload":{"text":"Hide scheduled posts from the feed until published_at passes."}}
{"ts":"2026-11-16T10:00:12.000-03:00","type":"narrate","payload":{"text":"I'll add a published scope to Post and use it in the feed.","spoken":"I'll add a published scope to Post and use it in the feed.","ms":4000}}
{"ts":"2026-11-16T10:00:13.000-03:00","type":"show","payload":{"file":"/repo/app/models/post.rb","start":1,"end":1,"note":"The scope goes here."}}
{"ts":"2026-11-16T10:00:14.000-03:00","type":"question","payload":{"text":"Should authors still see their scheduled posts? I recommend yes."}}
{"ts":"2026-11-16T10:00:24.000-03:00","type":"listen","payload":{"answers":true,"heard":{"text":"Yes, authors only.","seconds":1.5}}}
{"ts":"2026-11-16T10:00:24.200-03:00","type":"ask","payload":{"text":"Should authors still see their scheduled posts? I recommend yes.","ms":3500,"listen_ms":6000,"answer":{"text":"Yes, authors only.","seconds":1.5}}}
{"ts":"2026-11-16T10:00:30.000-03:00","type":"point","payload":{"file":"/repo/app/models/post.rb","start":2,"end":2,"note":"call it live"}}
{"ts":"2026-11-16T10:00:40.000-03:00","type":"listen","payload":{"heard":{"text":"I renamed it to live, carry on."}}}
{"ts":"2026-11-16T10:00:50.000-03:00","type":"stop","payload":{}}
`

func load(t *testing.T) []replay.Event {
	t.Helper()
	path := filepath.Join(t.TempDir(), "events.jsonl")
	_ = os.WriteFile(path, []byte(sample), 0o644)
	events, err := replay.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	return events
}

func TestCuesAreTimedFromTheEvents(t *testing.T) {
	cues := Build(load(t), "Claude Code")
	want := []Cue{
		// The recording was 3.2 s long and its event is stamped at its end.
		{Start: 1200 * time.Millisecond, End: 4400 * time.Millisecond, Speaker: You, Text: "Hide scheduled posts from the feed until published_at passes."},
		// A narration is stamped when the speech ends and says how long it took.
		{Start: 8 * time.Second, End: 12 * time.Second, Speaker: "Claude Code", Text: "I'll add a published scope to Post and use it in the feed."},
		// A note is stamped before it is spoken: four words at 2.5 a second.
		{Start: 13 * time.Second, End: 14600 * time.Millisecond, Speaker: "Claude Code", Text: "The scope goes here."},
		// The question's event comes 6 s of waiting after 3.5 s of speech.
		{Start: 14700 * time.Millisecond, End: 18200 * time.Millisecond, Speaker: "Claude Code", Text: "Should authors still see their scheduled posts? I recommend yes."},
		// The answer, once: from the push-to-talk, not again from the question.
		{Start: 22500 * time.Millisecond, End: 24 * time.Second, Speaker: You, Text: "Yes, authors only."},
		// No recording length: seven words at 2.5 a second.
		{Start: 37200 * time.Millisecond, End: 40 * time.Second, Speaker: You, Text: "I renamed it to live, carry on."},
	}
	if len(cues) != len(want) {
		t.Fatalf("got %d cues, want %d:\n%+v", len(cues), len(want), cues)
	}
	for i := range want {
		if cues[i] != want[i] {
			t.Errorf("cue %d\n got %+v\nwant %+v", i+1, cues[i], want[i])
		}
	}
	for _, c := range cues {
		if strings.Contains(c.Text, "call it live") {
			t.Fatal("a point's note is typed, not spoken: no cue")
		}
	}
}

func TestSRTAndVTTFormats(t *testing.T) {
	cues := Build(load(t), "Claude Code")
	var srt bytes.Buffer
	if err := WriteSRT(&srt, cues); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"1\n00:00:01,200 --> 00:00:04,400\nyou: Hide scheduled posts from the feed until published_at passes.\n\n",
		"2\n00:00:08,000 --> 00:00:12,000\nClaude Code: I'll add a published scope to Post and use it in the feed.\n\n",
		"4\n00:00:14,700 --> 00:00:18,200\nClaude Code: Should authors still see their scheduled posts? I recommend yes.\n\n",
		"6\n00:00:37,200 --> 00:00:40,000\nyou: I renamed it to live, carry on.\n\n",
	} {
		if !strings.Contains(srt.String(), want) {
			t.Errorf("SRT lacks %q:\n%s", want, srt.String())
		}
	}
	if n := strings.Count(srt.String(), "Yes, authors only."); n != 1 {
		t.Fatalf("the answer appears %d times", n)
	}

	var vtt bytes.Buffer
	if err := WriteVTT(&vtt, cues); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(vtt.String(), "WEBVTT\n\n") {
		t.Fatalf("VTT header:\n%s", vtt.String())
	}
	if !strings.Contains(vtt.String(), "00:00:08.000 --> 00:00:12.000\n<v Claude Code>I'll add a published scope to Post and use it in the feed.</v>\n\n") {
		t.Fatalf("VTT cue:\n%s", vtt.String())
	}
	if strings.Contains(vtt.String(), ",") && strings.Contains(vtt.String(), "-->") && strings.Contains(vtt.String(), "0,0") {
		t.Fatal("VTT times use a point, not a comma")
	}
}

func TestAnswerRecordedByTheServerAlone(t *testing.T) {
	// PAIR_ASK_MIC=auto: the server listens itself and writes no listen
	// event, so the answer is captioned from the ask.
	path := filepath.Join(t.TempDir(), "events.jsonl")
	_ = os.WriteFile(path, []byte(`{"ts":"2026-11-16T10:00:00.000-03:00","type":"question","payload":{"text":"Sidekiq or Solid Queue? I recommend Solid Queue."}}
{"ts":"2026-11-16T10:00:10.000-03:00","type":"ask","payload":{"text":"Sidekiq or Solid Queue? I recommend Solid Queue.","ms":3000,"listen_ms":5000,"answer":{"text":"Solid Queue.","seconds":1.0}}}
`), 0o644)
	events, _ := replay.Load(path)
	cues := Build(events, "agent")
	if len(cues) != 2 || cues[1].Speaker != You || cues[1].Text != "Solid Queue." || cues[1].Start != 9*time.Second || cues[1].End != 10*time.Second {
		t.Fatalf("cues = %+v", cues)
	}
	if cues[0].Start != 2*time.Second || cues[0].End != 5*time.Second {
		t.Fatalf("question cue = %+v", cues[0])
	}
}

func TestAgentInReadsTheSessionMeta(t *testing.T) {
	dir := t.TempDir()
	if got := AgentIn(dir); got != "agent" {
		t.Fatalf("no meta: %q", got)
	}
	_ = session.Save(dir, session.Meta{ID: "20261116-100000", Started: time.Now()})
	if got := AgentIn(dir); got != "agent" {
		t.Fatalf("meta without an agent: %q", got)
	}
	_ = session.Save(dir, session.Meta{ID: "20261116-100000", Started: time.Now(), Agent: "claude"})
	if got := AgentIn(dir); got != "Claude Code" {
		t.Fatalf("claude: %q", got)
	}
	_ = session.Save(dir, session.Meta{ID: "20261116-100000", Started: time.Now(), Agent: "codex"})
	if got := AgentIn(dir); got != "Codex" {
		t.Fatalf("codex: %q", got)
	}
}

func TestStamp(t *testing.T) {
	if got := stamp(3661*time.Second+7*time.Millisecond, ','); got != "01:01:01,007" {
		t.Fatalf("stamp = %q", got)
	}
	if got := stamp(-time.Second, '.'); got != "00:00:00.000" {
		t.Fatalf("negative stamp = %q", got)
	}
}
