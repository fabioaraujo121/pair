package agent

import (
	"context"
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

// Launch writes the session's settings (hooks and pre-approved tools) and
// the MCP server definition under o.RunDir and names them on the command
// line. Claude Code reads them for this process only.
func (c Claude) Launch(o Launch) ([]string, error) {
	settings := filepath.Join(o.RunDir, "claude-settings.json")
	servers := filepath.Join(o.RunDir, "claude-mcp.json")
	if err := os.MkdirAll(o.RunDir, 0o700); err != nil {
		return nil, err
	}
	doc, err := ClaudeSettings(o.Exe)
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(settings, doc, 0o600); err != nil {
		return nil, err
	}
	mcp, _ := json.MarshalIndent(map[string]any{"mcpServers": map[string]any{"pair": map[string]any{
		"type": "stdio", "command": o.Exe, "args": []string{"mcp", "--root", o.Root},
	}}}, "", "  ")
	if err := os.WriteFile(servers, append(mcp, '\n'), 0o600); err != nil {
		return nil, err
	}
	args := []string{"--settings", settings, "--mcp-config", servers}
	switch {
	case o.Resume != "":
		args = append(args, "--resume", o.Resume)
	case o.Continue:
		args = append(args, "--continue")
	}
	if o.Name != "" {
		args = append(args, "--name="+o.Name) // one word with "=", so a name that starts with a dash is a name
	}
	return args, nil
}

// ClaudeSettings is the settings document of a session: pair's hooks and
// the pre-approval of its three tools, nothing else.
func ClaudeSettings(exe string) ([]byte, error) {
	merged, err := MergeHooks(nil, exe, "claude", nil)
	if err != nil {
		return nil, err
	}
	doc := map[string]any{}
	if err := json.Unmarshal(merged, &doc); err != nil {
		return nil, err
	}
	allow := make([]any, 0, len(claudeTools))
	for _, t := range claudeTools {
		allow = append(allow, t)
	}
	doc["permissions"] = map[string]any{"allow": allow}
	return marshal(doc)
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
	return contextOutput(hookEvent(hook), text)
}

// claudeTools are pre-approved so narrate, ask and show never prompt.
var claudeTools = []string{"mcp__pair__narrate", "mcp__pair__ask", "mcp__pair__show"}

// Leftovers names what the first versions wrote into the repository.
func (Claude) Leftovers(root string) []string {
	var found []string
	if hooksInstalled(filepath.Join(root, ".claude", "settings.json")) {
		found = append(found, ".claude/settings.json (pair hooks)")
	}
	if hasBlock(filepath.Join(root, "CLAUDE.md")) {
		found = append(found, "CLAUDE.md (pairing block)")
	}
	return found
}

// Uninstall takes pair's hooks and permissions out of .claude/settings.json,
// the pairing block out of CLAUDE.md, and the MCP server out of Claude
// Code's local registration for the project.
func (Claude) Uninstall(root string, report io.Writer) error {
	settingsPath := filepath.Join(root, ".claude", "settings.json")
	if existing, err := os.ReadFile(settingsPath); err == nil {
		cleaned, changed, err := withoutClaudeSettings(existing)
		if err != nil {
			return err
		}
		switch {
		case changed && cleaned == nil:
			_ = os.Remove(settingsPath)
			_ = os.Remove(filepath.Dir(settingsPath)) // only when empty
			fmt.Fprintln(report, "✓ .claude/settings.json held nothing but pair's hooks: removed")
		case changed:
			if err := os.WriteFile(settingsPath, cleaned, 0o644); err != nil {
				return err
			}
			fmt.Fprintln(report, "✓ .claude/settings.json: pair's hooks and permissions removed, the rest kept")
		}
	}
	if changed, err := removeBlockFile(filepath.Join(root, "CLAUDE.md")); err != nil {
		return err
	} else if changed {
		fmt.Fprintln(report, "✓ CLAUDE.md: pairing block removed")
	}
	if claude, err := exec.LookPath("claude"); err == nil {
		get := claudeCmd(claude, "mcp", "get", "pair")
		get.Dir = root
		if get.Run() == nil {
			remove := claudeCmd(claude, "mcp", "remove", "--scope", "local", "pair")
			remove.Dir = root
			if out, err := remove.CombinedOutput(); err != nil {
				fmt.Fprintf(report, "! MCP server still registered: %v: %s\n  remove by hand: claude mcp remove --scope local pair\n", err, strings.TrimSpace(string(out)))
			} else {
				fmt.Fprintln(report, "✓ MCP server \"pair\" removed from Claude Code's registration for this project")
			}
		}
	}
	return nil
}

// withoutClaudeSettings returns settings.json without pair's hooks and
// permissions: nil when nothing else was in it, the same bytes when
// nothing of pair's was.
func withoutClaudeSettings(existing []byte) (cleaned []byte, changed bool, err error) {
	doc := map[string]any{}
	if err := json.Unmarshal(existing, &doc); err != nil {
		return nil, false, fmt.Errorf(".claude/settings.json is not valid JSON, not touching it: %w", err)
	}
	if hooks, _ := doc["hooks"].(map[string]any); hooks != nil {
		for event, groups := range hooks {
			list, _ := groups.([]any)
			kept := withoutOurs(list)
			if len(kept) != len(list) {
				changed = true
			}
			if len(kept) == 0 {
				delete(hooks, event)
			} else {
				hooks[event] = kept
			}
		}
		if len(hooks) == 0 {
			delete(doc, "hooks")
		}
	}
	if perms, _ := doc["permissions"].(map[string]any); perms != nil {
		if allow, _ := perms["allow"].([]any); allow != nil {
			kept := make([]any, 0, len(allow))
			for _, v := range allow {
				if str, ok := v.(string); ok && strings.HasPrefix(str, "mcp__pair__") {
					changed = true
					continue
				}
				kept = append(kept, v)
			}
			if len(kept) == 0 {
				delete(perms, "allow")
			} else {
				perms["allow"] = kept
			}
		}
		if len(perms) == 0 {
			delete(doc, "permissions")
		}
	}
	if !changed {
		return existing, false, nil
	}
	if len(doc) == 0 {
		return nil, true, nil
	}
	out, err := marshal(doc)
	return out, true, err
}

// claudeCmd builds a `claude` subcommand. It goes through the user's
// interactive shell so a wrapper function or alias around `claude` (for
// example one that selects a per-folder account) applies, exactly as if
// the user had typed the command. It also works when pair itself runs
// inside a Claude Code session.
//
// The command gets half a minute: a `claude` that is not logged in, or
// that asks something on its first run, must not hold pair for ever.
func claudeCmd(claude string, args ...string) *exec.Cmd {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	_ = cancel // the process ends with the context or with Wait
	var cmd *exec.Cmd
	sh := os.Getenv("SHELL")
	if base := filepath.Base(sh); base == "zsh" || base == "bash" {
		cmd = exec.CommandContext(ctx, sh, "-ic", "claude "+shell.Join(args))
	} else {
		cmd = exec.CommandContext(ctx, claude, args...)
	}
	cmd.WaitDelay = time.Second
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
