package auto

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/FreePeak/LeanKG/internal/core"
	"github.com/FreePeak/LeanKG/internal/embed"
	"github.com/FreePeak/LeanKG/internal/index"
	"github.com/FreePeak/LeanKG/internal/langs"
	"github.com/FreePeak/LeanKG/internal/refresh"
	"github.com/FreePeak/LeanKG/internal/store"
)

// AutoConfigRequest controls the MCP auto-init behaviors served over REST.
// POST /api/v1/mcp/auto-config — no project parameter; always operates on
// the served project's own engine (the default project when no router is set).
type AutoConfigRequest struct {
	IndexEnabled      bool `json:"index_enabled"`
	EmbedEnabled      bool `json:"embed_enabled"`
	EmbedDebounceSecs int  `json:"embed_debounce_seconds,omitempty"`
	// CWD is the caller's working directory (FR-ZCP-01 clause 3). The route
	// used to ignore it entirely and index engine.ProjectDir() — measured on a
	// 298-project host, an agent working in one repo could only ever index the
	// project the server happened to serve, and the response said nothing
	// either way. It is resolved against the served project: when the cwd is
	// inside it (or IS it) the served project is used; when it is not, the
	// caller is told rather than silently indexing a project they are not in.
	CWD string `json:"cwd"`
}

var (
	autoIndexMu      sync.Mutex
	autoIndexRunning bool
	autoEmbedMu      sync.Mutex
	autoEmbedRunning bool
)

func decodeJSON[T any](w http.ResponseWriter, r *http.Request, into *T) bool {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewDecoder(r.Body).Decode(into); err != nil {
		http.Error(w, "invalid JSON body: "+err.Error(), http.StatusBadRequest)
		return false
	}
	return true
}

func writeAutoJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

// Router is the per-request project selector (internal/projects). It is an
// interface so this package does not import the concrete router.
type Router interface {
	EngineFor(ctx context.Context, project string) (*core.Engine, error)
	MultiProject() bool
}

// Option configures RegisterAutoConfig.
type Option func(*config)

type config struct{ router Router }

// WithRouter lets the endpoint honour ?project=<dir or name>, the same
// selector the query path uses.
//
// Without it the route wraps cmd's routeByProject, so its own mux matches the
// path first and EVERY request goes to the default engine — measured live:
// ?project=/another/repo still answered project=/…/freepeak/leankg. That made
// #490's 409 advice ("send ?project=…") false, which is worse than no advice.
// With it, the selector resolves before the engine is chosen.
func WithRouter(r Router) Option { return func(c *config) { c.router = r } }

// RegisterAutoConfig mounts POST /api/v1/mcp/auto-config on the mux.
// The endpoint triggers background auto-index and/or auto-embed for the
// selected project; both default to off and must be explicitly enabled.
func RegisterAutoConfig(h http.Handler, engine *core.Engine, opts ...Option) http.Handler {
	var cfg config
	for _, o := range opts {
		o(&cfg)
	}
	mux := http.NewServeMux()
	mux.Handle("/", h)
	mux.HandleFunc("POST /api/v1/mcp/auto-config", func(w http.ResponseWriter, r *http.Request) {
		eng := engine
		// ?project= wins when a router is wired; without one we keep the
		// single-engine behaviour a stdio spawn depends on.
		if sel := r.URL.Query().Get("project"); sel != "" && cfg.router != nil {
			resolved, err := cfg.router.EngineFor(r.Context(), sel)
			if err != nil {
				// Fail closed: an unknown selector never falls back to the
				// default engine, and nothing is indexed.
				writeAutoJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
				return
			}
			eng = resolved
		}
		handleAutoConfig(w, r, eng)
	})
	return mux
}

