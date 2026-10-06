package plain

import "testing"

func TestControlCharactersAreRemoved(t *testing.T) {
	in := "clear\x1b[2J title\x1b]0;x\x07 csi\u009b31m nul\x00 tab\there\r\nnext ação 🎙"
	if got, want := Text(in), "clear[2J title]0;x csi31m nul tab here \nnext ação 🎙"; got != want {
		t.Fatalf("Text:\n got %q\nwant %q", got, want)
	}
	if got, want := Line(in), "clear[2J title]0;x csi31m nul tab here  next ação 🎙"; got != want {
		t.Fatalf("Line:\n got %q\nwant %q", got, want)
	}
	if Text("") != "" || Line("plain text") != "plain text" {
		t.Fatal("plain text is left as it is")
	}
}
