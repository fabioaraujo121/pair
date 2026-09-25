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

// NewListener picks the speech-to-text adapter named by PAIR_STT.
func NewListener(cfg config.Config) (Listener, error) {
	if cfg.Mute {
		return Deaf{}, nil
	}
	switch cfg.STT {
	case "", "whisper":
		return NewVoiceListener(cfg), nil
	case "none":
		return Deaf{}, nil
	default:
		return nil, fmt.Errorf("PAIR_STT=%q: unknown speech-to-text adapter (available: whisper, none)", cfg.STT)
	}
}
