// Package pair holds the assets embedded into the pair binary: the Neovim
// plugin that `pair start` installs into ~/.pair/nvim and the templates that
// `pair init` writes into a repository.
package pair

import "embed"

// PluginFS contains the pair.nvim plugin (nvim/lua/pair/init.lua and
// nvim/plugin/pair.lua).
//
//go:embed nvim/lua/pair/init.lua nvim/plugin/pair.lua
var PluginFS embed.FS

// PairingTemplate is the initial PAIRING.md scratchpad.
//
//go:embed templates/PAIRING.md
var PairingTemplate string

// ClaudeBlockTemplate is the pairing-mode block for CLAUDE.md; {{LANGUAGE}}
// is replaced with the spoken language.
//
//go:embed templates/CLAUDE.md.tmpl
var ClaudeBlockTemplate string

// HammerspoonTemplate defines the global hotkeys; {{PAIR_BIN}} is replaced
// with the binary path.
//
//go:embed templates/hammerspoon.lua
var HammerspoonTemplate string
