package agent

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestEveryAgentIsComplete(t *testing.T) {
	if !Known("claude") || !Known("Codex") || Known("cursor") {
		t.Fatalf("known agents: %v", Names())
	}
	if _, err := For("nope"); err == nil || !strings.Contains(err.Error(), "claude, codex") {
		t.Fatalf("an unknown agent must name the known ones: %v", err)
	}
	def, _ := For("")
	if def.Name() != Default {
		t.Fatalf("the default is %s, got %s", Default, def.Name())
	}
	for _, name := range Names() {
		a, err := For(name)
		if err != nil {
			t.Fatal(err)
		}
		if a.Name() != name || a.Title() == "" || a.Short() == "" || a.Binary() == "" || a.InstructionsFile() == "" || len(a.InterruptKeys()) == 0 {
			t.Errorf("%s: incomplete adapter", name)
		}
		if a.WaitingOnDialog("") || a.WaitingOnDialog("❯ Add email validation\n✻ Thinking… (12s)\n") {
			t.Errorf("%s: a working screen is not a dialog", name)
		}
		if !a.WaitingOnDialog(" ❯ No, exit\n   Yes, I trust this folder\n Enter to confirm · Esc to cancel\n") || !a.WaitingOnDialog("Folder access /x\nTrust this folder?\n1. Trust and continue\n2. Quit") {
			t.Errorf("%s: the trust dialog must be recognised", name)
		}
		if !a.ConversationID("0b6f4c1e-8a0d-4e55-9c1f-3a2b1c0d9e8f") || a.ConversationID("x; touch /tmp/owned") || a.ConversationID("") {
			t.Errorf("%s: conversation ids", name)
		}
		ev, err := a.ParseHook("prompt", nil)
		if err != nil || ev == nil || ev.Hook != "prompt" {
			t.Errorf("%s: empty input must give an empty event: %+v %v", name, ev, err)
		}
		if _, err := a.ParseHook("prompt", []byte("{not json")); err == nil {
			t.Errorf("%s: invalid JSON must error", name)
		}
		out := string(a.ContextOutput("prompt", "engineer changed <a> & b"))
		if !strings.Contains(out, `"additionalContext":"engineer changed <a> & b"`) || !strings.HasSuffix(out, "\n") {
			t.Errorf("%s: context output %q", name, out)
		}
	}
	if got := PeekCWD([]byte(`{"session_id":"x","cwd":"/tmp/proj"}`)); got != "/tmp/proj" {
		t.Fatalf("PeekCWD = %q", got)
	}
	if got := PeekCWD([]byte("garbage")); got != "" {
		t.Fatalf("PeekCWD of garbage = %q", got)
	}
}

func TestLaunchArguments(t *testing.T) {
	id := "0b6f4c1e-8a0d-4e55-9c1f-3a2b1c0d9e8f"
	cases := []struct {
		agent Agent
		in    Launch
		want  string
	}{
		{Claude{}, Launch{}, ""},
		{Claude{}, Launch{Resume: id}, "--resume " + id},
		{Claude{}, Launch{Continue: true}, "--continue"},
		{Claude{}, Launch{Continue: true, Name: "fix login"}, "--continue --name=fix login"},
		{Codex{}, Launch{}, ""},
		{Codex{}, Launch{Resume: id}, "resume " + id},
		{Codex{}, Launch{Continue: true, Name: "fix login"}, "resume --last"},
	}
	for _, c := range cases {
		if got := strings.Join(c.agent.Launch(c.in), " "); got != c.want {
			t.Errorf("%s %+v: got %q, want %q", c.agent.Name(), c.in, got, c.want)
		}
	}
	if env := (Claude{}).Env(); len(env) != 1 || env[0] != "CLAUDE_CODE_MCP_AUTO_BACKGROUND_MS=0" {
		t.Fatalf("Claude Code needs its MCP calls kept in the foreground: %v", env)
	}
	// Codex takes an Enter within 120 ms of a burst of characters for a
	// line break: the pause before Enter must outlast that window.
	if d := (Codex{}).SubmitDelay(); d <= 120*time.Millisecond || d > time.Second {
		t.Fatalf("Codex submit delay %s", d)
	}
	if d := (Claude{}).SubmitDelay(); d != 0 {
		t.Fatalf("Claude Code's prompt was always sent at once: %s", d)
	}
}

