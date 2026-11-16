package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	pair "github.com/fabioaraujo121/pair"
	"github.com/fabioaraujo121/pair/internal/agent"
	"github.com/fabioaraujo121/pair/internal/config"
)

// Init saves the engineer's settings (agent, language, voice) and installs
// what pair keeps under ~/.pair: the Neovim plugin and the hotkey file.
// Nothing is written into the repository: the agent gets its hooks,
// instructions and MCP server at `pair start`, for that process only.
// Re-running it is safe.
func Init(args []string) error {
	settings := map[string]string{}
	agentName := ""
	for i := 0; i < len(args); i++ {
		value := func() string {
			if i+1 < len(args) {
				i++
				return args[i]
			}
			return ""
		}
		switch args[i] {
		case "--lang":
			settings["PAIR_LANG"] = value()
		case "--voice":
			settings["PAIR_VOICE"] = value()
		case "--rate":
			settings["PAIR_RATE"] = value()
		case "--agent":
			agentName = value()
			if !agent.Known(agentName) {
				return fmt.Errorf("unknown agent %q (one of %s)", agentName, strings.Join(agent.Names(), ", "))
			}
			settings["PAIR_AGENT"] = agentName
		default:
			return fmt.Errorf("unknown option %q (usage: pair init [--agent claude|codex] [--lang en] [--voice Samantha] [--rate 190])", args[i])
		}
	}
	for k, v := range settings {
		if v == "" {
			return fmt.Errorf("%s needs a value", k)
		}
	}
	// The keys have no flags: one set in the environment when init runs
	// is kept, so `PAIR_KEY_TALK=F9 pair init` is enough.
	for _, k := range []string{"PAIR_KEY_TALK", "PAIR_KEY_INTERRUPT", "PAIR_KEY_AGENT", "PAIR_KEY_QUIT"} {
		if v := os.Getenv(k); v != "" {
			if !config.ValidKey(v) {
				return fmt.Errorf("%s=%q is not a key name tmux knows (F9, C-t, M-Space)", k, v)
			}
			settings[k] = v
		}
	}
	if len(settings) > 0 {
		if err := config.SaveUser(settings); err != nil {
			return err
		}
		fmt.Printf("✓ %s updated\n", config.UserConfigPath())
	}
	cfg := config.Load("")
	if agentName != "" {
		cfg.Agent = agentName
	}
	ag, err := agent.For(cfg.Agent)
	if err != nil {
		return err
	}
	fmt.Printf("• pairing with %s (change with `pair init --agent %s`)\n", ag.Title(), otherAgent(ag.Name()))
	fmt.Printf("• speaking %s with voice %s at %d wpm (change with `pair init --lang pt --voice Luciana`)\n", cfg.Lang, cfg.Voice, cfg.Rate)
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	if left := ag.Leftovers(cfg.Root); len(left) > 0 {
		fmt.Printf("! an earlier version of pair wrote into this repository: %s\n  run `pair uninstall` once to take it out\n", strings.Join(left, ", "))
	} else {
		fmt.Println("• nothing is written into the repository: the agent gets pair's hooks and instructions at `pair`, for that session only")
	}

	pluginDir, err := InstallPlugin(cfg.UserDir)
	if err != nil {
		return err
	}
	fmt.Printf("✓ pair.nvim installed in %s (pair start loads it automatically)\n", pluginDir)

	hsPath := filepath.Join(cfg.UserDir, "hammerspoon.lua")
	hs := strings.ReplaceAll(pair.HammerspoonTemplate, "{{PAIR_BIN}}", exe)
	if err := os.WriteFile(hsPath, []byte(hs), 0o644); err != nil {
		return err
	}
	fmt.Printf("✓ Hammerspoon hotkeys written to %s (optional)\n", hsPath)

	fmt.Printf(`
Optional, to use pair.nvim in your own Neovim config (lazy.nvim):

  { dir = %q, name = "pair.nvim" },

Optional, global hotkeys (ctrl+alt+cmd+space to talk, ctrl+alt+cmd+esc to interrupt)
with Hammerspoon: add to ~/.hammerspoon/init.lua

  dofile(os.getenv("HOME") .. "/.pair/hammerspoon.lua")

Next: run `+"`pair`"+` in a repository to start a session. In the session: %s to talk, %s to interrupt.
`, pluginDir, cfg.Keys.Talk, cfg.Keys.Interrupt)
	return nil
}

// Uninstall takes out of the current repository what earlier versions of
// pair wrote there: hooks, pairing blocks, MCP registrations, the old
// scratchpad's .gitignore line. Nothing of the kind is written any more.
func Uninstall(w io.Writer) error {
	cfg := config.Load("")
	before := len(leftovers(cfg.Root))
	for _, name := range agent.Names() {
		ag, _ := agent.For(name)
		if err := ag.Uninstall(cfg.Root, w); err != nil {
			return err
		}
	}
	if changed, err := dropGitignoreLines(filepath.Join(cfg.Root, ".gitignore"), "# pair session state", ".pair/", "PAIRING.md"); err != nil {
		return err
	} else if changed {
		fmt.Fprintln(w, "✓ .gitignore: pair's lines removed (.pair ignores itself)")
	}
	if before == 0 {
		fmt.Fprintf(w, "nothing of pair's in %s\n", tildePath(cfg.Root))
	}
	if _, err := os.Stat(filepath.Join(cfg.Root, "PAIRING.md")); err == nil {
		fmt.Fprintln(w, "• PAIRING.md at the root was the scratchpad of earlier versions; the scratchpad is .pair/PAIRING.md now. Keep or delete it as you like")
	}
	return nil
}

// leftovers lists what every agent's adapter finds of earlier versions.
func leftovers(root string) []string {
	var all []string
	for _, name := range agent.Names() {
		ag, _ := agent.For(name)
		all = append(all, ag.Leftovers(root)...)
	}
	return all
}

// dropGitignoreLines removes the given lines from a .gitignore when they
// are there, and the file when nothing else was.
func dropGitignoreLines(path string, lines ...string) (bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return false, nil
	}
	drop := map[string]bool{}
	for _, l := range lines {
		drop[l] = true
	}
	var kept []string
	changed := false
	for _, l := range strings.Split(strings.TrimRight(string(data), "\n"), "\n") {
		if drop[strings.TrimSpace(l)] {
			changed = true
			continue
		}
		kept = append(kept, l)
	}
	if !changed {
		return false, nil
	}
	if strings.TrimSpace(strings.Join(kept, "")) == "" {
		return true, os.Remove(path)
	}
	return true, os.WriteFile(path, []byte(strings.Join(kept, "\n")+"\n"), 0o644)
}

// otherAgent names an agent that is not this one, for the hint.
func otherAgent(name string) string {
	for _, n := range agent.Names() {
		if n != name {
			return n
		}
	}
	return name
}

// ensureGitignore appends the given patterns when they are missing and
// returns the ones it added.
func ensureGitignore(path string, patterns []string) ([]string, error) {
	data, _ := os.ReadFile(path)
	present := map[string]bool{}
	for _, line := range strings.Split(string(data), "\n") {
		present[strings.TrimSpace(line)] = true
	}
	var added []string
	for _, p := range patterns {
		if !present[p] && !present["/"+p] {
			added = append(added, p)
		}
	}
	if len(added) == 0 {
		return nil, nil
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	if len(data) > 0 && !strings.HasSuffix(string(data), "\n") {
		_, _ = f.WriteString("\n")
	}
	_, _ = f.WriteString("# pair session state\n" + strings.Join(added, "\n") + "\n")
	return added, nil
}
