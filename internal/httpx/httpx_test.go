package httpx

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/FreePeak/LeanKG/internal/errs"
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

// TestStatusForClassifiesEveryCatalogCode pins one status per catalog code. A
// code added to the catalog without a row here fails the loop, so no new code
// reaches the wire as an unclassified 400.
func TestStatusForClassifiesEveryCatalogCode(t *testing.T) {
	want := map[errs.ErrorCode]int{
		errs.PGUnreachable:         http.StatusServiceUnavailable,
		errs.TrgmUnavailable:       http.StatusServiceUnavailable,
		errs.PGURLMalformed:        http.StatusInternalServerError,
		errs.AutoAttachFailed:      http.StatusInternalServerError,
		errs.ProjectNotInitialized: http.StatusNotFound,
		errs.UnknownProject:        http.StatusNotFound,
		errs.UnknownTool:           http.StatusNotFound,
		errs.MethodNotFound:        http.StatusNotFound,
		errs.Unauthorized:          http.StatusUnauthorized,
		errs.PermissionDenied:      http.StatusForbidden,
		errs.ReadOnly:              http.StatusForbidden,
		errs.NoVectors:             http.StatusConflict,
		errs.IndexRootMismatch:     http.StatusConflict,
		errs.UnknownAction:         http.StatusBadRequest,
		errs.MissingParam:          http.StatusBadRequest,
		errs.PathOutsideProject:    http.StatusBadRequest,
		errs.InvalidParam:          http.StatusBadRequest,
		errs.RequestTooLarge:       http.StatusRequestEntityTooLarge,
		errs.IndexUnavailable:      http.StatusServiceUnavailable,
	}
	for _, entry := range errs.Catalog {
		wantStatus, ok := want[entry]
		if !ok {
			t.Errorf("%s has no expected status in this test", entry.Code)
			continue
		}
		if got := StatusFor(errs.NewError(entry, "cause", "")); got != wantStatus {
			t.Errorf("%s: StatusFor = %d, want %d", entry.Code, got, wantStatus)
		}
	}
}
