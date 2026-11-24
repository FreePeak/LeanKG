package main

import (
	"net/http"
	"testing"

	"github.com/FreePeak/LeanKG/internal/projects"
)

// TestRoutedHandlerIsBuiltForEachProjectInstance: an evicted project that is
// reopened under the same directory is a new instance with a new engine. The
// routing handler built over the closed instance must not be reused for it,
// or a request answers "database is closed". The cache must key on the
// instance, not on the directory alone.
func TestRoutedHandlerIsBuiltForEachProjectInstance(t *testing.T) {
	built := 0
	build := func(p *projects.Project) http.Handler {
		built++
		return http.NotFoundHandler()
	}
	h := newProjectRouting(projects.NewRouter(t.TempDir(), projects.Config{}), nil, nil, nil, build)

	first := &projects.Project{Dir: "/routed/a"}
	h.handlerFor(first)
	h.handlerFor(first)
	if built != 1 {
		t.Fatalf("the same instance built %d handlers, want 1 (cached)", built)
	}
	reopened := &projects.Project{Dir: "/routed/a"}
	h.handlerFor(reopened)
	if built != 2 {
		t.Fatalf("a reopened instance of the same dir built %d handlers in total, want 2 (a fresh one)", built)
	}
}
