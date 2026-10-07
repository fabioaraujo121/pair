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

// InstructionsTemplate is the pairing-mode block written into the file the
// agent reads its instructions from (CLAUDE.md, AGENTS.md); {{LANGUAGE}} is
// replaced with the spoken language.
//
//go:embed templates/pairing-block.md.tmpl
var InstructionsTemplate string

// HammerspoonTemplate defines the global hotkeys; {{PAIR_BIN}} is replaced
// with the binary path.
//
//go:embed templates/hammerspoon.lua
var HammerspoonTemplate string

// KokoroWorker is the Python program that keeps the Kokoro model loaded
// and synthesizes sentences on request.
//
//go:embed templates/kokoro_worker.py
var KokoroWorker string

// KokoroRequirements pins the Python packages of the Kokoro engine, each
// with the hashes of its wheels, so `pair voice setup kokoro` installs
// exactly what was tested. Refresh it with:
//
//	uv pip compile --universal --generate-hashes --python-version 3.12 requirements.in -o templates/kokoro-requirements.txt
//
// from a requirements.in holding kokoro-onnx==<version> and soundfile==<version>.
//
//go:embed templates/kokoro-requirements.txt
var KokoroRequirements string
