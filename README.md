# pair

Voice-driven pairing for Claude Code in the terminal. Claude explains what it
is about to do out loud, points at code in Neovim, asks questions and listens
for the answer, while you watch edits land live and can interrupt at any time.

Status: milestone 1 of 5 (skeleton). See `PRD.md` for the design and
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
- Every event is appended to `.pair/events.jsonl`.

Voice (narrate, ask, push-to-talk, interrupt) arrives in milestones 2 and 3.

## Install (development)

```sh
brew install tmux neovim sox whisper.cpp git-delta   # git-delta is optional
go install github.com/fabioaraujo121/pair/cmd/pair@latest
```

## Use

```sh
cd your-repo
pair init      # hooks + permissions in .claude/settings.json, PAIRING.md, .gitignore
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
| `PAIR_CLAUDE_ARGS` | | extra arguments appended to the `claude` command |
| `PAIR_NVIM_TRANSPORT` | | `rpc` or `cli` to pin the Neovim transport |
| `PAIR_DEBUG` | | print hook errors to stderr |

## Development

```sh
go test ./...     # includes headless Neovim integration tests when nvim is on PATH
go build ./cmd/pair
```

License: MIT.
