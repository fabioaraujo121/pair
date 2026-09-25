package replay

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fabioaraujo121/pair/internal/nvim"
)

const sample = `{"ts":"2026-09-25T10:00:00.000-03:00","type":"session_start","payload":{"root":"/repo"}}
{"ts":"2026-09-25T10:00:05.000-03:00","type":"narrate","payload":{"text":"Vou mexer no PR.","spoken":"Vou mexer no pull request.","chars":16}}
not json at all
{"ts":"2026-09-25T10:00:06.000-03:00","type":"show","payload":{"file":"/repo/app.rb","start":3,"end":5,"note":"aqui"}}
{"ts":"2026-09-25T10:03:00.000-03:00","type":"ask","payload":{"text":"Sidekiq?","answer":{"text":"Solid Queue."}}}
{"ts":"2026-09-25T10:03:01.000-03:00","type":"pre_edit","payload":{"file":"/repo/app.rb","start":3,"end":3}}
{"ts":"2026-09-25T10:03:02.000-03:00","type":"post_edit","payload":{"file":"/repo/app.rb"}}
{"ts":"2026-09-25T10:03:03.000-03:00","type":"prompt","payload":{"changes":["lib.rb"]}}
{"ts":"2026-09-25T10:03:04.000-03:00","type":"stop"}
`

type recSpeaker struct{ spoken []string }

func (r *recSpeaker) Speak(_ context.Context, t string) error {
	r.spoken = append(r.spoken, t)
	return nil
}

type recPointer struct{ shows []nvim.Shown }

func (r *recPointer) Show(_ context.Context, f string, s, e int) (nvim.Shown, error) {
	sh := nvim.Shown{File: f, Start: s, End: e}
	r.shows = append(r.shows, sh)
	return sh, nil
}

func TestLoadSkipsBadLines(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.jsonl")
	_ = os.WriteFile(path, []byte(sample), 0o644)
	events, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 8 || events[1].Type != "narrate" {
		t.Fatalf("events = %d %+v", len(events), events)
	}
}

func TestRunReplaysInOrderWithScaledGaps(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.jsonl")
	_ = os.WriteFile(path, []byte(sample), 0o644)
	events, _ := Load(path)
	var out bytes.Buffer
	sp := &recSpeaker{}
	np := &recPointer{}
	start := time.Now()
	err := Run(context.Background(), events, Options{Speed: 100, MaxGap: 50 * time.Millisecond, Speaker: sp, Nvim: np, Out: &out})
	if err != nil {
		t.Fatal(err)
	}
	if time.Since(start) > 2*time.Second {
		t.Fatalf("replay too slow: %s (the 3-minute gap must be capped)", time.Since(start))
	}
	text := out.String()
	for _, want := range []string{"● session in /repo", "🗣 Vou mexer no PR.", "→ /repo/app.rb:3-5 aqui", "? Sidekiq?", "👤 Solid Queue.", "(about to edit)", "✎ edited /repo/app.rb", "✎ engineer changed: lib.rb", "■ turn ended"} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q in:\n%s", want, text)
		}
	}
	if len(sp.spoken) != 3 || sp.spoken[0] != "Vou mexer no pull request." || sp.spoken[1] != "aqui" || sp.spoken[2] != "Sidekiq?" {
		t.Fatalf("spoken = %q", sp.spoken)
	}
	if len(np.shows) != 2 || np.shows[0].Start != 3 || np.shows[0].End != 5 || np.shows[1].End != 3 {
		t.Fatalf("shows = %+v", np.shows)
	}

	// Mute and print-only: nothing spoken, nothing jumped, no waiting.
	out.Reset()
	if err := Run(context.Background(), events, Options{Speed: 0, Out: &out}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "🗣 Vou mexer no PR.") {
		t.Fatal("print-only replay lost the narration")
	}
}
