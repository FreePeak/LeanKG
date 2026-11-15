package main

import (
	"context"
	"github.com/FreePeak/LeanKG/internal/auth"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/FreePeak/LeanKG/internal/httpx"
	"github.com/FreePeak/LeanKG/internal/projects"
	"github.com/FreePeak/LeanKG/internal/rest"
	"github.com/FreePeak/LeanKG/internal/store"
)

// TestRouteByProjectUsesEachRequestsContext proves the router keeps no
// context of its own: a first request whose client went away opens nothing
// and builds nothing, and the requests after it are served as usual.
func TestRouteByProjectUsesEachRequestsContext(t *testing.T) {
	defaultDir, dirA, dirB := t.TempDir(), t.TempDir(), t.TempDir()
	seedProjectDir(t, defaultDir, "DefaultSym")
	seedProjectDir(t, dirA, "AlphaSym")
	seedProjectDir(t, dirB, "BetaSym", "BetaSym2")

	defaultEng := engineOver(t, defaultDir)
	router := projects.NewRouter(defaultDir, projects.Config{
		ExtraDirs: []string{dirA, dirB}, Mode: store.RW, EngineName: "sqlite",
	})
	router.SeedDefault(&projects.Project{Dir: defaultDir, Name: filepath.Base(defaultDir), Engine: defaultEng})
	defer router.Close()

	built := map[string]int{}
	h := auth.MiddlewareWithStore(nil, routeByProject(router, rest.Handler(defaultEng, nil), func(p *projects.Project) http.Handler {
		built[p.Dir]++
		return rest.Handler(p.Engine, p.Memory)
	}))

	gone, cancel := context.WithCancel(context.Background())
	cancel()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/status?project="+filepath.Base(dirA), nil).WithContext(gone))
	if rec.Code != httpx.StatusClientClosedRequest {
		t.Fatalf("disconnected request: status = %d, want %d", rec.Code, httpx.StatusClientClosedRequest)
	}
	if built[dirA] != 0 {
		t.Fatalf("disconnected request built project A %d time(s), want 0", built[dirA])
	}

	srv := httptest.NewServer(h)
	defer srv.Close()
	if els, _ := statusElements(t, srv.URL+"/api/v1/status?project="+filepath.Base(dirB)); els != 2 {
		t.Fatalf("project B after a disconnect: elements = %d, want 2", els)
	}
	if els, _ := statusElements(t, srv.URL+"/api/v1/status?project="+filepath.Base(dirA)); els != 1 {
		t.Fatalf("project A after its disconnected request: elements = %d, want 1", els)
	}
}
