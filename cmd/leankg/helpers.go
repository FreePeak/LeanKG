package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/FreePeak/LeanKG/cmd/internal/embedenv"
	"github.com/FreePeak/LeanKG/internal/auth"
	"github.com/FreePeak/LeanKG/internal/cliexit"
	"github.com/FreePeak/LeanKG/internal/core"
	"github.com/FreePeak/LeanKG/internal/embed"
	"github.com/FreePeak/LeanKG/internal/index"
	"github.com/FreePeak/LeanKG/internal/langs"
	"github.com/FreePeak/LeanKG/internal/portfolioreg"
	"github.com/FreePeak/LeanKG/internal/projectcfg"
	"github.com/FreePeak/LeanKG/internal/refresh"
	"github.com/FreePeak/LeanKG/internal/sources"
	"github.com/FreePeak/LeanKG/internal/store"
	"github.com/FreePeak/LeanKG/internal/summarize"
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

// gateAutoConfig puts the auto-config route behind the REST auth middleware on
// the MCP listener and sends every other path to mcp. autoConfig is the handler
// restauto.RegisterAutoConfig returns (it mounts the route and sends the rest
// to mcp). The MCP handler authorises each tool itself, so the other paths
// stay open to an anonymous handshake while the gate is on.
func gateAutoConfig(st store.Backend, mcp, autoConfig http.Handler) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/api/v1/mcp/auto-config", auth.MiddlewareWithStore(st, autoConfig))
	mux.Handle("/", mcp)
	return mux
}

// shutdownTimeout bounds how long a listener may drain in-flight requests once
// serve is asked to stop. Past it the listener is closed by force.
const shutdownTimeout = 5 * time.Second

// joinFleet waits for a maintenance loop (maintain.Run or RunFleet) to exit
// after its context is cancelled. The loop is ctx-aware, so it returns within
// one unit of work. A loop still running after budget is logged and abandoned,
// so a stuck pass cannot hold shutdown forever. It reports whether the loop
// exited within the budget.
func joinFleet(done <-chan struct{}, budget time.Duration) bool {
	select {
	case <-done:
		return true
	case <-time.After(budget):
		log.Printf("maintenance: fleet did not stop within %s, shutting down anyway", budget)
		return false
	}
}

// endpoint is one listen address and the handler that serves it.
type endpoint struct {
	addr string
	h    http.Handler
}

// listener is a bound socket and the server that serves it.
type listener struct {
	ln  net.Listener
	srv *http.Server
}

// bindAll binds every endpoint before anything serves, so a port that is
// already in use is reported before a request is accepted. When one bind
// fails, the listeners bound so far are closed and the error is returned.
func bindAll(eps ...endpoint) ([]listener, error) {
	ls := make([]listener, 0, len(eps))
	for _, ep := range eps {
		ln, err := net.Listen("tcp", ep.addr)
		if err != nil {
			for _, l := range ls {
				_ = l.ln.Close()
			}
			return nil, fmt.Errorf("bind %s: %w", ep.addr, err)
		}
		ls = append(ls, listener{ln: ln, srv: &http.Server{Handler: ep.h, ReadHeaderTimeout: 5 * time.Second}})
	}
	return ls, nil
}

// runServers serves every listener until ctx is cancelled or one of them fails.
// A Serve error other than http.ErrServerClosed cancels the rest and is
// returned. On cancellation each server is shut down gracefully, within
// shutdownTimeout, and runServers returns only after every Shutdown has
// finished, so the caller may close its stores once it returns.
func runServers(ctx context.Context, ls ...listener) error {
	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() error {
		<-gctx.Done()
		sctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), shutdownTimeout)
		defer cancel()
		for _, l := range ls {
			if err := l.srv.Shutdown(sctx); err != nil {
				log.Printf("serve: %s did not drain within %s, closing it: %v", l.ln.Addr(), shutdownTimeout, err)
				_ = l.srv.Close()
			}
		}
		return nil
	})
	for _, l := range ls {
		g.Go(func() error {
			if err := l.srv.Serve(l.ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
				return fmt.Errorf("listener %s stopped: %w", l.ln.Addr(), err)
			}
			return nil
		})
	}
	return g.Wait()
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

