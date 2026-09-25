# PRD validation — Item 4 (Audio) — 2026-09-24

Machine: macOS 26.6.2 (Darwin 25.6.0), Apple M5 Pro (arm64), host terminal Warp (dev.warp.Warp-Stable),
whisper.cpp 1.9.4 (/opt/homebrew/bin/whisper-cli, ggml 0.25.3), SoX (/opt/homebrew/bin/rec, sox, soxi).
Work dir: /private/tmp/claude-501/-Users-fabioaraujo-Developer/c2b4051e-b6e0-4ea5-b4af-94b87a3a9700/scratchpad/validation/item4
Model dir: ~/.pair/models/

## Summary of verdicts

| # | Claim | Verdict | Evidence |
|---|-------|---------|----------|
| a | `say -v Luciana` exists | VERIFIED (basic voice, not Premium/Enhanced) | `say -v '?'` lists `Luciana pt_BR`; AVFoundation id `com.apple.voice.super-compact.pt-BR.Luciana`, quality rawValue 1 (= .default) |
| b1 | `rec ... silence 1 0.1 1% 1 1.5 1%` live mic stops after 1.5 s | UNCERTAIN | single 25 s-capped attempt hung the whole 25 s, exit 142 (SIGALRM), empty stderr, live.wav never created; no `timeout` binary on this Mac |
| b2 | sox `silence 1 0.1 1% 1 1.5 1%` semantics (offline) | VERIFIED | 11.75 s input (0.5 s sil + 3 s speech + 3 s sil + speech) -> 3.01 s output; 1.0 s gap does NOT stop it (9.75 -> 9.24 s) |
| c | whisper-cli accepts -l -nt -np --prompt -m -f | VERIFIED | `--help` lines quoted below; also -otxt, -oj, -ng/--no-gpu, -t, -fa (default true), -sns present; exact PRD command exit 0 |
| d | transcription time ~10 s audio, large-v3-turbo | VERIFIED (fast) | 11.3 s pt audio: wall 0.83 s warm (load 345 ms, encode 249 ms, decode 114 ms), RTF 0.07, Metal yes |
| e | jargon accuracy with vs without --prompt | VERIFIED (partial) | prompt fixed Sidekiq ("Sedeq") and "model" ("modelo"); Hotwire -> "Twitter" and migration -> "migração" in both |
| f | silence -> "(no answer)" needs a guard | WRONG as written (whisper hallucinates) | 2 s silence -> "Obrigado."; with prompt -> "Transcrição e Legendas por Quintena Coelho"; -sns+prompt -> "."; zeros file -> "E aí" |

## Step 1 — TTS (`say -v Luciana`)

### Voice inventory
```
$ say -v '?' | grep -i -E 'pt_BR|Luciana'
Eddy (Português (Brasil)) pt_BR    # Olá, meu nome é Eddy.
Flo (Português (Brasil)) pt_BR    # Olá, meu nome é Flo.
Grandma (Português (Brasil)) pt_BR    # Olá, meu nome é Grandma.
Grandpa (Português (Brasil)) pt_BR    # Olá, meu nome é Grandpa.
Luciana             pt_BR    # Olá, meu nome é Luciana.
Reed (Português (Brasil)) pt_BR    # Olá, meu nome é Reed.
Rocko (Português (Brasil)) pt_BR    # Olá, meu nome é Rocko.
Sandy (Português (Brasil)) pt_BR    # Olá, meu nome é Sandy.
Shelley (Português (Brasil)) pt_BR    # Olá, meu nome é Shelley.
```
`say -v '?'` does not show a "(Premium)"/"(Enhanced)" suffix for Luciana. /System/Library/Speech/Voices and ~/Library/Speech/Voices do not exist on macOS 26 (voices are MobileAssets). Definitive check via AVFoundation:
```
$ swift -e 'import AVFoundation; for v in AVSpeechSynthesisVoice.speechVoices() where v.language == "pt-BR" { print(v.name, v.identifier, v.quality.rawValue) }'
Luciana com.apple.voice.super-compact.pt-BR.Luciana 1
Eddy com.apple.eloquence.pt-BR.Eddy 1
... (Flo, Grandma, Grandpa, Reed, Rocko, Sandy, Shelley all eloquence, quality 1)
```
quality rawValue: 1 = default (basic), 2 = enhanced, 3 = premium. So the installed Luciana is the **super-compact/basic** variant. The Enhanced asset exists in the catalog (`/System/Library/AssetsV2/com_apple_MobileAsset_TTSAXResourceModelAssets/.../com.apple.voice.enhanced.pt-BR.Luciana.caf` is only the sample clip), but it is not downloaded. Eddy/Flo/Reed/... are Eloquence (robotic) voices, not alternatives worth defaulting to.

