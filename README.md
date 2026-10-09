# pair

Website: https://pairwithagent.com (built from `site/`, see `site/BRIEF.md`; served by Coolify from the gh-pages branch of pair-releases).

Voice-driven pairing with a coding agent in the terminal. Claude Code by
default; Codex CLI with `pair init --agent codex`.

The agent explains what it is about to do out loud, points at code in
Neovim, asks questions and listens for the answer, while you watch edits
land live and can interrupt at any time. Everything runs locally: macOS
`say` for speech, whisper.cpp for listening, tmux for the layout, the
agent's hooks and MCP tools for the events. No cloud, no daemon, no fork of
the agent.

```
┌─ files: what Claude reads and changes ───────┬─ claude ────────────────────┐
│  ▸ app/        │  8                          │  ⠹ Claude is working  0:42  │
│  ▾ models/     │  9   def valid?             │  ▂▄▆█▆▄▂▁▁▂▄▆█▆▄▂▁▁▂▄▆█▆▄▂ │
│     user.rb    │    -     !name.empty?       │  editing app/models/user.rb │
│  ▸ spec/       │+ 10     return false if …   ├─ activity: what and why ────┤
│  README.md     │+ 11                         │ you  10:14                  │
│  PAIRING.md    │+ 12     email.include?("@") │   add email validation      │
│                │  13   end                   │                             │
│                │  14 end                     │ claude  10:14               │
│                │                             │   I'll add the validation   │
│                │                             │   in the model, because …   │
│                │                             │   ✎ app/models/user.rb +3 −1│
└────────────────┴─────────────────────────────┴─────────────────────────────┘
 pair ● Claude is working: editing user.rb   🎙 talk F5  ✋ interrupt F6  ⌨ claude F7
```

The screen answers three questions: is Claude running, which files is it
changing, and why.

- **Is it running?** The panel at the top right is animated, with its own
  motion and colour per state, and a timer. The borders of every pane take
  the same colour, and the status bar says it in words.

  | State | Motion | Colour |
  | --- | --- | --- |
  | Claude is working | a wave travelling to the right | yellow |
  | Claude is speaking | bars jumping like a voice | cyan |
  | Claude is asking you | all bars breathing together | magenta |
  | listening | bars growing from the centre | red |
  | Claude needs you | the whole row blinking | red |
  | your turn | a calm line with one slow dot | green |

- **Which files?** Neovim is the main area: the project tree and one file,
  the one Claude is in, with the change drawn in place. `PAIRING.md` takes
  that place when Claude writes to it, or when you ask for it.
- **Why?** The activity column is the conversation: what you said, what
  Claude said, and a card per edit.

**Claude Code itself is hidden.** It runs in a background window. Press
`F7` or click `⌨ claude` to bring its pane under the editor, and again to
send it away. When Claude waits on a permission prompt the pane opens by
itself and closes when work resumes; a pane you opened yourself stays until
you close it.

Status: v0, milestones 1 to 5 of the [PRD](PRD.md) implemented. Everything
below was verified against Claude Code 2.1.282 on macOS 26 (see
[VALIDATION.md](VALIDATION.md)).

## Install

