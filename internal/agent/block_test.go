package agent

import (
	"strings"
	"testing"
)

func TestPairingBlockLanguage(t *testing.T) {
	pt := PairingBlock("pt")
	if !strings.Contains(pt, "Speak Portuguese.") || !strings.HasPrefix(pt, blockStart) || !strings.HasSuffix(pt, blockEnd+"\n") {
		t.Fatalf("block:\n%s", pt)
	}
	if !strings.Contains(PairingBlock("xx"), "Speak xx.") {
		t.Fatal("unknown codes fall back to the code")
	}
	for _, tool := range []string{"`narrate`", "`show`", "`ask`", "PAIRING.md"} {
		if !strings.Contains(pt, tool) {
			t.Fatalf("block must mention %s", tool)
		}
	}
}

func TestUpsertBlock(t *testing.T) {
	block := PairingBlock("pt")
	if got := UpsertBlock("", block); got != block {
		t.Fatalf("empty file: %q", got)
	}
	existing := "# My project\n\nRun `bin/setup` first."
	once := UpsertBlock(existing, block)
	if !strings.HasPrefix(once, existing+"\n\n"+blockStart) {
		t.Fatalf("append: %q", once)
	}
	twice := UpsertBlock(once, block)
	if twice != once {
		t.Fatalf("not idempotent:\n%s\n---\n%s", once, twice)
	}
	updated := UpsertBlock(once+"\n## After\ntext\n", PairingBlock("en"))
	if strings.Count(updated, blockStart) != 1 || !strings.Contains(updated, "Speak English.") || strings.Contains(updated, "Speak Portuguese.") || !strings.HasSuffix(updated, "## After\ntext\n") {
		t.Fatalf("replace in the middle:\n%s", updated)
	}
}
