# Code review, 2026-09-28

Seven reviewers read the code at commit `b5e5282`, each on one area: hooks
and hand edits, security, concurrency and process lifecycle, the CLI with
tmux and sessions, Neovim, the activity column and status, tests with CI
and docs. Every finding below was checked against the code before it was
kept.

The rule for this round: the way pair works does not change. What was
critical is fixed. The rest is listed for a decision.

## Fixed

| # | What was wrong | What it does now |
| --- | --- | --- |
| 1 | A cloned repository could carry `.pair/config` and choose what pair runs: arguments for `claude`, the session name that ends up in a key binding's command line, the editor's socket. | The repository file sets only how the session sounds and looks. Session names are letters, digits, `-` and `_`. The conversation id and the session name read from `.pair/session.json` are checked and quoted. Only a real socket is removed. |
| 2 | Pid files lived in the repository. Whatever pid a file named got SIGTERM at the next talk, interrupt or stop; a file left by a crash could hit an unrelated process days later. | They live in `~/.pair/run/<session>/`, hold the time the process began, and a signal is sent only when that time still matches. The directory is emptied when a session starts. |
| 3 | Paths in `.pair/state.json` were followed anywhere on disk and their content sent to Claude. | Only files of the repository, links resolved, never `.pair/` itself. Forged entries are dropped. |
| 4 | Secrets reached Claude's context and were copied to `.pair/snap`. Everything pair wrote was readable by every user of the machine. | Files that look like secrets or that git ignores are reported by name only, no copy. `.pair/` is `0700`, its files `0600`, and it ignores itself in git. |
| 5 | Text from transcripts, narrations, commands and file names reached the terminal raw: escape sequences were obeyed, and a command of several lines made the presence panel scroll ten times a second. | Control characters are removed wherever such text is printed: activity column, presence panel, status bar, replay, session list. |
| 6 | tmux read a transcript shown in the status bar as a format (`#{...}`, `#(...)`). | It is shown as text (`display-message -l`). |
| 7 | The test suite, run inside a pairing session, typed into the live Claude pane. A panic in a hook exited 2, which Claude Code reads as "block the tool call". | Tests run with a home directory, an environment and a tmux server of their own. A hook exits 0 whatever happens, checked on the built binary. |
| 8 | Neovim changed the buffer under an engineer in insert mode, so the next keys landed in another file. A long message raised "Press ENTER" and blocked every jump and reload. Recovery files were off. | While you type nothing moves; what happens in the file on screen is still marked. Messages are one line that fits. Recovery files are on, their prompt off. |
| 9 | A marker left by an interrupted narration silenced a later one with the same number, in any session: the tool answered "spoken" and a question could wait three minutes unheard. | Each process has its own directory for sound and clears the marker of a number before using it. |

Fixed on the way, in the same code: file names that are not ASCII were
never reported as hand edits; diff lines such as `-- comment` were dropped;
every changed file was read in full at every prompt whatever its size;
quotes inside a config value were stripped; a path with `'`, a space or a
comma broke the Neovim command line; the status text was cut in the middle
of a character; playing a prepared narration twice crashed the voice;
`<leader>pp` in normal mode sent the previous selection; the warning about
unsaved edits was never shown.

### What you may notice

- A file that git ignores, edited by Claude and then by you, is now
  reported to Claude by name, without the diff.
- Neovim no longer jumps while you are in insert or visual mode.
- Neovim writes swap files again.
- The tests that load Kokoro and whisper run only with `PAIR_REAL_AUDIO=1`.
- A session that was running before the new binary was installed keeps
  the old voice server and the old Neovim plugin until it is restarted.

## Open, for a decision

Recommendation in bold. "Changes behaviour" marks what you would notice.

### Voice

1. **Fix.** Claude can start speaking while your microphone is open, and
   its voice ends up in your transcript. Narration would wait for the
   microphone to close. Changes behaviour.
2. **Fix.** A recording that began before a question arrived is delivered
   as the answer to it.
3. **Fix.** A transcript that starts with `!` or `/` is read by Claude Code
   as a shell command or a slash command.
4. **Fix.** `pair mcp` ignores SIGTERM while a tool call runs, for up to
   `PAIR_ASK_WAIT`. Killed hard, it leaves an orphan voice and a pending
   question that swallows your next sentence.
5. **Fix.** While the Kokoro model loads (up to 45 s) Escape does nothing.
6. **Fix, small.** When the Kokoro worker dies the reason is sometimes
   empty: its error output is read before it is complete.
