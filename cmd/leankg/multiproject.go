package main

import (
	"net/http"
	"sync"

	"github.com/FreePeak/LeanKG/internal/auth"
	"github.com/FreePeak/LeanKG/internal/errs"
	"github.com/FreePeak/LeanKG/internal/httpx"
	"github.com/FreePeak/LeanKG/internal/projects"
)

// projectRouting wraps a default handler with per-project dispatch: a
// non-empty `project` query parameter selects the project's own handler
// (built lazily from the router's registry — LEANKG_PROJECT_DIRS);
// requests WITHOUT the parameter go to the default handler untouched, so
// the single-project serving path is byte-identical to a router-less
// process.
type projectRouting struct {
	router   *projects.Router
	store    auth.Store // the home store: token and org claims that scope a caller
	identify func(*http.Request) (auth.AuthContext, error)
	fallback http.Handler
	build    func(*projects.Project) http.Handler

	mu    sync.Mutex
	cache map[string]http.Handler // project dir -> built handler
}

// routeByProject wraps fallback with `?project=` dispatch. build mounts
// one transport handler over a project's engine (e.g. rest.Handler or
// leankgmcp.New(engine).HTTPHandler()). A caller whose token carries an org
// is limited to the projects that org claims (see auth.RequestScope).
//
// identify resolves the caller of a routed request. It is for a listener that
// routes outside its own gate (the MCP listener); a listener whose gate already
// stored the caller in the request context (REST, the dashboard) passes nil.
//
// The router keeps no context: each request opens its project on that
// request's own context, and a request whose client has gone away is refused
// before anything is opened.
func routeByProject(router *projects.Router, st auth.Store, identify func(*http.Request) (auth.AuthContext, error), fallback http.Handler, build func(*projects.Project) http.Handler) http.Handler {
	return httpx.Guard(newProjectRouting(router, st, identify, fallback, build))
}

// newProjectRouting builds the dispatcher and subscribes it to the router's
// evictions, so a handler built over a closed project engine is dropped with
// it.
func newProjectRouting(router *projects.Router, st auth.Store, identify func(*http.Request) (auth.AuthContext, error), fallback http.Handler, build func(*projects.Project) http.Handler) *projectRouting {
	// A URL-only transport (identify nil) gates the default project here. The MCP
	// listener does not: its project may ride in the tool arguments, and engineFor
	// checks the caller's grant and org scope on the project each call resolves.
	if identify == nil {
		fallback = defaultProjectGate(router, st, fallback)
	}
	h := &projectRouting{
		router:   router,
		store:    st,
		identify: identify,
		fallback: fallback,
		build:    build,
		cache:    map[string]http.Handler{},
	}
	router.OnEvict(h.forget)
	return h
}

// defaultProjectGate serves next only to a caller whose grant and org scope
// cover the default project, the project a request with no selector is served
// from. The refusal is the router's own, the same one a withheld project gets.
func defaultProjectGate(router *projects.Router, st auth.Store, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		scope, err := auth.RequestScope(r.Context(), st, r)
		if err != nil {
			httpx.WriteError(w, errs.NewError(errs.Unauthorized, errs.Unauthorized.Cause, ""))
			return
		}
		if _, err := router.AuthorizeAs(r.Context(), "", scope); err != nil {
			refuseSelector(w, err)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// resolveCaller is the identify step for a listener whose routing sits outside
// its gate: the caller resolves exactly as the gate resolves it.
func resolveCaller(st auth.Store) func(*http.Request) (auth.AuthContext, error) {
	return func(r *http.Request) (auth.AuthContext, error) { return auth.Resolve(st, r) }
}

func (h *projectRouting) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	sel := r.URL.Query().Get("project")
	if sel == "" {
		// A URL-only transport (REST) must name its project in ?project=. The MCP
		// listener (identify set) carries the project in the tool arguments, which
		// this dispatcher cannot read; its engineFor refuses a call with none.
		if h.identify == nil && h.router.Hosted() {
			httpx.WriteError(w, errs.NewError(errs.MissingParam,
				"this server is hosted, so every request must name its project",
				"add ?project=<repo dir or name> to the URL"))
			return
		}
		// The fallback is defaultProjectGate: the caller's grant and org scope
		// apply to the default's name too. The MCP listener's own gate covers
		// its callers, which carry no grant in this context.
		h.fallback.ServeHTTP(w, r)
		return
	}
	scope, err := auth.RequestScope(r.Context(), h.store, r)
	if err != nil {
		httpx.WriteError(w, errs.NewError(errs.Unauthorized, errs.Unauthorized.Cause, ""))
		return
	}
	ctx := r.Context()
	if h.identify != nil {
		caller, err := h.identify(r)
		if err != nil {
			httpx.WriteError(w, errs.NewError(errs.Unauthorized, errs.Unauthorized.Cause, ""))
			return
		}
		ctx = auth.WithAuth(ctx, caller)
	}
	p, release, err := h.router.OpenAs(ctx, sel, scope)
	if err != nil {
		refuseSelector(w, err)
		return
	}
	defer release()
	h.handlerFor(p).ServeHTTP(w, r.WithContext(ctx))
}

// refuseSelector answers a selector the caller may not use: a grant denial is
// a 403, and anything else (unknown, or withheld by org scope) is the same 404,
// so the answer never reveals another org's project.
func refuseSelector(w http.ResponseWriter, err error) {
	if httpx.CodeOf(err) == errs.PermissionDenied.Code {
		httpx.WriteError(w, err)
		return
	}
	// FR-ZCP-02: an unknown project is refused as a 404, never routed to the default.
	httpx.WriteMessage(w, http.StatusNotFound, err.Error())
}

// forget drops the handler built for dir when the router closes its project.
func (h *projectRouting) forget(dir string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.cache, dir)
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
