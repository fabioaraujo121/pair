#!/usr/bin/env python3
"""pair's Kokoro worker.

Keeps the model loaded and turns sentences into WAV files on request, so
each narration costs synthesis time only. It does not play anything: the
pair binary plays the files, which is what lets the engineer interrupt.

Protocol, one JSON object per line.
  in : {"id": 3, "text": "...", "voice": "af_heart", "lang": "en-us",
        "speed": 1.0, "dir": "/tmp/pair-tts"}
  out: {"ready": true, "voices": [...]}                       once, at start
       {"id": 3, "chunk": 0, "path": ".../3-0.wav", "seconds": 4.6}
       {"id": 3, "done": true}            or {"id": 3, "done": true, "error": "..."}
A file <dir>/<id>.cancel stops a request between two sentences.
"""
import json
import os
import re
import sys


def say(obj):
    sys.stdout.write(json.dumps(obj) + "\n")
    sys.stdout.flush()


def sentences(text, shortest=25):
    """Split into sentences, so the first one can be played while the next
    is synthesized. Very short pieces are joined to their neighbour: a lone
    "Done." sounds clipped and costs a round trip."""
    parts = [p.strip() for p in re.split(r"(?<=[.!?;:])\s+", text.strip()) if p.strip()]
    out = []
    for p in parts:
        if out and (len(out[-1]) < shortest or len(p) < shortest):
            out[-1] = out[-1] + " " + p
        else:
            out.append(p)
    return out or [text]


def main():
    model, voices = sys.argv[1], sys.argv[2]
    try:
        import soundfile as sf
        from kokoro_onnx import Kokoro

        engine = Kokoro(model, voices)
        # The first synthesis is slower than the rest: pay for it now.
        engine.create("Ready.", voice=sorted(engine.get_voices())[0], speed=1.0, lang="en-us")
    except Exception as e:  # noqa: BLE001
        say({"ready": False, "error": "%s: %s" % (type(e).__name__, e)})
        return 1
    say({"ready": True, "voices": sorted(engine.get_voices())})

    for line in sys.stdin:
        line = line.strip()
        if not line:
            continue
        try:
            req = json.loads(line)
        except ValueError:
            continue
        rid = req.get("id")
        out_dir = req.get("dir") or "."
        cancel = os.path.join(out_dir, "%s.cancel" % rid)
        try:
            os.makedirs(out_dir, exist_ok=True)
            for i, part in enumerate(sentences(req.get("text", ""))):
                if os.path.exists(cancel):
                    break
                samples, rate = engine.create(
                    part,
                    voice=req.get("voice") or "af_heart",
                    speed=float(req.get("speed") or 1.0),
                    lang=req.get("lang") or "en-us",
                )
                path = os.path.join(out_dir, "%s-%d.wav" % (rid, i))
                sf.write(path, samples, rate, subtype="PCM_16")
                say({"id": rid, "chunk": i, "path": path, "seconds": round(len(samples) / float(rate), 2)})
            say({"id": rid, "done": True})
        except Exception as e:  # noqa: BLE001
            say({"id": rid, "done": True, "error": "%s: %s" % (type(e).__name__, e)})
        finally:
            try:
                os.remove(cancel)
            except OSError:
                pass
    return 0


if __name__ == "__main__":
    sys.exit(main())
