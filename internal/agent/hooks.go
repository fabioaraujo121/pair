package agent

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
)

// Claude Code and Codex read the same shape of hook configuration: a
// "hooks" object of events, each a list of groups with an optional matcher
// and the commands to run. This file merges pair's hooks into such a
// document, keeping everything else in it.

// ourHook matches a command written by an earlier `pair init`, whatever
// the binary path was at the time.
var ourHook = regexp.MustCompile(`(?m)(^|[/'"])pair['"]? hook [a-z_]+( --agent [a-z]+)?$`)

// MergeHooks returns the JSON document with pair's hooks merged into its
// "hooks" key. Hooks from earlier runs are replaced, everything else is
// kept, and running it twice gives the same result. events maps pair's
// event names to the agent's; an event mapped to "" is left out.
func MergeHooks(existing []byte, exe, agent string, events map[string]string) ([]byte, error) {
	doc := map[string]any{}
	if len(bytes.TrimSpace(existing)) > 0 {
		if err := json.Unmarshal(existing, &doc); err != nil {
			return nil, fmt.Errorf("the existing file is not valid JSON, not touching it: %w", err)
		}
	}
	hooks, _ := doc["hooks"].(map[string]any)
	if hooks == nil {
		hooks = map[string]any{}
	}
	cleaned := map[string]bool{}
	for _, spec := range Hooks {
		event, mapped := events[spec.Event]
		if mapped && event == "" {
			continue
		}
		if !mapped {
			event = spec.Event
		}
		groups, _ := hooks[event].([]any)
		if !cleaned[event] { // drop what an earlier run wrote, once per event
			groups = withoutOurs(groups)
			cleaned[event] = true
		}
		group := map[string]any{
			"hooks": []any{map[string]any{
				"type":    "command",
				"command": hookCommand(exe, spec.Name, agent),
				"timeout": spec.Timeout,
			}},
		}
		if spec.Matcher != "" {
			group["matcher"] = spec.Matcher
		}
		hooks[event] = append(groups, group)
	}
	doc["hooks"] = hooks
	return marshal(doc)
}

// withoutOurs removes pair's hook entries from the groups of one event and
// the groups that held nothing else.
func withoutOurs(groups []any) []any {
	kept := make([]any, 0, len(groups)+2)
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
			continue
		}
		if len(entries) > 0 {
			gm["hooks"] = rest
		}
		kept = append(kept, gm)
	}
	return kept
}

func marshal(doc map[string]any) ([]byte, error) {
	out, err := json.MarshalIndent(doc, "", "  ")
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