Requirements: macOS 14+ (Apple Silicon recommended), [Claude
Code](https://docs.anthropic.com/en/docs/claude-code), Neovim 0.10+, tmux,
sox, whisper.cpp.

With Homebrew:

```sh
brew tap fabioaraujo121/tap
brew trust fabioaraujo121/tap             # Homebrew asks this once of any third-party tap
brew install --cask pair                  # pulls tmux, neovim, sox, whisper.cpp
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

## A more natural voice

The system voice that ships with macOS sounds synthetic. Two ways to a
more natural one, both free and both running on your Mac:

**Kokoro**, a neural voice engine (recommended):

```sh
brew install uv            # provides the Python it needs
pair voice setup kokoro    # installs it in ~/.pair/kokoro, about 500 MB, and switches to it
pair voice test            # hear it
pair voice list            # 54 voices: American and British English, Brazilian Portuguese, …
pair voice use kokoro am_michael
pair voice use say         # back to the system voice
```

Everything pair downloads is pinned: the whisper and Kokoro models come
from fixed revisions and are kept only when their size and SHA-256 match
the values in `internal/fetch/pins.go`, and the Python packages of Kokoro
are installed from a lock file with the hashes of their wheels
(`templates/kokoro-requirements.txt`). A model name pair has no pin for is
not downloaded.

A narration sentence takes about a second to synthesize on Apple Silicon,
and that second overlaps with the attention sound, so speech starts about
as fast as before. Sentences are played one by one: the first is heard
while the second is being synthesized. Interrupting works the same. If the
engine cannot start, the system voice speaks instead and the activity
column says why; a narration is never lost.

A Portuguese Kokoro voice pronounces English technical terms with a
Portuguese accent. For mixed sentences an English voice, or the system
voice, is clearer.

**Apple's Premium voices**: download one under System Settings →
Accessibility → Spoken Content → Manage Voices, then
`pair voice use say "Ava (Premium)"`.

Voices: the default is `Samantha` (English). Better-sounding "Enhanced" and
"Premium" voices are free downloads under System Settings → Accessibility →
Spoken Content → Manage Voices; select one with `pair init --voice "Ava
(Premium)"`. For Portuguese: `pair init --lang pt --voice Luciana`.

## First session

```sh
cd your-repo
pair        # opens the tmux session and attaches
```

Nothing is written into the repository. The agent gets pair's hooks, the
MCP server and the pairing instructions when `pair` starts it, for that
process only: a Claude Code or Codex you open without pair, in the same
repository, is exactly as it would be anywhere else. What pair keeps for a
session lives in `.pair/`, which ignores itself in git. `pair init` only
saves your settings and installs the Neovim plugin under `~/.pair`. The
first `pair` in a repository shows Claude Code's trust dialog; accept it
(its default is "No, exit").

Earlier versions wrote hooks, a `CLAUDE.md` block and an MCP registration
into the repository. `pair uninstall` takes them out; `pair` refuses to
start while they are there, since the hooks would run twice.

In the session:

1. Press `F5` (or click `🎙 talk` in the status bar) and say the task, or
   press `F7` and type it in Claude's pane. A short "Hm, let me see" in the
   agent's voice says the words arrived; a few seconds later the agent's
   own first sentence says what it understood and what it will do first. Claude writes the plan and open questions to
   `.pair/PAIRING.md` before touching code.
2. Before each change Claude calls `narrate`. An attention sound plays
   first when it has been quiet for a while, then one or two sentences are
   spoken. The same text lands in the activity log.
3. Before every `Edit`/`Write`, Neovim jumps to the spot and highlights what
   is about to change. After it, the change is drawn inside the file:
   added lines are highlighted with a `+` in the margin, removed lines are
   shown in place, prefixed with `-`. The activity column gets a card:
   `✎ file +added −removed`.
4. When something is ambiguous Claude calls `ask`: a distinct sound, then
   the question. The microphone stays closed. Press `F5` or click `🎙 talk`
   to answer; the transcript goes back to Claude as the answer and the
   decision lands in `.pair/PAIRING.md`.
5. `F6` interrupts at any time: speech stops, Claude's turn is cancelled,
   and the mic opens for you.
6. Edit files yourself whenever you like. Your next prompt carries a diff of
   what you changed by hand.
7. `pair stop` ends the session. `pair --continue` picks it up again later,
   `pair replay` plays it back.

**The microphone is closed by default.** Nothing is recorded until you
press `F5`/`F6` or click a button. While it is open the `talk` button reads
`■ stop`, in red, whatever Claude is doing meanwhile. The recording ends
when you pause for 1.5 s, or at once when you press `F5` again or click
`stop`; either way what you said is sent. Holding the key down does not
count as pressing it again.

The status bar always shows one of: `● Claude is working`, `🗣 Claude is
speaking`, `❓ Claude is asking you`, `🎙 listening`, `○ your turn`, or
`⚠ Claude needs you` when Claude Code is waiting on a permission prompt
(press `F7` to see it).

## Sessions

Each `pair` starts a new session with a clean activity column and a new
Claude Code conversation. Earlier sessions are kept, and can be listed,
named, read, replayed, and opened again with Claude's conversation intact.

```sh
pair                          # a new session
pair --name "checkout flow"   # a new session with a name
pair --continue               # continue the last session
pair sessions                 # list them
pair resume checkout          # open one again, by id or part of its name
pair rename checkout "checkout flow v2"
pair log checkout             # read one as a conversation
pair replay checkout          # play one back
```

```
  ID               NAME                       STARTED        LENGTH  PROMPTS  EDITS
▶ 20260928-142210  checkout flow              today 14:22       38m        6     12
  20260928-103424  investigate landing page   today 10:34     1h12m        9      4
```

- A session without a name is listed by its first prompt.
- Continuing or opening a session resumes the exact Claude Code
  conversation it had, so what you see in the activity column is what
  Claude remembers. The session's name is also given to Claude Code.
- `.pair/PAIRING.md`, the scratchpad, and your code are shared by all sessions of the repository.
- The running session writes to `.pair/events.jsonl`; the others live in
  `.pair/sessions/<id>/`. Deleting a folder there deletes that session.

Every repository has its own session, so several can be open at once, each
in its own terminal. A command acts on the session of the repository it is
run in and on no other: `pair stop` in one repository never ends the
session of another, and interrupting silences only its own voice.

Claude Code started any other way in the same repository (a plain `claude`
in another terminal) is left alone: the hooks and the voice tools act only
inside a pair session, and that conversation writes nothing to it.

## Keys

| Where | Key | Does |
| --- | --- | --- |
| Anywhere in the session | `F5`, `C-b t`, or click `🎙 talk` | open the mic: record, transcribe, send to Claude (or answer its question) |
| While the mic is open | `F5` again, or click `■ stop` | end the recording now and send what you said |
| Anywhere in the session | `F6`, `C-b i`, or click `✋ interrupt` | interrupt Claude, then open the mic |
| Anywhere in the session | `F7`, `C-b c`, or click `⌨ claude` | show Claude Code's pane under the editor, or hide it |
| Anywhere in the session | mouse click, `C-b` + arrows | switch pane |
| Neovim | `<leader>pt` / `<leader>pi` | same as `F5` / `F6` |
| Neovim (visual) | `<leader>pp` | point Claude at the selected lines, with an optional note |
| Neovim | `<leader>ps` | show the scratchpad `.pair/PAIRING.md` in the editor, or go back to the file |
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
| `pair` / `pair start` | start a new session, or go back to the one that is running |
| `pair --continue`, `pair --name <name>` | continue the last session; name the new one |
| `pair --agent codex` | this session with another agent (see [Agents](#agents)) |
| `pair sessions` | list the sessions of this repository |
| `pair resume <id or name>` | open a past session again, with Claude's conversation |
| `pair rename [<id or name>] <new name>` | name a session |
| `pair init [--agent claude\|codex] [--lang en] [--voice Samantha] [--rate 190]` | save your agent and voice settings, install the Neovim plugin; nothing goes into the repository |
| `pair uninstall` | remove what earlier versions wrote into the repository |
| `pair voice [setup kokoro \| use \| list \| test]` | which voice speaks; install and choose a natural one |
| `pair doctor [--fix] [--mic]` | check dependencies, voice, socket path, model, mic, MCP registration |
| `pair agent` | show or hide the agent's pane (what `F7` runs); `pair claude` still works |
| `pair stop` | kill the session and any running `say`/`rec` |
| `pair replay [<id or name>] [--speed N] [--mute] [--no-nvim]` | play a session back: narrations spoken, jumps replayed |
| `pair log [--follow] [<id or name>]` | a session as a conversation; this is what the activity column runs |
| `pair presence` | the animated state indicator; this is what the top right panel runs |
| `pair mcp` | the MCP server (run by the agent) |
| `pair hook <name>` | hook handlers (run by the agent, keymaps and hotkeys) |

## How it works

- **tmux** holds the panes on pair's own server (`tmux -L pair`), so its
  key bindings, mouse mode and status line never touch your other tmux
  sessions. Claude Code runs with `CLAUDE_CODE_MCP_AUTO_BACKGROUND_MS=0`, so
  `narrate` and `ask` stay blocking instead of being moved to a background
  task after two minutes.
- **MCP tools** `narrate`, `show`, `ask` live in `pair mcp`, a stdio server
  the agent is told about at launch (`--mcp-config` for Claude Code, a
  profile and `-c` override for Codex), never registered. All speech and
  listening go through one mutex. When you press Escape, Claude Code cancels
  the request; the handler's context is cancelled within ~100 ms and `pair`
  terminates `say` or `rec` itself.
- **Hooks**, given to the agent at launch (`--settings` for Claude Code, the
  profile for Codex) and kept under `~/.pair/run/<session>/`: `SessionStart`
  adds the pairing instructions as context, `PreToolUse` (`Edit|Write`)
  jumps and highlights, `PostToolUse` reloads and draws the change,
  `UserPromptSubmit` injects your hand edits, `Stop` hands you the turn,
  `Notification` raises an alert when Claude waits on a permission prompt.
  Each one also updates the status bar.
- **Neovim** is driven over msgpack-rpc (`ExecLua` into `pair.nvim`), with
  `--remote-expr` as fallback. Changes are drawn from Claude Code's own
  patch data (`tool_response.structuredPatch`), so what you see is exactly
  the edit that was made. The plugin handles `FileChangedShell` so a
  reload never opens a modal dialog, even with unsaved edits.
- **While you type, nothing moves.** If you are in insert or visual mode,
  or answering a prompt, Neovim keeps the file and the cursor you have.
  What Claude does in the file on screen is still marked; what it does in
  another file is in the activity column, and on screen at its next step
  once you are back in normal mode.
- **Hand edits** are detected by snapshotting each file as Claude leaves it
  and diffing against that snapshot at your next prompt, so only your hunks
  are reported, once. Only files of the repository are looked at. A file
  that looks like a secret (`.env`, `*.pem`, `*.key`, `master.key`,
  `credentials`…) or that git ignores is reported by name: its content is
  neither copied nor sent to Claude.
- **What pair keeps is private.** `.pair/` holds your prompts, the
  transcripts and your last recording. It is readable by you only, and it
  ignores itself in git even in a repository where `pair init` never ran.
- **The microphone** is guarded by one lock across all `pair` processes, so
  a key that auto-repeats cannot start several recordings. When Claude is
  waiting on a question, your push-to-talk delivers the transcript to that
  question instead of typing it as a new prompt.
- **Listening** decides "(no answer)" from the audio (too short, too quiet,
  no file) before whisper runs, and drops whisper's stock inventions for
  silence such as "Obrigado.".
- **Events** go to `.pair/events.jsonl` (`{ts, type, payload}`). The
  activity column renders that file as a conversation (`pair log
  --follow`): grouped by speaker, wrapped at word boundaries for the width
  of the pane, with what you said shown once. A plain-text copy goes to
  `.pair/session.log`.
- **Neovim without a config of your own** gets line numbers, mouse support,
  a colourful built-in theme and syntax highlighting. With your own config,
  pair changes none of that.

## Configuration

Settings come from, in order: the environment, `<repo>/.pair/config`,
`~/.pair/config`, the default. The files hold `KEY=VALUE` lines with the
names below. `pair init --lang pt --voice Luciana` writes `~/.pair/config`
for you; the language reaches the agent in the instructions of the next
session.

A repository is something you clone, so its file is not trusted with
everything. `<repo>/.pair/config` can set how the session sounds and looks:
`PAIR_LANG`, `PAIR_VOICE`, `PAIR_RATE`, `PAIR_KOKORO_VOICE`,
`PAIR_KOKORO_SPEED`, `PAIR_CUE`, `PAIR_CUE_IDLE`, `PAIR_ASK_MAX`,
`PAIR_ASK_WAIT`, `PAIR_SILENCE_SECS`, `PAIR_SILENCE_LEVEL`,
`PAIR_HIGHLIGHT_TTL`, `PAIR_DIFF_LIMIT`, `PAIR_LOG_WIDTH`, `PAIR_CLAUDE`,
`PAIR_CLAUDE_HEIGHT`, `PAIR_PRESENCE_HEIGHT`. Everything else (what is run,
which files and sockets are used, whether the microphone opens by itself)
is read from the environment and from `~/.pair/config` only.

| Variable | Default | Meaning |
| --- | --- | --- |
| `PAIR_AGENT` | `claude` | the coding agent: `claude` or `codex` (environment and `~/.pair/config` only) |
| `PAIR_LANG` | `en` | spoken language: the instructions block and whisper |
| `PAIR_VOICE` | `Samantha` | `say` voice (`say -v '?'` lists them) |
| `PAIR_RATE` | `190` | speech rate in words per minute |
| `PAIR_CUE` | `on` | attention sound before speech; `off` disables it |
| `PAIR_CUE_IDLE` | `6` | seconds of quiet after which a narration is announced |
| `PAIR_ACK` | `on` | a short spoken acknowledgement ("Hm, let me see.", "Okay.") right after you talk, in the agent's voice; `off` replaces it with a soft tick |
| `PAIR_WHISPER_MODEL` | `~/.pair/models/ggml-large-v3-turbo.bin` | whisper.cpp model |
| `PAIR_ASK_MIC` | `manual` | `manual`: you open the mic to answer a question; `auto`: it opens by itself after the question |
| `PAIR_ASK_WAIT` | `180` | seconds a question waits for you to open the mic |
| `PAIR_ASK_MAX` | `60` | longest single recording, in seconds |
| `PAIR_SILENCE_SECS` | `1.5` | pause that ends a recording |
| `PAIR_SILENCE_LEVEL` | `1%` | amplitude below which sox counts silence |
| `PAIR_SESSION` | `pair-<directory>-<hash>` | tmux session name, one per repository: letters, digits, `-` and `_` |
| `PAIR_TMUX_SOCKET` | `pair` | name of pair's tmux server |
| `PAIR_LOG_WIDTH` | `36%` | width of the activity log |
| `PAIR_CLAUDE` | `hidden` | `shown` starts the session with Claude Code's pane open |
| `PAIR_CLAUDE_HEIGHT` | `45%` | height of Claude Code's pane when it is shown |
| `PAIR_PRESENCE_HEIGHT` | `5` | lines of the animated panel |
| `PAIR_COLORSCHEME` | `retrobox` | Neovim colour scheme, applied only when you have no Neovim config |
| `PAIR_SOCK` | `$TMPDIR/pair-<repo hash>.sock` | Neovim listen address (≤ 104 bytes, or `127.0.0.1:<port>`) |
| `PAIR_HIGHLIGHT_TTL` | `20` | seconds before the "about to change" highlight clears |
| `PAIR_DIFF_TTL` | `120` | seconds a drawn change stays visible in Neovim (`0` = until the next one) |
| `PAIR_DIFF_LIMIT` | `200` | max diff lines injected at a prompt |
| `PAIR_TTS` / `PAIR_STT` | `say` / `whisper` | adapters: `say` or `kokoro`; `whisper` |
| `PAIR_KOKORO_VOICE` | by language | Kokoro voice, for example `af_heart`, `am_michael`, `pf_dora` |
| `PAIR_KOKORO_SPEED` | `1.0` | Kokoro pace; `1.1` is a little faster |
| `PAIR_MUTE` | | log without speaking or listening (tests, demos) |
| `PAIR_CLAUDE_ARGS` | | extra arguments for the agent's command, for example `--model opus` |
| `PAIR_DEBUG` | | print hook errors to stderr |

Words whisper should know for this project (product names, domain terms):
one per line in `.pair/vocab.txt`.

The pairing instructions (`templates/pairing-block.md.tmpl`) reach the
agent through its `SessionStart` hook at every start, resume and
compaction, only inside a pair session. Nothing of them is in `CLAUDE.md`
or `AGENTS.md`.

## Agents

pair drives Claude Code by default. Everything on screen and aloud is the
same whatever the agent; what differs is behind one interface,
`internal/agent.Agent`: how the agent is launched and resumed, how its
hooks, instructions and MCP server are handed to it, what it writes on a
hook's stdin, and how it is interrupted.

| | Claude Code | Codex CLI |
| --- | --- | --- |
| choose it | the default | `pair init --agent codex`, or `PAIR_AGENT=codex`, or `pair --agent codex` for one session |
| hooks | `--settings ~/.pair/run/<session>/claude-settings.json` | `-p pair`: `~/.codex/pair.config.toml`, reviewed once in Codex's `/hooks` screen |
| MCP server | `--mcp-config ~/.pair/run/<session>/claude-mcp.json` | the same profile, pre-approved, with a tool timeout long enough for `ask`; the repository by `-c` |
| instructions | the `SessionStart` hook, as context | the same |
| an edit | file, old and new text, and the patch after | one `apply_patch` call, possibly several files: the first is pointed at, all are drawn |
| continue | `claude --resume <id>` | `codex resume <id>` |

A session remembers its agent, so `pair --continue` goes on with the one
that held the conversation. The setting is yours: a repository's
`.pair/config` cannot choose the agent.

To add one, implement `agent.Agent` in `internal/agent` and list it in
`builtin`. The Codex adapter (`internal/agent/codex.go`) is the example:
its hooks read like Claude Code's, so most of it is the patch grammar.

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
`internal/speech/kokoro.go` is a complete example of an engine that needs
time before the first sound: it also implements `Preparer`, so synthesis
starts while the attention sound plays.
A `Speaker` must stop speaking when its context is cancelled; a `Listener`
should return an empty `Heard.Text` with a `Dropped` reason rather than
guessing.

## Troubleshooting

- **No sound, tool returns an error mentioning `say`**: run `say -v Samantha
  hello` in the same terminal; if the voice is missing, pick another with
  `pair init --voice <name>`.
- **`F5` does nothing**: on a Mac laptop press `fn`+`F5`, or use `C-b t`.
- **I need to see what Claude Code itself shows**: press `F7`. Its pane is
  hidden by default and the session works without it, but it is where
  permission prompts and Claude's typed answers appear.
- **I attached with plain `tmux attach` and see nothing**: pair runs on its
  own server; use `pair` in the repository, or `tmux -L pair ls` and
  `tmux -L pair attach -t <name>`.
- **Claude asked something and nothing happens**: the microphone is closed
  until you open it. Press `F5` or click `🎙 talk`, then answer.
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
- **A Claude Code opened without pair still narrates or asks**: an earlier
  version wrote its instructions into `CLAUDE.md` and its hooks into
  `.claude/settings.json`; run `pair uninstall` in that repository.

## Development

```sh
go test ./...        # includes headless Neovim and stdio MCP integration tests
go build ./cmd/pair
goreleaser check     # validates .goreleaser.yaml
```

The tests run cut off from your machine: a temporary home directory, no
`PAIR_` variables, and a tmux server of their own, so they cannot reach a
pairing session you have open. The tests that load the installed speech
engines (Kokoro, whisper-cli) are slow and run only when asked:

```sh
PAIR_REAL_AUDIO=1 go test ./internal/speech/
```

## Releasing

The source is private; the releases are public. A tag push runs
`.github/workflows/release.yml`: the tests, then goreleaser, which builds
the darwin arm64 and amd64 archives, creates the release in
[`fabioaraujo121/pair-releases`](https://github.com/fabioaraujo121/pair-releases)
and pushes the cask to
[`fabioaraujo121/homebrew-tap`](https://github.com/fabioaraujo121/homebrew-tap).
Both are public; this repository is not, and its commit log is not
published with the release.

One-time setup: a fine-grained personal access token with "Contents: read
and write" on those two repositories only, saved in this repository as the
secret `RELEASE_GITHUB_TOKEN`. Then:

```sh
git tag v0.1.0 && git push origin v0.1.0
```

The binary is not notarized; the cask strips the quarantine attribute after
install, through a `postflight` block that Homebrew 7 reports as deprecated
but still runs (goreleaser 2.18 writes no other form yet). To rehearse a
release without publishing anything:

```sh
RELEASE_GITHUB_TOKEN=x goreleaser release --snapshot --clean --skip=publish
```

## License

MIT.