### Synthesis to file
Text: `Vou adicionar validação no model User, porque hoje o controller aceita email vazio.` (83 chars, 13 words)
```
$ /usr/bin/time -p say -v Luciana -r 190 -o luciana.aiff "$TXT"     -> exit 0, real 0.49 s
$ afinfo luciana.aiff | grep duration                               -> estimated duration: 5.249705 sec
$ say -v Luciana -r 190 --file-format=WAVE --data-format=LEI16@16000 -o x.wav "$TXT"   -> exit 0
$ soxi x.wav  -> Channels 1, Sample Rate 16000, 16-bit Signed Integer PCM, Duration 00:00:05.25 (83996 samples)
$ sox luciana.aiff -r 16000 -c 1 -b 16 luciana16.wav; soxi -D luciana16.wav -> 5.249688
$ say -v Luciana (default rate) ... -> 5.655312 s
```
The direct-to-WAV trick works and produces exactly what whisper wants (16 kHz mono s16le). Note the file carries an Apple `FLLR` padding chunk after `fmt ` (xxd: `464c 4c52 cc0f 0000`); both sox and whisper-cli's miniaudio decoder read it fine (transcripts below were produced from such files).

### Pacing at rate 190
| Sample | chars | words | seconds | chars/s | effective wpm |
|---|---|---|---|---|---|
| sentence (above) | 83 | 13 | 5.25 | 15.8 | 149 |
| pt jargon 168-char | 168 | 29 | 11.28 | 14.9 | 154 |
| pt jargon 223-char | 223 | 40 | 15.15 | 14.7 | 158 |
| sentence, default rate (no -r) | 83 | 13 | 5.66 | 14.7 | 138 |

"16 chars/sec" is slightly optimistic; **~15 chars/s** (or ~150-158 wpm effective, i.e. `-r 190` does not mean 190 real wpm for Luciana) is the right estimate for pt-BR text with technical words.

## Step 2 — `rec` silence detection

### Live microphone attempt (exactly one, 25 s cap)
```
$ command -v timeout || command -v gtimeout     -> (empty: no `timeout` binary; coreutils not installed)
$ perl -e 'alarm 25; exec @ARGV' rec -q -r 16000 -c 1 live.wav silence 1 0.1 1% 1 1.5 1%
rec exit=142 elapsed=25s          (142 = 128 + SIGALRM)
stderr: (empty)
live.wav: No such file or directory
```
Observation: rec blocked for the full 25 s and never even created the output file (sox opens the input device before the output file, so the block is in CoreAudio input open). Diagnostics:
- No sandbox: no `sandbox-exec` process, no sandbox env vars in this shell.
- Mic exists: `system_profiler SPAudioDataType` -> "Microfone (MacBook Pro)", Default Input Device: Yes, 1 channel, 48 kHz.
- Warp declares `NSMicrophoneUsageDescription` ("A program in Warp wants to use your microphone."), so a TCC prompt for Warp is possible.
- `log show` (TCC subsystem / process rec) returned nothing in the window; TCC.db is unreadable without Full Disk Access ("authorization denied").
Most likely: the macOS microphone permission dialog for Warp was raised and nobody clicked it (or Warp is denied). The live-mic claim is therefore UNCERTAIN, not disproved. Not retried per instructions.

