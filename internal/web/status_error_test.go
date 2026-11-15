package web

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/FreePeak/LeanKG/internal/core"
	"github.com/FreePeak/LeanKG/internal/store"
)

// countFailStore fails the element count and answers everything else with the
// nil embedded interface, which panics: the status handler must stop at the
// failed read rather than publish a null count with a 200.
type countFailStore struct{ store.Backend }

func (countFailStore) Path() string { return "" }

func (countFailStore) ElementCount(context.Context) (int, error) {
	return 0, errors.New("count broken")
}

func TestIndexStatusSurfacesStoreError(t *testing.T) {
	h := APIHandler(core.New(countFailStore{}, core.Options{}), nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/index/status", nil))
	var env envelope
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	if rec.Code == http.StatusOK || env.Success || env.Error == nil {
		t.Fatalf("status %d env %+v; want a failed envelope, not a 200 with null counts", rec.Code, env)
	}
}