func TestCodexInstallWritesHooksMCPAndInstructions(t *testing.T) {
	root := t.TempDir()
	var report bytes.Buffer
	in := Install{Root: root, Exe: "/opt/homebrew/bin/pair", Lang: "pt", AskTimeout: 360}
	if err := (Codex{}).Install(in, &report); err != nil {
		t.Fatal(err)
	}
	hooks, _ := os.ReadFile(filepath.Join(root, ".codex", "hooks.json"))
	doc := decode(t, hooks)
	events := doc["hooks"].(map[string]any)
	for _, ev := range []string{"PreToolUse", "PostToolUse", "UserPromptSubmit", "Stop", "PermissionRequest"} {
		if _, ok := events[ev]; !ok {
			t.Errorf("hooks.json lacks %s:\n%s", ev, hooks)
		}
	}
	if _, ok := events["Notification"]; ok {
		t.Errorf("Codex has no Notification event:\n%s", hooks)
	}
	if !strings.Contains(string(hooks), `/opt/homebrew/bin/pair hook notify --agent codex`) || !strings.Contains(string(hooks), `"matcher": "Edit|Write"`) {
		t.Errorf("hooks.json:\n%s", hooks)
	}

	toml, _ := os.ReadFile(filepath.Join(root, ".codex", "config.toml"))
	for _, want := range []string{"[mcp_servers.pair]", `command = "/opt/homebrew/bin/pair"`, `args = ["mcp", "--root", "` + root + `"]`, "tool_timeout_sec = 600", `default_tools_approval_mode = "approve"`,
		`env_vars = ["PAIR_ACTIVE", "PAIR_SESSION", "PAIR_SOCK", "PAIR_ROOT", "PAIR_TMUX_SOCKET", "PAIR_AGENT",`} {
		if !strings.Contains(string(toml), want) {
			t.Errorf("config.toml lacks %q:\n%s", want, toml)
		}
	}
	agents, _ := os.ReadFile(filepath.Join(root, "AGENTS.md"))
	if !strings.Contains(string(agents), "Speak Portuguese") || !strings.Contains(string(agents), blockStart) {
		t.Errorf("AGENTS.md:\n%s", agents)
	}
	if _, err := os.Stat(filepath.Join(root, "CLAUDE.md")); err == nil {
		t.Error("Codex must not write CLAUDE.md")
	}
	if !strings.Contains(report.String(), "/hooks") {
		t.Errorf("the report must say hooks need a review:\n%s", report.String())
	}
	if ok, _ := (Codex{}).Registered(root); !ok {
		t.Error("the server must count as registered")
	}

	// A second run changes nothing; the engineer's own config is kept.
	_ = os.WriteFile(filepath.Join(root, ".codex", "config.toml"), []byte("model = \"gpt-5\"\n\n"+string(toml)), 0o644)
	report.Reset()
	if err := (Codex{}).Install(in, &report); err != nil {
		t.Fatal(err)
	}
	again, _ := os.ReadFile(filepath.Join(root, ".codex", "config.toml"))
	if !strings.HasPrefix(string(again), "model = \"gpt-5\"\n") || strings.Count(string(again), "[mcp_servers.pair]") != 1 {
		t.Errorf("second run:\n%s", again)
	}
	hooks2, _ := os.ReadFile(filepath.Join(root, ".codex", "hooks.json"))
	if string(hooks2) != string(hooks) {
		t.Errorf("hooks.json changed on the second run:\n%s", hooks2)
	}

	// A longer wait for the engineer raises the tool timeout.
	longer := UpsertCodexMCP(string(again), in.Exe, root, 1500)
	if !strings.Contains(longer, "tool_timeout_sec = 1500") || strings.Contains(longer, "tool_timeout_sec = 600") {
		t.Errorf("timeout not updated:\n%s", longer)
	}
	if got := tomlString(`C:\x "q"`); got != `"C:\\x \"q\""` {
		t.Errorf("tomlString: %s", got)
	}
}

const codexPatch = `*** Begin Patch
*** Update File: app/models/user.rb
@@ def valid?
   def valid?
-    true
+    email.include?("@")
   end
@@ def name
-  def name
+  def full_name
*** Add File: app/models/email.rb
+class Email
+end
*** Delete File: app/models/old.rb
*** Update File: lib/a.rb
*** Move to: lib/b.rb
@@
 x
+y
*** End Patch
`

