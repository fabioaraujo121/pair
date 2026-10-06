// Package session keeps pairing sessions apart.
//
// The session in progress writes to fixed paths in <repo>/.pair
// (events.jsonl, session.log, session.json), so the hooks never need to
// know which session is running. When a new session starts, the previous
// one is moved to <repo>/.pair/sessions/<id>/. A session can be named,
// listed, opened again and continued, the way Claude Code's own sessions
// can; it remembers the id of its Claude Code conversation so continuing
// resumes that exact conversation.
package session

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	metaFile   = "session.json"
	eventsFile = "events.jsonl"
	logFile    = "session.log"
	archiveDir = "sessions"
	idLayout   = "20060102-150405"
)

// Meta describes one session.
type Meta struct {
	ID           string    `json:"id"`
	Name         string    `json:"name,omitempty"`  // given by the engineer
	Title        string    `json:"title,omitempty"` // taken from the first prompt
	Started      time.Time `json:"started"`
	Ended        time.Time `json:"ended,omitempty"`
	Agent        string    `json:"agent,omitempty"`             // which agent held the conversation
	Conversation string    `json:"claude_session_id,omitempty"` // the agent's own id for it; the key is kept from the first versions
}

// Label is what to call the session: its name, else its title, else its id.
func (m Meta) Label() string {
	switch {
	case m.Name != "":
		return m.Name
	case m.Title != "":
		return m.Title
	default:
		return m.ID
	}
}

// Summary is a session with what its event log says about it.
type Summary struct {
	Meta
	Current    bool
	Dir        string // where its files are
	Prompts    int
	Edits      int
	Narrations int
	Last       time.Time // last event
}

// EventsPath is the event log of the session.
func (s Summary) EventsPath() string { return filepath.Join(s.Dir, eventsFile) }

// Length is how long the session lasted, judged by its events.
func (s Summary) Length() time.Duration {
	end := s.Last
	if end.IsZero() {
		end = s.Ended
	}
	if end.IsZero() || s.Started.IsZero() || end.Before(s.Started) {
		return 0
	}
	return end.Sub(s.Started)
}

// ErrNone is returned when there is no session to act on.
var ErrNone = errors.New("no session")

// Current returns the session in progress (or the last one, when none is
// running).
func Current(stateDir string) (Meta, bool) {
	var m Meta
	data, err := os.ReadFile(filepath.Join(stateDir, metaFile))
	if err != nil || json.Unmarshal(data, &m) != nil || m.ID == "" {
		return Meta{}, false
	}
	return m, true
}

// Save writes the metadata of the current session.
func Save(stateDir string, m Meta) error {
	return writeMeta(stateDir, m)
}

func writeMeta(dir string, m Meta) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	tmp := filepath.Join(dir, metaFile+".tmp")
	if err := os.WriteFile(tmp, append(data, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(dir, metaFile))
}

// New archives whatever session was current and starts a fresh one.
func New(stateDir, name string, now time.Time) (Meta, error) {
	if err := Archive(stateDir); err != nil {
		return Meta{}, err
	}
	m := Meta{ID: uniqueID(stateDir, now), Name: strings.TrimSpace(name), Started: now}
	return m, writeMeta(stateDir, m)
}

// uniqueID returns an id for a session starting at t that no archived
// session has.
func uniqueID(stateDir string, t time.Time) string {
	for {
		id := t.Local().Format(idLayout)
		if _, err := os.Stat(filepath.Join(stateDir, archiveDir, id)); os.IsNotExist(err) {
			return id
		}
		t = t.Add(time.Second)
	}
}

