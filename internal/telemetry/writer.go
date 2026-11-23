package telemetry

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"sync"
	"sync/atomic"
	"time"
)

// Queue and batch limits (DS-03).
const (
	queueSize     = 1024
	batchSize     = 64
	flushInterval = 250 * time.Millisecond
	flushTimeout  = 5 * time.Second
)

// queued is one event waiting for the writer. Exactly one field is set.
type queued struct {
	call *CallEvent
	mem  *MemoryEvent
}

// recorder is the hot-path sink. RecordCall only enqueues: a single writer
// goroutine redacts, caps and batch-inserts, so a slow disk never slows a
// tool call, and a full queue drops the event and counts the drop (DS-03).
type recorder struct {
	st      Store
	level   Level
	maxBody int
	// ctx is the server-lifetime ctx the caller passed to NewRecorder. The
	// recorder owns its writer goroutine and Close joins it, which is the one
	// runner type the context plan allows to hold a ctx. Flushes never use it
	// directly: they run detached from its cancellation.
	ctx context.Context

	ch      chan queued
	dropped atomic.Int64
	done    chan struct{}

	mu     sync.RWMutex // guards closed and the close of ch against sends
	closed bool

	closeOnce sync.Once
	closeErr  error
}

// NewRecorder returns a Recorder that writes to st at level. Off returns a
// recorder that drops everything and starts no goroutine. Close flushes,
// stops the writer and closes st. ctx is the server-lifetime context; a
// cancelled ctx does not drop queued events, because the flushes that carry
// them run detached from it.
func NewRecorder(ctx context.Context, st Store, level Level, maxBodyBytes int) Recorder {
	if level != Metadata && level != Bodies {
		level = Off
	}
	r := &recorder{ctx: ctx, st: st, level: level, maxBody: maxBodyBytes, done: make(chan struct{})}
	if level == Off {
		// No writer and no queue. `closed` stays false because it marks
		// "Close was called", not "not capturing": the level alone is Off, so
		// SetLevel can revive this recorder (the consent reload) while Close
		// semantics stay correct.
		return r
	}
	r.ch = make(chan queued, queueSize)
	go r.run()
	return r
}

// Level reports the level this recorder captures at.
func (r *recorder) Level() Level {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.level
}

// SetLevel applies a live level change — the SIGHUP / consent-screen path, so
// an operator who grants or withdraws consent mid-session does not have to
// restart the server. Two directions:
//
//   - rising from Off: the recorder was built with no writer and no queue, so
//     this creates the queue and starts the writer. Without that, events
//     would be accepted into a queue nobody drains.
//   - falling to Off: capture stops immediately (enqueue checks the level
//     under the same lock). The writer keeps running so a later raise is free.
func (r *recorder) SetLevel(level Level) error {
	if level != Metadata && level != Bodies {
		level = Off
	}
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return errors.New("telemetry: recorder is closed")
	}
	risingFromOff := r.level == Off && level != Off
	r.level = level
	needWriter := risingFromOff && r.ch == nil
	if needWriter {
		r.ch = make(chan queued, queueSize)
	}
	r.mu.Unlock()
	if needWriter {
		go r.run()
	}
	return nil
}

// RecordCall enqueues a call without blocking. It never panics into the caller.
func (r *recorder) RecordCall(ev CallEvent) {
	defer func() { _ = recover() }()
	r.enqueue(queued{call: &ev})
}

// RecordMemory enqueues a memory event without blocking.
func (r *recorder) RecordMemory(ev MemoryEvent) {
	defer func() { _ = recover() }()
	r.enqueue(queued{mem: &ev})
}

func (r *recorder) enqueue(q queued) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.closed || r.level == Off {
		return
	}
	select {
	case r.ch <- q:
	default:
		r.dropped.Add(1)
	}
}

// Close flushes what is queued, stops the writer and closes the store. It is
// safe to call more than once.
func (r *recorder) Close() error {
	r.closeOnce.Do(func() {
		r.mu.Lock()
		r.closed = true
		// r.ch is set exactly when a writer was started, so only then is there
		// a `done` to wait for.
		writing := r.ch != nil
		if writing {
			close(r.ch)
		}
		r.mu.Unlock()
		if writing {
			<-r.done
		}
		if r.st != nil {
			r.closeErr = r.st.Close()
		}
	})
	return r.closeErr
}

