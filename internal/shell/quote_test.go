package shell

import (
	"os/exec"
	"testing"
)

func TestJoinSurvivesAShell(t *testing.T) {
	words := []string{"printf", "%s|", "plain", "two words", "it's", "$(id)", "--name=x y"}
	out, err := exec.Command("/bin/sh", "-c", Join(words)).Output()
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(out), "plain|two words|it's|$(id)|--name=x y|"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}
