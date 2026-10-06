package agent

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fabioaraujo121/pair/internal/shell"
)

// Codex is OpenAI's Codex CLI. Its hooks were made to read like Claude
// Code's: the same events, the same JSON on stdin, the same way to add
// context and to block. What differs is how it edits files: one tool,
// apply_patch, whose argument is a patch in Codex's own grammar.
type Codex struct{}

func (Codex) Name() string             { return "codex" }
func (Codex) Title() string            { return "Codex" }
func (Codex) Short() string            { return "Codex" }
func (Codex) Binary() string           { return "codex" }
func (Codex) InstructionsFile() string { return "AGENTS.md" }
func (Codex) Env() []string            { return nil }

// InterruptKeys: a single Escape interrupts the running turn. Never C-c:
// one press ends the session.
func (Codex) InterruptKeys() []string { return []string{"Escape"} }

// SubmitDelay outlasts Codex's paste detection. Characters that arrive
// within 8 ms of each other are a paste burst, and for 120 ms after one
// (PASTE_ENTER_SUPPRESS_WINDOW in codex-rs/tui) Enter inserts a line
// break instead of sending the prompt.
func (Codex) SubmitDelay() time.Duration { return 400 * time.Millisecond }

func (Codex) Launch(o Launch) []string {
	switch {
	case o.Resume != "":
		return []string{"resume", o.Resume}
	case o.Continue:
		return []string{"resume", "--last"}
	}
	return nil // Codex takes no name at start; the session is named on pair's side
}

// ConversationID: Codex thread ids are UUIDs.
func (Codex) ConversationID(s string) bool { return claudeConversation.MatchString(s) }

// WaitingOnDialog recognises the folder trust question of a repository
// opened for the first time, in its current and its earlier wording.
func (Codex) WaitingOnDialog(screen string) bool {
	s := strings.ToLower(screen)
	return strings.Contains(s, "trust this folder") || strings.Contains(s, "do you trust") ||
		strings.Contains(s, "trust and continue")
}

func (Codex) ContextOutput(hook, text string) []byte {
	return contextOutput("UserPromptSubmit", text)
}

// codexEvents maps pair's events to Codex's. Codex asks for a permission
// through PermissionRequest and has no idle notification.
var codexEvents = map[string]string{"Notification": "PermissionRequest"}

// Install writes the hooks into .codex/hooks.json, the MCP server into
// .codex/config.toml, and the pairing block into AGENTS.md. Codex runs a
// repository's hooks only after they were reviewed once in its /hooks
// screen; Install says so.
func (c Codex) Install(in Install, report io.Writer) error {
	dir := filepath.Join(in.Root, ".codex")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	hooksPath := filepath.Join(dir, "hooks.json")
	existing, _ := os.ReadFile(hooksPath)
	merged, err := MergeHooks(existing, in.Exe, "codex", codexEvents)
	if err != nil {
		return err
	}
	if err := os.WriteFile(hooksPath, merged, 0o644); err != nil {
		return err
	}
	fmt.Fprintln(report, "✓ .codex/hooks.json: hooks (PreToolUse, PostToolUse, UserPromptSubmit, Stop, PermissionRequest)")

	configPath := filepath.Join(dir, "config.toml")
	current, _ := os.ReadFile(configPath)
	updated := UpsertCodexMCP(string(current), in.Exe, in.Root, in.AskTimeout)
	if updated != string(current) {
		if err := os.WriteFile(configPath, []byte(updated), 0o644); err != nil {
			return err
		}
		fmt.Fprintln(report, "✓ .codex/config.toml: MCP server \"pair\" for this project, its tools pre-approved")
	} else {
		fmt.Fprintln(report, "• .codex/config.toml: MCP server \"pair\" already registered")
	}

	changed, err := writeBlock(filepath.Join(in.Root, c.InstructionsFile()), in.Lang)
	if err != nil {
		return err
	}
	if changed {
		fmt.Fprintf(report, "✓ AGENTS.md: pairing-mode block (%s) between %s markers\n", in.Lang, blockStart)
	} else {
		fmt.Fprintln(report, "• AGENTS.md: pairing-mode block already up to date")
	}
	fmt.Fprintln(report, "! Codex runs a repository's hooks only once you have reviewed them: open Codex here, run /hooks, and trust them")
	return nil
}

