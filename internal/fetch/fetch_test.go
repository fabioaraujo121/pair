package fetch

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOnlyThePinnedFileIsKept(t *testing.T) {
	dir := t.TempDir()
	good := filepath.Join(dir, "model.bin")
	if err := os.WriteFile(good, []byte("the model\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sum, err := SHA256(good)
	if err != nil {
		t.Fatal(err)
	}
	pin := Pin{Name: "model.bin", Size: 10, SHA256: sum}
	if err := pin.Verify(good); err != nil {
		t.Fatalf("the right file: %v", err)
	}
	if !pin.SizeMatches(good) || pin.SizeMatches(filepath.Join(dir, "missing")) {
		t.Fatal("size check")
	}

	wrong := filepath.Join(dir, "wrong.bin")
	_ = os.WriteFile(wrong, []byte("the mode1\n"), 0o644) // same size, other content
	if err := pin.Verify(wrong); err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("a changed file must be refused: %v", err)
	}
	short := filepath.Join(dir, "short.bin")
	_ = os.WriteFile(short, []byte("x"), 0o644)
	if err := pin.Verify(short); err == nil || !strings.Contains(err.Error(), "bytes") {
		t.Fatalf("a file of another size must be refused: %v", err)
	}

	// A download that does not match is removed.
	served := filepath.Join(dir, "served.bin")
	_ = os.WriteFile(served, []byte("tampered!\n"), 0o644)
	dest := filepath.Join(dir, "out", "model.bin")
	bad := Pin{Name: "model.bin", URL: "file://" + served, Size: 10, SHA256: sum}
	if err := bad.Download(os.Stderr, dest); err == nil {
		t.Fatal("a tampered download must be refused")
	}
	if _, err := os.Stat(dest); err == nil {
		t.Fatal("the tampered file was kept")
	}
	if _, err := os.Stat(dest + ".part"); err == nil {
		t.Fatal("the partial file was kept")
	}
	// The right content is kept.
	_ = os.WriteFile(served, []byte("the model\n"), 0o644)
	if err := bad.Download(os.Stderr, dest); err != nil {
		t.Fatalf("the right download: %v", err)
	}
	if data, _ := os.ReadFile(dest); string(data) != "the model\n" {
		t.Fatalf("downloaded: %q", data)
	}
	for _, name := range []string{"ggml-large-v3-turbo-q5_0.bin", "ggml-large-v3-turbo.bin"} {
		if _, err := Find(Whisper, name); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := Find(Whisper, "ggml-tiny.bin"); err == nil {
		t.Fatal("an unpinned model must not be downloadable")
	}
}

func TestPinsAreComplete(t *testing.T) {
	for _, list := range [][]Pin{Whisper, Kokoro} {
		for _, p := range list {
			if p.Name == "" || !strings.HasPrefix(p.URL, "https://") || p.Size <= 0 || len(p.SHA256) != 64 {
				t.Errorf("incomplete pin: %+v", p)
			}
			if strings.Contains(p.URL, "/main/") {
				t.Errorf("%s: the URL follows a branch, not a fixed revision", p.Name)
			}
		}
	}
}
