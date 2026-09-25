package cli

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	pair "github.com/fabioaraujo121/pair"
	"github.com/fabioaraujo121/pair/internal/config"
)

// Init prepares the current repository for pairing: hooks and permissions in
// .claude/settings.json, the pairing-mode block in CLAUDE.md, the MCP server
// registration, a PAIRING.md scratchpad, .gitignore entries, and the Neovim
// plugin under ~/.pair/nvim. Re-running it is safe.
func Init() error {
	cfg := config.Load("")
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}

	settingsPath := filepath.Join(cfg.Root, ".claude", "settings.json")
	existing, _ := os.ReadFile(settingsPath)
	merged, err := MergeSettings(existing, exe)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(settingsPath), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(settingsPath, merged, 0o644); err != nil {
		return err
	}
	fmt.Println("✓ .claude/settings.json: hooks (PreToolUse, PostToolUse, Stop) and MCP permissions")

	claudeMD := filepath.Join(cfg.Root, "CLAUDE.md")
	current, _ := os.ReadFile(claudeMD)
	updated := UpsertBlock(string(current), PairingBlock(cfg.Lang))
	if updated != string(current) {
		if err := os.WriteFile(claudeMD, []byte(updated), 0o644); err != nil {
			return err
		}
		fmt.Printf("✓ CLAUDE.md: pairing-mode block (%s) between %s markers\n", cfg.Lang, blockStart)
	} else {
		fmt.Println("• CLAUDE.md: pairing-mode block already up to date")
	}

	if err := registerMCP(exe, cfg.Root); err != nil {
		fmt.Printf("! MCP server not registered: %v\n  register by hand: claude mcp add pair -- %s mcp --root %s\n", err, exe, cfg.Root)
	} else {
		fmt.Println("✓ MCP server \"pair\" registered for this project (claude mcp add, local scope)")
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

Next: run `+"`pair doctor --mic`"+` once to grant microphone access, then `+"`pair`"+` to start a session.
`, pluginDir)
	return nil
}

// registerMCP records the server in Claude Code's local scope for this
// project with the absolute binary path, replacing any earlier entry.
func registerMCP(exe, root string) error {
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

// claudeCmd builds a `claude` subcommand that works even when pair itself
// runs inside a Claude Code session.
func claudeCmd(claude string, args ...string) *exec.Cmd {
	cmd := exec.Command(claude, args...)
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "CLAUDECODE=") {
			cmd.Env = append(cmd.Env, kv)
		}
	}
	return cmd
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
