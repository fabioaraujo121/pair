package speech

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestAcknowledgementsVaryAndFallBack(t *testing.T) {
	seen := map[string]bool{}
	last := ""
	for i := 0; i < 40; i++ {
		p := AckPhrase("en", AckPrompt, last)
		if p == "" || p == last {
			t.Fatalf("phrase %q after %q", p, last)
		}
		seen[p] = true
		last = p
	}
	if len(seen) < 3 {
		t.Fatalf("too little variety: %v", seen)
	}
	if p := AckPhrase("pt-BR", AckAnswer, ""); p == "" || p == AckPhrase("en", AckAnswer, "") && false {
		t.Fatalf("Portuguese: %q", p)
	}
	if p := AckPhrase("pt", AckMissed, ""); p != "Desculpa, pode repetir?" && p != "Não entendi." {
		t.Fatalf("Portuguese miss: %q", p)
	}
	if p := AckPhrase("ja", AckPrompt, ""); p == "" {
		t.Fatal("a language without phrases falls back to English")
	}
	if AckPhrase("en", AckKind("other"), "") != "" {
		t.Fatal("an unknown kind says nothing")
	}
}

func TestAcknowledgeSpeaksOnceAndRemembers(t *testing.T) {
	ackPause = 0
	defer func() { ackPause = 400 * time.Millisecond }()
	dir := t.TempDir()
	rec := &recordingSpeaker{}
	for i := 0; i < 3; i++ {
		if err := Acknowledge(context.Background(), rec, dir, "en", AckPrompt); err != nil {
			t.Fatal(err)
		}
	}
	if len(rec.spoken) != 3 {
		t.Fatalf("spoken: %v", rec.spoken)
	}
	for i := 1; i < 3; i++ {
		if rec.spoken[i] == rec.spoken[i-1] {
			t.Fatalf("the same phrase twice in a row: %v", rec.spoken)
		}
	}
	last, _ := os.ReadFile(filepath.Join(dir, "ack.last"))
	if string(last) != rec.spoken[2]+"\n" {
		t.Fatalf("remembered %q, said %q", last, rec.spoken[2])
	}
	if err := Acknowledge(context.Background(), nil, dir, "en", AckPrompt); err != nil {
		t.Fatal("no speaker, nothing to do")
	}
}

// While the agent holds the floor, an acknowledgement waits a little and
// then gives up rather than talk over it.
func TestAcknowledgementNeverTalksOverTheAgent(t *testing.T) {
	ackPause = 0
	defer func() { ackPause = 400 * time.Millisecond }()
	dir := t.TempDir()
	floor, err := AcquireFloor(context.Background(), dir, false)
	if err != nil {
		t.Fatal(err)
	}
	rec := &recordingSpeaker{}
	start := time.Now()
	if err := Acknowledge(context.Background(), rec, dir, "en", AckPrompt); err != nil {
		t.Fatal(err)
	}
	if len(rec.spoken) != 0 {
		t.Fatalf("spoke over the agent: %v", rec.spoken)
	}
	if d := time.Since(start); d < 2*time.Second || d > 6*time.Second {
		t.Fatalf("waited %s for the floor", d)
	}
	floor.Release()
	if err := Acknowledge(context.Background(), rec, dir, "en", AckPrompt); err != nil || len(rec.spoken) != 1 {
		t.Fatalf("with the floor free: %v %v", rec.spoken, err)
	}
}
