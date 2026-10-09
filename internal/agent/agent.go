// Package agent is the seam between pair and the coding agent it drives.
//
// Everything pair does on screen and aloud is the same whatever the agent:
// the tmux layout, Neovim, the voice, the activity column, the sessions,
// and the MCP tools narrate, show and ask, which every agent reaches over
// the standard protocol. What differs from one agent to the next is how it
// is launched and resumed, where its hooks and instructions are written,
// what it writes on a hook's stdin, and how it is interrupted. Each Agent
// answers those questions for one agent; the rest of pair asks it and never
// looks at an agent's own format.
package agent

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/fabioaraujo121/pair/internal/shell"
)

// Kind is what a tool call does, in pair's terms. The status bar and the
// activity column describe the call from it, whatever the agent calls the
// tool.
type Kind string

const (
	KindEdit     Kind = "edit"     // changes part of a file
	KindWrite    Kind = "write"    // writes a whole file
	KindRead     Kind = "read"     // reads a file
	KindShell    Kind = "shell"    // runs a command
	KindSearch   Kind = "search"   // searches files
	KindWeb      Kind = "web"      // fetches or searches the web
	KindDelegate Kind = "delegate" // hands work to a sub-agent
	KindMCP      Kind = "mcp"      // calls an MCP tool of another server
	KindPair     Kind = "pair"     // calls one of pair's own tools: narrate, show, ask
	KindOther    Kind = "other"
)

// Hunk is one block of a patch, as Claude Code reports it after an edit
// (tool_response.structuredPatch) and as git prints it.
type Hunk struct {
	OldStart int      `json:"oldStart"`
	OldLines int      `json:"oldLines"`
	NewStart int      `json:"newStart"`
	NewLines int      `json:"newLines"`
	Lines    []string `json:"lines"`
}

// File is one file a tool call touches.
type File struct {
	Path    string // as the agent gave it, absolute or relative to the repository
	NewPath string // when the file is moved
	Old     string // the text an edit replaces, when the agent says so
	New     string // what it becomes
	Content string // the whole content, for a write
	Create  bool   // the file is new
	Delete  bool   // the file is removed
	Hunks   []Hunk // the change as the agent reports it after the call, when it does
}

// Event is what a hook learns from the agent, in pair's terms.
type Event struct {
	Hook         string // pre_edit, post_edit, prompt, stop, notify, activity
	Conversation string // the agent's own id for the conversation, to resume it
	CWD          string
	Tool         string // the tool as the agent names it
	Kind         Kind
	Detail       string // what describes the call: a command, a pattern, a URL, a description
	Files        []File
	Prompt       string // what the engineer typed or said
	Message      string // a notification
	Idle         bool   // the notification says the agent waits for input, not for a permission
}

// Launch is what a session asks of the agent's command line. Nothing is
// written into the repository: the hooks, the MCP server and the
// pre-approvals go to the agent as files under RunDir and flags, for that
// process only, so an agent started without pair in the same repository
// never sees them.
type Launch struct {
	Resume     string // a conversation to open again, already checked by ConversationID
	Continue   bool   // the last conversation, when Resume is empty
	Name       string // the session's name, for agents that take one
	Exe        string // pair's own binary, absolute
	Root       string // the repository
	RunDir     string // the session's own directory under ~/.pair/run, emptied at start
	AskTimeout int    // seconds a call to ask may take, for agents that limit tool calls
}

// Agent is one coding agent pair can drive.
type Agent interface {
	// Name is the setting value: "claude", "codex".
	Name() string
	// Title is what the agent is called on screen: "Claude Code".
	Title() string
	// Short is the word for it in a sentence: "Claude is working".
	Short() string
	// Binary is the agent's command on PATH.
	Binary() string
	// Launch writes the files the agent needs for this session under
	// o.RunDir, or elsewhere outside the repository, and returns the
	// arguments to start it with.
	Launch(o Launch) ([]string, error)
	// Env is what the session's environment adds for this agent.
	Env() []string
	// Leftovers names what an earlier version of pair wrote into the
	// repository for this agent and is still there. While any remains the
	// hooks would run twice.
	Leftovers(root string) []string
	// Uninstall removes those leftovers, and reports what it did.
	Uninstall(root string, report io.Writer) error
	// ParseHook reads what the agent wrote on the stdin of a hook.
	ParseHook(hook string, data []byte) (*Event, error)
	// ContextOutput is what a hook prints to add text to the model's
	// context: at a prompt ("prompt") or at the start of a session
	// ("session").
	ContextOutput(hook, text string) []byte
	// InterruptKeys are the tmux key names that stop the agent's turn.
	InterruptKeys() []string
	// SubmitDelay is how long to wait between typing a prompt into the
	// agent's pane and pressing Enter. Text that arrives all at once looks
	// like a paste to some agents, and an Enter right behind it is taken
	// for a line break.
	SubmitDelay() time.Duration
	// WaitingOnDialog reports whether the agent's screen shows a question
	// that blocks it before any hook can run.
	WaitingOnDialog(screen string) bool
	// ConversationID reports whether s has the shape of one of the agent's
	// conversation ids. Ids come from files in the repository and are
	// used only when they do.
	ConversationID(s string) bool
}

