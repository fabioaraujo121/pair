// Package fetch downloads the files pair needs that are too large to ship
// with it: speech models. Every file is pinned: a fixed URL that cannot
// change under the same name, its size, and its SHA-256. A download that
// does not match is removed and reported; nothing unknown is fetched.
package fetch

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
)

// Pin is one file pair may download.
type Pin struct {
	Name   string // the file name on disk
	URL    string // an immutable address: a commit, a release tag
	Size   int64  // bytes
	SHA256 string // hex
}

// ErrUnknown is returned for a file pair has no pin for.
var ErrUnknown = errors.New("no pinned download for this file")

// Find returns the pin of a file by name.
func Find(pins []Pin, name string) (Pin, error) {
	for _, p := range pins {
		if p.Name == name {
			return p, nil
		}
	}
	return Pin{}, fmt.Errorf("%w: %s", ErrUnknown, name)
}

// Download fetches the file to dest through curl, writing progress to w,
// and keeps it only when its size and SHA-256 are the pinned ones.
func (p Pin) Download(w io.Writer, dest string) error {
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	tmp := dest + ".part"
	cmd := exec.Command("curl", "-L", "--fail", "--progress-bar", "-o", tmp, p.URL)
	cmd.Stdout, cmd.Stderr = w, w
	if err := cmd.Run(); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("download %s: %w", p.URL, err)
	}
	if err := p.Verify(tmp); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, dest)
}

// Verify reports whether the file at path is the pinned one.
func (p Pin) Verify(path string) error {
	fi, err := os.Stat(path)
	if err != nil {
		return err
	}
	if fi.Size() != p.Size {
		return fmt.Errorf("%s is %d bytes, the pinned %s is %d: not the expected file", path, fi.Size(), p.Name, p.Size)
	}
	sum, err := SHA256(path)
	if err != nil {
		return err
	}
	if sum != p.SHA256 {
		return fmt.Errorf("%s does not match the pinned %s (sha256 %s, want %s): not the expected file", path, p.Name, sum[:12], p.SHA256[:12])
	}
	return nil
}

// SizeMatches is the cheap check for a file already on disk: hashing a
// model of more than a gigabyte takes seconds, comparing sizes does not.
func (p Pin) SizeMatches(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.Size() == p.Size
}

// SHA256 returns the hex digest of a file.
func SHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
