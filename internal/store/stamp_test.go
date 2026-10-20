package store

import "testing"

func TestModelStampMatchesIgnoresProvider(t *testing.T) {
	a := ModelStamp{ModelID: "m", Revision: "r", Dimensions: 10, Distance: "cos", ChunkerVersion: 1, QueryPrefix: "q", DocumentPrefix: "d", Provider: "local"}
	b := a
	b.Provider = "openai-compatible"
	if !a.Matches(b) {
		t.Fatal("provider alias should not break identity")
	}
	b.Dimensions = 11
	if a.Matches(b) {
		t.Fatal("dimension change must break identity")
	}
}

// TestModelStampMatchesIgnoresTextCap pins that the recorded text cap is
// provenance, not identity: two stamps that differ only in TextCap describe
// the same vector space, so Matches must still report them equal.
func TestModelStampMatchesIgnoresTextCap(t *testing.T) {
	a := ModelStamp{ModelID: "m", Revision: "r", Dimensions: 10, Distance: "cos", ChunkerVersion: 1, QueryPrefix: "q", DocumentPrefix: "d", Provider: "local", TextCap: 8000}
	b := a
	b.TextCap = 1000
	if !a.Matches(b) {
		t.Fatal("text cap must not participate in Matches")
	}
}
