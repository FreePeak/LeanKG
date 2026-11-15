package sessionlink

import (
	"context"
	"errors"
	"testing"
)

// countingParse returns a parse func that counts its calls through *n.
func countingParse(n *int, v string, err error) func() (string, error) {
	return func() (string, error) {
		*n++
		return v, err
	}
}

func TestParsedCallsParseOncePerKey(t *testing.T) {
	r := NewReader()
	var n int
	for i := 0; i < 3; i++ {
		v, err := Parsed(r, "a.jsonl", countingParse(&n, "events-a", nil))
		if err != nil || v != "events-a" {
			t.Fatalf("Parsed = %q, %v; want events-a, nil", v, err)
		}
	}
	if n != 1 {
		t.Fatalf("parse calls for one key = %d, want 1", n)
	}
	if _, err := Parsed(r, "b.jsonl", countingParse(&n, "events-b", nil)); err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("parse calls after a second key = %d, want 2", n)
	}
}

func TestParsedNilReaderParsesEveryCall(t *testing.T) {
	var r *Reader
	var n int
	for i := 0; i < 3; i++ {
		if _, err := Parsed(r, "a.jsonl", countingParse(&n, "x", nil)); err != nil {
			t.Fatal(err)
		}
	}
	if n != 3 {
		t.Fatalf("parse calls with a nil reader = %d, want 3", n)
	}
}

func TestParsedKeepsAnErrorForTheKey(t *testing.T) {
	r := NewReader()
	var n int
	want := ErrUnsupportedVersion
	for i := 0; i < 2; i++ {
		if _, err := Parsed(r, "bad.jsonl", countingParse(&n, "", want)); !errors.Is(err, want) {
			t.Fatalf("Parsed error = %v, want %v", err, want)
		}
	}
	if n != 1 {
		t.Fatalf("parse calls for a failing key = %d, want 1", n)
	}
}

func TestParsedDoesNotKeepContextErrors(t *testing.T) {
	r := NewReader()
	var n int
	if _, err := Parsed(r, "a.jsonl", countingParse(&n, "", context.Canceled)); !errors.Is(err, context.Canceled) {
		t.Fatalf("first error = %v, want context.Canceled", err)
	}
	v, err := Parsed(r, "a.jsonl", countingParse(&n, "fresh", nil))
	if err != nil || v != "fresh" {
		t.Fatalf("after a cancelled parse = %q, %v; want fresh, nil", v, err)
	}
	if n != 2 {
		t.Fatalf("parse calls = %d, want 2 (the cancelled result must not be kept)", n)
	}
}

func TestParsedReparsesWhenTypeDiffers(t *testing.T) {
	r := NewReader()
	var n int
	if _, err := Parsed(r, "a.jsonl", countingParse(&n, "text", nil)); err != nil {
		t.Fatal(err)
	}
	if v, err := Parsed(r, "a.jsonl", func() (int, error) { n++; return 7, nil }); err != nil || v != 7 {
		t.Fatalf("Parsed under another type = %d, %v; want 7, nil", v, err)
	}
	if n != 2 {
		t.Fatalf("parse calls = %d, want 2", n)
	}
}
