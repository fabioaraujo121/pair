package logview

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"golang.org/x/term"

	"github.com/fabioaraujo121/pair/internal/replay"
)

// keep is how many events are re-rendered when the pane is resized.
const keep = 400

// Follow renders the events file and keeps rendering what is appended to
// it, until ctx is done. When the terminal is resized everything on screen
// is wrapped again for the new width.
func Follow(ctx context.Context, path, root string, out *os.File) error {
	color := term.IsTerminal(int(out.Fd()))
	width := func() int {
		if w, _, err := term.GetSize(int(out.Fd())); err == nil && w > 0 {
			return w
		}
		return 80
	}
	printer := &Printer{W: out, Width: width(), Color: color}
	builder := &Builder{Root: root}
	var events []replay.Event
	var offset int64
	var partial []byte

	redraw := func() {
		if color {
			_, _ = io.WriteString(out, "\x1b[2J\x1b[3J\x1b[H")
		}
		printer.Reset()
		printer.Width = width()
		builder = &Builder{Root: root}
		for _, e := range events {
			if entries, ok := builder.Add(e); ok {
				for _, en := range entries {
					printer.Print(en)
				}
			}
		}
	}

	read := func() {
		fi, err := os.Stat(path)
		if err != nil {
			return
		}
		if fi.Size() < offset { // a new session truncated the log
			offset, partial, events = 0, nil, nil
			redraw()
		}
		if fi.Size() == offset {
			return
		}
		f, err := os.Open(path)
		if err != nil {
			return
		}
		defer f.Close()
		if _, err := f.Seek(offset, io.SeekStart); err != nil {
			return
		}
		data, err := io.ReadAll(f)
		if err != nil {
			return
		}
		offset += int64(len(data))
		data = append(partial, data...)
		last := bytes.LastIndexByte(data, '\n')
		if last < 0 {
			partial = data
			return
		}
		partial = append([]byte(nil), data[last+1:]...)
		sc := bufio.NewScanner(bytes.NewReader(data[:last+1]))
		sc.Buffer(make([]byte, 1<<20), 1<<24)
		for sc.Scan() {
			line := strings.TrimSpace(sc.Text())
			if line == "" {
				continue
			}
			var e replay.Event
			if json.Unmarshal([]byte(line), &e) != nil {
				continue
			}
			events = append(events, e)
			if len(events) > keep {
				events = events[len(events)-keep:]
			}
			if entries, ok := builder.Add(e); ok {
				for _, en := range entries {
					printer.Print(en)
				}
			}
		}
	}

	resized := make(chan os.Signal, 1)
	signal.Notify(resized, syscall.SIGWINCH)
	defer signal.Stop(resized)
	tick := time.NewTicker(200 * time.Millisecond)
	defer tick.Stop()

	redraw()
	read()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-resized:
			redraw()
		case <-tick.C:
			read()
		}
	}
}

// Render writes the whole events file once, for a fixed width.
func Render(path, root string, out io.Writer, width int, color bool) error {
	events, err := replay.Load(path)
	if err != nil {
		return err
	}
	printer := &Printer{W: out, Width: width, Color: color}
	builder := &Builder{Root: root}
	for _, e := range events {
		if entries, ok := builder.Add(e); ok {
			for _, en := range entries {
				printer.Print(en)
			}
		}
	}
	return nil
}