### Offline validation of the `silence` effect (what the PRD relies on)
```
$ sox -n -r 16000 -c 1 -b 16 sil05.wav trim 0 0.5
$ sox -n -r 16000 -c 1 -b 16 sil3.wav  trim 0 3
$ sox luciana16.wav speech3.wav trim 0 3
$ sox sil05.wav speech3.wav sil3.wav luciana16.wav in.wav ; soxi -D in.wav   -> 11.749688
$ sox in.wav out.wav silence 1 0.1 1% 1 1.5 1% ; soxi -D out.wav            -> 3.010875
# variant: speech first, no leading silence
$ sox speech3.wav sil3.wav luciana16.wav in2.wav ; sox in2.wav out2.wav silence 1 0.1 1% 1 1.5 1%
in2=11.249688 out2=3.010875
# variant: only a 1.0 s gap (must NOT stop)
$ sox sil05.wav speech3.wav sil1.wav luciana16.wav in3.wav ; sox in3.wav out3.wav silence 1 0.1 1% 1 1.5 1%
in3=9.749688 out3=9.240625      (only the 0.5 s leading silence was trimmed; the 1.0 s gap was kept and recording continued)
$ sox speech3.wav -n stat -> Maximum amplitude 0.571564, RMS amplitude 0.193634   (well above the 1% = 0.01 threshold)
```
Conclusion: the effect trims leading silence, keeps talking through pauses < 1.5 s, and stops at the first >= 1.5 s stretch below 1%. The trailing 1.5 s of silence is NOT included in the output (out = 3.01 s, i.e. speech only). The second speech segment after the 3 s gap was discarded, as intended.

