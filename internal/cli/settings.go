package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
)

// hookSpec is one hook `pair init` registers in .claude/settings.json.
type hookSpec struct {
	Event   string
	Matcher string
	Name    string
	Timeout int
}

// Only hooks that exist in this milestone are registered. MultiEdit is gone
// from Claude Code 2.1.x, so the matcher is Edit|Write.
var hookSpecs = []hookSpec{
	{"PreToolUse", "Edit|Write", "pre_edit", 10},
	{"PostToolUse", "Edit|Write", "post_edit", 15},
	{"Stop", "", "stop", 10},
}

// mcpTools are pre-approved so narrate/ask/show never prompt (milestone 2).
var mcpTools = []string{"mcp__pair__narrate", "mcp__pair__ask", "mcp__pair__show"}

// ourHook matches commands written by a previous `pair init`, whatever the
// binary path was at the time.
var ourHook = regexp.MustCompile(`(^|[/'"])pair['"]? hook [a-z_]+$`)

// MergeSettings returns existing settings.json content with pair's hooks and
// permissions merged in. Hooks from earlier runs are replaced, everything
// else is preserved. It is idempotent.
func MergeSettings(existing []byte, exe string) ([]byte, error) {
	settings := map[string]any{}
	if len(bytes.TrimSpace(existing)) > 0 {
		if err := json.Unmarshal(existing, &settings); err != nil {
			return nil, fmt.Errorf("existing settings.json is not valid JSON, not touching it: %w", err)
		}
	}

	hooks, _ := settings["hooks"].(map[string]any)
	if hooks == nil {
		hooks = map[string]any{}
	}
	for _, spec := range hookSpecs {
		groups, _ := hooks[spec.Event].([]any)
		kept := make([]any, 0, len(groups)+1)
		for _, g := range groups {
			gm, ok := g.(map[string]any)
			if !ok {
				kept = append(kept, g)
				continue
			}
			entries, _ := gm["hooks"].([]any)
			rest := make([]any, 0, len(entries))
			for _, e := range entries {
				if em, ok := e.(map[string]any); ok {
					if cmd, _ := em["command"].(string); ourHook.MatchString(cmd) {
						continue
					}
				}
				rest = append(rest, e)
			}
			if len(rest) == 0 && len(entries) > 0 {
				continue // the group only held our hooks
			}
			if len(entries) > 0 {
				gm["hooks"] = rest
			}
			kept = append(kept, gm)
		}
		group := map[string]any{
			"hooks": []any{map[string]any{
				"type":    "command",
				"command": shellQuote(exe) + " hook " + spec.Name,
				"timeout": spec.Timeout,
			}},
		}
		if spec.Matcher != "" {
			group["matcher"] = spec.Matcher
		}
		hooks[spec.Event] = append(kept, group)
	}
	settings["hooks"] = hooks

	perms, _ := settings["permissions"].(map[string]any)
	if perms == nil {
		perms = map[string]any{}
	}
	allow, _ := perms["allow"].([]any)
	for _, tool := range mcpTools {
		if !containsString(allow, tool) {
			allow = append(allow, tool)
		}
	}
	perms["allow"] = allow
	settings["permissions"] = perms

	out, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(out, '\n'), nil
}

func containsString(list []any, s string) bool {
	for _, v := range list {
		if str, ok := v.(string); ok && str == s {
			return true
		}
	}
	return false
}