// Default is the agent used when none is chosen.
const Default = "claude"

var builtin = map[string]Agent{
	"claude": Claude{},
	"codex":  Codex{},
}

// For returns the agent of that name.
func For(name string) (Agent, error) {
	if name == "" {
		name = Default
	}
	if a, ok := builtin[strings.ToLower(name)]; ok {
		return a, nil
	}
	return nil, fmt.Errorf("unknown agent %q (one of %s)", name, strings.Join(Names(), ", "))
}

// Known reports whether name is an agent pair can drive.
func Known(name string) bool {
	_, ok := builtin[strings.ToLower(name)]
	return ok
}

// Names lists the agents pair can drive.
func Names() []string {
	names := make([]string, 0, len(builtin))
	for n := range builtin {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// PeekCWD returns the working directory named in a hook's stdin, before
// the agent is known: every agent writes it under the same name.
func PeekCWD(data []byte) string {
	var head struct {
		CWD string `json:"cwd"`
	}
	_ = json.Unmarshal(data, &head)
	return head.CWD
}

// Hooks are pair's hooks, in the order they are registered. An agent maps
// the events to its own names.
var Hooks = []HookSpec{
	{"SessionStart", "", "session", 10}, // the pairing instructions, only while a session runs
	{"PreToolUse", "", "activity", 5},   // every tool: keeps the status bar saying what the agent is doing
	{"PreToolUse", "Edit|Write", "pre_edit", 10},
	{"PostToolUse", "Edit|Write", "post_edit", 15},
	{"UserPromptSubmit", "", "prompt", 20}, // Claude Code's default for this event is 30 s; output is discarded on timeout
	{"Stop", "", "stop", 10},
	{"Notification", "", "notify", 10}, // permission prompts must reach someone not watching the agent's pane
}

// HookSpec is one hook to register: the agent's event, a matcher on the
// tool name, pair's hook name, and the seconds allowed.
type HookSpec struct {
	Event   string
	Matcher string
	Name    string
	Timeout int
}

// hooksInstalled reports whether the hooks file holds pair's hooks.
func hooksInstalled(path string) bool {
	data, err := os.ReadFile(path)
	return err == nil && ourHook.Match(hookCommandsIn(data))
}

// hookCommandsIn returns the "command" values of a hooks document, one per
// line, so ourHook can be matched against each.
func hookCommandsIn(data []byte) []byte {
	var doc struct {
		Hooks map[string][]struct {
			Hooks []struct {
				Command string `json:"command"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	if json.Unmarshal(data, &doc) != nil {
		return nil
	}
	var out []byte
	for _, groups := range doc.Hooks {
		for _, g := range groups {
			for _, h := range g.Hooks {
				out = append(out, h.Command...)
				out = append(out, '\n')
			}
		}
	}
	return out
}

// hookCommand is the command an agent runs for a hook: pair's binary, the
// hook, and the agent the payload comes from.
func hookCommand(exe, hook, agent string) string {
	return shell.Quote(exe) + " hook " + hook + " --agent " + agent
}

// hookEvent is the agent's name for one of pair's hooks that adds context.
func hookEvent(hook string) string {
	if hook == "session" {
		return "SessionStart"
	}
	return "UserPromptSubmit"
}

// contextOutput is the JSON both Claude Code and Codex read from a hook
// that adds to the model's context.
func contextOutput(event, text string) []byte {
	out := map[string]any{
		"hookSpecificOutput": map[string]any{
			"hookEventName":     event,
			"additionalContext": text,
		},
	}
	var b strings.Builder
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(out)
	return []byte(b.String())
}