const (
	codexSectionStart = "# pair:start"
	codexSectionEnd   = "# pair:end"
)

// codexForwardedEnv are the variables Codex hands to pair's MCP server.
// Codex starts an MCP server with a minimal environment (HOME, PATH, SHELL,
// TERM, TMPDIR and little else) plus the names listed in env_vars; the
// ones that are not set are skipped. Without PAIR_ACTIVE and the session
// variables the server would believe no session is running and stay
// silent.
var codexForwardedEnv = []string{
	"PAIR_ACTIVE", "PAIR_SESSION", "PAIR_SOCK", "PAIR_ROOT", "PAIR_TMUX_SOCKET", "PAIR_AGENT",
	"PAIR_TTS", "PAIR_STT", "PAIR_MUTE", "PAIR_LANG", "PAIR_VOICE", "PAIR_RATE",
	"PAIR_KOKORO_VOICE", "PAIR_KOKORO_SPEED", "PAIR_KOKORO_MODEL", "PAIR_WHISPER_MODEL",
	"PAIR_CUE", "PAIR_CUE_IDLE", "PAIR_ASK_MAX", "PAIR_ASK_WAIT", "PAIR_ASK_MIC",
	"PAIR_SILENCE_SECS", "PAIR_SILENCE_LEVEL", "PAIR_HIGHLIGHT_TTL", "PAIR_DIFF_LIMIT", "PAIR_DEBUG",
}

// UpsertCodexMCP returns config.toml with pair's MCP server section put in
// place of an earlier one, or appended. The section is marked, so the rest
// of the file stays the engineer's, and no TOML is parsed.
func UpsertCodexMCP(content, exe, root string, askTimeout int) string {
	if askTimeout < 600 {
		askTimeout = 600
	}
	section := codexSectionStart + " (written by `pair init`; edited by hand, it is rewritten at the next run)\n" +
		"[mcp_servers.pair]\n" +
		"command = " + tomlString(exe) + "\n" +
		"args = [\"mcp\", \"--root\", " + tomlString(root) + "]\n" +
		"env_vars = [" + tomlList(codexForwardedEnv) + "] # the session's variables, which Codex does not pass on by itself\n" +
		fmt.Sprintf("tool_timeout_sec = %d # ask waits for the engineer's push-to-talk\n", askTimeout) +
		"default_tools_approval_mode = \"approve\" # narrate, show and ask never prompt\n" +
		codexSectionEnd + "\n"
	start := strings.Index(content, codexSectionStart)
	end := strings.Index(content, codexSectionEnd)
	if start >= 0 && end > start {
		end += len(codexSectionEnd)
		if end < len(content) && content[end] == '\n' {
			end++
		}
		return content[:start] + section + content[end:]
	}
	if strings.TrimSpace(content) == "" {
		return section
	}
	if !strings.HasSuffix(content, "\n") {
		content += "\n"
	}
	return content + "\n" + section
}

// tomlList renders strings as the inside of a TOML array.
func tomlList(items []string) string {
	quoted := make([]string, len(items))
	for i, it := range items {
		quoted[i] = tomlString(it)
	}
	return strings.Join(quoted, ", ")
}

