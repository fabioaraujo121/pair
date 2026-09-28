# pair

Voice-driven pairing for Claude Code in the terminal.

Claude explains what it is about to do out loud, points at code in Neovim,
asks questions and listens for the answer, while you watch edits land live
and can interrupt at any time. Everything runs locally: macOS `say` for
speech, whisper.cpp for listening, tmux for the layout, Claude Code hooks
and MCP tools for the events. No cloud, no daemon, no fork of Claude Code.

```
┌───────────────────────────────────────────┬──────────────────────────────┐
│ files: what Claude reads and changes      │ activity: what and why       │
│  ▸ app/        │ 11 class User            │ 10:14 › add email validation │
│  ▾ models/     │▶12   validates :email    │ 10:14 🗣 I'll add the         │
│     user.rb    │▶13     presence: true    │       validation in the      │
│  ▸ spec/       │ 14 end                   │       model, the controller  │
│                ├──────────────────────────│       accepts empty emails.  │
│                │ PAIRING.md               │ 10:14 → user.rb:12-13        │
├────────────────┴──────────────────────────┤ 10:14 ✎ user.rb (+2 −0)      │
│ claude: type here or press F5 to talk     │ 10:15 ❓ Sidekiq or Solid     │
│ ❯                                         │       Queue?                 │
│                                           │ 10:15 👤 Solid Queue.        │
└───────────────────────────────────────────┴──────────────────────────────┘
 pair  ● Claude is working: editing user.rb        F5 talk · F6 interrupt · F7 Claude full screen
```

The screen answers three questions: is Claude running (the status bar),
which files is it changing (Neovim, the main area), and why (the activity
log). Claude Code's own pane is a strip under the editor: it is where you
type and answer permission prompts, not what you watch.

Status: v0, milestones 1 to 5 of the [PRD](PRD.md) implemented. Everything
below was verified against Claude Code 2.1.282 on macOS 26 (see
[VALIDATION.md](VALIDATION.md)).

## Install