func TestApplyPatchIsRead(t *testing.T) {
	files := ParseApplyPatch(codexPatch)
	if len(files) != 4 {
		t.Fatalf("files: %+v", files)
	}
	user := files[0]
	if user.Path != "app/models/user.rb" || user.Create || user.Delete {
		t.Fatalf("user: %+v", user)
	}
	// The first hunk, as it stands before the change: context and removed lines.
	if user.Old != "  def valid?\n    true\n  end" || user.New != "  def valid?\n    email.include?(\"@\")\n  end" {
		t.Fatalf("first hunk before/after:\n%q\n%q", user.Old, user.New)
	}
	email := files[1]
	if email.Path != "app/models/email.rb" || !email.Create || email.Content != "class Email\nend\n" {
		t.Fatalf("added file: %+v", email)
	}
	if old := files[2]; old.Path != "app/models/old.rb" || !old.Delete {
		t.Fatalf("deleted file: %+v", old)
	}
	if moved := files[3]; moved.Path != "lib/a.rb" || moved.NewPath != "lib/b.rb" || moved.Old != "x" || moved.New != "x\ny" {
		t.Fatalf("moved file: %+v", moved)
	}
	if got := ParseApplyPatch(""); len(got) != 0 {
		t.Fatalf("empty patch: %+v", got)
	}
}

func TestCodexHookInput(t *testing.T) {
	c := Codex{}
	ev, err := c.ParseHook("pre_edit", []byte(`{"session_id":"0b6f4c1e-8a0d-4e55-9c1f-3a2b1c0d9e8f","cwd":"/tmp/proj","hook_event_name":"PreToolUse","tool_name":"apply_patch","tool_use_id":"call_1","tool_input":{"command":`+jsonString(codexPatch)+`}}`))
	if err != nil {
		t.Fatal(err)
	}
	if ev.Kind != KindEdit || ev.Conversation != "0b6f4c1e-8a0d-4e55-9c1f-3a2b1c0d9e8f" || ev.CWD != "/tmp/proj" || len(ev.Files) != 4 {
		t.Fatalf("apply_patch event: %+v", ev)
	}
	if ev.Detail != "app/models/user.rb, app/models/email.rb, app/models/old.rb, lib/a.rb" {
		t.Fatalf("detail: %q", ev.Detail)
	}

	ev, _ = c.ParseHook("activity", []byte(`{"tool_name":"Bash","tool_input":{"command":"bin/rails test","description":"Run the tests"}}`))
	if ev.Kind != KindShell || ev.Detail != "Run the tests" {
		t.Fatalf("shell: %+v", ev)
	}
	ev, _ = c.ParseHook("activity", []byte(`{"tool_name":"exec_command","tool_input":{"cmd":["git","status","-s"]}}`))
	if ev.Kind != KindShell || ev.Detail != "git status -s" {
		t.Fatalf("exec_command with a list: %+v", ev)
	}
	ev, _ = c.ParseHook("activity", []byte(`{"tool_name":"mcp__pair__narrate","tool_input":{"text":"x"}}`))
	if ev.Kind != KindPair {
		t.Fatalf("pair's own tool: %+v", ev)
	}
	ev, _ = c.ParseHook("activity", []byte(`{"tool_name":"mcp__context7__query","tool_input":{}}`))
	if ev.Kind != KindMCP || ev.Detail != "context7__query" {
		t.Fatalf("another server's tool: %+v", ev)
	}
	ev, _ = c.ParseHook("activity", []byte(`{"tool_name":"update_plan","tool_input":{"plan":[]}}`))
	if ev.Kind != KindOther || ev.Detail != "update_plan" {
		t.Fatalf("unknown tool: %+v", ev)
	}

	ev, _ = c.ParseHook("notify", []byte(`{"hook_event_name":"PermissionRequest","tool_name":"Bash","tool_input":{"command":"rm -rf build","description":"Clean the build"}}`))
	if ev.Idle || ev.Message != "Codex needs your permission to use Bash: Clean the build" {
		t.Fatalf("permission request: %+v", ev)
	}
	ev, _ = c.ParseHook("prompt", []byte(`{"hook_event_name":"UserPromptSubmit","prompt":"add validation"}`))
	if ev.Prompt != "add validation" {
		t.Fatalf("prompt: %+v", ev)
	}
	ev, _ = c.ParseHook("stop", []byte(`{"hook_event_name":"Stop","session_id":"s1","stop_hook_active":false,"last_assistant_message":"done"}`))
	if ev.Conversation != "s1" {
		t.Fatalf("stop: %+v", ev)
	}
}