### man sox, `silence` (quoted)
           silence [-l] above-periods [duration threshold[d|%]
    	      [below-periods duration threshold[d|%]]
    
    	      Removes silence from the beginning, middle, or end of the audio.
    	      `Silence' is determined by a specified threshold.
    
    	      The above-periods value is used to indicate if audio should be
    	      trimmed at the beginning of the audio. A value of zero indicates
    	      no silence should be trimmed from the beginning. When specifying
    	      a non-zero above-periods, it trims audio up until it finds non-
    	      silence. Normally, when trimming silence from beginning of audio
    	      the above-periods will be 1 but it can be increased to higher
    	      values to trim all audio up to a specific count of non-silence
    	      periods. For example, if you had an audio file with two songs
    	      that each contained 2 seconds of silence before the song, you
    	      could specify an above-period of 2 to strip out both silence
    	      periods and the first song.
    
    	      When above-periods is non-zero, you must also specify a duration
    	      and threshold. duration indicates the amount of time that non-
    	      silence must be detected before it stops trimming audio. By
    	      increasing the duration, burst of noise can be treated as
    	      silence and trimmed off.
    
    	      threshold is used to indicate what sample value you should treat
    	      as silence.  For digital audio, a value of 0 may be fine but for
    	      audio recorded from analog, you may wish to increase the value
    	      to account for background noise.
    
    	      When optionally trimming silence from the end of the audio, you
    	      specify a below-periods count.  In this case, below-period means
    	      to remove all audio after silence is detected.  Normally, this
    	      will be a value 1 of but it can be increased to skip over
    	      periods of silence that are wanted.  For example, if you have a
    	      song with 2 seconds of silence in the middle and 2 second at the
    	      end, you could set below-period to a value of 2 to skip over the
    	      silence in the middle of the audio.
    
    	      For below-periods, duration specifies a period of silence that
    	      must exist before audio is not copied any more.  By specifying a
    	      higher duration, silence that is wanted can be left in the
    	      audio.  For example, if you have a song with an expected 1
    	      second of silence in the middle and 2 seconds of silence at the
    	      end, a duration of 2 seconds could be used to skip over the
    	      middle silence.
    

So `1 1.5 1%` (below-periods=1, duration=1.5, threshold=1%) = "remove all audio after silence is detected ... duration specifies a period of silence that must exist before audio is not copied any more", i.e. stop after 1.5 s below 1%. Confirmed by the offline test.

### Hard cap behaviour (what `timeout`/PAIR_ASK_MAX leaves behind)
No `timeout` binary exists on this Mac (no coreutils). `pair` (Go) should use `exec.CommandContext`/its own timer, not shell out to `timeout`.
```
# sox fed 3200 zero samples every 0.1 s over a pipe, killed after ~2 s:
SIGTERM: sox exit=0, killed.wav 114732 bytes, soxi Duration 00:00:03.58 (valid header)
SIGKILL: sox exit=137, killed9.wav 114688 bytes, soxi shows Sample Rate but NO Duration (header not finalized)
         whisper-cli still decoded it (exit 0) and hallucinated " E aí" on pure zeros.
```
Use SIGTERM (or SIGINT) for the cap, never SIGKILL first; sox finalizes the WAV header on SIGTERM.

## Step 3 — whisper-cli flags (`whisper-cli --help`, version 1.9.4)
Note: even `--help` prints ggml/Metal backend lines to stderr (`load_backend: loaded BLAS backend ...`, `ggml_metal_device_init: ...`). A `-np` run still emits 41 such lines on stderr; only stdout is the transcript.
```
  -t N,      --threads N            [4      ] number of threads to use during computation
  -bs N,     --beam-size N          [5      ] beam size for beam search
  -nf,       --no-fallback          [false  ] do not use temperature fallback while decoding
  -otxt,     --output-txt           [false  ] output result in a text file
  -oj,       --output-json          [false  ] output result in a JSON file
  -ojf,      --output-json-full     [false  ] include more information in the JSON file
  -np,       --no-prints            [false  ] do not print anything other than the results
  -nt,       --no-timestamps        [false  ] do not print timestamps
  -l LANG,   --language LANG        [en     ] spoken language ('auto' for auto-detect)
             --prompt PROMPT        [       ] initial prompt (max n_text_ctx/2 tokens)
  -m FNAME,  --model FNAME          [models/ggml-base.en.bin] model path
  -f FNAME,  --file FNAME           [       ] input audio file path
  -ng,       --no-gpu               [false  ] disable GPU
  -fa,       --flash-attn           [true   ] enable flash attention
  -sns,      --suppress-nst         [false  ] suppress non-speech tokens
```
Exact PRD command with the tiny test model: `whisper-cli -m for-tests-ggml-tiny.bin -l pt -nt -np --prompt "Rails, ActiveRecord" -f pt10.wav` -> exit 0 (empty transcript; the for-tests tiny model is not a usable model, ignore its output). With large-v3-turbo the same flags produce the transcripts in Step 5.

## Step 4 — Model download
```
$ curl -L --silent --show-error -o ~/.pair/models/ggml-large-v3-turbo.bin https://huggingface.co/ggerganov/whisper.cpp/resolve/main/ggml-large-v3-turbo.bin
exit=0, real 120.45 s  (~13.5 MB/s)
-rw-r--r--  1624555275 bytes (1.51 GiB, 1.62 GB)  ~/.pair/models/ggml-large-v3-turbo.bin
sha256 (first 16 hex): 1fc70f774d38eb16
```
whisper reports `whisper_model_load: MTL0 total size = 1623.92 MB`. PRD's "about 1.6 GB" is correct.

## Step 5 — Transcription measurements (large-v3-turbo, Metal)
Backend lines (every run): `ggml_metal_device_init: GPU name: MTL0 (Apple M5 Pro)`, `whisper_init_with_params_no_state: use gpu = 1`, `flash attn = 1`, `whisper_backend_init_gpu: using MTL0 backend`. system_info: `n_threads = 4 / 15 | MTL : EMBED_LIBRARY = 1 | CPU : NEON = 1 ... SME = 1 | ACCELERATE = 1`. Default decoding: 5 beams + best of 5.

### Wall times (`/usr/bin/time -p`, run twice, both shown)
| run | audio | flags | run 1 | run 2 (warm) |
|---|---|---|---|---|
| jfk | 11.00 s en | -l en -nt | 1.09 s | 0.77 s |
| jfk | 11.00 s en | -l en -nt -np | — | 0.76 s |
| pt10 no prompt | 11.28 s pt | -l pt -nt -np | 0.91 s | 0.84 s |
| pt10 with prompt | 11.28 s pt | -l pt -nt -np --prompt ... | 1.00 s | 0.84 s |
| pt10 timings run | 11.28 s pt | -l pt -nt --prompt ... | — | 0.83 s |
| silence 2 s | 2.00 s | -l pt -nt -np | — | 0.70 s |

### whisper_print_timings split
```
jfk run 1 (cold):  load 437.87 ms | mel 3.66 | sample 19.73 (133 runs) | encode 331.96 (1 run) | batchd 151.68 (132 runs) | total 984.80 ms
jfk run 2 (warm):  load 345.45 ms | mel 3.03 | sample 19.64 (133 runs) | encode 251.34 (1 run) | batchd  64.10 (132 runs) | total 697.14 ms
pt10 w/ prompt:    load 344.98 ms | mel 3.13 | sample 37.48 (247 runs) | encode 249.20 (1 run) | batchd 114.43 (242 runs) | prompt 5.95 (36 runs) | total 768.80 ms
fallbacks = 0 p / 0 h in all runs
```
### Table for claim (d), ~10 s Portuguese audio (11.28 s), warm run
| model load | encode | decode (batchd+sample+prompt) | whisper total | wall (process) | RTF (wall/audio) | Metal |
|---|---|---|---|---|---|---|
| 345 ms | 249 ms | ~158 ms | 769 ms | 0.83 s | 0.074 | yes (MTL0 Apple M5 Pro, flash attn on) |
Per-process cost is dominated by model load (~45%); a long-lived whisper server/library binding would cut ~0.35 s per `ask`, but at 0.8 s total this is already well within the PRD's latency expectations. Cold first run of the session: ~1.1 s.

### Transcripts (verbatim stdout, `-nt`; stdout begins with a blank line and each line has a leading space)
jfk (`-l en -nt`, both runs identical):
```
 And so, my fellow Americans, ask not what your country can do for you, ask what you can do for your country.
```
Portuguese source text (168 chars, spoken by Luciana, 11.28 s):
```
Vou criar uma migration no PostgreSQL pro ObraFlow, ajustar o controller e o model ActiveRecord, enfileirar o job no Sidekiq, atualizar a view com Hotwire e abrir o PR.
```
WITHOUT --prompt (`-l pt -nt -np`, both runs identical):
```
 Vou criar uma migração no PostgreSQL para o ObraFlow, ajustar o controller e o modelo ActiveRecord, enfileirar o job no Sedeq, atualizar a view com o Twitter e abrir o PR.
```
WITH `--prompt "Rails, ActiveRecord, PostgreSQL, Sidekiq, Hotwire, Turbo, Stimulus, migration, controller, ObraFlow"` (both runs identical):
```
 Vou criar uma migração no PostgreSQL para o ObraFlow, ajustar o controller e o model ActiveRecord, enfileirar o job no Sidekiq, atualizar a view com o Twitter e abrir o PR.
```
Jargon scorecard (8 terms: migration, PostgreSQL, ObraFlow, controller, model, ActiveRecord, Sidekiq, Hotwire, PR):
| term | no prompt | with prompt |
|---|---|---|
| PostgreSQL | ok | ok |
| ObraFlow | ok | ok |
| controller | ok | ok |
| ActiveRecord | ok | ok |
| PR | ok | ok |
| model | "modelo" (translated) | ok |
| Sidekiq | "Sedeq" | ok |
| Hotwire | "Twitter" | "Twitter" |
| migration | "migração" (translated) | "migração" |
Word-level: 29 words; no-prompt 4 wrong (13.8% WER on the sentence), with-prompt 2 wrong (6.9%). Caveat: the speaker is Luciana TTS pronouncing English words with pt-BR phonology ("Hotwire" comes out as something like "rotch-uai-ri"), a human speaker may do better or worse. Minor: "pro" -> "para o" (acceptable normalization).

## Step 6 — Silence / hallucination
2 s of digital silence (`sox -n ... trim 0 2`), large-v3-turbo:
| flags | stdout |
|---|---|
| `-l pt -nt -np` | ` Obrigado.` |
| `-l pt -nt -np --prompt "<vocab>"` | ` Transcrição e Legendas por Quintena Coelho` |
| `-l pt -nt -np -sns` | ` Obrigado.` |
| `-l pt -nt -np -sns --prompt "<vocab>"` | ` .` |
| ~3.6 s of zero samples (SIGKILLed file), `-l pt -nt -np` | ` E aí` |
So whisper never returns an empty transcript for silence; the PRD's "Empty or very short transcripts return (no answer)" rule will not fire on its own and "Obrigado." would be relayed to Claude as the user's answer.

## Recommended PRD changes
1. Voice: keep Luciana as default but say explicitly that the stock voice is the basic (super-compact) one and that `pair doctor` should detect quality via `AVSpeechSynthesisVoice` (or just tell the user) and recommend downloading "Luciana (Enhanced)" in System Settings -> Accessibility -> Spoken Content -> System Voice -> Manage Voices. No Premium/Siri pt-BR voice is exposed to `say`.
2. Pacing: use ~15 chars/s (not 16) for `-r 190`; effective speed is ~150-158 wpm.
3. TTS pipeline: `say -v Luciana --file-format=WAVE --data-format=LEI16@16000 -o out.wav` gives whisper-ready audio without sox, useful for tests/fixtures; for playback keep `say` directly.
4. Recording: keep `rec -q -r 16000 -c 1 <wav> silence 1 0.1 1% 1 1.5 1%` (semantics verified offline). Implement the cap in Go with SIGTERM (sox finalizes the header), never rely on a `timeout` binary (absent on stock macOS). Treat "rec produced no file / file < 0.3 s / RMS < threshold" as `(no answer)` BEFORE calling whisper.
5. `pair doctor` mic step: run `rec -q -r 16000 -c 1 probe.wav trim 0 1` with a 10 s SIGTERM cap from the user's real terminal; if it times out with no file, print "macOS is waiting for/denied microphone permission for <terminal app>: System Settings -> Privacy & Security -> Microphone (or `tccutil reset Microphone <bundle id>` to re-prompt)"; if the file exists, print its RMS and warn when ambient RMS is above 1% (the silence stop would never trigger) and suggest raising the threshold (configurable per PRD).
6. `(no answer)` rule: whisper hallucinates on silence ("Obrigado.", "Transcrição e Legendas por ...", "E aí", "."). Gate on the audio (duration/RMS from rec) and additionally drop transcripts that match a small hallucination blocklist (Obrigado, Legendas, Transcrição, Amara.org, Tchau, "." only). `-sns` alone does not fix it.
7. Model: large-v3-turbo is the right default on Apple Silicon (0.8 s for 11 s, RTF 0.07, 1.62 GB). Only stderr filtering is needed: with `-np` whisper still prints ~40 ggml/Metal lines to stderr; read only stdout and trim it.
8. Download: 120 s at ~13.5 MB/s here; `pair doctor` should show progress and verify size (1,624,555,275 bytes).

## Raw logs in this directory
whisper_help.txt, sox_silence_man.txt, run_whisper.sh, run_whisper.log, *.out / *.err per run, rec_stderr.txt, download.log, pt_text10.txt, pt_text15.txt, luciana.aiff, x.wav, pt10.wav, silence2.wav, in*.wav/out*.wav, killed.wav, killed9.wav
