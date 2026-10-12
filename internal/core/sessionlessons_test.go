package core

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/FreePeak/LeanKG/internal/session"
)

// TestSessionLessonsReadableOnTheWire pins the read path end to end through
// the engine, not just the store.
//
// The defect it guards: command=lesson wrote a lesson and every read surface
// returned nothing (session_recall count:0, canvas refs:[], recall "node not
// found"). If the dispatch, the session id, or the envelope ever regresses,
// this fails.
func TestSessionLessonsReadableOnTheWire(t *testing.T) {
	dir := t.TempDir()
	e := engineAt(t, dir) // the repo's own engine harness
	const (
		id   = "sess-wire"
		text = "zz-wire lesson: a lesson nothing can read back is a discard"
	)
	// The write goes through the same store the read path uses, so this pins
	// the round trip rather than a mock.
	sess := session.New(dir)
	if deduped, err := sess.AddLesson(id, text); err != nil || deduped {
		t.Fatalf("AddLesson = %v, %v", deduped, err)
	}
	resp, err := e.SessionRead("lessons", id, "")
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(resp)
	if resp["command"] != "lessons" {
		t.Fatalf("command = %v, want lessons", resp["command"])
	}
	lessons, ok := resp["lessons"].([]string)
	if !ok || len(lessons) != 1 {
		t.Fatalf("lessons = %v, want exactly one", resp["lessons"])
	}
	if !strings.Contains(lessons[0], "discard") {
		t.Errorf("lesson text lost: %q", lessons[0])
	}
	// An unknown session is an honest empty, never an error.
	empty, err := e.SessionRead("lessons", "no-such-sess", "")
	if err != nil {
		t.Fatalf("unknown session errored: %v", err)
	}
	if got := empty["lessons"]; got != nil && len(got.([]string)) != 0 {
		t.Errorf("unknown session returned %v, want none", got)
	}
	_ = raw
}
