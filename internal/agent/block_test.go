package agent

import "testing"

func TestRemoveBlock(t *testing.T) {
	block := "<!-- pair:start -->\n## Pairing mode\nrules\n<!-- pair:end -->\n"
	if out, had := RemoveBlock(block); !had || out != "" {
		t.Fatalf("a file of only the block empties: %q %v", out, had)
	}
	if out, had := RemoveBlock("# Mine\n\ntext\n\n" + block); !had || out != "# Mine\n\ntext\n" {
		t.Fatalf("the block at the end: %q %v", out, had)
	}
	if out, had := RemoveBlock(block + "\n# After\ntext\n"); !had || out != "\n# After\ntext\n" {
		t.Fatalf("the block at the start: %q %v", out, had)
	}
	if out, had := RemoveBlock("# Mine\n"); had || out != "# Mine\n" {
		t.Fatalf("no block: untouched: %q %v", out, had)
	}
}
