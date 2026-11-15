package httpx

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGuardAnswersCancelledRequestWithoutCallingHandler(t *testing.T) {
	called := false
	h := Guard(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true }))

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil).WithContext(ctx))

	if called {
		t.Fatal("handler ran for a request whose client is already gone")
	}
	if rec.Code != StatusClientClosedRequest {
		t.Fatalf("status = %d, want %d", rec.Code, StatusClientClosedRequest)
	}
}

func TestGuardPassesLiveRequestThrough(t *testing.T) {
	called := false
	h := Guard(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusTeapot)
	}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if !called || rec.Code != http.StatusTeapot {
		t.Fatalf("called = %v, status = %d; a live request must reach the handler", called, rec.Code)
	}
}
