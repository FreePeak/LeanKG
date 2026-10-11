package auto

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/FreePeak/LeanKG/internal/core"
	"github.com/FreePeak/LeanKG/internal/memory"
	"github.com/FreePeak/LeanKG/internal/store"
)

// TestAutoConfigRefusesCwdOutsideServedProject is the fail-closed half: a cwd
// that is NOT inside the project being served must be refused with the fix,
// not silently index the served default. (Live before this: the route ignored
// the cwd entirely, so an agent in another repo got a reindex of a project it
// was not working in, and the response named neither.)
func TestAutoConfigRefusesCwdOutsideServedProject(t *testing.T) {
	base := t.TempDir()
	caller := filepath.Join(base, "caller")
	served := filepath.Join(base, "served")
	for _, d := range []string{caller, served} {
		if err := os.MkdirAll(filepath.Join(d, ".leankg"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	st, err := store.OpenBackend(context.Background(), served, store.EngineSQLite, "", "", store.RW)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.Migrate(); err != nil {
		t.Fatal(err)
	}
	mem, err := memory.Open(filepath.Join(served, ".leankg"), false)
	if err != nil {
		t.Fatal(err)
	}
	eng := core.New(st, mem, nil)
	eng.SetProjectDir(served)

	h := RegisterAutoConfig(http.NewServeMux(), eng)
	body, _ := json.Marshal(AutoConfigRequest{IndexEnabled: true, CWD: caller})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/mcp/auto-config", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409 Conflict: %s", rec.Code, rec.Body)
	}
	var got map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got["project"] != served {
		t.Errorf("project = %v, want the served project %q", got["project"], served)
	}
	if fix, _ := got["fix"].(string); fix == "" {
		t.Error("the refusal carries no fix — a refusal that does not say what to do is a dead end")
	}
}

// TestAutoConfigNoCWDKeepsServingDefault pins that an older caller (no cwd in
// the body) is unaffected and the answer now names the project it acts on.
func TestAutoConfigNoCWDKeepsServingDefault(t *testing.T) {
	served := t.TempDir()
	if err := os.MkdirAll(filepath.Join(served, ".leankg"), 0o755); err != nil {
		t.Fatal(err)
	}
	st, err := store.OpenBackend(context.Background(), served, store.EngineSQLite, "", "", store.RW)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.Migrate(); err != nil {
		t.Fatal(err)
	}
	mem, err := memory.Open(filepath.Join(served, ".leankg"), false)
	if err != nil {
		t.Fatal(err)
	}
	eng := core.New(st, mem, nil)
	eng.SetProjectDir(served)

	h := RegisterAutoConfig(http.NewServeMux(), eng)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/mcp/auto-config", bytes.NewReader([]byte(`{}`)))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body)
	}
	var got map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got["project"] != served {
		t.Errorf("project = %v, want the served default %q", got["project"], served)
	}
}

// TestAutoConfigCwdInsideServedProject pins the accepted case, including a
// subdirectory (the common shape: an agent opens in src/ of the repo).
func TestAutoConfigCwdInsideServedProject(t *testing.T) {
	base := t.TempDir()
	served := filepath.Join(base, "served")
	sub := filepath.Join(served, "src", "pkg")
	if err := os.MkdirAll(filepath.Join(served, ".leankg"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	st, err := store.OpenBackend(context.Background(), served, store.EngineSQLite, "", "", store.RW)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.Migrate(); err != nil {
		t.Fatal(err)
	}
	mem, err := memory.Open(filepath.Join(served, ".leankg"), false)
	if err != nil {
		t.Fatal(err)
	}
	eng := core.New(st, mem, nil)
	eng.SetProjectDir(served)

	h := RegisterAutoConfig(http.NewServeMux(), eng)
	body, _ := json.Marshal(AutoConfigRequest{CWD: sub})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/mcp/auto-config", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (a cwd inside the served project is fine): %s", rec.Code, rec.Body)
	}
}
