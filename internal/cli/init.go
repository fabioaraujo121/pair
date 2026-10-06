package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	pair "github.com/fabioaraujo121/pair"
	"github.com/fabioaraujo121/pair/internal/agent"
	"github.com/fabioaraujo121/pair/internal/config"
)

// Init prepares the current repository for pairing: the agent's hooks,
// instructions and MCP server registration, a PAIRING.md scratchpad,
// .gitignore entries, and the Neovim plugin under ~/.pair/nvim. Re-running
// it is safe.
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

	if err := ag.Install(agent.Install{Root: cfg.Root, Exe: exe, Lang: cfg.Lang, AskTimeout: cfg.AskWait + cfg.AskMax + 120}, os.Stdout); err != nil {
		return err
	}

	scratch := filepath.Join(cfg.Root, "PAIRING.md")
	if _, err := os.Stat(scratch); os.IsNotExist(err) {
		if err := os.WriteFile(scratch, []byte(pair.PairingTemplate), 0o644); err != nil {
			return err
		}
		fmt.Println("✓ PAIRING.md created")
	} else {
		fmt.Println("• PAIRING.md already exists, left as is")
	}

	ignores := []string{".pair/"}
	if os.Getenv("PAIR_TRACK_SCRATCHPAD") == "" {
		ignores = append(ignores, "PAIRING.md")
	}
	added, err := ensureGitignore(filepath.Join(cfg.Root, ".gitignore"), ignores)
	if err != nil {
		return err
	}
	if len(added) > 0 {
		fmt.Printf("✓ .gitignore: added %s\n", strings.Join(added, ", "))
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

Next: run `+"`pair`"+` to start a session. In the session: F5 to talk, F6 to interrupt.
`, pluginDir)
	return nil
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
