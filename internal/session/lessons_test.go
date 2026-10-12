package session

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestLessonsAreReadable pins that a recorded lesson is not write-only.
//
// Measured on the live self-host 2026-10-11: `import action=session
// command=lesson` wrote `.leankg/sessions/<sid>/lessons.json` and answered
// {"deduped":false}, but NOTHING could read it back — session_recall
// (memory banks) saw 0, session canvas saw 0 refs, offload-recall reported
// "node not found". A lesson was a black hole: it could be written and never
// heard from again.
//
// This is the exact shape #470 fixed for reflect. A lesson the next session
// cannot read is not a lesson, it is a discard with a JSON wrapper.
func TestLessonsAreReadable(t *testing.T) {
	dir := t.TempDir()
	s := New(dir)
	const (
		id   = "sess-lessons"
		text = "zz-lesson lesson: never parse a trust boundary with a regex"
	)
	if deduped, err := s.AddLesson(id, text); err != nil || deduped {
		t.Fatalf("AddLesson returned deduped=%v err=%v, want a first write", deduped, err)
	}
	// The same text twice must still read back as ONE lesson.
	if deduped, err := s.AddLesson(id, text); err != nil || !deduped {
		t.Fatalf("repeat AddLesson returned deduped=%v err=%v, want the dedupe", deduped, err)
	}
	got, err := s.Lessons(id)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("Lessons = %d, want 1", len(got))
	}
	if !strings.Contains(got[0], "trust boundary") {
		t.Errorf("lesson lost its text: %q", got[0])
	}
	// An unknown session is an empty list, never an error — a fresh session
	// asking "what did I learn here" must not be told off.
	none, err := s.Lessons("no-such-session")
	if err != nil {
		t.Errorf("unknown session errored: %v", err)
	}
	if len(none) != 0 {
		t.Errorf("unknown session returned %d lessons, want none", len(none))
	}
	// And the file it reads must be the one addLesson writes. New() derives
	// Root as <projectDir>/.leankg/sessions, so the file sits one segment
	// deeper than the project dir.
	if _, err := os.Stat(filepath.Join(dir, ".leankg", "sessions", id, "lessons.json")); err != nil {
		t.Errorf("lessons.json not where the reader looks: %v", err)
	}
}