// run is the single writer. It batches by count and by time, and reports
// drops with AddDropped on every flush.
func (r *recorder) run() {
	// The writer is the only closer of `done`, so each started writer closes
	// it exactly once, including one started by SetLevel after an Off build.
	defer close(r.done)

	var calls []CallEvent
	var mems []MemoryEvent
	pending := func() int { return len(calls) + len(mems) }
	flush := func() {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(r.ctx), flushTimeout)
		defer cancel()
		if len(calls) > 0 {
			if err := guard("InsertCalls", func() error { return r.st.InsertCalls(ctx, calls) }); err != nil {
				r.dropped.Add(int64(len(calls))) // lost with the batch; counted
			}
			calls = calls[:0]
		}
		if len(mems) > 0 {
			if err := guard("InsertMemoryEvents", func() error { return r.st.InsertMemoryEvents(ctx, mems) }); err != nil {
				r.dropped.Add(int64(len(mems)))
			}
			mems = mems[:0]
		}
		if n := r.dropped.Swap(0); n > 0 {
			if err := guard("AddDropped", func() error { return r.st.AddDropped(ctx, n) }); err != nil {
				r.dropped.Add(n) // keep the count for the next flush
			}
		}
	}

	ticker := time.NewTicker(flushInterval)
	defer ticker.Stop()
	for {
		select {
		case q, ok := <-r.ch:
			if !ok {
				flush()
				return
			}
			if err := guard("prepare", func() error {
				if q.call != nil {
					calls = append(calls, r.prepareCall(*q.call))
				} else if q.mem != nil {
					mems = append(mems, r.prepareMemory(*q.mem))
				}
				return nil
			}); err != nil {
				r.dropped.Add(1) // the event is lost; counted
			}
			if pending() >= batchSize {
				flush()
			}
		case <-ticker.C:
			flush()
		}
	}
}

// guard runs fn and turns a panic into a logged error. The writer runs guard
// around every sink call and every event, so a panicking sink or event loses
// only its own batch (the caller counts it) and capture continues. A single
// recover at the top of run would instead end the goroutine without a trace.
func guard(what string, fn func() error) (err error) {
	defer func() {
		if p := recover(); p != nil {
			log.Printf("telemetry: writer panic in %s: %v", what, p)
			err = fmt.Errorf("writer panic in %s: %v", what, p)
		}
	}()
	return fn()
}

// prepareCall fills ID and TS and applies the body policy (DS-02): below
// Bodies the args and bodies are blanked; at Bodies they are redacted, then
// capped, so a secret cut at the boundary is already masked.
func (r *recorder) prepareCall(c CallEvent) CallEvent {
	if c.ID == "" {
		c.ID = newEventID(time.Now())
	}
	if c.TS.IsZero() {
		c.TS = time.Now()
	}
	if r.Level() == Bodies {
		c.ArgsRedacted = Cap(Redact(c.ArgsRedacted), r.maxBody)
		c.BodyRedacted = Cap(Redact(c.BodyRedacted), r.maxBody)
	} else {
		c.ArgsRedacted = ""
		c.BodyRedacted = ""
	}
	return c
}

// prepareMemory fills ID and TS and redacts the free-text error.
func (r *recorder) prepareMemory(m MemoryEvent) MemoryEvent {
	if m.ID == "" {
		m.ID = newEventID(time.Now())
	}
	if m.TS.IsZero() {
		m.TS = time.Now()
	}
	m.Error = Redact(m.Error)
	return m
}

// newEventID returns a 32-character hex id whose first 12 characters are the
// millisecond timestamp, so ids sort by time. The rest is random.
func newEventID(t time.Time) string {
	var b [16]byte
	var ms [8]byte
	binary.BigEndian.PutUint64(ms[:], uint64(t.UnixMilli()))
	copy(b[:6], ms[2:]) // 48 bits of milliseconds
	if _, err := rand.Read(b[6:]); err != nil {
		binary.BigEndian.PutUint64(b[8:], uint64(time.Now().UnixNano()))
	}
	return hex.EncodeToString(b[:])
}