// Archive moves the current session into sessions/<id>/. A log written
// before sessions were kept apart holds several sessions in one file: it
// is split at each "session_start" event, so every past session gets its
// own entry.
func Archive(stateDir string) error {
	events := filepath.Join(stateDir, eventsFile)
	meta, hasMeta := Current(stateDir)
	_, statErr := os.Stat(events)
	hasEvents := statErr == nil

	switch {
	case !hasMeta && !hasEvents:
		_ = os.Remove(filepath.Join(stateDir, logFile))
		return nil
	case !hasMeta:
		return archiveLegacy(stateDir)
	}

	dir := filepath.Join(stateDir, archiveDir, meta.ID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if meta.Ended.IsZero() {
		meta.Ended = lastEventTime(events)
	}
	if err := writeMeta(dir, meta); err != nil {
		return err
	}
	for _, f := range []string{eventsFile, logFile} {
		src := filepath.Join(stateDir, f)
		if _, err := os.Stat(src); err == nil {
			if err := os.Rename(src, filepath.Join(dir, f)); err != nil {
				return err
			}
		}
	}
	return os.Remove(filepath.Join(stateDir, metaFile))
}

// event is the part of an event line this package reads.
type event struct {
	TS      time.Time      `json:"ts"`
	Type    string         `json:"type"`
	Payload map[string]any `json:"payload"`
}

func readEvents(path string, each func(line string, e event)) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<24)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var e event
		if json.Unmarshal([]byte(line), &e) != nil {
			continue
		}
		each(line, e)
	}
	return sc.Err()
}

func lastEventTime(path string) time.Time {
	var last time.Time
	_ = readEvents(path, func(_ string, e event) {
		if !e.TS.IsZero() {
			last = e.TS
		}
	})
	return last
}

// archiveLegacy splits a log that holds several sessions.
func archiveLegacy(stateDir string) error {
	type chunk struct {
		meta  Meta
		lines []string
	}
	var chunks []*chunk
	var cur *chunk
	err := readEvents(filepath.Join(stateDir, eventsFile), func(line string, e event) {
		if cur == nil || e.Type == "session_start" {
			cur = &chunk{meta: Meta{Started: e.TS}}
			chunks = append(chunks, cur)
		}
		cur.lines = append(cur.lines, line)
		if !e.TS.IsZero() {
			cur.meta.Ended = e.TS
		}
		absorb(&cur.meta, e)
	})
	if err != nil {
		return err
	}
	for i, c := range chunks {
		if len(c.lines) == 0 || onlyStart(c.lines) {
			continue
		}
		c.meta.ID = uniqueID(stateDir, c.meta.Started)
		dir := filepath.Join(stateDir, archiveDir, c.meta.ID)
		if err := writeMeta(dir, c.meta); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(dir, eventsFile), []byte(strings.Join(c.lines, "\n")+"\n"), 0o600); err != nil {
			return err
		}
		if i == len(chunks)-1 { // the plain-text log cannot be split: it goes with the last one
			_ = os.Rename(filepath.Join(stateDir, logFile), filepath.Join(dir, logFile))
		}
	}
	_ = os.Remove(filepath.Join(stateDir, logFile))
	return os.Remove(filepath.Join(stateDir, eventsFile))
}

func onlyStart(lines []string) bool {
	return len(lines) == 1 && strings.Contains(lines[0], `"type":"session_start"`)
}

// absorb takes from an event what describes its session.
func absorb(m *Meta, e event) {
	p := e.Payload
	str := func(k string) string { s, _ := p[k].(string); return s }
	switch e.Type {
	case "stop":
		if id := str("session_id"); id != "" {
			m.Conversation = id
		}
	case "prompt":
		if m.Title == "" {
			m.Title = TitleFrom(str("text"))
		}
	case "listen":
		if m.Title == "" {
			if h, ok := p["heard"].(map[string]any); ok {
				if t, _ := h["text"].(string); t != "" {
					m.Title = TitleFrom(t)
				}
			}
		}
	}
}

// TitleFrom shortens a prompt into a title.
func TitleFrom(prompt string) string {
	words := strings.Fields(prompt)
	if len(words) == 0 {
		return ""
	}
	if len(words) > 9 {
		words = append(words[:9], "…")
	}
	t := strings.Join(words, " ")
	if r := []rune(t); len(r) > 60 {
		t = string(r[:59]) + "…"
	}
	return t
}

// NoteConversation remembers the agent's own id for the current session's
// conversation, so the session can be continued with it.
func NoteConversation(stateDir, id string) {
	if id == "" {
		return
	}
	if m, ok := Current(stateDir); ok && m.Conversation != id {
		m.Conversation = id
		_ = writeMeta(stateDir, m)
	}
}

// NoteTitle titles the current session after its first prompt.
func NoteTitle(stateDir, prompt string) {
	if m, ok := Current(stateDir); ok && m.Title == "" {
		if t := TitleFrom(prompt); t != "" {
			m.Title = t
			_ = writeMeta(stateDir, m)
		}
	}
}

