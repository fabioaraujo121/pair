# Making pair usable, and getting it noticed

Research of 2026-11-16, four reviewers: the first run, the session itself,
the field, and how comparable tools got attention. What they found, then
one plan.

## The three findings that matter

**1. Voice is a commodity now. The editor is the empty square.** By November
2026 every agent has push-to-talk (Claude Code `/voice`, cloud dictation
only; Codex `/voice` two-way since 0.156; Cursor voice chat since Nov 6)
and "the agent talks back" exists locally (VoiceMode, OpenLive, Smalt,
Heard, a dozen TTS hooks). Nothing in the field narrates *before* the edit,
points at the lines in a real editor and draws the change in the real file,
asks and waits as a blocking tool call, or carries hand edits into the next
turn. No Neovim plugin combines an agent voice with the editor. Show HN
threads titled "voice for Claude Code" scored 1 to 15 points in 2026;
Claude Code plus something *visual* scored 100 to 500. Sell a pairing
protocol for Neovim, not voice.

Positioning to adopt: **"Your coding agent explains before it edits, points
at the line in Neovim, and waits for your answer."** It claims only the
uncontested behaviours and names the editor. "Out loud" and "local" are
true but shared with others now.

Audience, in order: Neovim users already on Claude Code or Codex, the
engineers who review more than they type (concentrated: r/neovim, dotfyle,
the claudecode.nvim and avante communities); RSI and low-vision engineers
on Talon (the strongest story for comments); people who think out loud.

**2. Fame needs the source open.** Every comparable tool that passed 10k
users is open source, except three with levers pair lacks (Amp had
Sourcegraph's channel, Superwhisper had a Karpathy tweet, Ghostty had a
28k-person waitlist and a famous founder). Stars gate awesome-claude-code,
dotfyle, Homebrew core and r/neovim; closed voice tools got 1 to 15 HN
points; closed-source HN threads turn into "why closed, here is the clone".
The site already says "MIT-licensed release" and the PRD promised "an
open-source tool". Total downloads of the releases so far: 4.

**3. The product's output is sound, and the site has none.** The website
cannot play the thing it sells. Every 2026 launch that travelled carried a
muted autoplay MP4 in the README and a clip with sound on X; VoiceMode's
17-minute conversation with sound is the most-watched OSS agent-voice demo
(25.6k views). pair has no recording at all.

## Usability: what to fix, in order

Time to first word spoken today: six commands and 10 to 25 minutes, with
two security asks (`brew trust`, an unsigned binary) before any value.
Half of a session's wall time is speech (validation run: 226 of 447 s).

| # | Change | Why | Effort |
| --- | --- | --- | --- |
| 1 | Default whisper model `ggml-large-v3-turbo-q5_0` (547 MB, same family); the 1.6 GB file behind a flag | Cuts the longest install step by three | 3 h |
| 2 | `pair setup`: doctor, download, mic test, voice test, "say your project's name" read back, then "ready: cd a repo and run pair". Drop `pair doctor --mic` from the docs | One command, proves mic, whisper, TTS and the fn+F5 issue in 15 s | 1 d |
| 3 | Speech budget: narrate only non-trivial changes, closing narration only when more than two files changed, tool descriptions aim at 120 chars, "speech 41 %" per turn in the column | Speech is half of wall time | hours |
| 4 | F6 tap silences the sentence, hold or second tap cancels the turn; a mic opened by F6 closes after 5 s of silence | F6 is blunt today; F5-while-speaking is the better barge-in and undocumented | hours |
| 5 | Transcript safety: 1.5 s status-bar preview with F5 to scratch, escape leading `!` and `/`, narrate waits on the mic lock | Three open review findings; mis-sends are the top voice complaint everywhere | 1 d |
| 6 | Ask recovery: a repeat key, the question stays in the panel, `pair answer "…"` typed fallback, timeout spoken ("no answer, going with X") | Questions evaporate after one hearing; no typed path while ask blocks | 1 d |
| 7 | Robust dialog watcher: poll the agent's pane for 60 s, recognise the login and theme wizards and Codex's /hooks review | A slow first start leaves a stuck, invisible agent and "your turn" | 0.5 d |
| 8 | First-session greeting: once, speak "press F5, or fn F5 on a laptop, and tell me what to do"; F5 collides with Dictation on MacBooks, so a doctor note and `PAIR_KEY_TALK` | Discovery of the keys, the single most common first-run failure | 1 d |
| 9 | Kokoro in the box: `uv` as a cask dependency, default to `kokoro-v1.0.int8.onnx` (92 MB), offered by `pair setup` | Samantha is the first voice people hear and it sounds robotic | 0.5 d |
| 10 | End of turn: silence 2.5 s by default, hold-to-talk mode (release sends) | Auto-send on thinking pauses is the complaint against every push-to-talk | hours |
| 11 | Earcons: four families, own tones under 300 ms, cue idle 20 s, no cue right after a show | Six stock macOS sounds, a Ping before nearly every narration | 1 d |
| 12 | Doctor checks the agent: signed in, tested version, contradictions fixed (cask caveats say `pair init`), total download size on the site | | 1 d |

