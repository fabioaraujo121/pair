package speech

import (
	"context"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Acknowledgements fill the longest silence of a session: between the
// engineer's last word and the agent's first sentence, which can be ten
// seconds or more. A short phrase in the agent's voice says that the words
// arrived and something is happening, the way a person pairing says
// "let me look" before they look.

// AckKind is what is being acknowledged.
type AckKind string

const (
	AckPrompt AckKind = "prompt" // the engineer gave the agent something to do
	AckAnswer AckKind = "answer" // the engineer answered the agent's question
	AckMissed AckKind = "missed" // the microphone caught nothing usable
)

// acks are the phrases, by language and kind. One is picked at random,
// never the one just said. A language that has none falls back to English.
var acks = map[string]map[AckKind][]string{
	"en": {
		AckPrompt: {"Let me look.", "On it.", "Got it, give me a moment.", "Okay, looking.", "Right, one moment."},
		AckAnswer: {"Got it.", "Okay.", "Thanks, going on.", "Noted."},
		AckMissed: {"Sorry, I didn't catch that."},
	},
	"pt": {
		AckPrompt: {"Deixa eu ver.", "Já vou.", "Entendi, um momento.", "Certo, olhando.", "Beleza, um instante."},
		AckAnswer: {"Entendi.", "Certo.", "Valeu, seguindo.", "Anotado."},
		AckMissed: {"Desculpa, não entendi."},
	},
	"es": {
		AckPrompt: {"Déjame ver.", "Voy.", "Entendido, un momento.", "Vale, mirando."},
		AckAnswer: {"Entendido.", "Vale.", "Gracias, sigo.", "Anotado."},
		AckMissed: {"Perdón, no entendí."},
	},
}

// AckPhrase picks the phrase to say for a kind in a language, avoiding
// the one said last. It returns "" when the language and English have
// none for the kind.
func AckPhrase(lang string, kind AckKind, last string) string {
	pool := acks[strings.ToLower(strings.SplitN(lang, "-", 2)[0])][kind]
	if len(pool) == 0 {
		pool = acks["en"][kind]
	}
	if len(pool) == 0 {
		return ""
	}
	if len(pool) == 1 {
		return pool[0]
	}
	choice := pool[rand.Intn(len(pool))]
	for choice == last {
		choice = pool[rand.Intn(len(pool))]
	}
	return choice
}

// Acknowledge says a phrase of the kind through the speaker, in the
// session's language, after taking the floor so it never talks over the
// agent. What was said is remembered in dir, so the next one differs.
// It returns at once when there is nothing to say.
func Acknowledge(ctx context.Context, speaker Speaker, dir, lang string, kind AckKind) error {
	if speaker == nil {
		return nil
	}
	lastFile := filepath.Join(dir, "ack.last")
	last, _ := os.ReadFile(lastFile)
	phrase := AckPhrase(lang, kind, strings.TrimSpace(string(last)))
	if phrase == "" {
		return nil
	}
	floorCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	floor, err := AcquireFloor(floorCtx, dir, true)
	if err != nil {
		return nil // the agent is speaking: it has said more than an acknowledgement would
	}
	defer floor.Release()
	if dir != "" {
		_ = os.MkdirAll(dir, 0o700)
		_ = os.WriteFile(lastFile, []byte(phrase+"\n"), 0o600)
	}
	return speaker.Speak(ctx, phrase)
}