// openEngine opens a project's store and returns a query-ready engine with the
// project directory and the lazily-activated language registry wired, exactly
// like the serving transports. The caller closes engine.Store().
func openEngine(ctx context.Context, dir string, mode store.Mode) (*core.Engine, error) {
	st, err := openStore(ctx, dir, "", mode)
	if err != nil {
		return nil, err
	}
	engine := core.New(st, nil, nil)
	engine.SetProjectDir(dir)
	// Query-time embedding for one-shot verbs: attach only (embed.Attach never
	// spawns), so a CLI run can never orphan a sidecar. Without this,
	// `leankg query --action semantic` could never reach L3 even with a
	// provider running — found dogfooding the real repo.
	if cfg, cerr := embedConfigFromEnv(); cfg.Provider != "" || cfg.BaseURL != "" {
		if cerr == nil {
			p, _, perr := embed.Attach(cfg)
			if cerr = perr; perr == nil {
				engine.SetEmbedder(p)
			}
		}
		if cerr != nil {
			fmt.Fprintf(os.Stderr, "embed provider: %v (L3 degraded)\n", cerr)
		}
	}
	reg := langs.DefaultRegistry()
	if _, aerr := reg.Activate(context.Background(), dir); aerr == nil {
		engine.SetLangsRegistry(reg)
	}
	return engine, nil
}

// hookMaxFilesDefault is the indexable-file count above which the SessionStart
// hook leaves a git repo to a manual index.
const hookMaxFilesDefault = 10000

// hookMaxEmbedDefault is the embed backlog (elements that need vectors) above
// which the SessionStart hook leaves the embed to a manual run.
const hookMaxEmbedDefault = 500

// hookLimit is the named env var when it parses as a positive integer, else def.
func hookLimit(env string, def int) int {
	if n, err := strconv.Atoi(os.Getenv(env)); err == nil && n > 0 {
		return n
	}
	return def
}

// hookBudgetDefault is the time budget of one index --hook run (plan D5).
const hookBudgetDefault = 10 * time.Second

// hookBudget is the time budget of one index --hook run: the --hook-budget flag
// when set, else LEANKG_HOOK_BUDGET when it parses as a positive duration, else
// hookBudgetDefault.
func hookBudget(flagValue time.Duration) time.Duration {
	if flagValue > 0 {
		return flagValue
	}
	if d, err := time.ParseDuration(os.Getenv("LEANKG_HOOK_BUDGET")); err == nil && d > 0 {
		return d
	}
	return hookBudgetDefault
}

// hookBigRepoSkip is the hook's size gate. A git repo root whose HEAD holds more
// indexable files than LEANKG_HOOK_MAX_FILES is left for a manual run, and so is a repo
// whose HEAD cannot be counted: the hook must not index what it could not size.
// It returns the skip line to print, or "" when the run may proceed.
func hookBigRepoSkip(ctx context.Context, dir string) string {
	n, ok, err := index.HeadIndexableFiles(ctx, dir)
	if err != nil {
		return fmt.Sprintf("leankg index --hook: skipped %s: cannot count HEAD files: %v", dir, err)
	}
	limit := hookLimit("LEANKG_HOOK_MAX_FILES", hookMaxFilesDefault)
	if !ok || n <= limit {
		return ""
	}
	return fmt.Sprintf("leankg index --hook: skipped %s: %d indexable files in HEAD exceeds LEANKG_HOOK_MAX_FILES=%d; run it manually: leankg index %s",
		dir, n, limit, dir)
}

// embedConfigFromEnv reads the LEANKG_EMBED_* environment: the one place the
// command reads it for the embed provider. The configuration is returned even
// when a variable is malformed, so the caller can name the provider it meant.
func embedConfigFromEnv() (embed.Config, error) {
	return embedenv.Load(os.Getenv)
}

