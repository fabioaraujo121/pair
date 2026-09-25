package events

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAppendWritesOneJSONLinePerEvent(t *testing.T) {
	dir := filepath.Join(t.TempDir(), ".pair")
	l := Open(dir)
	if err := l.Append("pre_edit", map[string]any{"file": "/x.go", "start": 3}); err != nil {
		t.Fatal(err)
	}
	if err := l.Append("stop", nil); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "events.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 2 {
		t.Fatalf("want 2 lines, got %d: %q", len(lines), data)
	}
	var first map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &first); err != nil {
		t.Fatal(err)
	}
	if first["type"] != "pre_edit" || first["ts"] == "" {
		t.Fatalf("unexpected record: %v", first)
	}
	if strings.Contains(lines[1], "payload") {
		t.Fatalf("nil payload must be omitted: %s", lines[1])
	}
}
