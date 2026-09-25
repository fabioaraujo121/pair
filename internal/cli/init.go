package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	pair "github.com/fabioaraujo121/pair"
	"github.com/fabioaraujo121/pair/internal/config"
)

// Init prepares the current repository for pairing: hooks and permissions in
// .claude/settings.json, a PAIRING.md scratchpad, .gitignore entries, and
// the Neovim plugin under ~/.pair/nvim. Re-running it is safe.
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

	fmt.Printf(`
Optional, to use pair.nvim in your own Neovim config (lazy.nvim):

  { dir = %q, name = "pair.nvim" },

Next: run `+"`pair`"+` inside this repository to start a session.
`, pluginDir)
	return nil
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
