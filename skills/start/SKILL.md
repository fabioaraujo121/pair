---
name: start
description: Set up pair and start a voice pairing session with Claude Code. Use when the user wants to talk to Claude by voice, hear it narrate and point at code in Neovim, or asks about pair, pairwithagent.com, or how to run pair setup.
allowed-tools: "Bash(command -v pair), Bash(pair version), Bash(pair doctor), Bash(brew --version)"
---

# Start a pair session

pair is a macOS command that runs Claude Code inside a tmux session with
Neovim as the stage. Claude says what it is about to do before each edit,
points at the line in Neovim, draws the change inside the real file, asks
when a decision is open and waits for the spoken answer. Listening and
speaking run on the Mac (whisper.cpp, macOS `say` or Kokoro).

A pair session is started from the user's own terminal, never from inside
Claude Code: `pair` launches Claude Code itself, with its hooks and tools
for that process only. Your job here is to get the user to that command.

## Steps

1. Check the install: `command -v pair && pair version`.
   - Missing: show the install, and run it only if the user asks you to:
     ```sh
     brew tap fabioaraujo121/tap
     brew trust fabioaraujo121/tap     # Homebrew asks this once of any third-party tap
     brew install --cask pair          # pulls tmux, neovim, sox, whisper.cpp
     ```
     Without Homebrew: `go install github.com/fabioaraujo121/pair/cmd/pair@latest`
     plus `brew install tmux neovim sox whisper.cpp`.
2. Run `pair doctor` and read it to the user in one or two sentences:
   what is missing, what is fine. Do not run `pair setup` or
   `pair doctor --mic` yourself: they record from the microphone and macOS
   asks the user for access, so they belong in the user's terminal.
3. Tell the user, in this order:
   - once per machine: `pair setup` (checks everything, downloads the speech
     model, tests the microphone and the voice, reads one sentence back);
   - then, in a repository: end this Claude Code session and run `pair`
     there. The first time, Claude Code's trust dialog appears; accept it.
   - in the session: `F5` to talk (`fn`+`F5` on a laptop), `F6` to
     interrupt, `F7` to show or hide Claude Code's pane, `F8` twice to
     quit. The microphone is closed until a key is pressed.
4. If the user already runs a pair session (the environment has
   `PAIR_ACTIVE` set), the pairing instructions were given to you at
   session start: follow them and do not repeat this skill.

Keep the answer short. Point to https://pairwithagent.com for the rest.
