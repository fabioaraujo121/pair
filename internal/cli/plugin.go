package cli

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	pair "github.com/fabioaraujo121/pair"
)

// InstallPlugin writes the embedded pair.nvim plugin under <userDir>/nvim so
// `pair start` can put it on Neovim's runtime path without any editor
// configuration. It overwrites on every call so the plugin tracks the binary.
func InstallPlugin(userDir string) (string, error) {
	dest := filepath.Join(userDir, "nvim")
	err := fs.WalkDir(pair.PluginFS, "nvim", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel := strings.TrimPrefix(path, "nvim")
		target := filepath.Join(dest, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := pair.PluginFS.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o644)
	})
	if err != nil {
		return "", fmt.Errorf("install pair.nvim into %s: %w", dest, err)
	}
	return dest, nil
}
