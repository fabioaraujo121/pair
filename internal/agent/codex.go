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

func (Codex) Name() string   { return "codex" }
func (Codex) Title() string  { return "Codex" }
func (Codex) Short() string  { return "Codex" }
func (Codex) Binary() string { return "codex" }
func (Codex) Env() []string  { return nil }

// InterruptKeys: a single Escape interrupts the running turn. Never C-c:
// one press ends the session.
func (Codex) InterruptKeys() []string { return []string{"Escape"} }

// SubmitDelay outlasts Codex's paste detection. Characters that arrive
// within 8 ms of each other are a paste burst, and for 120 ms after one
// (PASTE_ENTER_SUPPRESS_WINDOW in codex-rs/tui) Enter inserts a line
// break instead of sending the prompt.
func (Codex) SubmitDelay() time.Duration { return 400 * time.Millisecond }

// Launch writes pair's profile for Codex, a config layer that holds the
// hooks and the MCP server and applies only when Codex is started with
// `-p pair`, then names the repository through a command-line override.
// The profile is the same file on every start, at a path Codex keeps the
// hook trust for, so the hooks are reviewed once per machine.
func (c Codex) Launch(o Launch) ([]string, error) {
	path, err := c.profilePath()
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	if err := os.WriteFile(path, []byte(CodexProfile(o.Exe, o.AskTimeout)), 0o600); err != nil {
		return nil, err
	}
	args := []string{"-p", codexProfile, "-c", "mcp_servers.pair.args=[\"mcp\", \"--root\", " + tomlString(o.Root) + "]"}
	switch {
	case o.Resume != "":
		args = append(args, "resume", o.Resume)
	case o.Continue:
		args = append(args, "resume", "--last")
	}
	return args, nil // Codex takes no name at start; the session is named on pair's side
}

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

// codexProfile is the name of pair's config layer: $CODEX_HOME/pair.config.toml.
const codexProfile = "pair"

func (Codex) profilePath() (string, error) {
	home := os.Getenv("CODEX_HOME")
	if home == "" {
		h, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		home = filepath.Join(h, ".codex")
	}
	return filepath.Join(home, codexProfile+".config.toml"), nil
}

// CodexProfile renders the config layer: the hooks, and the MCP server
// with its tools pre-approved, the session's variables forwarded, and a
// tool timeout long enough for ask. The repository is not in it: it comes
// on the command line, so one profile serves every repository.
func CodexProfile(exe string, askTimeout int) string {
	if askTimeout < 600 {
		askTimeout = 600
	}
	var b strings.Builder
	b.WriteString("# Written by pair at every `pair start`; Codex loads it with `-p pair`.\n")
	b.WriteString("# Not read by a Codex started without pair.\n\n")
	b.WriteString("[mcp_servers.pair]\n")
	b.WriteString("command = " + tomlString(exe) + "\n")
	b.WriteString("args = [\"mcp\"] # the repository is added on the command line\n")
	b.WriteString("env_vars = [" + tomlList(codexForwardedEnv) + "]\n")
	fmt.Fprintf(&b, "tool_timeout_sec = %d\n", askTimeout)
	b.WriteString("default_tools_approval_mode = \"approve\"\n")
	for _, spec := range Hooks {
		event, mapped := codexEvents[spec.Event]
		if mapped && event == "" {
			continue
		}
		if !mapped {
			event = spec.Event
		}
		b.WriteString("\n[[hooks." + event + "]]\n")
		if spec.Matcher != "" {
			b.WriteString("matcher = " + tomlString(spec.Matcher) + "\n")
		}
		b.WriteString("[[hooks." + event + ".hooks]]\n")
		b.WriteString("type = \"command\"\n")
		b.WriteString("command = " + tomlString(hookCommand(exe, spec.Name, "codex")) + "\n")
		fmt.Fprintf(&b, "timeout = %d\n", spec.Timeout)
	}
	return b.String()
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
	return contextOutput(hookEvent(hook), text)
}

// codexEvents maps pair's events to Codex's. Codex asks for a permission
// through PermissionRequest and has no idle notification.
var codexEvents = map[string]string{"Notification": "PermissionRequest"}

// Leftovers names what the first versions wrote into the repository.
func (Codex) Leftovers(root string) []string {
	var found []string
	if hooksInstalled(filepath.Join(root, ".codex", "hooks.json")) {
		found = append(found, ".codex/hooks.json (pair hooks)")
	}
	if data, err := os.ReadFile(filepath.Join(root, ".codex", "config.toml")); err == nil && strings.Contains(string(data), codexSectionStart) {
		found = append(found, ".codex/config.toml (pair's MCP server)")
	}
	if hasBlock(filepath.Join(root, "AGENTS.md")) {
		found = append(found, "AGENTS.md (pairing block)")
	}
	return found
}

// Uninstall removes what the first versions wrote: pair's hooks from
// .codex/hooks.json, its section from .codex/config.toml, the pairing
// block from AGENTS.md. Files left empty go, and so does an emptied .codex.
func (Codex) Uninstall(root string, report io.Writer) error {
	dir := filepath.Join(root, ".codex")
	hooksPath := filepath.Join(dir, "hooks.json")
	if existing, err := os.ReadFile(hooksPath); err == nil {
		doc := map[string]any{}
		if err := json.Unmarshal(existing, &doc); err == nil {
			changed := false
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
			switch {
			case changed && len(doc) == 0:
				_ = os.Remove(hooksPath)
				fmt.Fprintln(report, "✓ .codex/hooks.json held nothing but pair's hooks: removed")
			case changed:
				out, _ := marshal(doc)
				if err := os.WriteFile(hooksPath, out, 0o644); err != nil {
					return err
				}
				fmt.Fprintln(report, "✓ .codex/hooks.json: pair's hooks removed, the rest kept")
			}
		}
	}
	configPath := filepath.Join(dir, "config.toml")
	if data, err := os.ReadFile(configPath); err == nil {
		if out, had := removeCodexSection(string(data)); had {
			if strings.TrimSpace(out) == "" {
				_ = os.Remove(configPath)
				fmt.Fprintln(report, "✓ .codex/config.toml held nothing but pair's MCP server: removed")
			} else if err := os.WriteFile(configPath, []byte(out), 0o644); err != nil {
				return err
			} else {
				fmt.Fprintln(report, "✓ .codex/config.toml: pair's MCP server removed, the rest kept")
			}
		}
	}
	_ = os.Remove(dir) // only when empty
	if changed, err := removeBlockFile(filepath.Join(root, "AGENTS.md")); err != nil {
		return err
	} else if changed {
		fmt.Fprintln(report, "✓ AGENTS.md: pairing block removed")
	}
	return nil
}

const (
	codexSectionStart = "# pair:start"
	codexSectionEnd   = "# pair:end"
)

// removeCodexSection returns config.toml without the section the first
// versions wrote, and whether there was one.
func removeCodexSection(content string) (string, bool) {
	start := strings.Index(content, codexSectionStart)
	end := strings.Index(content, codexSectionEnd)
	if start < 0 || end < start {
		return content, false
	}
	end += len(codexSectionEnd)
	if end < len(content) && content[end] == '\n' {
		end++
	}
	return strings.TrimRight(content[:start]+content[end:], "\n") + "\n", true
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