Bolder, after the above: a watch mode (F9) that speaks only on ask,
permission and turn end with a resumption summary; a shareable walkthrough
exported from `events.jsonl` with the voice regenerated, "a PR you can
listen to". A navigator mode (you drive, the agent speaks when it sees
something) is the most striking idea and the riskiest: the when-to-speak
policy is the whole product.

## The demo

One scenario, 58 seconds, recorded live three times, best take kept, the
model's thinking cut with jump cuts, every spoken word uncut. Designed for
muted first (captions for both voices, state visible in the bar and the
pane colours), rewarding with sound.

Scenario: a small Rails app everyone recognises (a feed). "Hide scheduled
posts from the feed until published_at passes." The four beats no other
tool can show in one shot: the agent says what it will do and asks one
question with its recommendation; F5, "yes, authors only"; it points at
`class Post`, the scope draws into the file, then the controller line is
replaced in place; the engineer renames the scope by hand in Neovim and
presses F5, "I renamed it to live, carry on"; next turn: "you renamed the
scope to live, updating the controller to match". Claude Code never appears
on screen; a caption says it ran hidden the whole time.

Capture: Screen Studio (system audio plus mic, captions, 16:9 and 9:16
from one recording) or OBS with separate tracks; wired mic, not AirPods;
Kokoro voice; captions generated from `events.jsonl` (exact words and
timestamps) and burned in for X and Bluesky. VHS and asciinema carry no
audio: VHS only for a 10 s install GIF in the README.

Per channel: X and Bluesky get the 58 s with sound; YouTube a 2 to 3 min
cut with a 10 s face-cam intro and the F7 reveal of the hidden pane; the
website a muted autoplay loop of the point-and-draw beat under 8 MB with a
sound toggle, the full clip below; Show HN links the site with the clip
above the fold.

## Distribution

| Channel | What it takes | Note |
| --- | --- | --- |
| Claude Code plugin | done: `.claude-plugin/` in the repo, `/plugin install pair --marketplace fabioaraujo121/pair` adds `/pair:start`; the directory at claude.ai/directory/manage wants an icon and a paid plan | one install line; 532 plugins listed, none voice |
| `pair.nvim` as its own repo | done: github.com/fabioaraujo121/pair.nvim, history from 2026-09-25; dotfyle and awesome-neovim need an OSS plugin repo at least a week old | precedent: claudecode.nvim drives a closed binary |
| Show HN | Sunday 21:00 BRT (Monday 00:00 UTC), title "Show HN: Pair – your coding agent narrates, points at code in Neovim, and listens (local, macOS)", a raw voiceover demo, no music, no logos; stay three hours | median Show HN is 2 points; stars arrive at ~1.4 per upvote |
| r/ClaudeCode (483k), r/commandline, r/macapps, #neovim Matrix | a 10 s GIF and "what I built" | r/neovim needs two months of visible commit history and an AI disclosure |
| awesome-claude-code | issue form; 14 days public with active development, or 100 stars | submit on day 15 |
| Newsletters | hello@console.dev, changelog.com/news/submit, editor@cooperpress.com (Golang Weekly) | the day after HN; the Rails-developer-in-Brazil angle |
| Creators | devaslife, DevOps Toolbox, typecraft, TJ DeVries, IndyDevDan, Enzo Barbatto (pt-BR) | DM the 60 s clip |
| Podcasts | Changelog, Latent Space, Ruby on Rails Podcast, Remote Ruby | after the first thread |
| Codex plugins, MCP registry, Homebrew core | plugins with hooks cannot be submitted yet; the registry needs npm or PyPI; core needs 75 stars | later |

