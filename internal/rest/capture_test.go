package rest

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/FreePeak/LeanKG/internal/memory"
	"github.com/FreePeak/LeanKG/internal/telemetry"
)

// captureRecorder is a fake sink: it keeps every call and memory event.
type captureRecorder struct {
	mu    sync.Mutex
	level telemetry.Level
	calls []telemetry.CallEvent
	mems  []telemetry.MemoryEvent
}

func (r *captureRecorder) Level() telemetry.Level { return r.level }
func (r *captureRecorder) RecordCall(ev telemetry.CallEvent) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, ev)
}
func (r *captureRecorder) RecordMemory(ev telemetry.MemoryEvent) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.mems = append(r.mems, ev)
}
func (r *captureRecorder) Close() error { return nil }

func (r *captureRecorder) snapshot() ([]telemetry.CallEvent, []telemetry.MemoryEvent) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]telemetry.CallEvent(nil), r.calls...), append([]telemetry.MemoryEvent(nil), r.mems...)
}

type reqSpec struct {
	method, path, body string
	headers            map[string]string
}

// do sends one request straight into h and returns status, content type and body.
func do(h http.Handler, s reqSpec) (int, string, []byte) {
	req := httptest.NewRequest(s.method, s.path, strings.NewReader(s.body))
	if s.body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range s.headers {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w.Code, w.Header().Get("Content-Type"), w.Body.Bytes()
}

// captured builds a fresh engine and serves the REST handler behind Capture.
func captured(t *testing.T, rec telemetry.Recorder) http.Handler {
	t.Helper()
	e, mem := newEngine(t)
	return Capture(Handler(e, mem, WithHindsightCompat()), rec, telemetry.TransportREST)
}

var routeCases = []struct {
	name, method, path, body                                     string
	wantStatus                                                   int
	wantTransport, wantMethod, wantTool, wantAction, wantCommand string
}{
	{"query", "POST", "/api/v1/query", `{"query":"anything"}`, 200,
		telemetry.TransportREST, "POST /api/v1/query", "query", "", ""},
	{"import memory", "POST", "/api/v1/import",
		`{"action":"memory","command":"session_retain","args":{"session_id":"sess-abc-123","turns":["hello turn"],"retained_through_user_turn":1}}`, 200,
		telemetry.TransportREST, "POST /api/v1/import", "import", "memory", "session_retain"},
	{"status", "GET", "/api/v1/status", "", 200,
		telemetry.TransportREST, "GET /api/v1/status", "status", "", ""},
	{"native bank recall", "POST", "/api/v1/memory/banks/my-bank-1/recall", `{"query":"hello"}`, 200,
		telemetry.TransportREST, "POST /api/v1/memory/banks/{bank}/recall", "memory", "my-bank-1", "recall"},
	{"hindsight retain", "POST", "/v1/default/banks/hb/memories", `{"items":[{"content":"pgvector holds the vectors"}]}`, 200,
		telemetry.TransportHS, "POST /v1/default/banks/{bank}/memories", "hindsight", "hb", "retain"},
	{"hindsight recall", "POST", "/v1/default/banks/hb/memories/recall", `{"query":"vectors","tags":["proj-a"],"tags_match":"any"}`, 200,
		telemetry.TransportHS, "POST /v1/default/banks/{bank}/memories/recall", "hindsight", "hb", "recall tags=proj-a tags_match=any"},
	{"hindsight delete", "DELETE", "/v1/default/banks/hb/documents/doc-42", "", 200,
		telemetry.TransportHS, "DELETE /v1/default/banks/{bank}/documents/{document_id}", "hindsight", "hb", "delete"},
}

// TestCaptureOneRowPerRouteFamily pins DS-08: one captured row per request,
// with the route pattern (ids normalized), the tool/action/command decoded
// from the request, and the transport the call entered on.
func TestCaptureOneRowPerRouteFamily(t *testing.T) {
	rec := &captureRecorder{level: telemetry.Metadata}
	memory.SetRecorder(rec)
	t.Cleanup(func() { memory.SetRecorder(nil) })
	h := captured(t, rec)

	for _, tc := range routeCases {
		t.Run(tc.name, func(t *testing.T) {
			before, _ := rec.snapshot()
			status, _, body := do(h, reqSpec{method: tc.method, path: tc.path, body: tc.body})
			if status != tc.wantStatus {
				t.Fatalf("status = %d body=%s, want %d", status, body, tc.wantStatus)
			}
			after, _ := rec.snapshot()
			if len(after) != len(before)+1 {
				t.Fatalf("captured %d rows for one request, want 1", len(after)-len(before))
			}
			ev := after[len(after)-1]
			if ev.Transport != tc.wantTransport || ev.Method != tc.wantMethod ||
				ev.Tool != tc.wantTool || ev.Action != tc.wantAction || ev.Command != tc.wantCommand {
				t.Fatalf("row = transport %q method %q tool %q action %q command %q; want %q %q %q %q %q",
					ev.Transport, ev.Method, ev.Tool, ev.Action, ev.Command,
					tc.wantTransport, tc.wantMethod, tc.wantTool, tc.wantAction, tc.wantCommand)
			}
			if ev.Outcome == "" || strings.HasPrefix(ev.Outcome, telemetry.OutcomeErrorPrefix) {
				t.Fatalf("outcome = %q, want a success outcome", ev.Outcome)
			}
			if ev.ID == "" || ev.TS.IsZero() {
				t.Fatalf("row missing id or timestamp: %+v", ev)
			}
		})
	}
}

// TestCaptureOutcomeFromStatus pins the status-derived outcome: a JSON error
// body that the classifier reads as success still becomes error:HTTP_<n>.
func TestCaptureOutcomeFromStatus(t *testing.T) {
	rec := &captureRecorder{level: telemetry.Metadata}
	h := captured(t, rec)

	if status, _, _ := do(h, reqSpec{method: "POST", path: "/api/v1/query", body: `{"query":`}); status != 400 {
		t.Fatalf("malformed query status = %d, want 400", status)
	}
	if status, _, _ := do(h, reqSpec{method: "GET", path: "/api/v1/nope/abc123"}); status != 404 {
		t.Fatalf("unknown route status = %d, want 404", status)
	}
	calls, _ := rec.snapshot()
	if len(calls) != 2 {
		t.Fatalf("rows = %d, want 2", len(calls))
	}
	if calls[0].Outcome != "error:HTTP_400" || calls[0].ErrorCode != "HTTP_400" {
		t.Fatalf("400 outcome = %q / %q", calls[0].Outcome, calls[0].ErrorCode)
	}
	if calls[1].Outcome != "error:HTTP_404" || calls[1].Method != "GET /api/v1/nope/{id}" {
		t.Fatalf("404 row = %q / %q, want error:HTTP_404 on the id-normalized pattern", calls[1].Outcome, calls[1].Method)
	}
}

// TestCaptureBytesIdentical pins the no-behaviour-change contract: the same
// request sequence yields byte-identical responses with capture Off, with
// Metadata and with Bodies.
func TestCaptureBytesIdentical(t *testing.T) {
	seq := []reqSpec{
		{method: "GET", path: "/health"},
		{method: "POST", path: "/api/v1/query", body: `{"query":"anything"}`},
		{method: "POST", path: "/v1/default/banks/hb/memories", body: `{"items":[{"content":"pgvector holds the vectors","document_id":"d1"}]}`},
		{method: "POST", path: "/v1/default/banks/hb/memories/recall", body: `{"query":"vectors"}`},
		{method: "DELETE", path: "/v1/default/banks/hb/documents/d1"},
		{method: "POST", path: "/v1/default/banks/hb/memories/recall", body: `{"query":"vectors","tags_match":"bogus"}`},
	}
	// Recall rows carry generated ids and wall-clock timestamps; mask them so
	// only the observable content is compared.
	volatileID := regexp.MustCompile(`"(id|mentioned_at)":"[^"]*"`)
	run := func(h http.Handler) []byte {
		var out bytes.Buffer
		for _, s := range seq {
			status, ct, body := do(h, s)
			body = volatileID.ReplaceAll(body, []byte(`"$1":"-"`))
			out.WriteString(strings.Join([]string{s.method, s.path, http.StatusText(status), ct, string(body)}, "|"))
			out.WriteByte('\n')
		}
		return out.Bytes()
	}
	off := run(captured(t, &captureRecorder{level: telemetry.Off}))
	meta := run(captured(t, &captureRecorder{level: telemetry.Metadata}))
	bodies := run(captured(t, &captureRecorder{level: telemetry.Bodies}))
	if !bytes.Equal(off, meta) {
		t.Fatalf("Metadata changed responses:\nOff:\n%s\nMetadata:\n%s", off, meta)
	}
	if !bytes.Equal(off, bodies) {
		t.Fatalf("Bodies changed responses:\nOff:\n%s\nBodies:\n%s", off, bodies)
	}
}

// TestCaptureOffRecordsNothing pins Off means off: no call rows, no memory
// events, and the wrapper hands the request straight to the handler.
func TestCaptureOffRecordsNothing(t *testing.T) {
	rec := &captureRecorder{level: telemetry.Off}
	memory.SetRecorder(rec)
	t.Cleanup(func() { memory.SetRecorder(nil) })
	h := captured(t, rec)
	for _, tc := range routeCases {
		do(h, reqSpec{method: tc.method, path: tc.path, body: tc.body})
	}
	calls, mems := rec.snapshot()
	if len(calls) != 0 || len(mems) != 0 {
		t.Fatalf("Off recorded %d calls and %d memory events", len(calls), len(mems))
	}
}

// TestCaptureIdentityJoinsMemoryEvents pins the identity rules and the join:
// headers name the client and session, the memory event for the retain
// carries the same identity and transport as its call row.
func TestCaptureIdentityJoinsMemoryEvents(t *testing.T) {
	rec := &captureRecorder{level: telemetry.Metadata}
	memory.SetRecorder(rec)
	t.Cleanup(func() { memory.SetRecorder(nil) })
	h := captured(t, rec)

	do(h, reqSpec{method: "POST", path: "/v1/default/banks/hb/memories",
		body: `{"items":[{"content":"joined row"}]}`,
		headers: map[string]string{
			"X-LeanKG-Client":  "omp",
			"X-LeanKG-Session": "sess-7",
			"X-LeanKG-Cwd":     "/work/x",
			"User-Agent":       "omp/1.4.2 (darwin)",
		}})
	do(h, reqSpec{method: "GET", path: "/api/v1/status", headers: map[string]string{"User-Agent": "claude-code/2.1.0"}})

	calls, mems := rec.snapshot()
	if len(calls) != 2 {
		t.Fatalf("calls = %d, want 2", len(calls))
	}
	id := calls[0].Identity
	if id.ClientName != "omp" || id.ClientSessionID != "sess-7" || id.Cwd != "/work/x" || id.ClientVersion != "1.4.2" {
		t.Fatalf("identity from headers = %+v", id)
	}
	if calls[0].Correlation != telemetry.CorrExact {
		t.Fatalf("correlation = %q, want exact", calls[0].Correlation)
	}
	if fallback := calls[1].Identity; fallback.ClientName != "claude-code" || fallback.ClientVersion != "2.1.0" || fallback.ClientSessionID != "" {
		t.Fatalf("User-Agent fallback identity = %+v", fallback)
	}
	if calls[1].Correlation != telemetry.CorrHeuristic {
		t.Fatalf("correlation without session = %q, want heuristic", calls[1].Correlation)
	}
	if len(mems) != 1 {
		t.Fatalf("memory events = %d, want 1 retain", len(mems))
	}
	m := mems[0]
	if m.Verb != "retain" || m.ClientSessionID != "sess-7" || m.Transport != telemetry.TransportHS {
		t.Fatalf("memory event not joined to call: %+v", m)
	}
}

// TestCaptureUnidentifiedClient keeps the default identity when no header or
// User-Agent names the caller.
func TestCaptureUnidentifiedClient(t *testing.T) {
	rec := &captureRecorder{level: telemetry.Metadata}
	h := captured(t, rec)
	do(h, reqSpec{method: "GET", path: "/api/v1/status"})
	calls, _ := rec.snapshot()
	if len(calls) != 1 || calls[0].ClientName != "unknown" || calls[0].Correlation != telemetry.CorrHeuristic {
		t.Fatalf("default identity = %+v", calls)
	}
}

// TestCaptureBodiesStoresRedactedBody pins that only Bodies level keeps the
// request args and the response body on the row.
func TestCaptureBodiesStoresRedactedBody(t *testing.T) {
	rec := &captureRecorder{level: telemetry.Bodies}
	h := captured(t, rec)
	do(h, reqSpec{method: "POST", path: "/api/v1/query", body: `{"query":"anything"}`})
	calls, _ := rec.snapshot()
	if len(calls) != 1 || calls[0].ArgsRedacted == "" || calls[0].BodyRedacted == "" {
		t.Fatalf("Bodies row missing bodies: %+v", calls)
	}
	meta := &captureRecorder{level: telemetry.Metadata}
	do(captured(t, meta), reqSpec{method: "POST", path: "/api/v1/query", body: `{"query":"anything"}`})
	mcalls, _ := meta.snapshot()
	if mcalls[0].ArgsRedacted != "" || mcalls[0].BodyRedacted != "" {
		t.Fatalf("Metadata row stored bodies: %+v", mcalls[0])
	}
}