// tomlString quotes s as a basic TOML string.
func tomlString(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`, "\t", `\t`)
	return `"` + r.Replace(s) + `"`
}

// Installed looks for pair's hooks in .codex/hooks.json.
func (Codex) Installed(root string) (bool, string) {
	if hooksInstalled(filepath.Join(root, ".codex", "hooks.json")) {
		return true, ""
	}
	return false, "no pair hooks in .codex/hooks.json"
}

// Registered looks for the section Install writes.
func (Codex) Registered(root string) (bool, string) {
	data, err := os.ReadFile(filepath.Join(root, ".codex", "config.toml"))
	if err != nil || !strings.Contains(string(data), "[mcp_servers.pair]") {
		return false, "run `pair init --agent codex` to register it"
	}
	return true, ""
}

// codexInput is the JSON Codex writes to a hook's stdin: the same names
// as Claude Code's, with tool_input holding whatever the tool takes.
type codexInput struct {
	SessionID string          `json:"session_id"`
	CWD       string          `json:"cwd"`
	ToolName  string          `json:"tool_name"`
	ToolInput json.RawMessage `json:"tool_input"`
	Prompt    string          `json:"prompt"`
}

// codexToolInput is what pair reads of a tool's arguments.
type codexToolInput struct {
	Command     json.RawMessage `json:"command"` // a string for Bash and apply_patch; other tools may send a list
	Description string          `json:"description"`
	Cmd         json.RawMessage `json:"cmd"`
	Pattern     string          `json:"pattern"`
	Query       string          `json:"query"`
	URL         string          `json:"url"`
	Path        string          `json:"path"`
	FilePath    string          `json:"file_path"`
}

// ParseHook reads Codex's hook input.
func (Codex) ParseHook(hook string, data []byte) (*Event, error) {
	var in codexInput
	if len(strings.TrimSpace(string(data))) > 0 {
		if err := json.Unmarshal(data, &in); err != nil {
			return nil, err
		}
	}
	ev := &Event{Hook: hook, Conversation: in.SessionID, CWD: in.CWD, Tool: in.ToolName, Prompt: in.Prompt}
	var ti codexToolInput
	if len(in.ToolInput) > 0 {
		_ = json.Unmarshal(in.ToolInput, &ti)
	}
	command := rawText(ti.Command)
	if command == "" {
		command = rawText(ti.Cmd)
	}
	switch name := in.ToolName; {
	case strings.HasPrefix(name, "mcp__pair__"):
		ev.Kind = KindPair
	case name == "apply_patch":
		ev.Kind = KindEdit
		ev.Files = ParseApplyPatch(command)
		names := make([]string, len(ev.Files))
		for i, f := range ev.Files {
			names[i] = f.Path
		}
		ev.Detail = strings.Join(names, ", ")
	case name == "Bash" || name == "shell" || name == "exec_command" || name == "shell_command":
		ev.Kind, ev.Detail = KindShell, firstNonEmpty(ti.Description, command)
	case name == "read_file" || name == "view_image":
		ev.Kind, ev.Detail = KindRead, firstNonEmpty(ti.Path, ti.FilePath)
	case name == "grep_files" || name == "list_dir" || name == "glob":
		ev.Kind, ev.Detail = KindSearch, firstNonEmpty(ti.Pattern, ti.Path)
	case name == "web_search" || name == "web_fetch":
		ev.Kind, ev.Detail = KindWeb, firstNonEmpty(ti.Query, ti.URL)
	case name == "spawn_agent" || name == "task":
		ev.Kind, ev.Detail = KindDelegate, ti.Description
	case strings.HasPrefix(name, "mcp__"):
		ev.Kind, ev.Detail = KindMCP, strings.TrimPrefix(name, "mcp__")
	default:
		ev.Kind, ev.Detail = KindOther, strings.ToLower(name)
	}
	if hook == "notify" {
		// PermissionRequest: Codex is about to ask for an approval.
		what := firstNonEmpty(ti.Description, command)
		ev.Message = "Codex needs your permission to use " + in.ToolName
		if what != "" {
			ev.Message += ": " + what
		}
	}
	return ev, nil
}

// rawText returns a JSON string as text, or a list of strings joined by
// spaces, or "".
func rawText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var list []string
	if json.Unmarshal(raw, &list) == nil {
		return shell.Join(list)
	}
	return ""
}