7. **Decide.** `pair doctor` records from the microphone on every run from
   a terminal, not only with `--mic`. This is at odds with "the microphone
   opens only when I ask". Recording only with `--mic` changes behaviour.

### Activity column, status, replay

8. **Decide.** The rule that shows what you said once can hide a prompt you
   typed (say a sentence, type "yes" nine seconds later) or show a spoken
   one twice (prompt logged long after the recording). A stricter match
   changes what the column shows in those cases.
9. **Fix.** `pair replay` prints and speaks a question after its answer.
10. **Fix.** `pair replay` during a live session moves the live Neovim and
    speaks over the live voice. It would only print while a session runs.
11. **Fix.** `status.json` is read, changed and written without a lock: a
    talk press racing a tool call can leave the bar on "talk" while the
    microphone is open.
12. **Fix, small.** A listener that died with the microphone open leaves a
    wrong elapsed time. A resize clears the activity column's scrollback
    even when the width did not change. A log replaced by a smaller or
    equal one is not noticed by `pair log --follow`.

### Hooks and settings

13. **Fix.** The state directory follows Claude's working directory. After
    `cd sub` in a project without git, events go to `sub/.pair`.
14. **Fix.** `pair init` rewrites `.claude/settings.json` through a generic
    map: very large numbers lose digits, `&`, `<`, `>` are escaped, keys
    are sorted.
15. **Fix.** `pair init` removes any hook command that contains
    `pair hook`, yours included (`notify-send done && pair hook stop`), and
    does not recognise its own under another binary name.
16. **Fix, small.** Pure deletions in the fallback diff are drawn one line
    early. An `old_string` with LF in a CRLF file is not found, so the
    jump goes to line 1. `pair init` ignores a read error before it writes
    `settings.json`.
17. **Decide.** The `show` tool opens any absolute path. Confining it to
    the repository would stop Claude from showing you a file of a
    dependency. Changes behaviour.

### CLI and sessions

18. **Fix.** `pair rename fix login bug`, without quotes, renames another
    session when the first word matches its title.
19. **Fix.** `pair log test` fails when a directory `test` exists.
20. **Fix.** A `#` in the repository's path (`~/C#Projects/app`) makes the
    panes open in the home directory.
21. **Fix.** `pair stop` typed inside the session is killed with the
    session, before it silences the voice and marks the session ended.
22. **Fix.** Inside a pane `PAIR_SESSION` is set, so `cd ../other && pair
    stop` stops the session you are in, not the other one.
23. **Decide.** `pair stop` can also kill a session named `pair` on your
    default tmux server, left by the first versions. I would remove that
    path now.
24. **Fix, small.** Archiving an old log can lose `session.log`. Creating a
    session id can loop forever when the sessions directory cannot be
    read.

### Neovim

25. **Fix.** A reload of a file that is not on screen counts as your edit
    and clears the marks just drawn.
26. **Fix, small.** Removed lines above line 1 are not visible. Under a
    linked root (`/tmp`) the second `<leader>ps` does not go back. A call
    that timed out can still run later.

### Before publishing

27. **Done on 2026-10-07.** The whisper model, the Kokoro models and the
    Python packages are pinned (fixed revisions, size and SHA-256, a lock
    file with wheel hashes); see `internal/fetch`.
28. **Done on 2026-10-07.** The release workflow runs vet and the tests
    before goreleaser, the actions are pinned by commit, and the release
    token can write to the two public repositories only.
29. **Decide.** `pair init` writes the absolute path of the binary into
    `.claude/settings.json`, a file that is often committed.

### Tests

30. **Fix.** The test of the permission prompt never sends one: it passes
    with the behaviour deleted.
31. **Fix over time.** Not covered: `Start`, `Stop`, `Init`, the status bar
    set-up, `Listen`, `Interrupt`, the Lua layout.
32. **Fix, small.** Three tests have limits tight enough to fail on a
    loaded machine.

### Documentation

33. **Fix, all at once.** The README says diffs go through `delta`; they do
    not. It lists as settable in the config file six variables that are
    read from the environment only (`PAIR_TMUX_SOCKET`, `PAIR_DIFF_TTL`,
    `PAIR_COLORSCHEME`, `PAIR_TRACK_SCRATCHPAD`, `PAIR_DEBUG`,
    `PAIR_HIGHLIGHT_TTL` in Neovim). `PAIR_KOKORO_MODEL`,
    `PAIR_NVIM_TRANSPORT` and `PAIR_SKIP_MIC` are not documented. The usage
    text lacks the hooks `activity`, `notify` and `click`. Two comments in
    the code describe the old layout.