// End stamps the end of the current session.
func End(stateDir string, now time.Time) {
	if m, ok := Current(stateDir); ok {
		m.Ended = now
		_ = writeMeta(stateDir, m)
	}
}

func summarize(dir string, m Meta, current bool) Summary {
	s := Summary{Meta: m, Current: current, Dir: dir}
	_ = readEvents(filepath.Join(dir, eventsFile), func(_ string, e event) {
		switch e.Type {
		case "prompt":
			if t, _ := e.Payload["text"].(string); t != "" || e.Payload["changes"] != nil {
				s.Prompts++
			}
		case "post_edit":
			s.Edits++
		case "narrate":
			s.Narrations++
		}
		if !e.TS.IsZero() {
			s.Last = e.TS
		}
		if s.Title == "" {
			absorb(&s.Meta, e)
		}
	})
	return s
}

// List returns every session of the repository, the most recently used
// first.
func List(stateDir string) []Summary {
	var out []Summary
	if m, ok := Current(stateDir); ok {
		out = append(out, summarize(stateDir, m, true))
	}
	entries, _ := os.ReadDir(filepath.Join(stateDir, archiveDir))
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dir := filepath.Join(stateDir, archiveDir, e.Name())
		m, ok := Current(dir)
		if !ok {
			continue
		}
		out = append(out, summarize(dir, m, false))
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].active().After(out[j].active()) })
	return out
}

// active is when the session was last used.
func (s Summary) active() time.Time {
	t := s.Started
	for _, other := range []time.Time{s.Last, s.Ended} {
		if other.After(t) {
			t = other
		}
	}
	return t
}

// Find returns the session a key names: an id, the beginning of an id, a
// name or title (or part of one, ignoring case), or "last" for the one
// used last. An ambiguous key is an error that lists the candidates.
func Find(stateDir, key string) (Summary, error) {
	all := List(stateDir)
	if len(all) == 0 {
		return Summary{}, ErrNone
	}
	key = strings.TrimSpace(key)
	if key == "" || key == "last" {
		// The last session is the one that was in use last: the current
		// one, even when an older session was opened again.
		for _, s := range all {
			if s.Current {
				return s, nil
			}
		}
		return all[0], nil
	}
	low := strings.ToLower(key)
	var exact, partial []Summary
	for _, s := range all {
		switch {
		case s.ID == key || strings.EqualFold(s.Name, key):
			exact = append(exact, s)
		case strings.HasPrefix(s.ID, key) || strings.Contains(strings.ToLower(s.Name), low) || strings.Contains(strings.ToLower(s.Title), low):
			partial = append(partial, s)
		}
	}
	found := exact
	if len(found) == 0 {
		found = partial
	}
	switch len(found) {
	case 0:
		return Summary{}, fmt.Errorf("no session matches %q (see `pair sessions`)", key)
	case 1:
		return found[0], nil
	}
	names := make([]string, len(found))
	for i, s := range found {
		names[i] = s.ID + " " + s.Label()
	}
	return Summary{}, fmt.Errorf("%q matches %d sessions: %s", key, len(found), strings.Join(names, "; "))
}

// Rename names a session.
func Rename(stateDir, key, name string) (Summary, error) {
	s, err := Find(stateDir, key)
	if err != nil {
		return s, err
	}
	s.Meta.Name = strings.TrimSpace(name)
	return s, writeMeta(s.Dir, s.Meta)
}

// Restore makes an archived session current again, archiving the one that
// was.
func Restore(stateDir, key string) (Meta, error) {
	s, err := Find(stateDir, key)
	if err != nil {
		return Meta{}, err
	}
	if s.Current {
		return s.Meta, nil
	}
	if err := Archive(stateDir); err != nil {
		return Meta{}, err
	}
	for _, f := range []string{eventsFile, logFile, metaFile} {
		src := filepath.Join(s.Dir, f)
		if _, err := os.Stat(src); err == nil {
			if err := os.Rename(src, filepath.Join(stateDir, f)); err != nil {
				return Meta{}, err
			}
		}
	}
	_ = os.Remove(s.Dir)
	m := s.Meta
	m.Ended = time.Time{}
	return m, writeMeta(stateDir, m)
}
