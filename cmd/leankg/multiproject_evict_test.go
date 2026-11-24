package main

import (
	"net/http"
	"path/filepath"
	"testing"

	"github.com/FreePeak/LeanKG/internal/projects"
)

// TestRouterEvictionDropsTheBuiltHandler: a handler is built over one project's
// engine. When the router closes that project (LRU eviction or removal), the
// handler must be dropped too, or the next request would be served by a
// closed engine.
func TestRouterEvictionDropsTheBuiltHandler(t *testing.T) {
	t.Setenv("LEANKG_PORTFOLIO_DB", filepath.Join(t.TempDir(), "portfolio.db"))
	alpha := t.TempDir()
	router := projects.NewRouter(t.TempDir(), projects.Config{})
	if err := router.Add(alpha); err != nil {
		t.Fatal(err)
	}
	key := router.List()[1]

	builds := 0
	h := newProjectRouting(router, nil, http.NotFoundHandler(), func(*projects.Project) http.Handler {
		builds++
		return http.NotFoundHandler()
	})
	p := &projects.Project{Dir: key}
	h.handlerFor(p)
	h.handlerFor(p)
	if builds != 1 {
		t.Fatalf("handler built %d times before eviction, want 1 (cached)", builds)
	}

	if err := router.Remove(alpha); err != nil {
		t.Fatal(err)
	}
	h.handlerFor(p)
	if builds != 2 {
		t.Fatalf("handler built %d times after the project was removed, want 2 (rebuilt, not reused)", builds)
	}
}
