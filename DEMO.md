# The demo: shooting script

Fifty-eight seconds, recorded live, best of three takes. This is the demo
LAUNCH.md asks for ("The demo"), written so it can be shot without going
back to the research. Every spoken word stays uncut; the model's thinking
is cut with jump cuts. The clip has to work muted first (captions for both
voices, the state in the bar and the pane colours) and reward sound.

## The scenario app

A feed of posts, the kind everyone recognises. Five posts, two of them
scheduled in the future, all shown on the index. The task, "hide scheduled
posts from the feed until published_at passes", then has one obvious scope
and one controller line to change.

Two minutes, in a scratch directory, with Rails 8 installed:

```sh
rails new feed --minimal --skip-git
cd feed
bin/rails generate scaffold Post title:string body:text published_at:datetime
bin/rails db:migrate
```

`db/seeds.rb`:

```ruby
Post.delete_all
[
  ["Welcome to the feed", -3.days],
  ["Release notes 2.4", -2.days],
  ["How we test voice", -1.day],
  ["Roadmap for December", 2.days],   # scheduled
  ["Office hours next week", 5.days], # scheduled
].each do |title, offset|
  Post.create!(title: title, body: "#{title}.", published_at: Time.current + offset)
end
```

In `config/routes.rb`, `root "posts#index"`. Then:

```sh
bin/rails db:seed
git init -q && git add -A && git commit -qm "Feed with scheduled posts"
```

`bin/rails server` and http://localhost:3000 shows all five. The browser is
not on screen in the 58 s; a before and after shot is for the YouTube cut.

What the agent is expected to write, so a take can be judged:

```ruby
# app/models/post.rb, inside class Post
scope :published, -> { where(published_at: ..Time.current) }

# app/controllers/posts_controller.rb, in index
@posts = Post.published      # was Post.all
```

The app has no accounts, so "authors only" means: the feed hides a
scheduled post, its page stays reachable by its link, which is what the
author has. `show` is not touched; one controller line changes.

## The four beats

Opening. Press F5 and say the task:

> Hide scheduled posts from the feed until their publish time passes.

