package memory

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/FreePeak/LeanKG/internal/telemetry"
)

// Memory events (plan v4.15 DS-09). The memory layer reports each operation
// to one process-wide sink. The sink is a no-op until SetRecorder installs a
// real one, and every emit site first asks emitter(), which returns nil when
// capture is Off, so an Off server builds no event and does no extra work
// beyond a single atomic load per operation.

type recorderBox struct{ rec telemetry.Recorder }

var activeRecorder atomic.Pointer[recorderBox]

// SetRecorder installs the sink that receives memory events from every
// Memory in this process. nil restores the no-op. serve calls it once at
// startup; tests call it with a fake and restore nil on cleanup.
func SetRecorder(r telemetry.Recorder) {
	if r == nil {
		activeRecorder.Store(nil)
		return
	}
	activeRecorder.Store(&recorderBox{rec: r})
}

// emitter returns the sink to write to, or nil when nothing should be
// recorded (no sink installed, or capture is Off).
func emitter() telemetry.Recorder {
	b := activeRecorder.Load()
	if b == nil || b.rec.Level() == telemetry.Off {
		return nil
	}
	return b.rec
}

// emitMemory stamps the event with an id, the caller identity carried on ctx
// (telemetry.WithIdentity) and the latency since start, then hands it to rec.
func emitMemory(ctx context.Context, rec telemetry.Recorder, start time.Time, ev telemetry.MemoryEvent) {
	id, transport, _ := telemetry.IdentityFrom(ctx)
	ev.ID = newMemoryEventID()
	ev.TS = start
	ev.LatencyMS = time.Since(start).Milliseconds()
	ev.Identity = id
	ev.Transport = transport
	rec.RecordMemory(ev)
}

// newMemoryEventID is time-sortable (nanosecond prefix) with a random tail.
func newMemoryEventID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return fmt.Sprintf("%016x%s", time.Now().UnixNano(), hex.EncodeToString(b[:]))
}

// recordRecall reports one recall. rows carry their relative Score in rank
// order; denseIDs names the rows the dense arm ranked (nil when it did not
// run). Returned rows are the page the caller receives, after the keep
// filter and limit.
func recordRecall(ctx context.Context, banks []string, query string, limit int, rows []Entry, denseIDs map[string]bool, start time.Time) {
	rec := emitter()
	if rec == nil {
		return
	}
	queryJSON, _ := json.Marshal(query)
	ids := make([]string, 0, len(rows))
	scores := make([]string, 0, len(rows))
	ages := make([]int64, 0, len(rows))
	dense := 0
	var maxAge int64
	now := time.Now().Unix()
	for _, e := range rows {
		ids = append(ids, e.ID)
		scores = append(scores, strconv.FormatFloat(e.Score, 'g', 6, 64))
		if denseIDs[e.ID] {
			dense++
		}
		age := max(now-e.Timestamp, 0)
		ages = append(ages, age)
		maxAge = max(maxAge, age)
	}
	emitMemory(ctx, rec, start, telemetry.MemoryEvent{
		Verb:        "recall",
		Banks:       strings.Join(banks, ","),
		QueryHash:   telemetry.ArgsHash(queryJSON),
		Limit:       limit,
		Returned:    len(rows),
		ReturnedIDs: strings.Join(ids, ","),
		Scores:      strings.Join(scores, ","),
		DenseHits:   dense,
		AgeMedianS:  medianOf(ages),
		AgeMaxS:     maxAge,
	})
}

// recordRetain reports one write batch. skipped counts rows the user-turn
// cursor refused; replaced counts rows a document upsert dropped first.
func recordRetain(ctx context.Context, bank string, written, skipped, replaced int, start time.Time) {
	rec := emitter()
	if rec == nil {
		return
	}
	emitMemory(ctx, rec, start, telemetry.MemoryEvent{
		Verb:     "retain",
		Banks:    bank,
		Written:  written,
		Skipped:  skipped,
		Replaced: replaced,
	})
}

// recordDelete reports a document delete.
func recordDelete(ctx context.Context, bank string, deleted int, start time.Time) {
	rec := emitter()
	if rec == nil {
		return
	}
	emitMemory(ctx, rec, start, telemetry.MemoryEvent{
		Verb:    "delete",
		Banks:   bank,
		Deleted: deleted,
	})
}

// recordInject reports the rows an injection block carried (the prefix of
// entries InjectBlock rendered) and the block's size in tokens (bytes/4).
func recordInject(ctx context.Context, banks []string, injected []Entry, text string, start time.Time) {
	rec := emitter()
	if rec == nil {
		return
	}
	ids := make([]string, 0, len(injected))
	scores := make([]string, 0, len(injected))
	for _, e := range injected {
		ids = append(ids, e.ID)
		scores = append(scores, strconv.FormatFloat(e.Score, 'g', 6, 64))
	}
	emitMemory(ctx, rec, start, telemetry.MemoryEvent{
		Verb:        "inject",
		Banks:       strings.Join(banks, ","),
		Returned:    len(injected),
		ReturnedIDs: strings.Join(ids, ","),
		Scores:      strings.Join(scores, ","),
		Tokens:      int64(len(text) / 4),
	})
}

// medianOf is the median of xs (mean of the two middle values for an even
// count); 0 for an empty slice. xs is not modified.
func medianOf(xs []int64) int64 {
	if len(xs) == 0 {
		return 0
	}
	s := append([]int64(nil), xs...)
	sort.Slice(s, func(i, j int) bool { return s[i] < s[j] })
	n := len(s)
	if n%2 == 1 {
		return s[n/2]
	}
	return (s[n/2-1] + s[n/2]) / 2
}
