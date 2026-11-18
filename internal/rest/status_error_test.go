package rest

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/FreePeak/LeanKG/internal/core"
	"github.com/FreePeak/LeanKG/internal/errs"
	"github.com/FreePeak/LeanKG/internal/store"
)

// elementCountFails fails the first status read; the nil embedded interface
// panics on anything else, so the handler must stop at the failed read.
type elementCountFails struct{ store.Backend }

func (elementCountFails) Path() string { return "" }

func (elementCountFails) ElementCount(context.Context) (int, error) {
	return 0, errors.New("no such table: elements")
}

// TestStatusStoreFailureAnswers500 pins the store-boundary rule: a failed read
// of the status is a server fault (500), not a client error (400).
func TestStatusStoreFailureAnswers500(t *testing.T) {
	h := gatedHandler(core.New(elementCountFails{}, core.Options{}), nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/status", nil))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status %d body %s; want 500 for a store failure", rec.Code, rec.Body.String())
	}
}

// TestOversizeQueryBodyAnswers413WithCode pins the body cap on the REST decode
// path: a body over 1 MiB is refused with the catalog code before the engine
// is touched (the fake store would fail the read if it were).
func TestOversizeQueryBodyAnswers413WithCode(t *testing.T) {
	h := gatedHandler(core.New(elementCountFails{}, core.Options{}), nil)
	body := `{"query":"` + strings.Repeat("a", 1<<20) + `"}`
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/query", strings.NewReader(body)))
	var got struct {
		Code string `json:"code"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &got)
	if rec.Code != http.StatusRequestEntityTooLarge || got.Code != errs.RequestTooLarge.Code {
		t.Fatalf("status %d code %q; want 413 with %s", rec.Code, got.Code, errs.RequestTooLarge.Code)
	}
}
