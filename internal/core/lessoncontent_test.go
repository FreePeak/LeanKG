package core

import (
	"context"
	"strings"
	"testing"

	"github.com/FreePeak/LeanKG/internal/session"
)

// TestSessionLessonRequiresContent pins the two halves of one defect, both
// measured live on the 298-project self-host 2026-10-11 against main:
//
//	import action=session command=lesson session_id=s summary="…"
//	  -> {"deduped":false}, and lessons.json held {"text":""}
//	import action=session command=lesson session_id=s      (no content at all)
//	  -> {"deduped":false}, the SAME empty row (sha256 of "")
//
// The import schema advertises `summary` for this arm; sessionWrite read only
// `text`. So a schema-shaped call was a successful write of nothing, and the
// second such call reported `deduped: true` — a duplicate of nothing.
func TestSessionLessonRequiresContent(t *testing.T) {
	dir := t.TempDir()
	e := engineAt(t, dir)

	// 1. `text` still works, unchanged.
	if _, err := e.Import(context.Background(), lessonReq("s1", map[string]any{"text": "text spelling works"})); err != nil {
		t.Fatalf("text= rejected: %v", err)
	}
	// 2. `summary` — the field the schema names — lands too.
	if _, err := e.Import(context.Background(), lessonReq("s1", map[string]any{"summary": "summary spelling works"})); err != nil {
		t.Fatalf("summary= rejected: %v", err)
	}
	// 3. NO content at all is REFUSED, not stored empty.
	_, err := e.Import(context.Background(), lessonReq("s1", nil))
	if err == nil {
		t.Fatal("an empty lesson was accepted — a write that cannot report itself empty cannot be wrong loudly")
	}
	if !strings.Contains(err.Error(), "requires text") {
		t.Errorf("refusal does not name the fix: %v", err)
	}
	// 4. Whitespace is empty too.
	if _, err := e.Import(context.Background(), lessonReq("s1", map[string]any{"text": "   "})); err == nil {
		t.Error("whitespace-only lesson accepted")
	}

	// The store holds exactly the two real lessons — no empty row.
	lessons, err := session.New(dir).Lessons("s1")
	if err != nil {
		t.Fatal(err)
	}
	if len(lessons) != 2 {
		t.Fatalf("stored %d lessons, want the 2 real ones (an empty row means a half-fix): %q", len(lessons), lessons)
	}
	for _, l := range lessons {
		if strings.TrimSpace(l) == "" {
			t.Errorf("an empty lesson reached the store: %q", lessons)
		}
	}
}

func lessonReq(sessionID string, extra map[string]any) ImportRequest {
	args := map[string]any{"session_id": sessionID}
	for k, v := range extra {
		args[k] = v
	}
	return ImportRequest{Action: "session", Command: "lesson", Args: args}
}
