package auto

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/FreePeak/LeanKG/internal/core"
	"github.com/FreePeak/LeanKG/internal/memory"
	"github.com/FreePeak/LeanKG/internal/store"
)

type fakeRouter struct {
	engines map[string]*core.Engine
	multi   bool
	err     error
}

func (f *fakeRouter) EngineFor(_ context.Context, project string) (*core.Engine, func(), error) {
	if f.err != nil {
		return nil, nil, f.err
	}
	e, ok := f.engines[project]
	if !ok {
		return nil, nil, errors.New("unknown project " + project)
	}
	return e, func() {}, nil
}

func (f *fakeRouter) MultiProject() bool { return f.multi }

func openEngine(t *testing.T, dir string) *core.Engine {
	t.Helper()
	e, _ := openEngineWithStore(t, dir)
	return e
}

// openEngineWithStore is openEngine that also returns the store the engine
// wraps, for tests that read rows directly.
func openEngineWithStore(t *testing.T, dir string) (*core.Engine, store.Backend) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	st, err := store.OpenBackend(context.Background(), store.OpenOptions{ProjectDir: dir, Engine: store.EngineSQLite, PGURL: "", DBPath: "", Mode: store.RW})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	mem, err := memory.Open(context.Background(), dir, false)
	if err != nil {
		t.Fatal(err)
	}
	return core.New(st, core.Options{Memory: mem, ProjectDir: dir}), st
}

// TestAutoConfigHonoursProjectSelector is the follow-up to #490's finding:
// the 409 there tells the caller to add ?project=<dir or name>, and on the
// REST wiring that advice was FALSE — RegisterAutoConfig wrapped the router,
// so its own mux matched the path first and every request went to the default
// engine. Measured live: ?project=/Users/linh.doan/work/ai_generated_docs
// still answered project=/…/freepeak/leankg.
//
// A refusal that names a fix which does not work is worse than no refusal.
func TestAutoConfigHonoursProjectSelector(t *testing.T) {
	base := t.TempDir()
	defaultDir := filepath.Join(base, "default")
	otherDir := filepath.Join(base, "other")
	defEng := openEngine(t, defaultDir)
	otherEng := openEngine(t, otherDir)
	r := &fakeRouter{engines: map[string]*core.Engine{otherDir: otherEng}, multi: true}

	h := gatedAutoConfig(http.NewServeMux(), defEng, WithRouter(r))
	body := []byte(`{"index_enabled":false,"embed_enabled":false}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/mcp/auto-config?project="+otherDir, bytes.NewReader(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body)
	}
	var got map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got["project"] != otherDir {
		t.Errorf("project = %v, want %q — the ?project= selector must reach this route", got["project"], otherDir)
	}
}

// TestAutoConfigRefusesUnknownProject pins that a selector naming no
// registered project is refused with the same fail-closed message the query
// path uses, rather than silently falling back to the default engine.
func TestAutoConfigRefusesUnknownProject(t *testing.T) {
	base := t.TempDir()
	defEng := openEngine(t, filepath.Join(base, "default"))
	r := &fakeRouter{engines: map[string]*core.Engine{}, multi: true, err: errors.New("unknown project \"/nope\" — queries never fall back to another project's data")}

	h := gatedAutoConfig(http.NewServeMux(), defEng, WithRouter(r))
	body := []byte(`{"index_enabled":true}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/mcp/auto-config?project=/nope", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body)
	}
	var got map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if _, ok := got["error"]; !ok {
		t.Errorf("no error in the refusal: %v", got)
	}
	// Never index anything when the selector is unknown.
	if got["project"] != nil {
		t.Errorf("refusal names a project (%v) — it must not imply one was used", got["project"])
	}
}
