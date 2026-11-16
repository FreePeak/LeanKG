package dashboard

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/FreePeak/LeanKG/internal/httpx"
	"github.com/FreePeak/LeanKG/internal/sessionlink"
	"github.com/FreePeak/LeanKG/internal/telemetry"
)

// countingAdapter counts transcript reads so a test can see whether work ran.
type countingAdapter struct {
	fakeAdapter
	reads *int
}

func (c countingAdapter) Transcript(_ context.Context, _ sessionlink.TranscriptRef, _ int) ([]sessionlink.Turn, error) {
	*c.reads++
	return nil, nil
}

func TestTranscriptReadStopsOnCancelledContext(t *testing.T) {
	reads := 0
	srv := testServer(t, t.TempDir(), func(o *Options) {
		o.Adapters = []sessionlink.Adapter{countingAdapter{
			fakeAdapter: fakeAdapter{client: "claude-code", detect: true}, reads: &reads,
		}}
	})
	cfg := telemetry.Config{Sessions: telemetry.SessionsConfig{Enabled: true, Clients: []string{"claude-code"}}}
	read := srv.transcriptSource(cfg)
	if read == nil {
		t.Fatal("transcriptSource returned nil for a granted client")
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	sess := telemetry.Session{ClientName: "claude-code", ClientSessionID: "s1", TranscriptPath: "/tmp/x.jsonl"}
	if _, err := read(ctx, sess); err == nil {
		t.Fatal("cancelled read returned no error")
	}
	if reads != 0 {
		t.Fatalf("adapter ran %d time(s) for a cancelled request, want 0", reads)
	}
}

func TestRespondCancelledIsNotInternalError(t *testing.T) {
	var s Server
	rec := httptest.NewRecorder()
	s.respond(rec, nil, context.Canceled)
	if rec.Code != httpx.StatusClientClosedRequest {
		t.Fatalf("status = %d, want %d (a cancelled report is not a server failure)", rec.Code, httpx.StatusClientClosedRequest)
	}

	rec = httptest.NewRecorder()
	s.respond(rec, nil, context.DeadlineExceeded)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("deadline: status = %d, want 500 (a server timeout is a failure)", rec.Code)
	}
}