// embedStart is the start function for the configuration cfg names (a spawning
// start for a manual run). A malformed configuration is returned by the function
// rather than by the caller, so it degrades the embed stage with its reason.
func embedStart(cfg embed.Config, cfgErr error) func(context.Context) (embed.Provider, func(), error) {
	return func(ctx context.Context) (embed.Provider, func(), error) {
		if cfgErr != nil {
			return nil, func() {}, cfgErr
		}
		return embed.Start(ctx, cfg)
	}
}

// embedStartFromEnv is embedStart over the environment, for verbs that refresh
// the index and embed in one pass.
func embedStartFromEnv() func(context.Context) (embed.Provider, func(), error) {
	cfg, err := embedConfigFromEnv()
	return embedStart(cfg, err)
}

// hookEmbedStart is the hook's provider policy for the embed stage. It attaches
// the provider (never spawns one), counts the backlog that run would embed, and
// refuses when that exceeds LEANKG_HOOK_MAX_EMBED. The refusal and a failed count
// come back as the stage's skip reason, so the hook prints one `embed skipped:`
// line and exits 0. Nothing is embedded before the gate passes.
func hookEmbedStart(st store.Backend, storeDir string, cfg embed.Config, cfgErr error) func(context.Context) (embed.Provider, func(), error) {
	return func(ctx context.Context) (embed.Provider, func(), error) {
		if cfgErr != nil {
			return nil, func() {}, cfgErr
		}
		p, release, err := embed.AttachHook(cfg)
		if err != nil {
			return nil, release, err
		}
		n, err := embed.IncrementalBacklog(st, p)
		if err != nil {
			release()
			return nil, func() {}, fmt.Errorf("cannot count embed backlog: %w", err)
		}
		if limit := hookLimit("LEANKG_HOOK_MAX_EMBED", hookMaxEmbedDefault); n > limit {
			release()
			return nil, func() {}, fmt.Errorf("%d elements need vectors, over LEANKG_HOOK_MAX_EMBED=%d; run it manually: leankg index %s", n, limit, storeDir)
		}
		return p, release, nil
	}
}

// indexStoreDir is the project directory whose .leankg an index run writes to:
// the project flag, else the target. With neither (`index --source <uri>` with no
// positional argument) it is the cwd (Rust find_project_root), never the
// filesystem root.
func indexStoreDir(project, target string) string {
	if project != "" {
		return project
	}
	if target != "" {
		return target
	}
	return "."
}

