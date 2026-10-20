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

func (f *fakeRouter) EngineFor(_ context.Context, project string) (*core.Engine, error) {
	if f.err != nil {
		return nil, f.err
	}
	e, ok := f.engines[project]
	if !ok {
		return nil, errors.New("unknown project " + project)
	}
	return e, nil
}

func (f *fakeRouter) MultiProject() bool { return f.multi }

func openEngine(t *testing.T, dir string) *core.Engine {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	st, err := store.OpenBackend(context.Background(), dir, store.EngineSQLite, "", "", store.RW)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.Migrate(); err != nil {
		t.Fatal(err)
	}
	mem, err := memory.Open(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	e := core.New(st, mem, nil)
	e.SetProjectDir(dir)
	return e
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

	h := RegisterAutoConfig(http.NewServeMux(), defEng, WithRouter(r))
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

	h := RegisterAutoConfig(http.NewServeMux(), defEng, WithRouter(r))
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
