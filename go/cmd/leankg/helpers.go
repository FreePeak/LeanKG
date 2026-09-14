package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/FreePeak/LeanKG/go/internal/core"
	"github.com/FreePeak/LeanKG/go/internal/embed"
	"github.com/FreePeak/LeanKG/go/internal/index"
	"github.com/FreePeak/LeanKG/go/internal/langs"
	"github.com/FreePeak/LeanKG/go/internal/portfolioreg"
	"github.com/FreePeak/LeanKG/go/internal/projectcfg"
	"github.com/FreePeak/LeanKG/go/internal/sources"
	"github.com/FreePeak/LeanKG/go/internal/store"
	"github.com/FreePeak/LeanKG/go/internal/summarize"
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
	// Query-time embedding for one-shot verbs: attach only (embed.FromEnv never
	// spawns), so a CLI run can never orphan a sidecar. Without this,
	// `leankg query --action semantic` could never reach L3 even with a
	// provider running — found dogfooding the real repo.
	if os.Getenv("LEANKG_EMBED_PROVIDER") != "" || os.Getenv("LEANKG_EMBED_BASE_URL") != "" {
		if p, perr := embed.FromEnv(); perr == nil {
			engine.SetEmbedder(core.QueryEmbedderFromProvider(p))
		} else {
			fmt.Fprintf(os.Stderr, "embed provider: %v (L3 degraded)\n", perr)
		}
	}
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
	// The store reconciles its file set against the walk: paths are relative to
	// the walk root, so walking anything other than the project root reads every
	// file outside it as deleted and sweeps it. `index . --source <path>` did
	// exactly that (the source replaced the project's own elements), so the two
	// roots must agree. Index the source as its own project instead.
	if same, serr := sameIndexRoot(dir, indexTarget); serr == nil && !same {
		return fmt.Errorf("refusing to index %s into the store of %s: the store's paths are relative to its project root, so reconciling against a different tree would delete every element outside it — run `leankg index %s` as its own project",
			indexTarget, dir, indexTarget)
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

	// Issue #376: stamp the project in the fleet registry. Best-effort by
	// contract — a fleet-bookkeeping failure must never turn a successful index
	// into a failed command, and an unreachable registry (a Postgres fleet the
	// CLI cannot see) is exactly that. dir, not indexTarget: dir is the store's
	// project identity, which is what a portfolio child opens.
	if rerr := portfolioreg.RegisterAfterIndex(context.Background(),
		portfolioreg.Options{Engine: os.Getenv("LEANKG_DB_ENGINE"), PGURL: pgURLFor(dir)},
		st, dir); rerr != nil {
		fmt.Fprintf(os.Stderr, "index: portfolio registry: %v\n", rerr)
	}

	// Optional LLM-meaning pass (issue #297): OFF unless opted in —
	// summarizing spends tokens and indexing must never start calling an
	// LLM just because one is configured for the query tier.
	if sr, ran, serr := summarize.RunAfterIndex(context.Background(), st, dir); ran {
		if serr != nil {
			fmt.Fprintf(os.Stderr, "index: post-index summarize skipped: %v\n", serr)
		} else {
			fmt.Println(sr.Summary())
		}
	}
	// Freshness bookkeeping: status/doctor compare the watermark against this
	// snapshot, and the CLI writer path bypasses the Engine — without it a
	// freshly indexed project reads as possibly_stale forever.
	if _, ierr := store.RefreshInventory(st); ierr != nil {
		fmt.Fprintf(os.Stderr, "%s: inventory snapshot: %v\n", "index", ierr)
	}
	return nil
}

// parseInterspersed parses flags that may appear before OR after positionals
// (clap semantics) and returns the positionals in order. verb names the
// command in usage errors; max is how many positionals the verb accepts.
//
// Go's flag package stops at the first positional, so the documented
// `verb <positional> --flag` form left the flag — and every token after it —
// unparsed, producing a confidently wrong answer (`refresh . --full` ran
// incrementally; `env-conflicts w2 --env production` reported on an empty
// service). Re-parsing the remainder after each positional fixes the drop; the
// bound is checked as tokens are collected, so a surplus positional fails with
// "unexpected argument" before any trailing flag is parsed, and nothing is ever
// silently ignored. Parse errors keep the flag package's exit-2 contract (every
// verb builds its FlagSet with flag.ExitOnError).
func parseInterspersed(verb string, fs *flag.FlagSet, args []string, max int) []string {
	var positional []string
	rest := args
	for {
		if err := fs.Parse(rest); err != nil {
			os.Exit(2)
		}
		rest = fs.Args()
		if len(rest) == 0 {
			return positional
		}
		positional = append(positional, rest[0])
		if len(positional) > max {
			fmt.Fprintf(os.Stderr, "%s: unexpected argument %q\n", verb, rest[0])
			os.Exit(2)
		}
		rest = rest[1:]
	}
}

// sameIndexRoot reports whether two index roots are the same directory after
// absolutising and resolving symlinks (macOS /tmp vs /private/tmp is the classic
// mismatch). An unreadable path yields an error, which callers treat as "do not
// block the run" — the write itself will surface a real path problem.
func sameIndexRoot(a, b string) (bool, error) {
	norm := func(p string) (string, error) {
		if p == "" {
			p = "."
		}
		abs, err := filepath.Abs(p)
		if err != nil {
			return "", err
		}
		if r, err := filepath.EvalSymlinks(abs); err == nil {
			abs = r
		}
		return abs, nil
	}
	na, err := norm(a)
	if err != nil {
		return false, err
	}
	nb, err := norm(b)
	if err != nil {
		return false, err
	}
	return na == nb, nil
}
