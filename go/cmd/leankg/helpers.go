package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/FreePeak/LeanKG/go/internal/core"
	"github.com/FreePeak/LeanKG/go/internal/index"
	"github.com/FreePeak/LeanKG/go/internal/langs"
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
// --project/--path value, then LEANKG_PROJECT, then the working directory.
func resolveProjectDir(flagValue string) string {
	if flagValue != "" {
		return flagValue
	}
	return envOr("LEANKG_PROJECT", ".")
}

// openEngine opens a project's store and returns a query-ready engine with the
// project directory and the lazily-activated language registry wired, exactly
// like the serving transports. The caller closes engine.Store().
func openEngine(dir string, mode store.Mode) (*core.Engine, error) {
	st, err := store.OpenBackend(context.Background(), dir,
		envOr("LEANKG_DB_ENGINE", "sqlite"), os.Getenv("LEANKG_PG_URL"), mode)
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

// runIndex performs a one-shot index run into the project store.
func runIndex(project, target string) error {
	dir := project
	if dir == "" {
		dir = target
	}
	st, err := store.OpenBackend(context.Background(), dir, os.Getenv("LEANKG_DB_ENGINE"), os.Getenv("LEANKG_PG_URL"), store.RW)
	if err != nil {
		return fmt.Errorf("open store: %w", err)
	}
	defer st.Close()
	if err := st.Migrate(); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	// Lazy language activation governs what gets indexed: only the languages
	// this target actually uses own their extensions.
	reg := langs.DefaultRegistry()
	if _, aerr := reg.Activate(target); aerr != nil {
		return fmt.Errorf("language detection: %w", aerr)
	}
	res, err := index.IndexDirWith(context.Background(), st, target, reg)
	if err != nil {
		return fmt.Errorf("index %s: %w", target, err)
	}
	fmt.Printf("indexed %s: files=%d elements=%d relationships=%d skipped=%d\n",
		target, res.Files, res.Elements, res.Relationships, res.Skipped)
	return nil
}
