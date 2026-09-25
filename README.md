# pair

Voice-driven pairing for Claude Code in the terminal. Claude explains what it
is about to do out loud, points at code in Neovim, asks questions and listens
for the answer, while you watch edits land live and can interrupt at any time.

Status: milestone 3 of 5 (voice in). See `PRD.md` for the design and
`VALIDATION.md` for what was verified against Claude Code 2.1.282 before
building.

## What works today

- `pair` opens a tmux session: Claude Code on the left, Neovim top right, a
  narration/diff log bottom right.
- Before every `Edit`/`Write`, Neovim jumps to the range Claude is about to
  change and highlights it (`PreToolUse` hook).
- After the edit, Neovim reloads the file and the diff is rendered into the
  log pane, through `delta` when installed (`PostToolUse` hook).
- A chime marks the end of each turn (`Stop` hook).
- Claude speaks: the `narrate` MCP tool says one or two sentences through
  macOS `say` and blocks until done; `show` points at a file range in Neovim
  and can speak a note. Text is normalized for speech (markdown stripped,
  identifiers split, `PR` and `DB` expanded). Pressing Escape in Claude Code
  interrupts the speech.
- `pair init` adds the pairing-mode block to `CLAUDE.md` (plan in
  `PAIRING.md` first, narrate before each change, `ask` when ambiguous) and
  registers the MCP server for the project.
- Every event is appended to `.pair/events.jsonl`.

- Claude listens: `ask` speaks the question, plays a chime, records until
  you pause for 1.5 s (sox `rec`, capped by `PAIR_ASK_MAX`), transcribes
  with whisper.cpp (`large-v3-turbo`, under a second for a 10 s answer on
  Apple Silicon) and returns the transcript. Silence and whisper's stock
  hallucinations ("Obrigado.") come back as `(no answer)`.
- Push-to-talk and interrupt: `<leader>pt` in Neovim (or ctrl+alt+cmd+space
  with Hammerspoon) records and types the transcript into the Claude pane;
  `<leader>pi` (ctrl+alt+cmd+esc) first silences Claude and sends Escape.
  Both call `pair hook listen` / `pair hook interrupt`.
- `pair doctor --fix` downloads the whisper model; `pair doctor --mic`
  records one second to trigger the macOS microphone permission and
  reports the ambient noise level.

Still to come: hand-edit awareness and scratchpad keymaps (milestone 4),
replay and Homebrew packaging (milestone 5).

## Install (development)

```sh
brew install tmux neovim sox whisper.cpp git-delta   # git-delta is optional
brew install --cask hammerspoon                      # optional, global hotkeys
go install github.com/fabioaraujo121/pair/cmd/pair@latest
```

## Use

```sh
cd your-repo
pair init      # hooks, permissions, CLAUDE.md block, MCP registration, PAIRING.md, .gitignore
pair doctor --fix   # checks everything and downloads the whisper model (1.6 GB)
pair doctor --mic   # one-second recording: grants microphone access, measures noise
pair                # start (or attach to) the session
pair stop
```

Project-specific words that whisper should know (product names, domain
terms) go one per line in `.pair/vocab.txt`.

`pair start` installs the bundled `pair.nvim` plugin into `~/.pair/nvim` and
loads it with `--cmd 'set rtp^=…'`, so no Neovim configuration is required.
To use it from your own config, add `{ dir = "~/.pair/nvim", name = "pair.nvim" }`
to lazy.nvim.

## Configuration

| Variable | Default | Meaning |
| --- | --- | --- |
| `PAIR_SESSION` | `pair` | tmux session name |
| `PAIR_SOCK` | `$TMPDIR/pair-<repo hash>.sock` | Neovim listen address (≤ 104 bytes, or `127.0.0.1:<port>`) |
| `PAIR_HIGHLIGHT_TTL` | `20` | seconds before a highlight clears |
| `PAIR_VOICE` | `Luciana` | `say` voice (`say -v '?'` lists them; "Luciana (Enhanced)" sounds better) |
| `PAIR_RATE` | `190` | speech rate in words per minute |
| `PAIR_LANG` | `pt` | language of the CLAUDE.md block and (milestone 3) of whisper |
| `PAIR_WHISPER_MODEL` | `~/.pair/models/ggml-large-v3-turbo.bin` | whisper.cpp model |
| `PAIR_ASK_MAX` | `60` | seconds of recording before `ask` gives up |
| `PAIR_SILENCE_SECS` | `1.5` | pause that ends a recording |
| `PAIR_SILENCE_LEVEL` | `1%` | amplitude below which sox counts silence |
| `PAIR_MUTE` | | log narrations without speaking or listening (tests, demos) |
| `PAIR_CLAUDE_ARGS` | | extra arguments appended to the `claude` command |
| `PAIR_NVIM_TRANSPORT` | | `rpc` or `cli` to pin the Neovim transport |
| `PAIR_DEBUG` | | print hook errors to stderr |

## Development

```sh
go test ./...     # includes headless Neovim integration tests when nvim is on PATH
go build ./cmd/pair
```

License: MIT.
