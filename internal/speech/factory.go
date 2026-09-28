package speech

import (
	"fmt"

	"github.com/fabioaraujo121/pair/internal/config"
)

// NewSpeaker picks the text-to-speech adapter named by PAIR_TTS.
//
// Adding an adapter: implement Speaker, add a case here, document the name.
func NewSpeaker(cfg config.Config) (Speaker, error) {
	if cfg.Mute {
		return Silent{}, nil
	}
	switch cfg.TTS {
	case "", "say":
		return NewSay(cfg.Voice, cfg.Rate), nil
	case "silent", "none":
		return Silent{}, nil
	default:
		return nil, fmt.Errorf("PAIR_TTS=%q: unknown text-to-speech adapter (available: say, silent)", cfg.TTS)
	}
}

// NewListener picks the speech-to-text adapter named by PAIR_STT. With
// lock, the listener takes the cross-process microphone lock itself and
// waits for it; callers that already hold the lock pass false.
func NewListener(cfg config.Config, lock bool) (Listener, error) {
	if cfg.Mute {
		return Deaf{}, nil
	}
	switch cfg.STT {
	case "", "whisper":
		l := NewVoiceListener(cfg)
		if lock {
			l.LockPath, l.LockWait = MicLockPath(cfg), true
		}
		return l, nil
	case "none":
		return Deaf{}, nil
	default:
		return nil, fmt.Errorf("PAIR_STT=%q: unknown speech-to-text adapter (available: whisper, none)", cfg.STT)
	}
}
