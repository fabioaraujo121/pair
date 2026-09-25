package speech

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestNormalize(t *testing.T) {
	cases := []struct{ in, want string }{
		{"Vou criar a **migration** no `users_controller` e abrir o PR.", "Vou criar a migration no users controller e abrir o pull request."},
		{"# Plano\n- primeiro item\n- segundo item\n", "Plano primeiro item segundo item"},
		{"Veja [a doc](https://x.dev) e o ActiveRecord::Base.", "Veja a doc e o Active Record::Base."},
		{"código:\n```go\nfunc x() {}\n```\nfim", "código: fim"},
		{"> citação _leve_ e __forte__", "citação leve e forte"},
		{"isValidEmail vira emailLooksValid", "is Valid Email vira email Looks Valid"},
		{"   muitos    espaços\n\n\nlinhas  ", "muitos espaços linhas"},
	}
	for _, c := range cases {
		if got := Normalize(c.in, nil); got != c.want {
			t.Errorf("Normalize(%q)\n got %q\nwant %q", c.in, got, c.want)
		}
	}
	if got := Normalize("o DB e o ObraFlow", map[string]string{"ObraFlow": "Obra Flow", "DB": "banco"}); got != "o banco e o Obra Flow" {
		t.Errorf("extra vocabulary: %q", got)
	}
}

// fakeSay writes a `say` stand-in that records its arguments and stdin and
// optionally sleeps (FAKE_SAY_SLEEP seconds) before writing a done marker.
func fakeSay(t *testing.T) (bin, dir string) {
	t.Helper()
	dir = t.TempDir()
	bin = filepath.Join(dir, "say")
	script := `#!/bin/sh
printf '%s\n' "$*" > "$FAKE_SAY_DIR/args"
cat > "$FAKE_SAY_DIR/text"
if [ -n "$FAKE_SAY_SLEEP" ]; then sleep "$FAKE_SAY_SLEEP"; fi
echo done > "$FAKE_SAY_DIR/done"
`
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FAKE_SAY_DIR", dir)
	return bin, dir
}

func TestSaySpeaksTextOnStdinAndBlocks(t *testing.T) {
	bin, dir := fakeSay(t)
	t.Setenv("FAKE_SAY_SLEEP", "0.3")
	s := &Say{Voice: "Luciana", Rate: 190, Bin: bin}
	start := time.Now()
	if err := s.Speak(context.Background(), "olá mundo"); err != nil {
		t.Fatal(err)
	}
	if time.Since(start) < 250*time.Millisecond {
		t.Fatal("Speak returned before say finished")
	}
	args, _ := os.ReadFile(filepath.Join(dir, "args"))
	if strings.TrimSpace(string(args)) != "-v Luciana -r 190" {
		t.Fatalf("args = %q", args)
	}
	text, _ := os.ReadFile(filepath.Join(dir, "text"))
	if string(text) != "olá mundo" {
		t.Fatalf("text = %q", text)
	}
	if _, err := os.Stat(filepath.Join(dir, "done")); err != nil {
		t.Fatal("done marker missing")
	}
	if err := s.Speak(context.Background(), "   "); err != nil {
		t.Fatalf("blank text must be a no-op: %v", err)
	}
}

func TestSayIsInterruptedByContext(t *testing.T) {
	bin, dir := fakeSay(t)
	t.Setenv("FAKE_SAY_SLEEP", "5")
	s := &Say{Bin: bin}
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	start := time.Now()
	err := s.Speak(ctx, "uma narração longa")
	if err == nil || ctx.Err() == nil {
		t.Fatalf("expected a context error, got %v", err)
	}
	if time.Since(start) > 2*time.Second {
		t.Fatalf("Speak took %s after cancellation", time.Since(start))
	}
	if _, err := os.Stat(filepath.Join(dir, "done")); err == nil {
		t.Fatal("say was not stopped: done marker written")
	}
}
