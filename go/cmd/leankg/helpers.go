package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/FreePeak/LeanKG/go/internal/core"
	"github.com/FreePeak/LeanKG/go/internal/index"
	"github.com/FreePeak/LeanKG/go/internal/langs"
	"github.com/FreePeak/LeanKG/go/internal/projectcfg"
	"github.com/FreePeak/LeanKG/go/internal/sources"
	"github.com/FreePeak/LeanKG/go/internal/store"
)

// httpMux wraps the MCP handler with a /health endpoint for supervision.
func httpMux(mcp http.Handler) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	})
	mux.Handle("/mcp", mcp)
	return mux
}

func serveHTTP(ctx context.Context, h http.Handler, addr string) {
	srv := &http.Server{Addr: addr, Handler: h, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		fmt.Fprintf(os.Stderr, "leankg: http %s: %v\n", addr, err)
	}
}

// resolveProjectDir picks the project directory for a verb: the explicit
// --project/--path value, then LEANKG_PROJECT, then the working directory,
// then the leankg.yaml project.project_path / project.root anchor
// (canonicalized before use — Rust MCPServer::resolve_project_root, N3).
func resolveProjectDir(flagValue string) string {
	dir := flagValue
	if dir == "" {
		dir = envOr("LEANKG_PROJECT", ".")
	}
	if dbDir := filepath.Join(dir, ".leankg"); projectcfg.ResolveProjectDBDir(dbDir) != dbDir {
		dir = filepath.Dir(projectcfg.ResolveProjectRoot(dbDir))
	}
	return dir
}

// pgURLFor applies Rust's POSTGRES precedence: the LEANKG_PG_URL environment
// variable > the nearest leankg.yaml `db:` block > empty (driver default).
func pgURLFor(dir string) string {
	if v := os.Getenv("LEANKG_PG_URL"); v != "" {
		return v
	}
	if db := projectcfg.DBConfigFromDir(dir); db != nil {
		return db.URL
	}
	return ""
}

// openEngine opens a project's store and returns a query-ready engine with the
// project directory and the lazily-activated language registry wired, exactly
// like the serving transports. The caller closes engine.Store().
func openEngine(dir string, mode store.Mode) (*core.Engine, error) {
	st, err := store.OpenBackend(context.Background(), dir,
		envOr("LEANKG_DB_ENGINE", "sqlite"), pgURLFor(dir), mode)
	if err != nil {
		return nil, err
	}
	engine := core.New(st, nil, nil)
	engine.SetProjectDir(dir)
	reg := langs.DefaultRegistry()
	if _, aerr := reg.Activate(dir); aerr == nil {
		engine.SetLangsRegistry(reg)
	}
	return engine, nil
}

// runIndex performs a one-shot index run into the project store. A non-empty
// source is a --source URI whose tree is synced into <project>/.leankg/sources
// and indexed instead of target (Rust main.rs index path resolution).
func runIndex(project, target, source, refName, auth string) error {
	dir := project
	if dir == "" {
		dir = target
	}
	if dir == "" {
		// `index --source <uri>` with no positional argument: the project root
		// is the cwd (Rust find_project_root), never the filesystem root.
		dir = "."
	}
	indexTarget := target
	if source != "" {
		synced, err := sources.Resolve(context.Background(), dir, source, auth, refName, sources.CLIProgress{})
		if err != nil {
			return err
		}
		indexTarget = synced
	}
	// N1 self-heal: refill a missing project.project_path anchor before
	// deriving the schema, using THIS run's canonical identity (Rust
	// config::project::ensure_identity_fields_for_db).
	if abs, err := filepath.Abs(indexTarget); err == nil {
		if resolved, rerr := filepath.EvalSymlinks(abs); rerr == nil {
			abs = resolved
		}
		projectcfg.EnsureIdentityFieldsForDB(filepath.Join(dir, ".leankg"), abs)
	}
	st, err := store.OpenBackend(context.Background(), dir,
		os.Getenv("LEANKG_DB_ENGINE"), pgURLFor(dir), store.RW)
	if err != nil {
		return fmt.Errorf("open store: %w", err)
	}
	defer st.Close()
	if err := st.Migrate(); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	reg := langs.DefaultRegistry()
	if _, aerr := reg.Activate(indexTarget); aerr != nil {
		return fmt.Errorf("language detection: %w", aerr)
	}
	res, err := index.IndexDirWith(context.Background(), st, indexTarget, reg)
	if err != nil {
		return fmt.Errorf("index %s: %w", indexTarget, err)
	}
	fmt.Printf("indexed %s: files=%d elements=%d relationships=%d skipped=%d\n",
		indexTarget, res.Files, res.Elements, res.Relationships, res.Skipped)
	return nil
}
