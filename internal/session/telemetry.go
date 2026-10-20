package session

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/FreePeak/LeanKG/internal/telemetry"
)

// Lesson events (plan v4.15 DS-09). The session package reports lessons to
// one process-wide sink, a no-op until SetRecorder installs a real one. Off
// capture builds nothing.

type recorderBox struct{ rec telemetry.Recorder }

var activeRecorder atomic.Pointer[recorderBox]

// SetRecorder installs the sink for lesson events; nil restores the no-op.
func SetRecorder(r telemetry.Recorder) {
	if r == nil {
		activeRecorder.Store(nil)
		return
	}
	activeRecorder.Store(&recorderBox{rec: r})
}

// recordLesson reports one lesson write (deduped true when nothing was
// written because the text was already present).
func recordLesson(ctx context.Context, deduped bool, start time.Time) {
	b := activeRecorder.Load()
	if b == nil || b.rec.Level() == telemetry.Off {
		return
	}
	id, transport, _ := telemetry.IdentityFrom(ctx)
	written := 1
	if deduped {
		written = 0
	}
	var tail [8]byte
	_, _ = rand.Read(tail[:])
	b.rec.RecordMemory(telemetry.MemoryEvent{
		ID:        fmt.Sprintf("%016x%s", start.UnixNano(), hex.EncodeToString(tail[:])),
		TS:        start,
		LatencyMS: time.Since(start).Milliseconds(),
		Verb:      "lesson",
		Transport: transport,
		Identity:  id,
		Written:   written,
		Deduped:   deduped,
	})
}
