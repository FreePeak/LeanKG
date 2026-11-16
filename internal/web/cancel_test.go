package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/FreePeak/LeanKG/internal/core"
	"github.com/FreePeak/LeanKG/internal/httpx"
	"github.com/FreePeak/LeanKG/internal/store"
)

// poisonStore fails any test that reaches the store: every method call goes
// through the nil embedded interface and panics.
type poisonStore struct{ store.Backend }

// Path is read once when the handler is built, not per request.
func (poisonStore) Path() string { return "" }

func TestCancelledAPIRequestNeverReachesStore(t *testing.T) {
	h := APIHandler(core.New(poisonStore{}, core.Options{}), nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	for _, path := range []string{"/api/search?q=x", "/api/index/status", "/api/graph/clusters"} {
		req := httptest.NewRequest(http.MethodGet, path, nil).WithContext(ctx)
		rec := httptest.NewRecorder()
		func() {
			defer func() {
				if p := recover(); p != nil {
					t.Errorf("GET %s reached the store on a cancelled request: %v", path, p)
				}
			}()
			h.ServeHTTP(rec, req)
		}()
		if rec.Code != httpx.StatusClientClosedRequest {
			t.Errorf("GET %s: status = %d, want %d", path, rec.Code, httpx.StatusClientClosedRequest)
		}
	}
}
