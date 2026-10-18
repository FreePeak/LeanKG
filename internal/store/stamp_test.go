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