Native risk, stated plainly: Codex and Cursor already talk back; Claude
Code's spoken replies are the obvious next step. "The agent talks back" is
not defensible. The protocol is: pointing before the edit, blocking
narration, ask as a tool result, hand-edit diffs, offline, replayable.

## The 30 days

- **Days 1 to 7, assets.** Done on 2026-11-16: source opened (MIT, full
  history, scanned for secrets first); `pair.nvim` split out with its
  history (github.com/fabioaraujo121/pair.nvim, mirrored by the nvim
  workflow); the Claude Code plugin manifest (`/plugin install pair
  --marketplace fabioaraujo121/pair`, the `/pair:start` skill). Ship fixes
  1, 2, 3, 4 and 8 above. Record the demo (script in DEMO.md).
- **Days 8 to 13, seed.** r/ClaudeCode, r/commandline, #neovim Matrix,
  dotfyle, awesome-neovim, a Lobsters invite. Fix what the first strangers
  trip on.
- **Day 14, Sunday 21:00 BRT.** Show HN. First comment from the author:
  the problem, the four beats, what is local, what it does not do (no
  sub-second latency, no VS Code, no Linux), the honest overlaps (VoiceMode's
  converse, Codex's voice), the install line.
- **Days 15 to 20.** Console.dev, Changelog News, Golang Weekly,
  awesome-claude-code, the Anthropic directory (expect a hold for the
  binary), the Rails podcasts.
- **Days 21 to 30.** Creators, r/neovim once eligible, Product Hunt, and
  let others resubmit to HN with a new angle: Aider, Handy, Atuin and Paseo
  all broke out on a second to fourth post, by someone else.

Three mistakes that sink it: launching as "voice for Claude Code"; a silent
demo or a produced one with music; friction (a 1.6 GB model, `brew trust`,
an unsigned binary) that turns the thread into "here is the
open-source clone".

## Sources

Field: code.claude.com/docs/en/voice-dictation · developers.openai.com/codex/cli/reference · cursor.com/changelog · voicemode.dev · github.com/katipally/openlive · heard.dev · superwhisper.com/docs/get-started/coding-agents · github.com/T0mSIlver/localvoxtral · github.com/coder/claudecode.nvim · github.com/yetone/avante.nvim.
Launches: news.ycombinator.com items 42517447 (Ghostty), 44736176 (Crush), 46985151 (peon-ping), 45399106 (Handy), 35839470 (Atuin), 44482504 (OpenCode), 49113559 (Ski), 48377250 (Paseo) · youtu.be/cYdwOD_-dQc (VoiceMode) · danfking.github.io/blog/2026/04/23/show-hn-by-the-numbers.
Channels: code.claude.com/docs/en/plugins/publish.md · github.com/hesreallyhim/awesome-claude-code · dotfyle.com/neovim/plugins/add · docs.brew.sh/Package-Acceptance-Policy.
Voice UX: arxiv.org/html/2507.22352v1 (fillers vs spinner) · dl.acm.org/doi/10.1145/3841187 · developers.openai.com/api/docs/guides/realtime-models-prompting · docs.livekit.io/agents/logic/turns · tuple.app/pair-programming-guide/antipatterns · jakobnielsenphd.substack.com/p/slow-ai.
Onboarding: github.com/ggml-org/whisper.cpp/blob/master/models/README.md · brew.sh/2026/06/11/homebrew-6.0.0 · docs.ollama.com/quickstart.
Demo: repoclip.io/blog/show-hn-demo-video-pattern · screen.studio · docs.asciinema.org/manual/player/options (audioUrl) · github.com/charmbracelet/vhs.
