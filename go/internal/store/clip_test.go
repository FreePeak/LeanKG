package store

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// TestClipUTF8NeverSplitsARune pins the byte class PostgreSQL rejects: every
// prefix length of a multi-byte string must come back valid and inside budget.
// The previous `RuneStart` backoff failed here at 1 and 2 bytes into U+2026 —
// it stopped on the lone lead byte 0xE2, which is still an incomplete rune.
func TestClipUTF8NeverSplitsARune(t *testing.T) {
	s := "ab…cd⌘e" // 11 bytes: boundaries at 3,4,9,10 all split a rune
	for n := 0; n <= len(s)+2; n++ {
		got := ClipUTF8(s, n)
		if len(got) > n {
			t.Errorf("ClipUTF8(%d) = %q: %d bytes over budget", n, got, len(got))
		}
		if !utf8.ValidString(got) {
			t.Errorf("ClipUTF8(%d) = %q: invalid UTF-8 (PostgreSQL rejects the whole batch)", n, got)
		}
	}
	if got := ClipUTF8(s, len(s)); got != s {
		t.Errorf("at budget ClipUTF8 must be identity, got %q", got)
	}
	if got := ClipUTF8(s, 3); got != "ab" {
		t.Errorf("clip at a rune start: got %q, want %q", got, "ab")
	}
	if got := ClipUTF8("longer", 0); got != "" {
		t.Errorf("zero budget must be empty, got %q", got)
	}
}

// TestValidTextGuardsForeignContent covers the write boundary: invalid bytes
// from any producer arrive as U+FFFD so both engines store the same text,
// instead of one engine storing bytes the other refuses.
func TestValidTextGuardsForeignContent(t *testing.T) {
	if ValidText("plain") != "plain" {
		t.Error("valid text must pass through untouched")
	}
	bad := "ok" + string([]byte{0xE2, 0x80}) + "tail" // truncated ellipsis
	got := ValidText(bad)
	if !utf8.ValidString(got) {
		t.Fatalf("ValidText must produce valid UTF-8, got %q", got)
	}
	if !strings.Contains(got, "ok") || !strings.Contains(got, "tail") {
		t.Errorf("only the invalid bytes may be replaced, got %q", got)
	}
	e := Element{Name: "na" + string([]byte{0xFF}) + "me", Content: bad}
	if s := e.sanitize(); utf8.ValidString(s.Name) && utf8.ValidString(s.Content) {
		if s.Name == e.Name || s.Content == e.Content {
			t.Error("sanitize must replace the invalid fields")
		}
	} else {
		t.Error("sanitize left invalid bytes in the element")
	}
}