// runIndex performs a one-shot index run into the project store. A non-empty
// source is a --source URI whose tree is synced into <project>/.leankg/sources
// and indexed instead of target (Rust main.rs index path resolution). ctx is
// honoured between files: a done ctx stops the run with its error, and the
// directories already written stay committed.
func runIndex(ctx context.Context, project, target, source, refName, auth string, rebaseRoot ...bool) error {
	dir := indexStoreDir(project, target)
	indexTarget := target
	if source != "" {
		synced, err := sources.Resolve(ctx, dir, source, auth, refName, sources.CLIProgress{})
		if err != nil {
			return err
		}
		indexTarget = synced
	}
	// The walk root must EXIST. WalkDir treats a missing root as an empty tree,
	// so `leankg index ./nope` printed the ordinary success line and exited 0 —
	// after creating a store at the missing path, which every later signal
	// (status cold, doctor empty) then reported as a healthy project that
	// indexed nothing. A typo is the most common mistake this verb gets, and an
	// empty-but-real directory stays valid.
	if info, serr := os.Stat(indexTarget); serr != nil {
		if os.IsNotExist(serr) {
			return fmt.Errorf("refusing to index %s: no such file or directory (an empty existing directory indexes fine — this is a path that does not exist)", indexTarget)
		}
		return fmt.Errorf("refusing to index %s: %w", indexTarget, serr)
	} else if !info.IsDir() {
		return fmt.Errorf("refusing to index %s: not a directory", indexTarget)
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
	st, err := openStore(ctx, dir, "", store.RW)
	if err != nil {
		return fmt.Errorf("open store: %w", err)
	}
	defer st.Close()
	if err := st.Migrate(); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	reg := langs.DefaultRegistry()
	if _, aerr := reg.Activate(ctx, indexTarget); aerr != nil {
		return fmt.Errorf("language detection: %w", aerr)
	}
	if len(rebaseRoot) > 0 && rebaseRoot[0] {
		// The checkout moved: adopt the new root deliberately (RS-02 tripwire).
		if err := index.RebaseRoot(st, indexTarget); err != nil {
			return fmt.Errorf("rebase root: %w", err)
		}
	}
	res, err := index.IndexDirWith(ctx, st, indexTarget, reg)
	if err != nil {
		return fmt.Errorf("index %s: %w", indexTarget, err)
	}
	fmt.Printf("indexed %s: files=%d elements=%d relationships=%d skipped=%d\n",
		indexTarget, res.Files, res.Elements, res.Relationships, res.Skipped)
	// The count line ends the command with no obvious next step, and the one
	// that matters (wire an agent) lives in a different verb.
	fmt.Println("next: leankg install --target <client>  (claude-code | cursor | codex | gemini | opencode | omp | xdev)")

	// Issue #376: stamp the project in the fleet registry. Best-effort by
	// contract — a fleet-bookkeeping failure must never turn a successful index
	// into a failed command, and an unreachable registry (a Postgres fleet the
	// CLI cannot see) is exactly that. dir, not indexTarget: dir is the store's
	// project identity, which is what a portfolio child opens.
	if rerr := portfolioreg.RegisterAfterIndex(ctx,
		portfolioreg.Options{Engine: os.Getenv("LEANKG_DB_ENGINE"), PGURL: projectcfg.PGURL(dir)},
		st, dir); rerr != nil {
		fmt.Fprintf(os.Stderr, "index: portfolio registry: %v\n", rerr)
	}

	// Optional LLM-meaning pass (issue #297): OFF unless opted in —
	// summarizing spends tokens and indexing must never start calling an
	// LLM just because one is configured for the query tier.
	if sr, ran, serr := summarize.RunAfterIndex(ctx, st, dir); ran {
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

// indexEmbed runs the incremental embed that follows a successful index, on the
// project store at storeDir. A manual run may spawn the provider; the hook only
// attaches (embed.AttachHookProvider) and only within LEANKG_HOOK_MAX_EMBED
// (hookEmbedStart). It never fails the command: a skip or a failed embed prints
// one `embed skipped:` line and returns.
func indexEmbed(ctx context.Context, storeDir string, hook bool) {
	st, err := openStore(ctx, storeDir, "", store.RW)
	if err != nil {
		fmt.Printf("embed skipped: open store: %v\n", err)
		return
	}
	defer st.Close()
	cfg, cfgErr := embedConfigFromEnv()
	start := embedStart(cfg, cfgErr)
	if hook {
		start = hookEmbedStart(st, storeDir, cfg, cfgErr)
	}
	rep, skipped, err := refresh.EmbedStage(ctx, st, storeDir, start, "incremental")
	switch {
	case err != nil:
		fmt.Printf("embed skipped: %v\n", err)
	case skipped != "":
		fmt.Printf("embed skipped: %s\n", skipped)
	default:
		fmt.Printf("embedded %d elements (%d failed)\n", rep.Embedded, rep.Failed)
	}
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
// verb builds its FlagSet with flag.ContinueOnError).
func parseInterspersed(verb string, fs *flag.FlagSet, args []string, max int) ([]string, error) {
	var positional []string
	rest := args
	for {
		if err := cliexit.Parse(fs, rest); err != nil {
			return nil, err
		}
		rest = fs.Args()
		if len(rest) == 0 {
			return positional, nil
		}
		positional = append(positional, rest[0])
		if len(positional) > max {
			return nil, cliexit.Usage(fmt.Errorf("%s: unexpected argument %q", verb, rest[0]))
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
