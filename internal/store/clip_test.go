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

// TestClipUTF8AtRuneEdges pins the cut at the edges a snippet meets: a 4-byte
// emoji, a ZWJ sequence, a combining mark and invalid input. Output must be
// valid UTF-8 and inside budget. A ZWJ sequence or a combining mark can be
// separated from its base at a valid boundary (the cut keeps whole runes, not
// whole graphemes), so those cases pin the separated form. This is a
// characterization of the current behaviour, not a red-first change: the
// function already produced valid output at every case here.
func TestClipUTF8AtRuneEdges(t *testing.T) {
	const (
		emoji   = "😀" // U+1F600, 4 bytes
		zwj     = "‍" // zero-width joiner, 3 bytes
		man     = "👨" // U+1F468, 4 bytes
		woman   = "👩" // U+1F469, 4 bytes
		combine = "́" // combining acute accent, 2 bytes
	)
	cases := []struct {
		name string
		s    string
		max  int
		want string
	}{
		{"4-byte emoji whole", "a" + emoji + "b", 5, "a" + emoji},
		{"4-byte emoji cut at its lead byte", "a" + emoji + "b", 2, "a"},
		{"4-byte emoji cut mid-rune", "a" + emoji + "b", 4, "a"},
		{"ZWJ sequence cut at the first joiner byte", man + zwj + woman, 5, man},
		{"ZWJ sequence cut after the joiner", man + zwj + woman, 8, man + zwj},
		{"combining mark separated from its base", "e" + combine + "x", 1, "e"},
		{"combining mark cut mid-rune", "e" + combine + "x", 2, "e"},
		{"combining mark kept with its base", "e" + combine + "x", 3, "e" + combine},
		{"truncated 3-byte rune at the cut", "abc\xe2\x80" + "tail", 5, "abc"},
		{"invalid byte at the cut", "ab\xffcd", 3, "ab"},
		{"invalid byte mid-string, cut after it", "a\xffbc", 2, "a"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ClipUTF8(tc.s, tc.max)
			if !utf8.ValidString(got) {
				t.Fatalf("ClipUTF8 = %q: invalid UTF-8", got)
			}
			if len(got) > tc.max {
				t.Errorf("ClipUTF8 = %d bytes, budget %d", len(got), tc.max)
			}
			if got != tc.want {
				t.Errorf("ClipUTF8(%q, %d) = %q, want %q", tc.s, tc.max, got, tc.want)
			}
		})
	}
}
