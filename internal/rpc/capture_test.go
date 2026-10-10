package rpc

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"

	connect "connectrpc.com/connect"

	"github.com/FreePeak/LeanKG/internal/core"
	"github.com/FreePeak/LeanKG/internal/memory"
	leankgv1 "github.com/FreePeak/LeanKG/internal/rpc/leankg/v1"
	"github.com/FreePeak/LeanKG/internal/rpc/leankg/v1/leankgv1connect"
	"github.com/FreePeak/LeanKG/internal/store"
	"github.com/FreePeak/LeanKG/internal/telemetry"
)

type callRecorder struct {
	mu    sync.Mutex
	level telemetry.Level
	calls []telemetry.CallEvent
}

func (r *callRecorder) Level() telemetry.Level { return r.level }
func (r *callRecorder) RecordCall(ev telemetry.CallEvent) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, ev)
}
func (r *callRecorder) RecordMemory(telemetry.MemoryEvent) {}
func (r *callRecorder) Close() error                       { return nil }

func (r *callRecorder) snapshot() []telemetry.CallEvent {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]telemetry.CallEvent(nil), r.calls...)
}

// serveRPC starts the ConnectRPC handler with the capture interceptor and
// returns a client bound to it.
func serveRPC(t *testing.T, rec telemetry.Recorder) leankgv1connect.LeanKGClient {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, ".leankg", "leankg.db"), store.RW)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.Migrate(); err != nil {
		t.Fatal(err)
	}
	mem, err := memory.Open(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	engine := core.New(st, mem, nil)
	path, handler := leankgv1connect.NewLeanKGHandler(NewLeanKGService(engine),
		connect.WithInterceptors(CaptureInterceptor(rec)))
	mux := http.NewServeMux()
	mux.Handle(path, handler)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return leankgv1connect.NewLeanKGClient(srv.Client(), srv.URL)
}

// TestCaptureInterceptorRecordsUnaryCalls pins DS-08 for ConnectRPC: each
// unary call is one row on transport rpc, with the procedure, the decoded
// action and the caller identity from the request headers.
func TestCaptureInterceptorRecordsUnaryCalls(t *testing.T) {
	rec := &callRecorder{level: telemetry.Metadata}
	client := serveRPC(t, rec)
	ctx := context.Background()

	q := connect.NewRequest(&leankgv1.QueryRequest{Query: "anything"})
	q.Header().Set("X-LeanKG-Client", "omp")
	q.Header().Set("X-LeanKG-Session", "rpc-sess-1")
	if _, err := client.Query(ctx, q); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Status(ctx, connect.NewRequest(&leankgv1.StatusRequest{})); err != nil {
		t.Fatal(err)
	}
	if _, err := client.MemoryRead(ctx, connect.NewRequest(&leankgv1.MemoryReadRequest{Command: "snapshot"})); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Import(ctx, connect.NewRequest(&leankgv1.ImportRequest{Action: "bogus"})); err == nil {
		t.Fatal("bogus import unexpectedly succeeded; the error row cannot be checked")
	}

	calls := rec.snapshot()
	if len(calls) != 4 {
		t.Fatalf("rows = %d, want 4 (one per unary call)", len(calls))
	}
	want := []struct{ method, tool string }{
		{"/leankg.v1.LeanKG/Query", "query"},
		{"/leankg.v1.LeanKG/Status", "status"},
		{"/leankg.v1.LeanKG/MemoryRead", "memory_read"},
		{"/leankg.v1.LeanKG/Import", "import"},
	}
	for i, w := range want {
		if calls[i].Transport != telemetry.TransportRPC || calls[i].Method != w.method || calls[i].Tool != w.tool {
			t.Fatalf("row %d = transport %q method %q tool %q; want rpc %q %q", i, calls[i].Transport, calls[i].Method, calls[i].Tool, w.method, w.tool)
		}
	}
	if calls[0].ClientName != "omp" || calls[0].ClientSessionID != "rpc-sess-1" {
		t.Fatalf("query identity = %+v", calls[0].Identity)
	}
	if calls[2].Command != "snapshot" {
		t.Fatalf("memory_read command = %q, want snapshot", calls[2].Command)
	}
	if calls[3].Outcome != "error:INVALID_ARGUMENT" || calls[3].ErrorCode != "INVALID_ARGUMENT" {
		t.Fatalf("import error outcome = %q / %q", calls[3].Outcome, calls[3].ErrorCode)
	}
	if calls[3].Action != "bogus" {
		t.Fatalf("import action = %q, want bogus", calls[3].Action)
	}
}

// TestCaptureInterceptorResponsesIdentical pins that the interceptor does not
// change the response: the same call returns the same JSON with capture off
// and with capture on.
func TestCaptureInterceptorResponsesIdentical(t *testing.T) {
	off := serveRPC(t, &callRecorder{level: telemetry.Off})
	on := serveRPC(t, &callRecorder{level: telemetry.Bodies})
	a, err := off.Query(context.Background(), connect.NewRequest(&leankgv1.QueryRequest{Query: "anything"}))
	if err != nil {
		t.Fatal(err)
	}
	b, err := on.Query(context.Background(), connect.NewRequest(&leankgv1.QueryRequest{Query: "anything"}))
	if err != nil {
		t.Fatal(err)
	}
	if a.Msg.Json != b.Msg.Json {
		t.Fatalf("capture changed the response:\noff: %s\non:  %s", a.Msg.Json, b.Msg.Json)
	}
}

// TestCaptureInterceptorOffRecordsNothing pins Off means off for RPC.
func TestCaptureInterceptorOffRecordsNothing(t *testing.T) {
	rec := &callRecorder{level: telemetry.Off}
	client := serveRPC(t, rec)
	if _, err := client.Status(context.Background(), connect.NewRequest(&leankgv1.StatusRequest{})); err != nil {
		t.Fatal(err)
	}
	if n := len(rec.snapshot()); n != 0 {
		t.Fatalf("Off recorded %d rows", n)
	}
}
