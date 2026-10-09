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
		if a.Name() != name || a.Title() == "" || a.Short() == "" || a.Binary() == "" || len(a.InterruptKeys()) == 0 {
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
		if !strings.Contains(out, `"additionalContext":"engineer changed <a> & b"`) || !strings.Contains(out, `"hookEventName":"UserPromptSubmit"`) || !strings.HasSuffix(out, "\n") {
			t.Errorf("%s: context output %q", name, out)
		}
		if out := string(a.ContextOutput("session", "rules")); !strings.Contains(out, `"hookEventName":"SessionStart"`) {
			t.Errorf("%s: session context output %q", name, out)
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
	run := t.TempDir()
	base := Launch{Exe: "/opt/pair", Root: "/repo/app", RunDir: run, AskTimeout: 360}
	with := func(l Launch, resume string, cont bool, name string) Launch {
		l.Resume, l.Continue, l.Name = resume, cont, name
		return l
	}
	settings, servers := filepath.Join(run, "claude-settings.json"), filepath.Join(run, "claude-mcp.json")
	cases := []struct {
		agent Agent
		in    Launch
		want  string
	}{
		{Claude{}, base, "--settings " + settings + " --mcp-config " + servers},
		{Claude{}, with(base, id, false, ""), "--settings " + settings + " --mcp-config " + servers + " --resume " + id},
		{Claude{}, with(base, "", true, "fix login"), "--settings " + settings + " --mcp-config " + servers + " --continue --name=fix login"},
		{Codex{}, base, `-p pair -c mcp_servers.pair.args=["mcp", "--root", "/repo/app"]`},
		{Codex{}, with(base, id, false, ""), `-p pair -c mcp_servers.pair.args=["mcp", "--root", "/repo/app"] resume ` + id},
		{Codex{}, with(base, "", true, "fix login"), `-p pair -c mcp_servers.pair.args=["mcp", "--root", "/repo/app"] resume --last`},
	}
	t.Setenv("CODEX_HOME", filepath.Join(t.TempDir(), "codex"))
	for _, c := range cases {
		args, err := c.agent.Launch(c.in)
		if err != nil {
			t.Fatal(err)
		}
		if got := strings.Join(args, " "); got != c.want {
			t.Errorf("%s %+v:\n got %q\nwant %q", c.agent.Name(), c.in, got, c.want)
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

// What Claude Code gets for a session lives under the run directory: its
// hooks and pre-approvals in one settings file, pair's MCP server in
// another. Nothing of it is in the repository.
func TestClaudeSessionFiles(t *testing.T) {
	run := filepath.Join(t.TempDir(), "run", "pair-x")
	if _, err := (Claude{}).Launch(Launch{Exe: "/opt/pair", Root: "/repo/app", RunDir: run}); err != nil {
		t.Fatal(err)
	}
	settings, _ := os.ReadFile(filepath.Join(run, "claude-settings.json"))
	doc := decode(t, settings)
	hooks := doc["hooks"].(map[string]any)
	for _, ev := range []string{"SessionStart", "PreToolUse", "PostToolUse", "UserPromptSubmit", "Stop", "Notification"} {
		if _, ok := hooks[ev]; !ok {
			t.Errorf("settings lack the %s hook:\n%s", ev, settings)
		}
	}
	if !strings.Contains(string(settings), `/opt/pair hook session --agent claude`) {
		t.Errorf("the session hook carries the instructions:\n%s", settings)
	}
	allow := doc["permissions"].(map[string]any)["allow"].([]any)
	if len(allow) != 3 || allow[0] != "mcp__pair__narrate" {
		t.Errorf("pre-approved tools: %v", allow)
	}
	servers, _ := os.ReadFile(filepath.Join(run, "claude-mcp.json"))
	if !strings.Contains(string(servers), `"command": "/opt/pair"`) || !strings.Contains(string(servers), `"--root",`) || !strings.Contains(string(servers), `"/repo/app"`) {
		t.Errorf("mcp file:\n%s", servers)
	}
	for _, f := range []string{"claude-settings.json", "claude-mcp.json"} {
		if fi, _ := os.Stat(filepath.Join(run, f)); fi.Mode().Perm() != 0o600 {
			t.Errorf("%s is readable by others: %v", f, fi.Mode().Perm())
		}
	}
}

// Codex gets one profile under its home, the same on every start so the
// hooks stay trusted, with the repository given on the command line.
func TestCodexProfile(t *testing.T) {
	home := filepath.Join(t.TempDir(), "codex")
	t.Setenv("CODEX_HOME", home)
	if _, err := (Codex{}).Launch(Launch{Exe: "/opt/pair", Root: "/repo/app", RunDir: t.TempDir(), AskTimeout: 900}); err != nil {
		t.Fatal(err)
	}
	profile, err := os.ReadFile(filepath.Join(home, "pair.config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(profile)
	for _, want := range []string{"[mcp_servers.pair]", `command = "/opt/pair"`, `args = ["mcp"]`, "tool_timeout_sec = 900", `default_tools_approval_mode = "approve"`,
		`env_vars = ["PAIR_ACTIVE", "PAIR_SESSION"`, "[[hooks.SessionStart]]", "[[hooks.PreToolUse]]", `matcher = "Edit|Write"`, "[[hooks.PermissionRequest]]", "[[hooks.Stop]]", "[[hooks.UserPromptSubmit]]",
		`command = "/opt/pair hook session --agent codex"`, `command = "/opt/pair hook notify --agent codex"`} {
		if !strings.Contains(text, want) {
			t.Errorf("profile lacks %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "/repo/app") || strings.Contains(text, "[[hooks.Notification]]") {
		t.Errorf("the profile is for every repository and uses Codex's own events:\n%s", text)
	}
	// Written again for another repository, it is byte for byte the same:
	// Codex keeps the trust of hooks by file path and content.
	if _, err := (Codex{}).Launch(Launch{Exe: "/opt/pair", Root: "/other/repo", RunDir: t.TempDir(), AskTimeout: 900}); err != nil {
		t.Fatal(err)
	}
	again, _ := os.ReadFile(filepath.Join(home, "pair.config.toml"))
	if string(again) != text {
		t.Error("the profile changed with the repository")
	}
}

// Earlier versions wrote hooks, blocks and registrations into the
// repository. Uninstall takes exactly those out and keeps everything else.
func TestUninstallRemovesWhatEarlierVersionsWrote(t *testing.T) {
	root := t.TempDir()
	write := func(rel, content string) {
		t.Helper()
		path := filepath.Join(root, rel)
		_ = os.MkdirAll(filepath.Dir(path), 0o755)
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(".claude/settings.json", `{"model":"opus","permissions":{"allow":["Bash(npm test)","mcp__pair__narrate","mcp__pair__ask","mcp__pair__show"],"deny":["Bash(rm -rf:*)"]},"hooks":{"PreToolUse":[{"matcher":"Bash","hooks":[{"type":"command","command":"echo audit"}]},{"matcher":"Edit|Write","hooks":[{"type":"command","command":"/old/pair hook pre_edit"}]}],"Stop":[{"hooks":[{"type":"command","command":"/old/pair hook stop"}]}]}}`)
	write("CLAUDE.md", "# My project\n\nRules of the house.\n\n<!-- pair:start -->\n## Pairing mode\nrules\n<!-- pair:end -->\n")
	write(".codex/hooks.json", `{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"/old/pair hook stop --agent codex"}]}]}}`)
	write(".codex/config.toml", "model = \"gpt-5\"\n\n# pair:start (written by `pair init`)\n[mcp_servers.pair]\ncommand = \"/old/pair\"\n# pair:end\n")
	write("AGENTS.md", "<!-- pair:start -->\n## Pairing mode\nrules\n<!-- pair:end -->\n")

	if left := (Claude{}).Leftovers(root); len(left) != 2 {
		t.Fatalf("claude leftovers: %v", left)
	}
	if left := (Codex{}).Leftovers(root); len(left) != 3 {
		t.Fatalf("codex leftovers: %v", left)
	}
	t.Setenv("PATH", "/usr/bin:/bin") // no claude on PATH: the registration step is skipped
	var report bytes.Buffer
	for _, a := range []Agent{Claude{}, Codex{}} {
		if err := a.Uninstall(root, &report); err != nil {
			t.Fatal(err)
		}
		if left := a.Leftovers(root); len(left) != 0 {
			t.Errorf("%s: still there after uninstall: %v", a.Name(), left)
		}
	}
	settings, _ := os.ReadFile(filepath.Join(root, ".claude", "settings.json"))
	doc := decode(t, settings)
	if doc["model"] != "opus" {
		t.Error("the engineer's settings must survive")
	}
	hooks := doc["hooks"].(map[string]any)
	if _, ok := hooks["Stop"]; ok {
		t.Error("a group that held only pair's hook must go")
	}
	if pre := hooks["PreToolUse"].([]any); len(pre) != 1 || pre[0].(map[string]any)["matcher"] != "Bash" {
		t.Errorf("the audit hook must survive alone: %v", pre)
	}
	perms := doc["permissions"].(map[string]any)
	if allow := perms["allow"].([]any); len(allow) != 1 || allow[0] != "Bash(npm test)" {
		t.Errorf("allow: %v", allow)
	}
	if deny := perms["deny"].([]any); len(deny) != 1 {
		t.Errorf("deny: %v", deny)
	}
	claudeMD, _ := os.ReadFile(filepath.Join(root, "CLAUDE.md"))
	if string(claudeMD) != "# My project\n\nRules of the house.\n" {
		t.Errorf("CLAUDE.md:\n%q", claudeMD)
	}
	if _, err := os.Stat(filepath.Join(root, "AGENTS.md")); err == nil {
		t.Error("an AGENTS.md that held only the block must go")
	}
	if _, err := os.Stat(filepath.Join(root, ".codex", "hooks.json")); err == nil {
		t.Error("a hooks.json that held only pair's hooks must go")
	}
	toml, _ := os.ReadFile(filepath.Join(root, ".codex", "config.toml"))
	if string(toml) != "model = \"gpt-5\"\n" {
		t.Errorf("config.toml:\n%q", toml)
	}
	if !strings.Contains(report.String(), "CLAUDE.md: pairing block removed") || !strings.Contains(report.String(), ".codex/hooks.json held nothing") {
		t.Errorf("report:\n%s", report.String())
	}
	// A repository that never saw pair: nothing to do, nothing created.
	clean := t.TempDir()
	report.Reset()
	for _, a := range []Agent{Claude{}, Codex{}} {
		if err := a.Uninstall(clean, &report); err != nil {
			t.Fatal(err)
		}
	}
	if entries, _ := os.ReadDir(clean); len(entries) != 0 || report.Len() != 0 {
		t.Errorf("a clean repository was touched: %v %q", entries, report.String())
	}
}

func TestInstructions(t *testing.T) {
	text := Instructions("pt")
	for _, want := range []string{"Speak Portuguese", ".pair/PAIRING.md", "`narrate`", "`ask`", "`show`"} {
		if !strings.Contains(text, want) {
			t.Errorf("instructions lack %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "<!--") || !strings.HasSuffix(text, "\n") {
		t.Errorf("no markers, one trailing newline:\n%q", text)
	}
	if got := Instructions("xx"); !strings.Contains(got, "Speak xx.") {
		t.Errorf("unknown language code is used as is: %s", got)
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
