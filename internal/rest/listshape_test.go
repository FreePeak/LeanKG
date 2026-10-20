package rest

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/FreePeak/LeanKG/internal/core"
	"github.com/FreePeak/LeanKG/internal/memory"
	"github.com/FreePeak/LeanKG/internal/store"
)

// TestTheRESTSuccessShapeNeverContradictsTheBody pins the last surface the
// loop's convergence had not reached, found by wave 22 driving every REST route.
//
// Wave 21 fixed the two `doctor --deep` WARNs so they name what they inspected.
// REST has the same missing fact in three places, and each is a way for a
// client to be told something false:
//
//   - `GET /api/v1/ontology/matches` answered `{"matches": null}` on a store with
//     no imported ontology — and `null` reads as "the query found no matches",
//     which is a statement about the corpus. It is a statement about ABSENCE OF
//     CONFIGURATION, and the caller cannot tell the two. It is the same
//     "diagnosed nothing, reported it as a result" shape wave 15/16 fixed in
//     writes and wave 18/19 fixed in reads.
//
//   - `GET /api/v2/incidents` answered `{"incidents": null}` for the same reason.
//
//   - `POST /api/v1/memory/banks/{bank}/recall` answered `{"entries": null}`.
//
// Go marshals a nil slice as `null` and an empty slice as `[]`. The three are
// *different answers* — "never configured", "configured and found nothing" — and
// the wire is currently collapsing the first two into `null`. Every other list
// this product returns over the same path (`hits`, `conflicts`, `refs`) already
// distinguishes them, so the fix is consistency, not a new concept.
//
// The rule, applied: **a list answer says whether there is a list to return.**
func TestTheRESTSuccessShapeNeverContradictsTheBody(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(dir+"/.leankg/leankg.db", store.RW)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.Migrate(); err != nil {
		t.Fatal(err)
	}
	eng := core.New(st, nil, nil)
	eng.SetProjectDir(dir)
	// A real memory handle: the memory routes are only mounted when one is
	// present, and without it they 404 (a separate, separately-honest fact).
	mem, err := memory.Open(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	mux := Handler(eng, mem)

	for _, tc := range []struct{ name, method, path, listKey string }{
		{"ontology matches", http.MethodGet, "/api/v1/ontology/matches", "matches"},
		{"incidents", http.MethodGet, "/api/v2/incidents", "incidents"},
		{"memory recall", http.MethodPost, "/api/v1/memory/banks/b1/recall", "entries"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var body string
			if tc.method == http.MethodPost {
				body = `{}`
			}
			req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, req)
			if rec.Code != http.StatusOK {
				t.Fatalf("%s: want 200, got %d (%s)", tc.name, rec.Code, rec.Body.String())
			}
			// The LIST must be a list (never null), and when it is empty the
			// answer must carry the sentence that says WHY.
			var got map[string]any
			if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
				t.Fatalf("%s: %v (%s)", tc.name, err, rec.Body.String())
			}
			list, ok := got[tc.listKey]
			if !ok {
				t.Fatalf("%s: no %q key in %s", tc.name, tc.listKey, rec.Body.String())
			}
			if list == nil {
				t.Fatalf("%s: %q is null — a list answer must be an array, so 'never configured' stays "+
					"distinguishable from 'found nothing': %s", tc.name, tc.listKey, rec.Body.String())
			}
			if arr, isArr := list.([]any); isArr && len(arr) == 0 {
				if note, _ := got["note"].(string); strings.TrimSpace(note) == "" {
					t.Errorf("%s: an empty %q must carry a note saying whether anything is configured "+
						"at all, got %s", tc.name, tc.listKey, rec.Body.String())
				}
			}
		})
	}
}
