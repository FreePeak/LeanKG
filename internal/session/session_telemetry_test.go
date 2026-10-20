package session

import (
	"context"
	"sync"
	"testing"

	"github.com/FreePeak/LeanKG/internal/telemetry"
)

type lessonRecorder struct {
	mu    sync.Mutex
	level telemetry.Level
	evs   []telemetry.MemoryEvent
}

func (r *lessonRecorder) Level() telemetry.Level { return r.level }

// SetLevel satisfies telemetry.Recorder; this fake keeps one level for its test.
func (r *lessonRecorder) SetLevel(telemetry.Level) error { return nil }
func (r *lessonRecorder) RecordCall(telemetry.CallEvent) {}
func (r *lessonRecorder) RecordMemory(ev telemetry.MemoryEvent) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.evs = append(r.evs, ev)
}
func (r *lessonRecorder) Close() error { return nil }

// TestAddLessonEmitsLessonEvent pins DS-09 lesson events: a new lesson is a
// written lesson, a repeat is deduped, and both join the caller identity.
func TestAddLessonEmitsLessonEvent(t *testing.T) {
	rec := &lessonRecorder{level: telemetry.Metadata}
	SetRecorder(rec)
	t.Cleanup(func() { SetRecorder(nil) })

	s := New(t.TempDir())
	ctx := telemetry.WithIdentity(context.Background(),
		telemetry.Identity{ClientName: "claude-code", ClientSessionID: "sess-l"}, telemetry.TransportREST)
	if _, err := s.AddLessonCtx(ctx, "sess-l", "always check the stamp"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddLessonCtx(ctx, "sess-l", "always check the stamp"); err != nil {
		t.Fatal(err)
	}
	if len(rec.evs) != 2 {
		t.Fatalf("lesson events = %d, want 2", len(rec.evs))
	}
	first, second := rec.evs[0], rec.evs[1]
	if first.Verb != "lesson" || first.Written != 1 || first.Deduped {
		t.Fatalf("first lesson event = %+v, want written=1 deduped=false", first)
	}
	if second.Written != 0 || !second.Deduped {
		t.Fatalf("repeat lesson event = %+v, want deduped=true written=0", second)
	}
	if first.ClientSessionID != "sess-l" || first.Transport != telemetry.TransportREST {
		t.Fatalf("lesson identity not joined: %+v / %s", first.Identity, first.Transport)
	}
}

// TestAddLessonOffRecordsNothing pins the Off contract for lessons.
func TestAddLessonOffRecordsNothing(t *testing.T) {
	rec := &lessonRecorder{level: telemetry.Off}
	SetRecorder(rec)
	t.Cleanup(func() { SetRecorder(nil) })

	s := New(t.TempDir())
	if _, err := s.AddLesson("sess-off", "a lesson"); err != nil {
		t.Fatal(err)
	}
	if len(rec.evs) != 0 {
		t.Fatalf("Off recorded %d lesson events", len(rec.evs))
	}
}
