# pair

Voice-driven pairing for Claude Code in the terminal. Claude explains what it
is about to do out loud, points at code in Neovim, asks questions and listens
for the answer, while you watch edits land live and can interrupt at any time.

Status: milestone 2 of 5 (voice out). See `PRD.md` for the design and
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

`ask` currently speaks the question and tells Claude that voice input is not
enabled yet. Listening (whisper.cpp), push-to-talk and the interrupt hotkey
arrive in milestone 3.

## Install (development)

```sh
brew install tmux neovim sox whisper.cpp git-delta   # git-delta is optional
go install github.com/fabioaraujo121/pair/cmd/pair@latest
```

## Use

```sh
cd your-repo
pair init      # hooks, permissions, CLAUDE.md block, MCP registration, PAIRING.md, .gitignore
pair doctor    # checks tmux, nvim, claude, say voice, socket path, whisper model
pair           # start (or attach to) the session
pair stop
```

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
| `PAIR_MUTE` | | log narrations without speaking (useful in tests and demos) |
| `PAIR_CLAUDE_ARGS` | | extra arguments appended to the `claude` command |
| `PAIR_NVIM_TRANSPORT` | | `rpc` or `cli` to pin the Neovim transport |
| `PAIR_DEBUG` | | print hook errors to stderr |

## Development

```sh
go test ./...     # includes headless Neovim integration tests when nvim is on PATH
go build ./cmd/pair
```

License: MIT.
