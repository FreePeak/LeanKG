package embed

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/FreePeak/LeanKG/internal/store"
)

// TestIdentityKeyEncodingIsGolden pins the one Key encoding. The shape is the
// vector-space key the memory index already stores rows under
// (model|revision|provider|dims|chunker|query-prefix|document-prefix), so a
// change here re-embeds every memory row. An uncatalogued model has no prefix.
func TestIdentityKeyEncodingIsGolden(t *testing.T) {
	id := newIdentity("local", "custom-gguf", "rev-x", 384, 0)
	want := "custom-gguf|rev-x|local|384|3||"
	if got := id.Key(); got != want {
		t.Fatalf("Key() = %q, want %q", got, want)
	}
}

// TestIdentityGettersDoNoIO proves the identity getters never reach the
// endpoint: Identity() and the TextBudget it carries are pure, and only the
// explicit Probe(ctx) asks the sidecar /props, once, after which the answer is
// cached.
func TestIdentityGettersDoNoIO(t *testing.T) {
	var hits atomic.Int32
	const sha = "0123456789abcdef0123456789abcdef01234567"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if r.URL.Path != "/props" {
			http.NotFound(w, r)
			return
		}
		body := map[string]any{
			"model_path":                  "/models/snapshots/" + sha + "/m.gguf",
			"default_generation_settings": map[string]any{"n_ctx": 512},
		}
		_ = json.NewEncoder(w).Encode(body)
	}))
	defer srv.Close()

	p, _, err := Attach(Config{Provider: "local", BaseURL: srv.URL + "/v1"})
	if err != nil {
		t.Fatalf("Attach: %v", err)
	}
	_ = p.Identity()
	if got := hits.Load(); got != 0 {
		t.Fatalf("Identity() made %d requests, want 0", got)
	}
	if err := p.Probe(context.Background()); err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if got := hits.Load(); got != 1 {
		t.Fatalf("Probe made %d requests, want 1", got)
	}
	if err := p.Probe(context.Background()); err != nil {
		t.Fatalf("second Probe: %v", err)
	}
	id := p.Identity()
	if got := hits.Load(); got != 1 {
		t.Fatalf("probed identity read made requests: total %d, want 1", got)
	}
	if want := "local:m.gguf@" + sha; id.Revision != want {
		t.Fatalf("Revision = %q, want %q", id.Revision, want)
	}
	if want := 512*2 - 64; id.TextBudget != want {
		t.Fatalf("TextBudget = %d, want %d", id.TextBudget, want)
	}
}

// TestReadCalibrationAcceptsPreCX40Identity keeps a noise floor written by the
// previous binary valid: its identity string has no model id, and it must
// still read back, so the upgrade does not re-measure every collection.
func TestReadCalibrationAcceptsPreCX40Identity(t *testing.T) {
	st := testStore(t)
	stamp := store.ModelStamp{ModelID: "m", Revision: "r", Dimensions: 384, Distance: Distance,
		Provider: "local", ChunkerVersion: ChunkerVersion}
	legacy := fmt.Sprintf("%s|%s|%d|%d|%s|%s", "r", "local", 384, ChunkerVersion, "", "")
	raw, _ := json.Marshal(map[string]any{"floor": 0.5, "identity": legacy})
	if err := st.KVSet(context.Background(), calibrationNS, "m", string(raw)); err != nil {
		t.Fatalf("seed: %v", err)
	}
	floor, ok := ReadCalibration(st, stamp)
	if !ok || floor != 0.5 {
		t.Fatalf("ReadCalibration = (%v, %v), want (0.5, true) for the pre-CX-40 identity", floor, ok)
	}
}