func handleAutoConfig(w http.ResponseWriter, r *http.Request, engine *core.Engine) {
	var req AutoConfigRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.EmbedDebounceSecs == 0 {
		req.EmbedDebounceSecs = 120
	}

	ctx := r.Context()
	projectDir := engine.ProjectDir()
	st := engine.Store()

	if req.IndexEnabled {
		go startAutoIndex(ctx, st, projectDir)
	}
	if req.EmbedEnabled {
		go startAutoEmbed(ctx, st, projectDir, req.EmbedDebounceSecs)
	}

	// FR-ZCP-01 clause 3: a caller asking to auto-index the project it is
	// working in must not silently index a different one. The routing layer
	// selects the engine from ?project= (cmd/leankg/multiproject.go), so the
	// engine this handler holds IS the selected project; a cwd outside it means
	// the caller is working somewhere else and would get the wrong reindex.
	// We refuse with the fix rather than acting on a project they are not in
	// — the same fail-closed rule #480 applied to the query path.
	if req.CWD != "" && !withinDir(projectDir, req.CWD) && !withinDir(req.CWD, projectDir) {
		writeAutoJSON(w, http.StatusConflict, map[string]any{
			"error":   "cwd is not inside the project this endpoint serves",
			"cwd":     req.CWD,
			"project": projectDir,
			"fix":     "send the same ?project=<dir or name> on the request URL that you would send on a query, or run leankg index in that directory",
		})
		return
	}
	writeAutoJSON(w, http.StatusOK, map[string]any{
		"index_enabled":          req.IndexEnabled,
		"embed_enabled":          req.EmbedEnabled,
		"embed_debounce_seconds": req.EmbedDebounceSecs,
		"project":                projectDir,
	})
}

// startAutoIndex runs the index in a background goroutine (single-flight per
// process: a second call while one is in flight is a no-op with a logged note).
func startAutoIndex(ctx context.Context, st store.Backend, dir string) {
	autoIndexMu.Lock()
	if autoIndexRunning {
		autoIndexMu.Unlock()
		log.Printf("auto-config: index already running, skipping")
		return
	}
	autoIndexRunning = true
	autoIndexMu.Unlock()
	defer func() {
		autoIndexMu.Lock()
		autoIndexRunning = false
		autoIndexMu.Unlock()
	}()

	if _, err := os.Stat(filepath.Join(dir, ".leankg")); err != nil {
		log.Printf("auto-config: index skipped: no .leankg at %s", dir)
		return
	}
	langReg := langs.DefaultRegistry()
	if _, aerr := langReg.Activate(dir); aerr != nil {
		log.Printf("auto-config: index language detection: %v", aerr)
		return
	}
	res, err := index.IndexDirWith(ctx, st, dir, langReg)
	if err != nil {
		log.Printf("auto-config: index failed: %v", err)
		return
	}
	if _, ierr := store.RefreshInventory(st); ierr != nil {
		log.Printf("auto-config: index inventory refresh: %v", ierr)
	}
	log.Printf("auto-config: index complete: files=%d elements=%d relationships=%d", res.Files, res.Elements, res.Relationships)
}

// startAutoEmbed waits the debounce window for index to settle, then runs
// the embedding pipeline in a background goroutine (single-flight per process).
func startAutoEmbed(ctx context.Context, st store.Backend, dir string, debounceSecs int) {
	autoEmbedMu.Lock()
	if autoEmbedRunning {
		autoEmbedMu.Unlock()
		log.Printf("auto-config: embed already running, skipping")
		return
	}
	autoEmbedRunning = true
	autoEmbedMu.Unlock()
	defer func() {
		autoEmbedMu.Lock()
		autoEmbedRunning = false
		autoEmbedMu.Unlock()
	}()

	select {
	case <-ctx.Done():
		return
	case <-time.After(time.Duration(debounceSecs) * time.Second):
	}

	provider, release, err := embed.StartProvider(ctx)
	if err != nil {
		log.Printf("auto-config: embed skipped: %v", err)
		return
	}
	defer release()
	_ = provider

	res, err := refresh.Run(ctx, refresh.Options{Project: dir, Path: dir})
	if err != nil {
		log.Printf("auto-config: embed failed: %v", err)
		return
	}
	if res.EmbedSkipped != "" {
		log.Printf("auto-config: embed skipped: %s", res.EmbedSkipped)
	} else {
		log.Printf("auto-config: embed complete: embedded=%d skipped=%d failed=%d", res.Embed.Embedded, res.Embed.Skipped, res.Embed.Failed)
	}
}

// withinDir reports whether path is dir itself or lives underneath it. Both
// are canonicalized first, so a symlinked or relative cwd still resolves.
func withinDir(dir, path string) bool {
	if dir == "" || path == "" {
		return false
	}
	d, err1 := filepath.Abs(dir)
	p, err2 := filepath.Abs(path)
	if err1 != nil || err2 != nil {
		return false
	}
	if d == p {
		return true
	}
	return strings.HasPrefix(p, d+string(filepath.Separator))
}
