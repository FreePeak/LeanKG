package core

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestSessionLessonReadsTheFieldTheSchemaNames pins the one session-write defect
// wave 15 found by driving the schema's own shape over stdio.
//
// The import schema advertises, for `action=session`:
// `command: offload | lesson`, `summary` ("offload summary"), `text`,
// `session_id`, `node_id`, `payload`. `command=lesson` therefore has a
// documented content field — and the schema names `summary`.
//
// `sessionWrite` reads `text` and ignores `summary` entirely. So a
// schema-shaped lesson call writes an EMPTY lesson and answers
// `{"deduped":false}` — a successful write of nothing, reported as a success.
// The agent has recorded that it learned something; the store holds a blank.
//
// This is not a fresh class: wave 6 fixed exactly this for memory curation
// (`withFlatArgs` folds the flat fields into `Args`) and wave 7 found the same
// silent success on the REST retain route. The lesson arm was missed because
// no battery had called it — every earlier drive read memory and offloaded
// sessions, and never once wrote a lesson.
//
// The fix has two halves, and the second is the one that matters: `summary`
// feeds `text` for the lesson arm, AND an empty lesson is REFUSED rather than
// written. Accepting an empty lesson is what let the mismatch hide — a write
// that cannot report itself empty is a write that cannot be wrong loudly.
func TestSessionLessonReadsTheFieldTheSchemaNames(t *testing.T) {
	e, _ := newEngine(t)
	dir := t.TempDir()
	e.SetProjectDir(dir)
	ctx := context.Background()

	// The schema's field.
	if _, err := e.Import(ctx, ImportRequest{
		Action: "session", Command: "lesson", SessionID: "s1",
		Summary: "use retrieval before bash",
	}); err != nil {
		t.Fatalf("lesson via the schema's summary field: %v", err)
	}
	if got := readLessons(t, dir, "s1"); !strings.Contains(got, "use retrieval before bash") {
		t.Fatalf("a lesson written via summary is missing from the store: %s", got)
	}

	// The field the code used to read still works; both spellings land.
	if _, err := e.Import(ctx, ImportRequest{
		Action: "session", Command: "lesson", SessionID: "s1",
		Text: "prefer the graph verbs for impact questions",
	}); err != nil {
		t.Fatalf("lesson via text: %v", err)
	}
	got := readLessons(t, dir, "s1")
	for _, want := range []string{"use retrieval before bash", "prefer the graph verbs for impact questions"} {
		if !strings.Contains(got, want) {
			t.Errorf("lesson %q is missing from the store: %s", want, got)
		}
	}

	// A lesson that says nothing is REFUSED. This is the half that would have
	// surfaced the mismatch on the first call instead of after an agent had
	// recorded the write.
	for _, blank := range []string{"", "   ", "\n\t "} {
		if _, err := e.Import(ctx, ImportRequest{
			Action: "session", Command: "lesson", SessionID: "s1", Summary: blank,
		}); err == nil {
			t.Errorf("an empty lesson (%q) must be refused, not stored", blank)
		}
	}
	// And a refused lesson leaves the store exactly as it was.
	if after := readLessons(t, dir, "s1"); strings.Count(after, `"sha256"`) != 2 {
		t.Errorf("a refused lesson must not be written; the store now has %d lessons:\n%s",
			strings.Count(after, `"sha256"`), after)
	}

	// Dedup still works, on the content that actually landed.
	if _, err := e.Import(ctx, ImportRequest{
		Action: "session", Command: "lesson", SessionID: "s1", Summary: "use retrieval before bash",
	}); err != nil {
		t.Fatal(err)
	}
	if after := readLessons(t, dir, "s1"); strings.Count(after, `"sha256"`) != 2 {
		t.Errorf("a repeated lesson must dedupe, not append: %d lessons:\n%s",
			strings.Count(after, `"sha256"`), after)
	}
}

// readLessons returns the raw lessons file for one session, so the test asserts
// on what was WRITTEN rather than on a return value the engine could shape.
func readLessons(t *testing.T, projectDir, sessionID string) string {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(projectDir, ".leankg", "sessions", sessionID, "lessons.json"))
	if err != nil {
		t.Fatalf("read lessons for %s: %v", sessionID, err)
	}
	return string(body)
}
