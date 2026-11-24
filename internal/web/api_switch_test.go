package web

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/FreePeak/LeanKG/internal/core"
	"github.com/FreePeak/LeanKG/internal/errs"
	"github.com/FreePeak/LeanKG/internal/store"
)

// TestProjectSwitchBehavior pins the CONTRACT, not the status code: the
// dashboard's switchProject call is a dead end if the envelope says
// success:false, and failEnvelope is ALSO HTTP 200 — so a bare status check
// passed while the feature was broken. These cases assert the payload.
func TestProjectSwitchBehavior(t *testing.T) {
	served := t.TempDir()
	other := t.TempDir()
	for _, d := range []string{served, other} {
		if err := os.MkdirAll(filepath.Join(d, "src"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(d, "src", "a.go"), []byte("package a\n\nfunc A() int { return 1 }\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	st, err := store.OpenBackend(context.Background(), store.OpenOptions{DBPath: filepath.Join(served, ".leankg", "leankg.db"), Mode: store.RW})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	engine := core.New(st, core.Options{ProjectDir: served})

	// A multi-project server resolves the other root; the same handler without
	// the switcher must still refuse — and say why truthfully.
	withSwitcher := APIHandler(engine, nil, WithProjectDir(served),
		WithProjectSwitcher(func(_ context.Context, path string) (string, error) {
			if filepath.Clean(path) == filepath.Clean(other) {
				return other, nil
			}
			return "", errors.New("unknown project")
		}))
	single := APIHandler(engine, nil, WithProjectDir(served))

	post := func(h http.Handler, path string) (bool, string) {
		t.Helper()
		body, err := json.Marshal(map[string]string{"path": path})
		if err != nil {
			t.Fatal(err)
		}
		req := httptest.NewRequest("POST", "/api/project/switch", strings.NewReader(string(body)))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK && rec.Code/100 != 4 {
			t.Fatalf("status = %d, want 200 or a 4xx refusal (envelope carries the outcome)", rec.Code)
		}
		var env struct {
			Success bool           `json:"success"`
			Data    map[string]any `json:"data"`
			Error   *string        `json:"error"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
			t.Fatalf("decode %q: %v", rec.Body.String(), err)
		}
		msg := ""
		if env.Error != nil {
			msg = *env.Error
		}
		return env.Success, msg
	}

	// 1. The served project always succeeds.
	if ok, msg := post(withSwitcher, served); !ok {
		t.Fatalf("switch to the served project failed: %s", msg)
	}
	// 2. A registered project succeeds in multi-project mode, and the payload
	//    names the project (the UI then selects it per request).
	ok, msg := post(withSwitcher, other)
	if !ok {
		t.Fatalf("switch to a served project failed: %s", msg)
	}
	// 3. Without the switcher the refusal must be accurate: it must NOT claim
	//    the engine cannot serve several projects, and must name the fix.
	ok, msg = post(single, other)
	if ok {
		t.Fatal("single-project handler reported a successful switch it cannot serve")
	}
	if strings.Contains(msg, "not supported by this engine") {
		t.Fatalf("stale refusal text (multi-project serving exists now): %s", msg)
	}
	if !strings.Contains(msg, "LEANKG_PROJECT_DIRS") || !strings.Contains(msg, "?project=") {
		t.Fatalf("refusal does not name the fix: %s", msg)
	}
	// 4. An unknown path is an unknown path, in both modes.
	if ok, _ := post(withSwitcher, filepath.Join(t.TempDir(), "nope")); ok {
		t.Fatal("switch to a directory without a store reported success")
	}
}

// TestProjectSwitchDeniedWithoutProjectDir pins that the switcher's refusal
// holds when the handler has no project dir (a PG backend derives none): the
// fallback must not answer for an arbitrary directory on the caller's behalf.
func TestProjectSwitchDeniedWithoutProjectDir(t *testing.T) {
	served := t.TempDir()
	other := t.TempDir()
	st, err := store.OpenBackend(context.Background(), store.OpenOptions{DBPath: filepath.Join(served, ".leankg", "leankg.db"), Mode: store.RW})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	engine := core.New(st, core.Options{ProjectDir: served})
	h := APIHandler(engine, nil, WithProjectDir(""), WithProjectSwitcher(func(_ context.Context, path string) (string, error) {
		return "", errs.NewError(errs.PermissionDenied, "not granted", "")
	}))

	body, err := json.Marshal(map[string]string{"path": other})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("POST", "/api/project/switch", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden || strings.Contains(rec.Body.String(), "project_path") {
		t.Fatalf("withheld path without a project dir = %d, want 403 with no project_path; got %s", rec.Code, rec.Body.String())
	}
}
