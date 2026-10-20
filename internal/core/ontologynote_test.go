package core

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestEmptyMatchesSaysWhyItIsEmpty pins the meaning half of a fix the dormant
// #449 loop PR made and that never merged.
//
// The SHAPE half is already on main via a better generic fix (RS-09's
// emptyLists turns every nil slice into `[]`), so this is what remains:
// measured live on the 298-project self-host 2026-10-11, `ontology cmd=matches`
// on a store with no catalog answers `{"matches":[]}` — an empty ARRAY with
// nothing said about it.
//
// An empty array has two meanings and they want opposite next steps:
//
//	no catalog was ever imported   -> import one (the answer will never change
//	                                  until you do)
//	a catalog is imported, and
//	nothing matched                -> look harder, or the corpus is what it is
//
// A reader that cannot tell them apart looks harder at a corpus that has no
// catalog to match against.
func TestEmptyMatchesSaysWhyItIsEmpty(t *testing.T) {
	dir := t.TempDir()
	e := engineAt(t, dir)

	out, err := e.OntologyMatches()
	if err != nil {
		t.Fatal(err)
	}
	// Shape: the ENGINE returns the raw nil slice (json.Marshal -> null); the
	// ENVELOPE is what guarantees `[]` on the wire, via emptyLists (RS-09).
	// Assert both layers where each is actually guaranteed, so this test does
	// not claim the engine owns a normalisation it does not.
	raw, _ := json.Marshal(out)
	if !strings.Contains(string(raw), `"matches":null`) {
		t.Logf("engine-level marshal is already normalised (fine): %s", raw)
	}
	wire, ok := emptyLists(out).(map[string]any)
	if !ok {
		t.Fatalf("emptyLists did not return the map shape: %T", emptyLists(out))
	}
	wireRaw, _ := json.Marshal(wire)
	if strings.Contains(string(wireRaw), `"matches":null`) {
		t.Fatalf("the envelope left matches as null: %s", wireRaw)
	}
	if !strings.Contains(string(wireRaw), `"matches":[]`) {
		t.Fatalf("the envelope did not produce an empty array: %s", wireRaw)
	}
	// Meaning: with no catalog, say so and say what to do.
	note, _ := out["note"].(string)
	if note == "" {
		t.Fatalf("an empty matches answer carries no note: %s", raw)
	}
	if !strings.Contains(note, "catalog") {
		t.Errorf("the note does not name the cause (a catalog): %q", note)
	}
	if !strings.Contains(note, "import") {
		t.Errorf("the note does not name the next step: %q", note)
	}
	// And a NON-empty answer must NOT carry the note — otherwise the note
	// stops distinguishing anything.
	if err := e.st.KVSet("ontology", "matches", `[{"concept_id":"c","qn":"pkg::A","via":"name"}]`); err != nil {
		t.Fatal(err)
	}
	populated, err := e.OntologyMatches()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := populated["note"]; ok {
		t.Errorf("a populated answer carries the empty-case note: %+v", populated)
	}
}
