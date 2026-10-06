package session

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The events in these tests are stamped -03:00; the sessions are started
// in the same zone, so their order does not depend on where the tests run.
var brt = time.FixedZone("BRT", -3*60*60)

func write(t *testing.T, path, content string) {
	t.Helper()
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestNewSessionArchivesThePreviousOne(t *testing.T) {
	dir := filepath.Join(t.TempDir(), ".pair")
	t0 := time.Date(2026, 9, 28, 10, 0, 0, 0, brt)

	first, err := New(dir, "landing page", t0)
	if err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(dir, "events.jsonl"), `{"ts":"2026-09-28T10:00:05-03:00","type":"prompt","payload":{"text":"improve the hero section of the landing page please"}}
{"ts":"2026-09-28T10:03:00-03:00","type":"post_edit","payload":{"file":"/r/a.erb","added":3,"removed":1}}
{"ts":"2026-09-28T10:20:00-03:00","type":"stop","payload":{"session_id":"claude-1"}}
`)
	write(t, filepath.Join(dir, "session.log"), "text log\n")
	NoteConversation(dir, "claude-1")
	NoteTitle(dir, "improve the hero section of the landing page please")

	second, err := New(dir, "", t0.Add(2*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if second.ID == first.ID {
		t.Fatal("ids must differ")
	}
	// The new session starts with nothing on screen.
	if _, err := os.Stat(filepath.Join(dir, "events.jsonl")); err == nil {
		t.Fatal("the event log of the previous session is still current")
	}
	archived := filepath.Join(dir, "sessions", first.ID)
	for _, f := range []string{"events.jsonl", "session.log", "session.json"} {
		if _, err := os.Stat(filepath.Join(archived, f)); err != nil {
			t.Fatalf("%s not archived: %v", f, err)
		}
	}

	all := List(dir)
	if len(all) != 2 || !all[0].Current || all[0].ID != second.ID || all[1].ID != first.ID {
		t.Fatalf("list: %+v", all)
	}
	old := all[1]
	if old.Name != "landing page" || old.Conversation != "claude-1" || old.Prompts != 1 || old.Edits != 1 {
		t.Fatalf("archived summary: %+v", old)
	}
	if old.Title != "improve the hero section of the landing page please" {
		t.Fatalf("title: %q", old.Title)
	}
	if got := old.Length().Round(time.Minute); got != 20*time.Minute {
		t.Fatalf("length: %s", got)
	}
}

func TestFindRenameRestore(t *testing.T) {
	dir := filepath.Join(t.TempDir(), ".pair")
	t0 := time.Date(2026, 9, 28, 10, 0, 0, 0, brt)
	a, _ := New(dir, "landing page", t0)
	write(t, filepath.Join(dir, "events.jsonl"), `{"ts":"2026-09-28T10:00:05-03:00","type":"narrate","payload":{"text":"hello"}}`+"\n")
	NoteConversation(dir, "claude-a")
	b, _ := New(dir, "checkout flow", t0.Add(time.Hour))
	write(t, filepath.Join(dir, "events.jsonl"), `{"ts":"2026-09-28T11:00:05-03:00","type":"narrate","payload":{"text":"second"}}`+"\n")
	c, _ := New(dir, "", t0.Add(2*time.Hour))

	for key, want := range map[string]string{a.ID: a.ID, "landing": a.ID, "CHECKOUT FLOW": b.ID, "last": c.ID, "": c.ID, a.ID[:13]: a.ID} {
		s, err := Find(dir, key)
		if err != nil || s.ID != want {
			t.Fatalf("Find(%q) = %s, %v; want %s", key, s.ID, err, want)
		}
	}
	if _, err := Find(dir, "nothing like this"); err == nil {
		t.Fatal("unknown key must fail")
	}
	if _, err := Find(dir, "2026"); err == nil || !strings.Contains(err.Error(), "matches 3") {
		t.Fatalf("ambiguous key: %v", err)
	}

	if _, err := Rename(dir, "landing", "landing page v2"); err != nil {
		t.Fatal(err)
	}
	if s, _ := Find(dir, a.ID); s.Name != "landing page v2" {
		t.Fatalf("rename of an archived session: %+v", s.Meta)
	}
	if _, err := Rename(dir, "last", "spike"); err != nil {
		t.Fatal(err)
	}
	if m, _ := Current(dir); m.Name != "spike" {
		t.Fatalf("rename of the current session: %+v", m)
	}

	// Open the first session again: it becomes current with its log and
	// its Claude conversation, and the one that was current is archived.
	m, err := Restore(dir, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if m.ID != a.ID || m.Conversation != "claude-a" {
		t.Fatalf("restored: %+v", m)
	}
	data, _ := os.ReadFile(filepath.Join(dir, "events.jsonl"))
	if !strings.Contains(string(data), "hello") {
		t.Fatalf("the restored session's log is not current:\n%s", data)
	}
	// "last" is the session in use, although two others started later.
	if last, err := Find(dir, "last"); err != nil || last.ID != a.ID {
		t.Fatalf("last after reopening an old session = %s, %v; want %s", last.ID, err, a.ID)
	}
	all := List(dir)
	if len(all) != 3 {
		t.Fatalf("sessions after restore: %d", len(all))
	}
	for _, s := range all {
		if s.Current != (s.ID == a.ID) {
			t.Fatalf("current flag: %+v", s)
		}
	}
}

func TestALogHoldingSeveralSessionsIsSplit(t *testing.T) {
	dir := filepath.Join(t.TempDir(), ".pair")
	write(t, filepath.Join(dir, "events.jsonl"), `{"ts":"2026-09-28T10:34:24-03:00","type":"session_start","payload":{"root":"/r"}}
{"ts":"2026-09-28T10:35:00-03:00","type":"prompt","payload":{"chars":40}}
{"ts":"2026-09-28T10:35:51-03:00","type":"narrate","payload":{"text":"Vou começar mapeando o projeto."}}
{"ts":"2026-09-28T10:40:00-03:00","type":"stop","payload":{"session_id":"claude-old"}}
{"ts":"2026-09-28T11:21:00-03:00","type":"session_start","payload":{"root":"/r"}}
{"ts":"2026-09-28T11:22:06-03:00","type":"session_start","payload":{"root":"/r"}}
{"ts":"2026-09-28T11:22:48-03:00","type":"listen","payload":{"heard":{"text":"So we were investigating the landing page. Where did we stop?"}}}
{"ts":"2026-09-28T11:23:43-03:00","type":"stop","payload":{"session_id":"claude-new"}}
`)
	write(t, filepath.Join(dir, "session.log"), "old text\n")

	m, err := New(dir, "", time.Date(2026, 9, 28, 14, 0, 0, 0, brt))
	if err != nil {
		t.Fatal(err)
	}
	all := List(dir)
	if len(all) != 3 { // the new one and two with content; the empty start is dropped
		t.Fatalf("sessions: %d %+v", len(all), all)
	}
	if !all[0].Current || all[0].ID != m.ID {
		t.Fatalf("current: %+v", all[0])
	}
	if all[1].Conversation != "claude-new" || !strings.HasPrefix(all[1].Title, "So we were investigating") {
		t.Fatalf("newest past session: %+v", all[1].Meta)
	}
	if all[2].Conversation != "claude-old" || all[2].Narrations != 1 {
		t.Fatalf("oldest past session: %+v", all[2])
	}
	if _, err := os.Stat(filepath.Join(dir, "events.jsonl")); err == nil {
		t.Fatal("the mixed log must be gone")
	}
}

func TestTitleFrom(t *testing.T) {
	if got := TitleFrom("  fix   the login bug  "); got != "fix the login bug" {
		t.Fatalf("%q", got)
	}
	long := TitleFrom("one two three four five six seven eight nine ten eleven")
	if !strings.HasSuffix(long, "…") || strings.Contains(long, "ten") {
		t.Fatalf("%q", long)
	}
	if TitleFrom("") != "" {
		t.Fatal("empty prompt")
	}
}