Requirements: macOS 14+ (Apple Silicon recommended), [Claude
Code](https://docs.anthropic.com/en/docs/claude-code), Neovim 0.10+, tmux,
sox, whisper.cpp.

With Homebrew, once the tap is published:

```sh
brew install fabioaraujo121/tap/pair      # pulls tmux, neovim, sox, whisper.cpp
```

With Go:

```sh
brew install tmux neovim sox whisper.cpp git-delta   # git-delta is optional
go install github.com/fabioaraujo121/pair/cmd/pair@latest
```

Then, once per machine:

```sh
pair doctor --fix   # checks every dependency and downloads the whisper model (1.6 GB)
pair doctor --mic   # records one second so macOS asks for microphone access
```

Voices: the default is `Samantha` (English). Better-sounding "Enhanced" and
"Premium" voices are free downloads under System Settings → Accessibility →
Spoken Content → Manage Voices; select one with `pair init --voice "Ava
(Premium)"`. For Portuguese: `pair init --lang pt --voice Luciana`.

## First session

```sh
cd your-repo
pair init   # hooks + permissions in .claude/settings.json, CLAUDE.md block,
            # MCP server registration, PAIRING.md, .gitignore entries
pair        # opens the tmux session and attaches
```

`pair init` is idempotent and keeps whatever else is in your settings. The
first `pair` in a repository shows Claude Code's trust dialog; accept it
(its default is "No, exit").

In the session:

1. Press `F5` and say the task (or type it in the Claude pane). Claude
   writes the plan and open questions to `PAIRING.md` before touching code.
2. Before each change Claude calls `narrate`. An attention sound plays
   first when it has been quiet for a while, then one or two sentences are
   spoken. The same text lands in the activity log.
3. Before every `Edit`/`Write`, Neovim jumps to the spot and highlights it;
   after it, the buffer reloads and the log shows `✎ file (+added −removed)`
   with the first lines of the diff.
4. When something is ambiguous Claude calls `ask`: a distinct sound, the
   question, a chime, and the mic records until you pause for 1.5 s. The
   transcript goes back to Claude and the decision lands in `PAIRING.md`.
5. `F6` interrupts at any time: speech stops, Claude's turn is cancelled,
   and the mic opens for you.
6. Edit files yourself whenever you like. Your next prompt carries a diff of
   what you changed by hand.
7. `pair stop` ends the session. `pair replay` plays it back.

The status bar always shows one of: `● Claude is working`, `🗣 Claude is
speaking`, `❓ Claude is asking you`, `🎙 listening`, `○ your turn`, or
`⚠ Claude needs you` when Claude Code is waiting on a permission prompt
(press `F7` to see it).

## Keys

| Where | Key | Does |
| --- | --- | --- |
| Anywhere in the session | `F5` (or `C-b t`) | push-to-talk: record, transcribe, send to Claude |
| Anywhere in the session | `F6` (or `C-b i`) | interrupt Claude, then listen |
| Anywhere in the session | `F7` | Claude's pane full screen, and back |
| Anywhere in the session | mouse click, `C-b` + arrows | switch pane |
| Neovim | `<leader>pt` / `<leader>pi` | same as `F5` / `F6` |
| Neovim (visual) | `<leader>pp` | point Claude at the selected lines, with an optional note |
| Neovim | `<leader>ps` | toggle the `PAIRING.md` split |
| Neovim tree | `Enter` | open the file in the editor window |
| Outside the terminal | ctrl+alt+cmd+space / esc | push-to-talk / interrupt, with Hammerspoon |

The Neovim leader key is `\` unless your own configuration changes it. On
a Mac laptop, `F5` is `fn`+`F5` unless "Use F1, F2, etc. keys as standard
function keys" is on.

Global hotkeys: `brew install --cask hammerspoon`, then add
`dofile(os.getenv("HOME") .. "/.pair/hammerspoon.lua")` to
`~/.hammerspoon/init.lua` (the file is written by `pair init`).

## Commands

| Command | Does |
| --- | --- |
| `pair` / `pair start` | create the session (editor, activity log, Claude strip) or attach |
| `pair init [--lang en] [--voice Samantha] [--rate 190]` | prepare the repository (see above) and save your voice settings |
| `pair doctor [--fix] [--mic]` | check dependencies, voice, socket path, model, mic, MCP registration |
| `pair stop` | kill the session and any running `say`/`rec` |
| `pair replay [file] [--speed N] [--mute] [--no-nvim]` | play `.pair/events.jsonl` back: narrations spoken, jumps replayed |
| `pair mcp` | the MCP server (run by Claude Code) |
| `pair hook <name>` | hook handlers (run by Claude Code, keymaps and hotkeys) |

## How it works

- **tmux** holds three panes on pair's own server (`tmux -L pair`), so its
  key bindings, mouse mode and status line never touch your other tmux
  sessions. Claude Code runs with `CLAUDE_CODE_MCP_AUTO_BACKGROUND_MS=0`, so
  `narrate` and `ask` stay blocking instead of being moved to a background
  task after two minutes.
- **MCP tools** `narrate`, `show`, `ask` live in `pair mcp`, a stdio server
  registered per project with the absolute binary path. All speech and
  listening go through one mutex. When you press Escape, Claude Code cancels
  the request; the handler's context is cancelled within ~100 ms and `pair`
  terminates `say` or `rec` itself.
- **Hooks** in `.claude/settings.json`: `PreToolUse` (`Edit|Write`) jumps
  and highlights, `PostToolUse` reloads and logs the diff through `delta`,
  `UserPromptSubmit` injects your hand edits, `Stop` hands you the turn,
  `Notification` raises an alert when Claude waits on a permission prompt.
  Each one also updates the status bar.
- **Neovim** is driven over msgpack-rpc (`ExecLua` into `pair.nvim`), with
  `--remote-expr` as fallback. The plugin handles `FileChangedShell` so a
  reload never opens a modal dialog, even with unsaved edits.
- **Hand edits** are detected by snapshotting each file as Claude leaves it
  and diffing against that snapshot at your next prompt, so only your hunks
  are reported, once.
- **Listening** decides "(no answer)" from the audio (too short, too quiet,
  no file) before whisper runs, and drops whisper's stock inventions for
  silence such as "Obrigado.".
- **Events** go to `.pair/events.jsonl` (`{ts, type, payload}`), the log
  pane tails `.pair/session.log`.

## Configuration

Settings come from, in order: the environment, `<repo>/.pair/config`,
`~/.pair/config`, the default. The files hold `KEY=VALUE` lines with the
names below. `pair init --lang pt --voice Luciana` writes `~/.pair/config`
for you and updates the language line in `CLAUDE.md`.

| Variable | Default | Meaning |
| --- | --- | --- |
| `PAIR_LANG` | `en` | spoken language: the `CLAUDE.md` block and whisper |
| `PAIR_VOICE` | `Samantha` | `say` voice (`say -v '?'` lists them) |
| `PAIR_RATE` | `190` | speech rate in words per minute |
| `PAIR_CUE` | `on` | attention sound before speech; `off` disables it |
| `PAIR_CUE_IDLE` | `6` | seconds of quiet after which a narration is announced |
| `PAIR_WHISPER_MODEL` | `~/.pair/models/ggml-large-v3-turbo.bin` | whisper.cpp model |
| `PAIR_ASK_MAX` | `60` | seconds of recording before `ask` gives up |
| `PAIR_SILENCE_SECS` | `1.5` | pause that ends a recording |
| `PAIR_SILENCE_LEVEL` | `1%` | amplitude below which sox counts silence |
| `PAIR_SESSION` | `pair` | tmux session name |
| `PAIR_TMUX_SOCKET` | `pair` | name of pair's tmux server |
| `PAIR_LOG_WIDTH` | `36%` | width of the activity log |
| `PAIR_CLAUDE_HEIGHT` | `30%` | height of Claude's strip |
| `PAIR_LOG_DIFF_LINES` | `30` | diff lines shown per edit in the log (`0` = summary only) |
| `PAIR_SOCK` | `$TMPDIR/pair-<repo hash>.sock` | Neovim listen address (≤ 104 bytes, or `127.0.0.1:<port>`) |
| `PAIR_HIGHLIGHT_TTL` | `20` | seconds before a highlight clears |
| `PAIR_DIFF_LIMIT` | `200` | max diff lines injected at a prompt |
| `PAIR_TTS` / `PAIR_STT` | `say` / `whisper` | adapters (see below) |
| `PAIR_MUTE` | | log without speaking or listening (tests, demos) |
| `PAIR_CLAUDE_ARGS` | | extra arguments appended to the `claude` command |
| `PAIR_TRACK_SCRATCHPAD` | | set to keep `PAIRING.md` out of `.gitignore` |
| `PAIR_DEBUG` | | print hook errors to stderr |

Words whisper should know for this project (product names, domain terms):
one per line in `.pair/vocab.txt`.

The pairing-mode block that `pair init` writes to `CLAUDE.md` sits between
`<!-- pair:start -->` and `<!-- pair:end -->`; edit it freely, `pair init`
only replaces what is between the markers.

## Adding a TTS or STT adapter

`internal/speech` defines two interfaces:

```go
type Speaker interface {
    Speak(ctx context.Context, text string) error // blocks; ctx cancel = interrupt
}

type Listener interface {
    Listen(ctx context.Context) (Heard, error) // records until silence, returns the transcript
}
```

Implement one, add a case to `NewSpeaker` / `NewListener` in
`internal/speech/factory.go`, and select it with `PAIR_TTS` / `PAIR_STT`.
A `Speaker` must stop speaking when its context is cancelled; a `Listener`
should return an empty `Heard.Text` with a `Dropped` reason rather than
guessing.

## Troubleshooting

- **No sound, tool returns an error mentioning `say`**: run `say -v Samantha
  hello` in the same terminal; if the voice is missing, pick another with
  `pair init --voice <name>`.
- **`F5` does nothing**: on a Mac laptop press `fn`+`F5`, or use `C-b t`.
- **I attached with plain `tmux attach` and see nothing**: pair runs on its
  own server; use `pair` or `tmux -L pair attach -t pair`.
- **`ask` always returns "(no answer)"**: run `pair doctor --mic`. If no
  audio is captured, grant your terminal app microphone access under System
  Settings → Privacy & Security → Microphone. If the ambient level is above
  the silence threshold, raise `PAIR_SILENCE_LEVEL`.
- **Neovim does not follow the edits**: `pair doctor` while the session runs
  reports "neovim rpc"; a socket path over 104 bytes is refused by Neovim,
  set `PAIR_SOCK` to a short path or `127.0.0.1:<port>`.
- **Narration stops after two minutes of a long `ask`**: your shell sets
  `CLAUDE_CODE_MCP_AUTO_BACKGROUND_MS`; `pair start` overrides it to `0`
  inside the session.
- **`claude mcp` shows `pair` failed to connect**: the registration holds an
  absolute path to the binary; re-run `pair init` after moving or
  reinstalling it.

## Development

```sh
go test ./...        # includes headless Neovim and stdio MCP integration tests
go build ./cmd/pair
goreleaser check     # validates .goreleaser.yaml
```

The real `say` → whisper-cli round-trip test runs when the model is
installed; set `PAIR_SKIP_REAL_AUDIO=1` to skip it.

## Releasing

Releases are built by goreleaser on a tag push (`.github/workflows/release.yml`):
darwin arm64 and amd64 archives, a GitHub release, and a cask pushed to
`fabioaraujo121/homebrew-tap`. One-time setup: create the `homebrew-tap`
repository and add a `HOMEBREW_TAP_GITHUB_TOKEN` secret (a token with `repo`
scope) to this repository. Then:

```sh
git tag v0.1.0 && git push --tags
```

The binary is not notarized; the cask strips the quarantine attribute after
install.

## License

MIT.