func TestClaudeHookInput(t *testing.T) {
	c := Claude{}
	ev, _ := c.ParseHook("post_edit", []byte(`{"tool_name":"Edit","tool_input":{"file_path":"/p/user.rb","old_string":"true","new_string":"false"},"tool_response":{"filePath":"/p/user.rb","structuredPatch":[{"oldStart":1,"oldLines":1,"newStart":1,"newLines":1,"lines":["-true","+false"]}]}}`))
	if ev.Kind != KindEdit || len(ev.Files) != 1 || len(ev.Files[0].Hunks) != 1 || ev.Files[0].Old != "true" {
		t.Fatalf("edit with patch: %+v", ev)
	}
	ev, _ = c.ParseHook("post_edit", []byte(`{"tool_name":"Write","tool_input":{"file_path":"/p/new.rb","content":"x\n"},"tool_response":{"type":"create","filePath":"/p/new.rb"}}`))
	if ev.Kind != KindWrite || !ev.Files[0].Create || ev.Files[0].Content != "x\n" {
		t.Fatalf("write of a new file: %+v", ev)
	}
	for js, want := range map[string]Kind{
		`{"tool_name":"Read","tool_input":{"file_path":"/p/x"}}`:        KindRead,
		`{"tool_name":"Bash","tool_input":{"command":"ls"}}`:            KindShell,
		`{"tool_name":"Grep","tool_input":{"pattern":"x"}}`:             KindSearch,
		`{"tool_name":"WebFetch","tool_input":{"url":"https://x"}}`:     KindWeb,
		`{"tool_name":"Agent","tool_input":{"description":"look"}}`:     KindDelegate,
		`{"tool_name":"mcp__pair__ask","tool_input":{}}`:                KindPair,
		`{"tool_name":"mcp__linear__issues","tool_input":{}}`:           KindMCP,
		`{"tool_name":"NotebookEdit","tool_input":{"notebook_path":1}}`: KindOther,
	} {
		ev, err := c.ParseHook("activity", []byte(js))
		if err != nil || ev.Kind != want {
			t.Errorf("%s: kind %s, err %v", js, ev.Kind, err)
		}
	}
	ev, _ = c.ParseHook("notify", []byte(`{"message":"Claude is waiting for your input","notification_type":"idle_prompt"}`))
	if !ev.Idle {
		t.Fatalf("idle: %+v", ev)
	}
	ev, _ = c.ParseHook("notify", []byte(`{"message":"Claude needs your permission to use Bash","notification_type":"permission_prompt"}`))
	if ev.Idle || ev.Message == "" {
		t.Fatalf("permission: %+v", ev)
	}
}

func jsonString(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`, "\t", `\t`)
	return `"` + r.Replace(s) + `"`
}

func TestInstalledMeansTheHooksAreThere(t *testing.T) {
	for _, a := range []Agent{Claude{}, Codex{}} {
		root := t.TempDir()
		if ok, why := a.Installed(root); ok || why == "" {
			t.Errorf("%s: an empty repository is not set up", a.Name())
		}
		if a.Name() == "claude" {
			// Claude Code's install registers the MCP server through the
			// claude command: only its hooks file is written here.
			merged, err := MergeClaudeSettings(nil, "/opt/pair")
			if err != nil {
				t.Fatal(err)
			}
			_ = os.MkdirAll(filepath.Join(root, ".claude"), 0o755)
			_ = os.WriteFile(filepath.Join(root, ".claude", "settings.json"), merged, 0o644)
		} else if err := a.Install(Install{Root: root, Exe: "/opt/pair", Lang: "en"}, &bytes.Buffer{}); err != nil {
			t.Fatal(err)
		}
		if ok, why := a.Installed(root); !ok {
			t.Errorf("%s: set up, yet %s", a.Name(), why)
		}
	}
	// A hooks file of someone else's does not count.
	root := t.TempDir()
	_ = os.MkdirAll(filepath.Join(root, ".codex"), 0o755)
	_ = os.WriteFile(filepath.Join(root, ".codex", "hooks.json"), []byte(`{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"notify-send done"}]}]}}`), 0o644)
	if ok, _ := (Codex{}).Installed(root); ok {
		t.Error("another tool's hooks are not pair's")
	}
}
