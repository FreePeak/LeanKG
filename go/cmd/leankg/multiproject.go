package main

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"

	"github.com/FreePeak/LeanKG/go/internal/projects"
)

// projectRouting wraps a default handler with per-project dispatch: a
// non-empty `project` query parameter selects the project's own handler
// (built lazily from the router's registry — LEANKG_PROJECT_DIRS);
// requests WITHOUT the parameter go to the default handler untouched, so
// the single-project serving path is byte-identical to a router-less
// process.
type projectRouting struct {
	ctx      context.Context
	router   *projects.Router
	fallback http.Handler
	build    func(*projects.Project) http.Handler

	mu    sync.Mutex
	cache map[string]http.Handler // project dir -> built handler
}

// routeByProject wraps fallback with `?project=` dispatch. build mounts
// one transport handler over a project's engine (e.g. rest.Handler or
// leankgmcp.New(engine).HTTPHandler()).
func routeByProject(ctx context.Context, router *projects.Router, fallback http.Handler, build func(*projects.Project) http.Handler) http.Handler {
	return &projectRouting{
		ctx:      ctx,
		router:   router,
		fallback: fallback,
		build:    build,
		cache:    map[string]http.Handler{},
	}
}

func (h *projectRouting) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	sel := r.URL.Query().Get("project")
	if sel == "" {
		h.fallback.ServeHTTP(w, r)
		return
	}
	p, err := h.router.Open(h.ctx, sel)
	if err != nil {
		writeProjectError(w, err)
		return
	}
	h.handlerFor(p).ServeHTTP(w, r)
}

// handlerFor builds (once) the transport handler for a project.
func (h *projectRouting) handlerFor(p *projects.Project) http.Handler {
	h.mu.Lock()
	defer h.mu.Unlock()
	if got, ok := h.cache[p.Dir]; ok {
		return got
	}
	built := h.build(p)
	h.cache[p.Dir] = built
	return built
}

// writeProjectError renders the FR-ZCP-02 unknown-project rejection as
// JSON — an explicit route key never falls back to the default project.
func writeProjectError(w http.ResponseWriter, err error) {
	b, merr := json.Marshal(map[string]string{"error": err.Error()})
	if merr != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusNotFound)
	_, _ = w.Write(b)
}
