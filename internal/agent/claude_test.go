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

func TestClaudeSettingsHoldOnlyPairs(t *testing.T) {
	first, err := ClaudeSettings("/opt/homebrew/bin/pair")
	if err != nil {
		t.Fatal(err)
	}
	second, _ := ClaudeSettings("/opt/homebrew/bin/pair")
	if string(first) != string(second) {
		t.Fatal("not deterministic")
	}
	m := decode(t, first)
	hooks := m["hooks"].(map[string]any)
	pre := hooks["PreToolUse"].([]any)
	if len(pre) != 2 {
		t.Fatalf("PreToolUse groups = %d, want 2 (activity, pre_edit)", len(pre))
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
	if len(m) != 2 {
		t.Fatalf("only hooks and permissions belong in a session's settings: %v", m)
	}
}

func TestWithoutClaudeSettingsKeepsTheRest(t *testing.T) {
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
	out, changed, err := withoutClaudeSettings([]byte(existing))
	if err != nil || !changed {
		t.Fatal(err, changed)
	}
	m := decode(t, out)
	if m["model"] != "opus" {
		t.Fatal("unrelated keys must survive")
	}
	perms := m["permissions"].(map[string]any)
	if len(perms["deny"].([]any)) != 1 || len(perms["allow"].([]any)) != 1 {
		t.Fatalf("permissions: %v", perms)
	}
	pre := m["hooks"].(map[string]any)["PreToolUse"].([]any)
	if len(pre) != 2 {
		t.Fatalf("PreToolUse groups = %d, want audit + lint: %s", len(pre), out)
	}
	lint := pre[1].(map[string]any)["hooks"].([]any)
	if len(lint) != 1 || lint[0].(map[string]any)["command"] != "lint" {
		t.Fatalf("old pair entry must be removed from the shared group, lint kept: %v", lint)
	}
	if strings.Contains(string(out), "hook pre_edit") {
		t.Fatalf("a pair hook survived:\n%s", out)
	}
	same, changed, err := withoutClaudeSettings([]byte(`{"model":"opus"}`))
	if err != nil || changed || string(same) != `{"model":"opus"}` {
		t.Fatalf("nothing of pair's: must come back untouched: %s %v %v", same, changed, err)
	}
	gone, changed, _ := withoutClaudeSettings([]byte(`{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"/p/pair hook stop"}]}]},"permissions":{"allow":["mcp__pair__narrate"]}}`))
	if !changed || gone != nil {
		t.Fatalf("only pair's content: the file must go: %s", gone)
	}
	if _, _, err := withoutClaudeSettings([]byte("{oops")); err == nil {
		t.Fatal("invalid existing JSON must be refused, not overwritten")
	}
}
