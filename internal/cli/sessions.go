package cli

import (
	"fmt"
	"io"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/fabioaraujo121/pair/internal/config"
	"github.com/fabioaraujo121/pair/internal/session"
)

// Sessions lists the pairing sessions of this repository, newest first.
func Sessions(w io.Writer) error {
	cfg := config.Load("")
	all := session.List(cfg.StateDir)
	if len(all) == 0 {
		// A log written before sessions were kept apart still counts.
		if err := session.Archive(cfg.StateDir); err != nil {
			return err
		}
		all = session.List(cfg.StateDir)
	}
	if len(all) == 0 {
		fmt.Fprintln(w, "no sessions yet in this repository; start one with `pair`")
		return nil
	}
	running := hasSession(cfg.Session)
	now := time.Now()
	fmt.Fprintf(w, "  %-15s  %-40s  %-13s  %6s  %7s  %5s\n", "ID", "NAME", "STARTED", "LENGTH", "PROMPTS", "EDITS")
	for _, s := range all {
		mark := " "
		if s.Current {
			mark = "•"
			if running {
				mark = "▶"
			}
		}
		name := s.Label()
		if s.Name == "" && s.Title != "" {
			name = s.Title
		} else if s.Name == "" {
			name = "(unnamed)"
		}
		fmt.Fprintf(w, "%s %-15s  %-40s  %-13s  %6s  %7d  %5d\n", mark, s.ID, pad(name, 40), when(s.Started, now), length(s.Length()), s.Prompts, s.Edits)
	}
	fmt.Fprintln(w)
	fmt.Fprintln(w, "▶ running   • last one, not running")
	fmt.Fprintln(w, "pair --continue            continue the last session, with Claude's conversation")
	fmt.Fprintln(w, "pair resume <id or name>   open a session again")
	fmt.Fprintln(w, "pair rename <id or name> <new name>      pair log <id or name>      pair replay <id or name>")
	return nil
}

// Rename names a session: `pair rename <new name>` for the current one,
// `pair rename <id or name> <new name>` for any.
func Rename(args []string, w io.Writer) error {
	cfg := config.Load("")
	key, name := "last", strings.Join(args, " ")
	if len(args) >= 2 {
		if _, err := session.Find(cfg.StateDir, args[0]); err == nil {
			key, name = args[0], strings.Join(args[1:], " ")
		}
	}
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("usage: pair rename [<id or name>] <new name>")
	}
	if cur, ok := session.Current(cfg.StateDir); ok && key == "last" {
		key = cur.ID
	}
	s, err := session.Rename(cfg.StateDir, key, name)
	if err != nil {
		return err
	}
	fmt.Fprintf(w, "session %s is now %q\n", s.ID, s.Name)
	return nil
}

// SessionEvents returns the event log a key names, for `pair log` and
// `pair replay`.
func SessionEvents(key string) (string, error) {
	cfg := config.Load("")
	s, err := session.Find(cfg.StateDir, key)
	if err != nil {
		return "", err
	}
	return s.EventsPath(), nil
}

func pad(s string, n int) string {
	if utf8.RuneCountInString(s) > n {
		r := []rune(s)
		return string(r[:n-1]) + "…"
	}
	return s
}

// when formats a start time relative to today.
func when(t, now time.Time) string {
	if t.IsZero() {
		return "?"
	}
	t, now = t.Local(), now.Local()
	y, m, d := now.Date()
	today := time.Date(y, m, d, 0, 0, 0, 0, now.Location())
	switch {
	case !t.Before(today):
		return "today " + t.Format("15:04")
	case !t.Before(today.AddDate(0, 0, -1)):
		return "yesterday " + t.Format("15:04")
	case t.Year() == now.Year():
		return t.Format("Jan 2 15:04")
	default:
		return t.Format("Jan 2 2006")
	}
}

func length(d time.Duration) string {
	switch {
	case d <= 0:
		return "-"
	case d < time.Minute:
		return "<1m"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	default:
		return fmt.Sprintf("%dh%02dm", int(d.Hours()), int(d.Minutes())%60)
	}
}
