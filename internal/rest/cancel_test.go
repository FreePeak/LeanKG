package rest

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/FreePeak/LeanKG/internal/core"
	"github.com/FreePeak/LeanKG/internal/httpx"
	"github.com/FreePeak/LeanKG/internal/store"
)

// poisonStore fails any test that reaches the store: every method call goes
// through the nil embedded interface and panics.
type poisonStore struct{ store.Backend }

// serveNoPanic runs one request and turns a store call into a test failure
// instead of a crashed test binary.
func serveNoPanic(t *testing.T, h http.Handler, req *http.Request) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	defer func() {
		if p := recover(); p != nil {
			t.Errorf("%s %s reached the store on a cancelled request: %v", req.Method, req.URL.Path, p)
		}
	}()
	h.ServeHTTP(rec, req)
	return rec
}

func TestCancelledRequestNeverReachesStore(t *testing.T) {
	h := Handler(core.New(poisonStore{}, nil, nil), nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	for _, rq := range []struct{ method, path, body string }{
		{http.MethodGet, "/api/v1/status", ""},
		{http.MethodPost, "/api/v1/query", `{"query":"x"}`},
		{http.MethodPost, "/api/v1/import", `{"action":"index","path":"/x"}`},
	} {
		req := httptest.NewRequest(rq.method, rq.path, strings.NewReader(rq.body)).WithContext(ctx)
		rec := serveNoPanic(t, h, req)
		if rec.Code != httpx.StatusClientClosedRequest {
			t.Errorf("%s %s: status = %d, want %d", rq.method, rq.path, rec.Code, httpx.StatusClientClosedRequest)
		}
	}
}

func TestWriteErrorCancelledIsNotBadRequest(t *testing.T) {
	rec := httptest.NewRecorder()
	httpx.WriteError(rec, context.Canceled)
	if rec.Code != httpx.StatusClientClosedRequest {
		t.Fatalf("cancelled: status = %d, want %d (a cancelled call is not a data failure)", rec.Code, httpx.StatusClientClosedRequest)
	}

	rec = httptest.NewRecorder()
	httpx.WriteError(rec, errors.New("boom"))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("ordinary error: status = %d, want 400", rec.Code)
	}
}
