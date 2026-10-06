package agent

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/fabioaraujo121/pair/internal/shell"
)

// Claude is Claude Code, the agent pair was built with.
type Claude struct{}

func (Claude) Name() string               { return "claude" }
func (Claude) Title() string              { return "Claude Code" }
func (Claude) Short() string              { return "Claude" }
func (Claude) Binary() string             { return "claude" }
func (Claude) InstructionsFile() string   { return "CLAUDE.md" }
func (Claude) InterruptKeys() []string    { return []string{"Escape"} } // never C-c: a double C-c exits Claude Code
func (Claude) SubmitDelay() time.Duration { return 0 }                  // Claude Code submits typed text at once

// Env keeps narrate and ask blocking: without it Claude Code moves an MCP
// call to a background task after two minutes.
func (Claude) Env() []string { return []string{"CLAUDE_CODE_MCP_AUTO_BACKGROUND_MS=0"} }

func (Claude) Launch(o Launch) []string {
	var args []string
	switch {
	case o.Resume != "":
		args = append(args, "--resume", o.Resume)
	case o.Continue:
		args = append(args, "--continue")
	}
	if o.Name != "" {
		args = append(args, "--name="+o.Name) // one word with "=", so a name that starts with a dash is a name
	}
	return args
}

// claudeConversation is the shape of a Claude Code conversation id.
var claudeConversation = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F-]{20,60}$`)

func (Claude) ConversationID(s string) bool { return claudeConversation.MatchString(s) }

// WaitingOnDialog recognises the workspace trust question of a repository
// opened for the first time, and the other dialogs drawn the same way.
func (Claude) WaitingOnDialog(screen string) bool {
	s := strings.ToLower(screen)
	return strings.Contains(s, "trust this folder") || strings.Contains(s, "do you trust") ||
		(strings.Contains(s, "enter to confirm") && strings.Contains(s, "esc to cancel"))
}

func (Claude) ContextOutput(hook, text string) []byte {
	return contextOutput("UserPromptSubmit", text)
}

// claudeTools are pre-approved so narrate, ask and show never prompt.
var claudeTools = []string{"mcp__pair__narrate", "mcp__pair__ask", "mcp__pair__show"}

// Install writes the hooks and permissions into .claude/settings.json, the
// pairing block into CLAUDE.md, and registers the MCP server in Claude
// Code's local scope for the project.
func (c Claude) Install(in Install, report io.Writer) error {
	settingsPath := filepath.Join(in.Root, ".claude", "settings.json")
	existing, _ := os.ReadFile(settingsPath)
	merged, err := MergeClaudeSettings(existing, in.Exe)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(settingsPath), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(settingsPath, merged, 0o644); err != nil {
		return err
	}
	fmt.Fprintln(report, "✓ .claude/settings.json: hooks (PreToolUse, PostToolUse, UserPromptSubmit, Stop, Notification) and MCP permissions")

	changed, err := writeBlock(filepath.Join(in.Root, c.InstructionsFile()), in.Lang)
	if err != nil {
		return err
	}
	if changed {
		fmt.Fprintf(report, "✓ CLAUDE.md: pairing-mode block (%s) between %s markers\n", in.Lang, blockStart)
	} else {
		fmt.Fprintln(report, "• CLAUDE.md: pairing-mode block already up to date")
	}

	if err := registerClaudeMCP(in.Exe, in.Root); err != nil {
		fmt.Fprintf(report, "! MCP server not registered: %v\n  register by hand: claude mcp add pair -- %s mcp --root %s\n", err, in.Exe, in.Root)
	} else {
		fmt.Fprintln(report, "✓ MCP server \"pair\" registered for this project (claude mcp add, local scope)")
	}
	return nil
}

// MergeClaudeSettings returns settings.json with pair's hooks and
// permissions merged in.
func MergeClaudeSettings(existing []byte, exe string) ([]byte, error) {
	merged, err := MergeHooks(existing, exe, "claude", nil)
	if err != nil {
		return nil, err
	}
	doc := map[string]any{}
	if err := json.Unmarshal(merged, &doc); err != nil {
		return nil, err
	}
	perms, _ := doc["permissions"].(map[string]any)
	if perms == nil {
		perms = map[string]any{}
	}
	allow, _ := perms["allow"].([]any)
	for _, tool := range claudeTools {
		if !containsString(allow, tool) {
			allow = append(allow, tool)
		}
	}
	perms["allow"] = allow
	doc["permissions"] = perms
	return marshal(doc)
}

// Installed looks for pair's hooks in .claude/settings.json.
func (Claude) Installed(root string) (bool, string) {
	if hooksInstalled(filepath.Join(root, ".claude", "settings.json")) {
		return true, ""
	}
	return false, "no pair hooks in .claude/settings.json"
}

// Registered asks Claude Code whether the server is known for the project.
func (Claude) Registered(root string) (bool, string) {
	claude, err := exec.LookPath("claude")
	if err != nil {
		return false, "claude is not on PATH"
	}
	get := claudeCmd(claude, "mcp", "get", "pair")
	get.Dir = root
	if err := get.Run(); err != nil {
		return false, "run `pair init` to register it"
	}
	return true, ""
}

// registerClaudeMCP records the server in Claude Code's local scope for
// this project with the absolute binary path, replacing any earlier entry.
func registerClaudeMCP(exe, root string) error {
	claude, err := exec.LookPath("claude")
	if err != nil {
		return err
	}
	remove := claudeCmd(claude, "mcp", "remove", "--scope", "local", "pair")
	remove.Dir = root
	_ = remove.Run() // absent is fine
	add := claudeCmd(claude, "mcp", "add", "--scope", "local", "--transport", "stdio", "pair", "--", exe, "mcp", "--root", root)
	add.Dir = root
	if out, err := add.CombinedOutput(); err != nil {
		return fmt.Errorf("%v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// claudeCmd builds a `claude` subcommand. It goes through the user's
// interactive shell so a wrapper function or alias around `claude` (for
// example one that selects a per-folder account) applies, exactly as if
// the user had typed the command. It also works when pair itself runs
// inside a Claude Code session.
func claudeCmd(claude string, args ...string) *exec.Cmd {
	var cmd *exec.Cmd
	sh := os.Getenv("SHELL")
	if base := filepath.Base(sh); base == "zsh" || base == "bash" {
		cmd = exec.Command(sh, "-ic", "claude "+shell.Join(args))
	} else {
		cmd = exec.Command(claude, args...)
	}
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "CLAUDECODE=") {
			cmd.Env = append(cmd.Env, kv)
		}
	}
	return cmd
}

// claudeInput is the JSON Claude Code writes to a hook's stdin (fields
// observed on 2.1.282; unknown fields are ignored).
type claudeInput struct {
	SessionID    string          `json:"session_id"`
	CWD          string          `json:"cwd"`
	ToolName     string          `json:"tool_name"`
	ToolInput    claudeToolInput `json:"tool_input"`
	ToolResponse json.RawMessage `json:"tool_response"`
	Prompt       string          `json:"prompt"`
	Message      string          `json:"message"`           // Notification
	Notification string          `json:"notification_type"` // Notification
}

// claudeToolInput covers the tools whose arguments the hooks read.
type claudeToolInput struct {
	Command     string `json:"command"`     // Bash
	Description string `json:"description"` // Bash, Agent
	Pattern     string `json:"pattern"`     // Grep, Glob
	URL         string `json:"url"`         // WebFetch
	Query       string `json:"query"`       // WebSearch
	FilePath    string `json:"file_path"`
	OldString   string `json:"old_string"`
	NewString   string `json:"new_string"`
	Content     string `json:"content"`
}

// ParseHook reads Claude Code's hook input. An empty stdin gives an empty
// event, not an error, so hooks can be run by hand.
func (Claude) ParseHook(hook string, data []byte) (*Event, error) {
	var in claudeInput
	if len(strings.TrimSpace(string(data))) > 0 {
		if err := json.Unmarshal(data, &in); err != nil {
			return nil, err
		}
	}
	ev := &Event{Hook: hook, Conversation: in.SessionID, CWD: in.CWD, Tool: in.ToolName, Prompt: in.Prompt, Message: strings.TrimSpace(in.Message)}
	ev.Idle = in.Notification == "idle_prompt" || strings.Contains(strings.ToLower(ev.Message), "waiting for your input")
	ti := in.ToolInput
	switch {
	case strings.HasPrefix(in.ToolName, "mcp__pair__"):
		ev.Kind = KindPair
	case in.ToolName == "Edit":
		ev.Kind = KindEdit
		ev.Files = []File{{Path: ti.FilePath, Old: ti.OldString, New: ti.NewString}}
	case in.ToolName == "Write":
		ev.Kind = KindWrite
		ev.Files = []File{{Path: ti.FilePath, Content: ti.Content}}
	case in.ToolName == "Read":
		ev.Kind, ev.Detail = KindRead, ti.FilePath
	case in.ToolName == "Bash":
		ev.Kind, ev.Detail = KindShell, firstNonEmpty(ti.Description, ti.Command)
	case in.ToolName == "Grep" || in.ToolName == "Glob":
		ev.Kind, ev.Detail = KindSearch, ti.Pattern
	case in.ToolName == "WebFetch" || in.ToolName == "WebSearch":
		ev.Kind, ev.Detail = KindWeb, firstNonEmpty(ti.URL, ti.Query)
	case in.ToolName == "Agent" || in.ToolName == "Task":
		ev.Kind, ev.Detail = KindDelegate, ti.Description
	case strings.HasPrefix(in.ToolName, "mcp__"):
		ev.Kind, ev.Detail = KindMCP, strings.TrimPrefix(in.ToolName, "mcp__")
	default:
		ev.Kind, ev.Detail = KindOther, strings.ToLower(in.ToolName)
	}
	if len(ev.Files) == 1 && len(in.ToolResponse) > 0 {
		// After the call, Claude Code reports the change it made.
		var resp struct {
			Type            string `json:"type"`
			StructuredPatch []Hunk `json:"structuredPatch"`
		}
		_ = json.Unmarshal(in.ToolResponse, &resp)
		ev.Files[0].Hunks = resp.StructuredPatch
		ev.Files[0].Create = resp.Type == "create"
	}
	return ev, nil
}

func firstNonEmpty(a, b string) string {
	if strings.TrimSpace(a) != "" {
		return a
	}
	return b
}