Expected: a tick, then the agent's first sentence, under 15 words, in its
own words ("Hiding scheduled posts from the feed: a scope on Post, then the
controller."). It writes the plan to `.pair/PAIRING.md`, which takes the
editor for a moment: keep one second of it, or cut it.

**Beat 1, narrate and ask with a recommendation.** Expected from the agent,
one question, recommended option first, something like: "Should scheduled
posts stay reachable for their authors? I recommend yes." The panel
breathes magenta, the bar says `❓ Claude is asking you`. Press F5 and say:

> Yes, authors only.

If the agent asks something else, answer it in the same spirit; the take
counts only if the question carried a recommendation.

**Beat 2, pointing, the scope drawn in, the controller line replaced.**
The agent points at `class Post < ApplicationRecord` (`show`, with a note
such as "The scope goes here."), narrates one sentence ("Adding a published
scope to Post; the feed will use it."), and the scope appears in the file
with `+` in the margin. Then the controller: `@posts = Post.all` shown with
`-`, `@posts = Post.published` with `+`, in place. Two cards in the
activity column. End of turn: "Done; look at the controller." The bar says
`○ your turn`.

**Beat 3, the hand rename.** In Neovim, in `app/models/post.rb`: cursor on
`published`, `ciw`, type `live`, Esc, `:w`. Press F5 and say:

> I renamed it to live, carry on.

**Beat 4, the agent picks up the hand edit.** Expected first sentence:
"You renamed the scope to live, updating the controller to match." The
controller line is replaced in place: `Post.live`. Then "Done." The column
shows `✎ you edited app/models/post.rb` above the agent's card.

Closing caption, added in the edit, not spoken: "Claude Code ran hidden the
whole time." The YouTube cut presses F7 here and shows the pane.

Timing to aim for: opening 8 s, beat 1 14 s, beat 2 18 s, beat 3 8 s,
beat 4 10 s. The cuts fall in the model's thinking time, the yellow wave.

## Pre-flight

- Terminal window at 16:9 (1920×1080 or 2560×1440), title bar hidden,
  font 16 to 18 pt so the 9:16 crop stays readable. `PAIR_LOG_WIDTH=36%`.
- Theme: no Neovim config of your own on this account, so pair's `retrobox`
  applies (`PAIR_COLORSCHEME=retrobox`); check that `+` and `-` lines read
  well on the recording, not only on the screen.
- Voice: `pair voice use kokoro am_michael` (or the voice chosen), then
  `pair voice test`. Not Samantha.
- Microphone: a wired microphone, not AirPods, which switch to the
  low-quality headset profile when used as input. `pair setup` when it has
  landed, else `pair doctor --mic`: the project's name said and read back.
- `pair doctor`: everything green, the whisper model present, Claude Code
  signed in and the trust dialog for this directory accepted once before.
- Claude Code's pane hidden: `PAIR_CLAUDE=hidden`, the default. No F7 in
  the 58 s take.
- A fresh `.pair`, so the activity column is empty and whisper knows the
  word: `rm -rf .pair && mkdir .pair && printf 'published_at\n' > .pair/vocab.txt`.
- Between takes: `git checkout -- . && git clean -fdq` puts the app back.
- macOS: Do Not Disturb on, Slack and Mail closed, keyboard sounds off, the
  laptop on power.
- One rehearsal without recording. The agent's words vary; the four beats
  must not.
- Three takes, all kept. Choose the one where the question carried a
  recommendation and the rename was picked up in the first sentence.

## Capture

- Screen Studio: record the terminal window with system audio and the
  microphone, no zoom animations (the pane is the picture), cursor hidden.
  Export 16:9 at 1080p and 9:16 from the same recording: the 9:16 crop
  keeps the editor and the activity column, with the status bar in frame.
- Or OBS 30+: a macOS Screen Capture source of the window with its audio,
  the microphone as a second track, 1080p60, record to mkv and remux to
  mp4.
- pair's voice is on the system track, yours on the microphone track;
  level the two in the edit. No music.
- Name the session right after the take: `pair rename "demo take 2"`.

## Captions

The words and the timestamps come from the session's events, not from a
transcription of the recording:

```sh
pair captions "demo take 2" --srt --out take2.srt
```

Times count from the session's first event; the recording started earlier.
Find the offset once: the first cue of the agent starts where its voice
starts on the system-audio waveform. Shift the whole file by it:

```sh
ffmpeg -itsoffset 4.2 -i take2.srt -c copy take2-shifted.srt
```

Burn them in (16:9; for 9:16 raise `FontSize` and `MarginV`):

```sh
ffmpeg -i take2.mp4 \
  -vf "subtitles=take2-shifted.srt:force_style='FontName=Menlo,FontSize=20,Outline=1,MarginV=36'" \
  -c:a copy take2-captioned.mp4
```

Each cue names its speaker (`Claude Code:`, `you:`). `--vtt` is for the
website's player, which draws the captions itself.

## The cut per channel

- X and Bluesky: the 58 s with sound, captions burned in, 16:9. The first
  frame is the agent asking, panel magenta, not the empty session.
- YouTube: 2 to 3 minutes. A 10 s face-cam intro (the problem in one
  sentence), the 58 s, then F7 for the hidden Claude Code pane with the
  question visible as a tool call, an F6 interrupt, and `pair replay` to
  end.
- Website: a muted autoplay loop of beat 2, the pointing and the scope
  drawing in, under 8 MB, with a sound toggle that swaps in the full clip;
  the full captioned clip below it.

  ```sh
  ffmpeg -ss 22 -t 18 -i take2.mp4 -an -vf scale=1280:-2 -c:v libx264 -crf 30 -pix_fmt yuv420p loop.mp4
  ```

- Show HN: the site with the clip above the fold. The first comment, by the
  author: the problem, the four beats, what runs locally, what pair does
  not do, the honest overlaps, the install line.
