package agent

import (
	"encoding/json"
	"strings"
	"testing"
)

func decode(t *testing.T, b []byte) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, b)
	}
	return m
}

func TestMergeSettingsFromScratchIsIdempotent(t *testing.T) {
	first, err := MergeClaudeSettings(nil, "/opt/homebrew/bin/pair")
	if err != nil {
		t.Fatal(err)
	}
	second, err := MergeClaudeSettings(first, "/opt/homebrew/bin/pair")
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != string(second) {
		t.Fatalf("not idempotent:\n%s\n---\n%s", first, second)
	}
	m := decode(t, first)
	hooks := m["hooks"].(map[string]any)
	pre := hooks["PreToolUse"].([]any)
	if len(pre) != 2 {
		t.Fatalf("PreToolUse groups = %d, want activity + pre_edit", len(pre))
	}
	if _, has := pre[0].(map[string]any)["matcher"]; has {
		t.Fatal("the activity hook covers every tool: no matcher")
	}
	g := pre[1].(map[string]any)
	if g["matcher"] != "Edit|Write" {
		t.Fatalf("matcher = %v", g["matcher"])
	}
	cmd := g["hooks"].([]any)[0].(map[string]any)["command"]
	if cmd != "/opt/homebrew/bin/pair hook pre_edit --agent claude" {
		t.Fatalf("command = %v", cmd)
	}
	if _, has := hooks["Stop"].([]any)[0].(map[string]any)["matcher"]; has {
		t.Fatal("Stop hook must not carry a matcher")
	}
	allow := m["permissions"].(map[string]any)["allow"].([]any)
	if len(allow) != 3 {
		t.Fatalf("allow = %v", allow)
	}
}

func TestMergeSettingsPreservesOtherHooksAndReplacesOurs(t *testing.T) {
	existing := `{
  "permissions": {"allow": ["Bash(npm test)", "mcp__pair__ask"], "deny": ["Bash(rm -rf:*)"]},
  "hooks": {
    "PreToolUse": [
      {"matcher": "Bash", "hooks": [{"type": "command", "command": "echo audit"}]},
      {"matcher": "Edit|MultiEdit|Write", "hooks": [{"type": "command", "command": "/old/path/pair hook pre_edit"}]},
      {"matcher": "Write", "hooks": [{"type": "command", "command": "'/Applications/My Tools/pair' hook pre_edit --agent claude"}, {"type": "command", "command": "lint"}]}
    ]
  },
  "model": "opus"
}`
	out, err := MergeClaudeSettings([]byte(existing), "/new/pair")
	if err != nil {
		t.Fatal(err)
	}
	m := decode(t, out)
	if m["model"] != "opus" {
		t.Fatal("unrelated keys must survive")
	}
	perms := m["permissions"].(map[string]any)
	if len(perms["deny"].([]any)) != 1 {
		t.Fatal("deny list must survive")
	}
	allow := perms["allow"].([]any)
	if len(allow) != 4 { // npm test + ask (kept once) + narrate + show
		t.Fatalf("allow = %v", allow)
	}
	pre := m["hooks"].(map[string]any)["PreToolUse"].([]any)
	if len(pre) != 4 {
		t.Fatalf("PreToolUse groups = %d, want audit + lint + our two: %s", len(pre), out)
	}
	if pre[0].(map[string]any)["matcher"] != "Bash" {
		t.Fatal("audit group must be first and untouched")
	}
	lint := pre[1].(map[string]any)["hooks"].([]any)
	if len(lint) != 1 || lint[0].(map[string]any)["command"] != "lint" {
		t.Fatalf("old pair entry must be removed from the shared group, lint kept: %v", lint)
	}
	if strings.Count(string(out), "hook pre_edit") != 1 {
		t.Fatalf("exactly one pre_edit hook expected:\n%s", out)
	}
	if _, err := MergeClaudeSettings([]byte("{oops"), "/new/pair"); err == nil {
		t.Fatal("invalid existing JSON must be refused, not overwritten")
	}
}
