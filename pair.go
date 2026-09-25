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
