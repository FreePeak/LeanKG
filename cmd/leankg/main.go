// cmd/leankg — the Go engine serving binary: MCP (stdio + HTTP) and REST
// from one core. Role flags:
//
//	leankg serve --stdio                       MCP over stdio (coding tools)
//	leankg serve --http 127.0.0.1:9699 --rest 127.0.0.1:8080   MCP HTTP + REST API
//	leankg serve --read-only ...               RO store handle (reader role)
//	leankg index <dir>                         one-shot index run
//	leankg doctor                              store diagnostics
//
// The serving binary performs ZERO embedding inference (issue #368): query-
// time embedding is an HTTP client call (sidecar or API) wired through
// internal/embed providers; the pipeline itself is cmd/leankg-embed.
package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/FreePeak/LeanKG/internal/auth"
	"github.com/FreePeak/LeanKG/internal/cliexit"
	"github.com/FreePeak/LeanKG/internal/core"
	"github.com/FreePeak/LeanKG/internal/doctor"
	"github.com/FreePeak/LeanKG/internal/embed"
	"github.com/FreePeak/LeanKG/internal/errs"
	"github.com/FreePeak/LeanKG/internal/flock"
	"github.com/FreePeak/LeanKG/internal/httpx"
	"github.com/FreePeak/LeanKG/internal/index"
	"github.com/FreePeak/LeanKG/internal/langs"
	"github.com/FreePeak/LeanKG/internal/maintain"
	leankgmcp "github.com/FreePeak/LeanKG/internal/mcp"
	"github.com/FreePeak/LeanKG/internal/memory"
	"github.com/FreePeak/LeanKG/internal/portfolioreg"
	"github.com/FreePeak/LeanKG/internal/projectcfg"
	"github.com/FreePeak/LeanKG/internal/projects"
	"github.com/FreePeak/LeanKG/internal/rest"
	restauto "github.com/FreePeak/LeanKG/internal/rest/auto"
	"github.com/FreePeak/LeanKG/internal/rpc"
	"github.com/FreePeak/LeanKG/internal/servebind"
	"github.com/FreePeak/LeanKG/internal/session"
	"github.com/FreePeak/LeanKG/internal/setupcfg"
	"github.com/FreePeak/LeanKG/internal/store"
	"github.com/FreePeak/LeanKG/internal/telemetry"
	"github.com/FreePeak/LeanKG/internal/web"
)

// verbFunc is the handler shape of every verb: the root context from main, the
// arguments after the verb name, and the error main maps to an exit status.
type verbFunc func(ctx context.Context, args []string) error

// verbs routes every command name to its handler.
var verbs = map[string]verbFunc{
	"serve": cmdServe,
	"index": cmdIndex,
	"query": cmdQuery,
	"impact": func(ctx context.Context, args []string) error {
		return cmdQuery(ctx, append([]string{args[0], "--kind", "impact"}, args[1:]...))
	},
	"writer":  cmdWriter,
	"connect": cmdConnect,
	"install": cmdInstall,
	"version": func(context.Context, []string) error {
		fmt.Println("leankg " + Version())
		return nil
	},
	"obsidian":           cmdObsidian,
	"setup":              cmdSetup,
	"status":             cmdStatus,
	"doctor":             cmdDoctor,
	"run":                cmdRun,
	"detect-clusters":    cmdDetectClusters,
	"report":             cmdReport,
	"gods":               cmdGods,
	"ctags":              cmdCtags,
	"cost":               cmdCost,
	"migrate":            cmdMigrate,
	"audit":              cmdAudit,
	"metrics":            cmdMetrics,
	"dashboard":          cmdDashboard,
	"telemetry":          cmdTelemetry,
	"auth":               cmdAuth,
	"tunnels":            cmdTunnels,
	"quality":            cmdQuality,
	"reflect":            cmdReflect,
	"refresh":            cmdRefresh,
	"gc":                 cmdGC,
	"vacuum":             cmdVacuum,
	"register":           cmdRegister,
	"unregister":         cmdUnregister,
	"list":               cmdListRepos,
	"status-repo":        cmdStatusRepo,
	"export":             cmdExport,
	"pack":               cmdPack,
	"generate":           cmdGenerate,
	"annotate":           cmdAnnotate,
	"prd":                cmdPRD,
	"prd-trace":          cmdPRDTrace,
	"link":               cmdLink,
	"delete-annotation":  cmdDeleteAnnotation,
	"search-annotations": cmdSearchAnnotations,
	"show-annotations":   cmdShowAnnotations,
	"mine-conversations": cmdMineConversations,
	"incident":           cmdIncident,
	"note":               cmdNote,
	"env-conflicts":      cmdEnvConflicts,
	"service-context":    cmdServiceContext,
	"team-map":           cmdTeamMap,
	"push":               cmdPush,
	"pull":               cmdPull,
	"update":             cmdUpdate,
	"summarize":          cmdSummarize,
	"register-project":   cmdRegisterProject,
	"projects":           cmdProjects,
	"help":               showUsage,
	"-h":                 showUsage,
	"--help":             showUsage,
}

// runVerb runs the verb named by argv[0] and returns the process exit status.
// It is the one place a verb's error becomes a status (cliexit.Finish).
func runVerb(ctx context.Context, argv []string) int {
	fn, ok := verbs[argv[0]]
	if !ok {
		fmt.Fprintf(os.Stderr, "leankg: unknown command %q\n\n", argv[0])
		usage()
		return 2
	}
	return cliexit.Finish(fn(ctx, argv[1:]), os.Stderr)
}

func showUsage(context.Context, []string) error {
	usage()
	return nil
}

func main() {
	log.SetFlags(0)
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	// The one root context: the first SIGINT or SIGTERM cancels it, and the
	// verbs that take ctx stop at their next unit of work. Restoring the default
	// handlers on that first cancel lets a second Ctrl-C end a run that is slow
	// to stop.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	context.AfterFunc(ctx, stop)
	status := runVerb(ctx, os.Args[1:])
	stop()
	os.Exit(status)
}

func usage() {
	fmt.Fprint(os.Stderr, `leankg — LeanKG Go engine

Usage:
  leankg query <text> [--kind name|impact] [--depth N] [--compress]
  leankg query <text> --action <a> [--to QN] [--lang L] [--pattern P] [--cmd C]
                 [--command workspace|document] [--path FILE] [--mode M]
                 [--lines SPEC] [--fresh] [--limit N] [--depth N]
                 [--service NAME] [--env E]
  leankg run [--compress] -- <command> [args...]
  leankg detect-clusters [--path DIR] [--min-hub-edges N]
  leankg report [--project DIR] [--project-name NAME] [--out FILE]
  leankg gods [--project DIR] [--limit N] [--exclude-hubs-percentile N]
  leankg ctags [--project DIR] [--out FILE] [--format ctags]
  leankg cost [--project DIR] [--format text|json]
  leankg vacuum [DIR] [--project DIR] [--engine sqlite|postgres] [--full] [--max-pages N]
  leankg migrate [--project DIR] [--engine sqlite|postgres]
  leankg gc [DIR] [--project DIR] [--engine sqlite|postgres] [--keep-model ID]
  leankg audit export [--project DIR] [--since T] [--until T] [--format jsonl] [--out FILE]
  leankg audit verify [--project DIR] [--since T] [--until T]
  leankg metrics [--project DIR] [--since N|Nd] [--tool NAME] [--json|-j] [--session] [--session-id ID]
                 [--reset] [--cleanup] [--retention DAYS] [--seed]
  leankg dashboard [--addr 127.0.0.1:9701] [--no-open] [--project DIR] [--since 24h|7d|30d|2w]
                   [--allow-remote --token T] [--format text|json]
  leankg telemetry status [--json] | enable [--bodies] [--sessions LIST|all] [--yes] | disable
                   | purge (--before 30d | --all) | link [--older-than 2m] | import-ab DIR
  leankg auth register     [--project DIR] --email EMAIL --password PW --name NAME
  leankg auth token create [--project DIR] --name NAME [--role admin|contributor|viewer]
                           [--account-id ID] [--org-id ID] [--scopes a,b] [--ttl 24h]
  leankg auth token list   [--project DIR] [--account-id ID]
  leankg auth token revoke [--project DIR] --token-id ID
  leankg tunnels [--path DIR] [--limit N]
  leankg quality [--path DIR] [--min-lines N] [--lang L]
  leankg reflect <question> <outcome> [--nodes a,b] [--note TEXT]
  leankg refresh [PATH] [--project DIR] [--docs DIR] [--source URI] [--ref-name REF] [--auth TOKEN] [--full]
  leankg register <name> | unregister <name> | list | status-repo <name>
  leankg register-project <dir> [--name N] [--project DIR] [--engine sqlite|postgres]
  leankg projects [--json] [--forget DIR] [--project DIR] [--engine sqlite|postgres]
  leankg obsidian <init|push|pull|watch|status> [--project DIR] [--vault PATH] [--debounce-ms N]
  leankg export [--output FILE] [--format json|dot|mermaid] [--markdown] [--out FILE]
                [--file F] [--depth N] [--path P] [--community C] [--max-nodes N]
  leankg pack   [--output DIR] [--path P] [--max-nodes N] [--revision REV] [--project DIR]
  leankg generate [--project DIR]
  leankg annotate <element> --description TEXT [--user-story ID] [--feature ID]
  leankg link <element> <id> [--kind story|feature]
  leankg search-annotations <query> | show-annotations <element> | delete-annotation <element>
  leankg mine-conversations --format claude|chatgpt|slack --input FILE_OR_DIR [--project DIR]
  leankg incident add --title T --severity P0|P1|P2|P3 --affected A,B --root-cause R --resolution X
                      [--prevention P] [--env production] [--ticket ID] [--project DIR]
  leankg incident list --service NAME [--env production] [--pattern P] [--limit N] [--project DIR]
  leankg incident show <id> [--project DIR]
  leankg note --target SERVICE_OR_QN --content TEXT [--env local] [--project DIR]
  leankg env-conflicts --service NAME [--project DIR]
  leankg service-context --service NAME [--env production] [--project DIR]
  leankg team-map [--env production] [--project DIR]
  leankg serve  [--project DIR] [--stdio] [--http ADDR] [--rest ADDR] [--read-only] [--memory] [--embed-provider P]
  leankg setup [--reset] [--clone] [--index] [--embed] [--status]
  leankg index <dir> [--hook] [--no-embed] [--source URI] [--ref-name REF] [--auth TOKEN] [--rebase-root]
  leankg prd [--source docs/prd.md] [--environment local] [--project DIR]
  leankg prd-trace [FEATURE_ID] [--project DIR]
  leankg push --remote URL --token TOKEN [--env local] [--project DIR]
  leankg pull --remote URL --token TOKEN [--env production]
  leankg doctor [--project DIR] [--deep] [--format text|json]
  leankg status [--project DIR]
  leankg update [--check] [--repo OWNER/NAME]
  leankg summarize [--project DIR] [--force] [--dry-run] [--concurrency N]
  leankg version

--action vocabulary: path|callers|callees|context|explain|pattern|lsp|compress|read|
search|exact|fuzzy|semantic|element|impact|languages|prd|incidents|env_conflicts|
service_context|portfolio (portfolio = #376 fleet read via the registry;
queried through the same core wiring the MCP transports use).
Time filters (audit --since/--until): RFC3339 | epoch seconds | 90s|30m|24h|7d.

Defaults: --http 127.0.0.1:9699 (MCP streamable HTTP, loopback) when neither
--stdio nor an address is given; --project defaults to cwd.
A --http/--rest/--rpc address beyond loopback is REFUSED unless a bearer
token is configured (leankg auth token create, or LEANKG_TOKEN_ADMIN);
--ui is the one deliberately public listener.
LEANKG_PROJECT_DIRS (comma-separated) registers extra projects; REST/MCP
requests select one with ?project= (dir path or name).
`)
}

// newScopedMCPServer builds the MCP server for a surface that already serves
// ONE project:
//
//   - the stdio session — one process, spawned by one client in the
//     repository it is working in, so the process's own project IS the
//     answer;
//   - the per-request `?project=` handler — the routing layer already chose
//     the project before the handler ran.
//
// On both, the `project` argument stays optional and a call that omits it is
// answered by the engine the surface was built with. The multi-project
// fail-closed rule belongs to the UNSCOPED HTTP surface, where the caller is
// remote and the server cannot know which project a question is about.
//
// Measured live on the 298-project self-host before this: a stdio session
// (the client had spawned the process inside its own repository) was refused
// with LEANKG_ERROR_MISSING_PARAM — "this server serves multiple projects"
// — which contradicts the stdio contract this command documents. The
// argument still selects another project when it is present.
func newScopedMCPServer(engine *core.Engine, router *projects.Router) *leankgmcp.Server {
	srv := leankgmcp.New(engine)
	srv.SetProjectRouter(router)
	srv.SetProjectScoped(true)
	return srv
}

// cmdServe labels serve's error for main. serve returns its error rather than
// calling log.Fatal, so its defers (store close, telemetry flush, auto-config
// job join) run before the process exits.
func cmdServe(ctx context.Context, args []string) error {
	if err := serve(ctx, args); err != nil {
		return fmt.Errorf("serve: %w", err)
	}
	return nil
}

// serveGateOn reports whether the auth gate is on for a serve of dir. It is
// read before serve touches the store: a read-only open never creates the
// database, and a project with no store yet can only be gated by an env token.
// An unknown gate state fails closed (off), so a non-loopback bind is refused
// rather than silently exposed.
func serveGateOn(ctx context.Context, dir, engine, dbPath string) bool {
	st, err := openStoreWith(ctx, dir, engine, dbPath, store.RO)
	if err != nil {
		st = nil
	} else {
		defer st.Close()
	}
	gateOn, gateErr := auth.GateEnabled(ctx, st)
	if gateErr != nil {
		log.Printf("warning: auth gate state unknown: %v", gateErr)
		return false
	}
	return gateOn
}

// uiFileGate refuses GET /api/file on a --ui listener bound beyond loopback
// while no token gates it: with the gate off every caller resolves to Admin,
// so the file reader would serve any file under the project root to the LAN.
// The gate is read per request, so a token revoked while serving closes the
// route at once. An unknown gate state counts as off (fail closed).
func uiFileGate(st auth.Store, addr string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gated, err := auth.GateEnabled(r.Context(), st)
		if servebind.Exposed(addr, err == nil && gated) {
			httpx.WriteError(w, errs.NewError(errs.PermissionDenied,
				"the dashboard file viewer is refused on a non-loopback --ui bind while no auth token is configured",
				"bind 127.0.0.1 (--ui 127.0.0.1:PORT), or configure a token (leankg auth token create, or LEANKG_TOKEN_VIEWER)"))
			return
		}
		next.ServeHTTP(w, r)
	})
}

func serve(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	project := fs.String("project", "", "project directory (default cwd)")
	dbFlag := fs.String("db", "", "standalone SQLite store file path (default empty = <project>/.leankg/leankg.db); sets the project directory optional (FR-P2)")
	stdio := fs.Bool("stdio", false, "serve MCP over stdio")
	httpAddr := fs.String("http", "", "MCP streamable HTTP address (e.g. 127.0.0.1:9699; a non-loopback address needs an auth token)")
	restAddr := fs.String("rest", "", "REST API address (e.g. 127.0.0.1:8080; a non-loopback address needs an auth token)")
	readOnly := fs.Bool("read-only", false, "open the store read-only (reader role)")
	withMemory := fs.Bool("memory", false, "enable the markdown memory layer")
	engineName := fs.String("engine", "", "storage engine: sqlite|postgres (default: LEANKG_DB_ENGINE, else sqlite; postgres needs LEANKG_PG_URL)")
	embedProvider := fs.String("embed-provider", "", "query-time embedder: openai|local|deterministic (env LEANKG_EMBED_* configure it; empty = L3 degrades with reason)")
	rpcAddr := fs.String("rpc", "", "ConnectRPC address (gRPC + gRPC-Web + JSON, e.g. 127.0.0.1:9090; a non-loopback address needs an auth token)")
	uiAddr := fs.String("ui", "", "dashboard address serving the embedded ui-v2 build (e.g. 127.0.0.1:8081; deliberately public — its API is unauthenticated)")
	hindsightCompat := fs.Bool("hindsight-compat", false, "on the REST listener, mount the Hindsight-wire compat aliases (/v1/default/banks/...) that omp's memory.backend=hindsight speaks; requires --memory")
	memoryGlobal := fs.Bool("memory-global", false, "keep the memory layer in ~/.leankg/memory instead of <project>/.leankg/memory (shared/portfolio memory root; requires --memory)")
	if err := cliexit.Parse(fs, args); err != nil {
		return err
	}
	addr := *httpAddr
	if addr == "" && *restAddr == "" {
		// Loopback by default (RS-03): the MCP surface reads project files and
		// indexes, and with no token configured every caller is Admin. Pass
		// --http :9699 explicitly to listen on every interface.
		addr = defaultHTTPAddr
	}
	// Flag facts are checked before any store is opened or migrated, so a bad
	// combination leaves the database file untouched. The listen addresses are
	// checked for syntax only here (GateOn is set so no exposure is refused);
	// the exposure rule below needs the auth state the store holds.
	binds := []struct {
		kind servebind.Kind
		addr string
	}{
		{servebind.KindMCP, addr},
		{servebind.KindREST, *restAddr},
		{servebind.KindRPC, *rpcAddr},
	}
	if *stdio && (*httpAddr != "" || *restAddr != "" || *rpcAddr != "" || *uiAddr != "") {
		return errors.New("--stdio is exclusive of --http/--rest/--rpc/--ui")
	}
	for _, l := range binds {
		if err := servebind.Check(l.kind, l.addr, servebind.Options{GateOn: true}); err != nil {
			return err
		}
	}
	// SIGHUP re-reads consent: an operator who grants or withdraws telemetry
	// mid-session must not have to restart the server (plan v4.15 §8).
	watchConsentReload(ctx)

	dir := *project
	if dir == "" {
		var err error
		dir, err = os.Getwd()
		if err != nil {
			return fmt.Errorf("resolve cwd: %w", err)
		}
	}
	// leankg.yaml project.project_path / project.root anchor, canonicalized
	// before use (Rust MCPServer::resolve_project_root, N3); the writer applies
	// the same rule (anchorProjectDir).
	dir = anchorProjectDir(dir)

	eng := *engineName
	if eng == "" {
		eng = envOr("LEANKG_DB_ENGINE", "sqlite")
	}
	// Exposure gate (RS-03, enforced) before any side effect: a listener that
	// leaves the loopback interface is REFUSED unless a token gates its
	// callers. With the gate off every caller resolves to Admin, so a wildcard
	// bind hands the project graph, the file reader and the memory write path
	// to every host that can route to this machine (measured 2026-10-21: a LAN
	// POST to the REST listener answered {"written":1}). The gate state is read
	// through a read-only open, which never creates the store, so a refused
	// serve leaves no database file, starts no sidecar and runs no migration.
	//
	// The UI listener is deliberately NOT gated here: the dashboard is public
	// by design (the Render demo serves --read-only --ui :${PORT}), so it keeps
	// its own warning.
	gateOn := serveGateOn(ctx, dir, eng, *dbFlag)
	for _, l := range binds {
		if err := servebind.Check(l.kind, l.addr, servebind.Options{GateOn: gateOn}); err != nil {
			return err
		}
	}

	// Standalone mode skips the project-gated sidecars (they expect
	// a .leankg dir in dir — FR-P2-3).
	if dbPath := projectcfg.StandaloneDBPath(dir); dbPath != "" {
		goto afterSidecars
	}
	// FR-ZCP-13: the auto-index gates the mcp.auto_index_* keys configure, and
	// the LEANKG_SETUP=1 post-bind setup trigger. Both run in the background so
	// the listener binds immediately.
	go maybeAutoIndexOnStart(ctx, dir, *readOnly)
	go maybeRunServeSetup(ctx, dir)

afterSidecars:

	mode := store.RW
	if *readOnly {
		mode = store.RO
	}
	st, err := openStoreWith(ctx, dir, eng, *dbFlag, mode)
	if err != nil {
		return fmt.Errorf("open store: %s", storeErrText(eng, err))
	}
	defer st.Close()
	if !*readOnly {
		if err := st.Migrate(ctx); err != nil {
			return fmt.Errorf("migrate: %w", err)
		}
	}

	// Process-wide capture sink (plan v4.15): a no-op unless the user granted
	// consent in telemetry.yaml. Memory and lesson events and the REST and
	// ConnectRPC call rows all go to it.
	rec := telemetryRecorder(ctx)
	defer closeTelemetry()
	// The sinks are registered so a SIGHUP consent reload can re-point them at
	// a recorder built after startup (capture was off then). Every consumer
	// holds the value it was given, so a level change reaches nobody until this
	// list is replayed.
	AddTelemetrySink(memory.SetRecorder)
	AddTelemetrySink(session.SetRecorder)

	memory.SetRecorder(rec)
	session.SetRecorder(rec)

	var mem *memory.Memory
	if *withMemory {
		// K1: --memory-global moves the bank root out of every indexed
		// repository into ~/.leankg/memory, so several projects can share
		// one memory service. Opt-in — the default stays per-project, so an
		// existing deployment's banks never move underneath it. The root is
		// logged because it is also the single-writer anchor: two servers on
		// one root append to the same JSONL with no cross-process lock.
		if mem, err = memory.Open(dir, *memoryGlobal); err != nil {
			return fmt.Errorf("memory: %w", err)
		}
		log.Printf("leankg memory root %s", mem.Root())
	}

	// Storage maintenance runs LATER, once the router exists: a serving
	// process fronts many project stores, and the default one is not the
	// only one that fills up. See the fleet block below.

	embedCfg, embedCfgErr := embedConfigFromEnv()
	if *embedProvider != "" {
		embedCfg.Provider = *embedProvider
	}
	var embedder embed.Provider
	if embedCfg.Provider != "" {
		// A query-time embedder is an OPTION on the serve path, not a
		// precondition: the engine serves exact (L1), fuzzy (L2), graph,
		// memory, REST and UI without it, and the L3 rung already degrades
		// with a reason when no embedder is wired (core.rungSemantic). So a
		// provider that will not start — no llama.cpp installed, a port held
		// by something else, a model that will not load — costs L3 and
		// nothing else. It used to be log.Fatalf, which meant one failed
		// sidecar took the whole MCP server down with it: an operator's
		// server answered no health probe at all, and the client reported
		// "unreachable" with no clue why (found 2026-10-03 driving
		// xdev's autostart, where LEANKG_EMBED_SIDECAR_PORT pointed at a
		// port a previous sidecar still held).
		p, release, perr := embedStart(embedCfg, embedCfgErr)(ctx)
		if perr != nil {
			log.Printf("embed provider unavailable — serving without L3 semantic: %v", perr)
		} else {
			defer release()
			embedder = p
		}
	}
	// Lazy language activation: detect the languages this codebase (incl.
	// nested repos) actually uses; everything else stays idle.
	langReg := langs.DefaultRegistry()
	if roots, err := langReg.Activate(ctx, dir); err != nil {
		log.Printf("language detection failed: %v", err)
	} else {
		var summary []string
		for root, ls := range roots {
			names := make([]string, len(ls))
			for i, l := range ls {
				names[i] = string(l)
			}
			summary = append(summary, root+" ["+strings.Join(names, ",")+"]")
		}
		sort.Strings(summary)
		log.Printf("languages active: %s", strings.Join(summary, "; "))
	}
	engine := core.New(st, core.Options{Memory: mem, Embedder: embedder, ProjectDir: dir, Langs: langReg})
	defer engine.Close()

	// Multi-project serving (LEANKG_PROJECT_DIRS): register the extra
	// projects and route `?project=` / tool-arg `project` to them. With
	// the env unset the registry holds only the default project and the
	// routing wrapper is a passthrough — the single-project path stays
	// byte-identical.
	router := projects.NewRouter(dir, projects.Config{
		ExtraDirs:  projects.ParseDirs(os.Getenv("LEANKG_PROJECT_DIRS")),
		Mode:       mode,
		EngineName: eng,
		PGURL:      os.Getenv("LEANKG_PG_URL"),
		Memory:     *withMemory,
		Embedder:   embedder,
		Logf:       log.Printf,
	})
	router.SeedDefault(&projects.Project{
		Dir: dir, Name: filepath.Base(dir), Store: st, Engine: engine, Memory: mem,
	})
	defer router.Close()
	if extra := router.List(); len(extra) > 1 {
		log.Printf("projects registered: %s", strings.Join(extra, ", "))
	}

	// Storage maintenance: the Rust engine's hourly vacuum scheduler
	// (FR-V2-08), restored in Go, extended to a FLEET.
	//
	// One pass does three things per store: it purges the rows nobody
	// reads any more (edges into deleted files, vectors for deleted
	// elements, and vector collections under a model id the query path no
	// longer uses), it returns the freelist to the filesystem, and it
	// truncates the WAL sidecar — and, when --memory is on, the same for
	// the memory FTS index.
	//
	// The fleet is the part that matters on a multi-project host: this
	// process fronts every LEANKG_PROJECT_DIRS project, and before this
	// only the DEFAULT project's store was ever maintained — the routed
	// projects filled up with nothing reclaiming them.
	//
	// The stale-collection purge needs to know which collection is live.
	// The L3 rung queries the collection under the embedder's model id
	// (core.rungSemantic), so that id is exactly the keep set. With no
	// embedder wired the keep set is empty and the purge is skipped
	// entirely: a server that cannot say which collection is live must not
	// be able to delete it.
	//
	// Skipped under --read-only (a reader takes no lock it may need to
	// rewrite) and disabled by LEANKG_VACUUM_INTERVAL_HOURS=0.
	if mode == store.RW {
		interval := maintain.IntervalFromEnv(os.Getenv)
		var keepModelIDs []string
		if embedder != nil {
			if id := embedder.Identity().Model; id != "" {
				keepModelIDs = []string{id}
			}
		}
		if interval > 0 {
			log.Printf("maintenance: sweep+vacuum every %s over %d project(s) (0 disables)",
				interval, len(router.List()))
		}
		// The memory layer of the default project gets the same keep set:
		// it names the vector collections that survive the stale sweep.
		if mem != nil {
			mem.SetKeepModelIDs(keepModelIDs)
		}
		// The fleet runs on its own context so it stops on every serve return,
		// not only when the root ctx is cancelled (stdio EOF, a bind failure).
		// Defers run LIFO: cancelFleet fires before joinFleet waits, and the
		// join runs after runServers drains and before the router and the
		// default store close.
		fctx, cancelFleet := context.WithCancel(ctx)
		defer joinFleet(maintain.RunFleet(fctx, func() []maintain.Target {
			// The REGISTRY sweep rides the same tick as the store
			// maintenance: a row for a project that left the fleet must not
			// outlive it, and nothing else ever pruned the registry. The
			// callback runs once per pass, so this is once per tick.
			sweepRegistry(fctx, eng)
			return fleetTargets(fctx, dir, st, mem, router.List(), eng, keepModelIDs)
		}, maintain.Options{Logf: log.Printf, KeepModelIDs: keepModelIDs}, interval, maintain.FirstIdle), shutdownTimeout)
		defer cancelFleet()
	}

	// Auto-config jobs outlive the request that started them. serve owns them:
	// they are cancelled and joined when serve returns, before the store closes
	// (defers run last-in first-out).
	jobs := restauto.NewJobs(ctx)
	defer jobs.Stop()

	if *stdio {
		// stdio is one process-bound MCP session; per-project selection
		// travels as the `project` tool argument, routed through the
		// router hook.
		srv := newScopedMCPServer(engine, router)
		srv.SetAuthStore(st) // authenticate logs in against the store serve opened
		srv.SetRecorder(rec) // plan v4.15 DS-04: capture tools/call and identity
		registerMCPSink(srv)
		log.Printf("leankg serve (stdio) project=%s engine=%s", dir, st.Engine())
		if err := srv.RunStdio(ctx); err != nil && !errors.Is(err, context.Canceled) {
			return fmt.Errorf("stdio: %w", err)
		}
		return nil
	}

	// Every listener is collected here and bound together below, so a port
	// already in use is reported before any of them starts serving.
	var eps []endpoint
	if addr != "" {
		mcpSrv := leankgmcp.New(engine, leankgmcp.WithProjectRequired(router.MultiProject()))
		mcpSrv.SetProjectRouter(router)
		mcpSrv.SetAuthStore(st)
		mcpSrv.SetRecorder(rec) // plan v4.15 DS-04/DS-05: HTTP capture and headers
		registerMCPSink(mcpSrv)
		h := httpMux(mcpSrv.HTTPHandler())
		h = routeByProject(router, h, func(p *projects.Project) http.Handler {
			pSrv := newScopedMCPServer(p.Engine, router)
			pSrv.SetAuthStore(st)
			pSrv.SetRecorder(rec)
			registerMCPSink(pSrv)
			return httpMux(pSrv.HTTPHandler())
		})
		// RegisterAutoConfig RETURNS the wrapped mux; the endpoint lives in
		// that returned handler, so serving the original one served everything
		// except /api/v1/mcp/auto-config. Auto-config is the one write route on
		// this listener, so it alone is gated (CX-02).
		autoConfig := restauto.RegisterAutoConfig(h, engine, restauto.WithRouter(router), restauto.WithJobs(jobs),
			restauto.WithEmbedStart(embedStart(embedCfg, embedCfgErr)))
		h = gateAutoConfig(st, h, autoConfig)
		eps = append(eps, endpoint{addr: addr, h: h})
	}
	if *restAddr != "" {
		var restOpts []rest.HandlerOption
		if *hindsightCompat {
			restOpts = append(restOpts, rest.WithHindsightCompat())
		}
		// Routing sits INSIDE the auth boundary: token gating applies
		// before a selector is resolved or its name echoed back.
		h := routeByProject(router, rest.Handler(engine, mem, restOpts...), func(p *projects.Project) http.Handler {
			return rest.Handler(p.Engine, p.Memory, restOpts...)
		})
		h = restauto.RegisterAutoConfig(h, engine, restauto.WithRouter(router), restauto.WithJobs(jobs),
			restauto.WithEmbedStart(embedStart(embedCfg, embedCfgErr)))
		// The REST capture handler is built explicitly so a consent reload can
		// re-point its sink (a level raise that reached nobody was the bug).
		cap := rest.NewCaptureHandler(rec, telemetry.TransportREST)
		h = cap.Wrap(h)
		AddTelemetrySink(cap.SetRecorder)
		// /api/v1/auth/* is public by design: register/login/token are the
		// bootstrap (the handlers enforce their own caller/account checks), and
		// Rust registered them outside the auth middleware for the same reason.
		// Everything else sits behind the bearer gate.
		root := http.NewServeMux()
		root.Handle("/api/v1/auth/", auth.Public(auth.Routes(st)))
		root.Handle("/", auth.Public(auth.MiddlewareWithStore(st, h)))
		log.Printf("leankg serve (REST) on %s", *restAddr)
		eps = append(eps, endpoint{addr: *restAddr, h: root})
	}
	if *rpcAddr != "" {
		mux := http.NewServeMux()
		// Liveness for the same reason the MCP, REST and UI mounts carry one:
		// FR-SELF-01 asks for /health on every listener, and a ConnectRPC
		// mux answers 404 for everything but the service route, so an
		// orchestrator probe against this address saw a dead server that was
		// in fact serving. Same envelope as the other listeners.
		mux.Handle("GET /health", auth.Public(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"ok":true}`))
		})))
		// The descriptor types `args` as map<string,string>, so a typed action
		// argument cannot be carried on this wire. The codec refuses by naming
		// its own internal `value` field, which the caller never sent; this
		// translates the refusal to name their value and this wire's limit.
		rpc.Mount(mux, engine, rec)
		log.Printf("leankg serve (ConnectRPC: gRPC+gRPC-Web+JSON) on %s", *rpcAddr)
		eps = append(eps, endpoint{addr: *rpcAddr, h: auth.MiddlewareWithStore(st, mux)})
	}
	if *uiAddr != "" {
		// One address, two surfaces: /api/ wins, the SPA fallback serves
		// everything else (web.Handler returns JSON 404 for unknown
		// api/* paths). The API handler routes ?project= like the REST
		// and MCP mounts, so the dashboard follows the selected project.
		// The dashboard's project switcher is real only when this listener
		// serves several projects: hand the handler a resolver bounded to the
		// registered set (unknown selectors still refuse).
		// Resolve, never Open: the switch endpoint must not migrate a store or
		// start language servers just because the UI asked about a path.
		switcher := func(path string) (string, bool) {
			if router == nil {
				return "", false
			}
			dir, err := router.Resolve(path)
			if err != nil {
				return "", false
			}
			return dir, true
		}
		dash := routeByProject(router,
			web.APIHandler(engine, mem, web.WithProjectSwitcher(switcher)),
			func(p *projects.Project) http.Handler {
				return web.APIHandler(p.Engine, p.Memory, web.WithProjectDir(p.Dir), web.WithProjectSwitcher(switcher))
			})
		uiMux := http.NewServeMux()
		// Liveness for container/orchestrator probes on this listener: the
		// SPA fallback would answer 200 for /health anyway, but with HTML,
		// which makes a health check that cannot tell a serving dashboard from
		// a broken one. Same envelope as rest.Handler's /health.
		uiMux.Handle("GET /health", auth.Public(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"ok":true}`))
		})))
		// D1: /api/file serves any file under the project root. It needs the
		// bearer token (Viewer minimum) whenever the gate is on. With the gate off
		// it is served only on a loopback --ui bind; any other bind refuses it
		// (uiFileGate), because the middleware resolves every caller to Admin when
		// no token exists. The Render demo therefore needs a token for the viewer.
		uiMux.Handle("GET /api/file", auth.Public(uiFileGate(st, *uiAddr, auth.MiddlewareWithStore(st, auth.Require(auth.Viewer, dash)))))
		uiMux.Handle("/api/", auth.Public(dash))
		uiMux.Handle("/", auth.Public(web.Handler()))
		log.Printf("leankg serve (UI) on %s", *uiAddr)
		// The UI listener is deliberately NOT gated by the exposure rule: the
		// dashboard is public by design (the Render demo serves
		// --read-only --ui :${PORT} to the internet), so it keeps a loud
		// warning instead of a refusal. Its query/import API is unauthenticated
		// either way; /api/file is refused unless a token is configured (D1).
		if !servebind.IsLoopback(*uiAddr) {
			bound := "all interfaces" // `--ui :PORT` — the container/PaaS shape
			if host, _, err := net.SplitHostPort(*uiAddr); err == nil && host != "" {
				bound = host
			}
			log.Printf("warning: dashboard bound to %s — its query/import API is unauthenticated and /api/file is refused unless a token is configured; bind loopback or front it with a proxy", bound)
		}
		eps = append(eps, endpoint{addr: *uiAddr, h: uiMux})
	}
	ls, err := bindAll(eps...)
	if err != nil {
		return err
	}
	return runServers(ctx, ls...)
}

// defaultHTTPAddr is the MCP listener when serve is given no address.
const defaultHTTPAddr = "127.0.0.1:9699"

// maintenanceStoreExists reports whether the project at dir already HAS a
// store, so the maintenance fleet does not open (and thereby create) one for
// a project nobody indexed.
//
// SQLite: the store file is on disk, so a stat decides it. PostgreSQL: the
// store is a schema, and opening it read-write CREATES a missing schema — so
// the probe is a read-only open, which reports ErrSchemaMissing instead of
// creating anything.
func maintenanceStoreExists(ctx context.Context, dir, engine string) bool {
	if engine == store.EngineSQLite {
		_, err := os.Stat(filepath.Join(dir, ".leankg", "leankg.db"))
		return err == nil
	}
	probe, err := openStoreWith(ctx, dir, engine, "", store.RO)
	if err != nil {
		return false
	}
	_ = probe.Close()
	return true
}

// sweepRegistry prunes the fleet registry on the maintenance tick.
//
// The registry is the SERVER-side list of what this deployment serves, and
// until the sweep existed nothing ever pruned it: a row for a project that
// left the fleet outlived the project forever, and the fleet kept answering
// questions about directories that no longer existed.
//
// The sweep removes REGISTRY ROWS only — never a file — keeps every row
// whose directory is still a live project, and reports the stores whose
// recorded index root no longer matches the directory they live in (a
// mismatch is REPORTED, never acted on: only a human can decide between
// rebasing the root and forgetting the project).
//
// It is quiet on a healthy registry: the log line below prints only when a
// row was removed or a mismatch was found.
//
// Like the store maintenance it rides, the sweep does not run when the
// maintenance loop is disabled (LEANKG_VACUUM_INTERVAL_HOURS=0): the
// registry is swept as often as the fleet is maintained, and not more often.
func sweepRegistry(ctx context.Context, eng string) {
	rep, err := portfolioreg.AutoSweep(ctx, portfolioreg.Options{
		Engine: eng,
		PGURL:  os.Getenv("LEANKG_PG_URL"),
	})
	if err != nil {
		log.Printf("maintenance: registry sweep: %v", err)
		return
	}
	if len(rep.Removed) > 0 || len(rep.RootMismatch) > 0 {
		log.Printf("maintenance: registry sweep removed %d row(s) for vanished project(s), %d store(s) record a different index root",
			len(rep.Removed), len(rep.RootMismatch))
	}
}

// fleetTargets builds the maintenance fleet: the default project's store
// and memory layer, plus every routed project that has a store on disk AND
// a memory index.
//
// The memory layer of a ROUTED project used to be skipped entirely: the
// fleet set Mem only for the default project, so the routed projects'
// memory indexes were never compacted. Measured: xdev/.leankg/memory/
// index.db sat at 4 KB plus a 124.8 KB WAL, untouched for a week, with the
// duplicate rows still in the bank file.
func fleetTargets(ctx context.Context, defaultDir string, st store.Backend, mem *memory.Memory, routed []string, eng string, keepModelIDs []string) []maintain.Target {
	out := []maintain.Target{{Store: st, Mem: mem, Label: filepath.Base(defaultDir)}}
	for _, d := range routed {
		if d == defaultDir {
			continue
		}
		// Only a store that already exists: opening a missing one
		// would CREATE it (and its schema) for a project nobody
		// has indexed yet.
		if !maintenanceStoreExists(ctx, d, eng) {
			continue
		}
		bs, err := openStoreWith(ctx, d, eng, "", store.RW)
		if err != nil {
			log.Printf("maintenance: open %s: %v", d, err)
			continue
		}
		t := maintain.Target{Store: bs, Label: filepath.Base(d)}
		closeTarget := func() { _ = bs.Close() }
		if pm := openProjectMemory(d, keepModelIDs); pm != nil {
			t.Mem = pm
			closeTarget = func() { _ = pm.Close(); _ = bs.Close() }
		}
		t.Close = closeTarget
		out = append(out, t)
	}
	return out
}

// openProjectMemory opens the memory layer of a project that HAS one, and
// returns nil for a project that has none.
//
// The existence probe matters: memory.Open CREATES the tree (the root, the
// core files and the index), so a maintenance pass must not give a project
// that never used memory a memory tree.
func openProjectMemory(dir string, keepModelIDs []string) *memory.Memory {
	if _, err := os.Stat(filepath.Join(dir, ".leankg", "memory", "index.db")); err != nil {
		return nil
	}
	m, err := memory.Open(dir, false)
	if err != nil {
		log.Printf("maintenance: open memory %s: %v", dir, err)
		return nil
	}
	m.SetKeepModelIDs(keepModelIDs)
	return m
}

func cmdDoctor(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	project := fs.String("project", "", "project directory (default: cwd, discovered upward for --deep)")
	deep := fs.Bool("deep", false, "run the deep self-diagnosis suite (latency, migrations, freshness, embeddings, pool env, orphans, duplicates, .leankg dir)")
	format := fs.String("format", "text", "output format for --deep: text (default) or json")
	if err := cliexit.Parse(fs, args); err != nil {
		return err
	}
	if *deep {
		return cliexit.Code(doctorDeep(ctx, *project, *format))
	}
	dir := *project
	if dir == "" {
		dir, _ = os.Getwd()
	}
	// find_project_root (nearest .leankg OR leankg.yaml), then the config's
	// project_path anchor: doctor must inspect the store the server serves
	// (Rust main.rs find_project_root + MCPServer::resolve_project_root).
	dir = projectcfg.FindProjectRoot(dir)
	// The CONFIGURED store wins: standalone mode (FR-P2) has no
	// <project>/.leankg at all, and the doctor's job is to report on the
	// store the engine actually uses. Reading the project-scoped path
	// instead made the diagnosis say "not indexed" about a project that
	// WAS indexed — into the store the env named.
	dbPath := store.StandaloneDBPath(dir)
	if dbPath == "" {
		dbDir := projectcfg.ResolveProjectRoot(filepath.Join(dir, ".leankg"))
		dbPath = filepath.Join(dbDir, "leankg.db")
	}
	st, err := openStore(ctx, dir, dbPath, store.RO)
	if err != nil {
		// A missing store is not a storage-layer event to the reader: it means
		// the project has not been indexed. Naming the syscall ("read-only open
		// of missing store", a raw stat error) buries the one action that fixes
		// it, so the diagnosis states the state and the command.
		if errors.Is(err, os.ErrNotExist) || strings.Contains(err.Error(), "no such file or directory") {
			fmt.Printf("FAIL not indexed: no store at %s\n", dbPath)
			fmt.Println("     run `leankg index .` in this project, then `leankg doctor` again")
			return cliexit.Code(2)
		}
		fmt.Printf("FAIL store: %v\n", err)
		return cliexit.Code(2)
	}
	defer st.Close()
	els, err1 := st.ElementCount(ctx)
	files, err2 := st.FileCount(ctx)
	seq, at, err3 := st.Watermark(ctx)
	if err1 != nil || err2 != nil || err3 != nil {
		fmt.Println("FAIL store queries:", err1, err2, err3)
		return cliexit.Code(2)
	}
	fmt.Printf("ok store=%s elements=%d files=%d watermark(seq=%d at=%d)\n", st.Path(), els, files, seq, at)
	return nil
}

// doctorDeep runs the H9 self-diagnosis suite (internal/doctor) and
// returns the CI exit code: 0 all-pass, 1 any warn, 2 any fail.
func doctorDeep(ctx context.Context, projectFlag, format string) int {
	dir := projectFlag
	if dir == "" {
		dir, _ = os.Getwd()
	}
	// find_project_root parity: walk up to the nearest dir owning a .leankg
	// entry or a leankg.yaml — the hand-rolled walk missed the config-only
	// case, and the config's project_path anchor is applied inside RunDeep.
	dir = projectcfg.FindProjectRoot(dir)
	report, err := doctor.RunDeep(ctx, dir, envOr("LEANKG_DB_ENGINE", ""), projectcfg.PGURL(dir), nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "leankg doctor --deep: %v\n", err)
		return 2
	}
	switch format {
	case "json":
		out, jerr := report.RenderJSON()
		if jerr != nil {
			fmt.Fprintf(os.Stderr, "leankg doctor --deep: %v\n", jerr)
			return 2
		}
		fmt.Println(out)
	default:
		fmt.Printf("LeanKG doctor --deep — %s\n", dir)
		fmt.Print(report.RenderTable())
	}
	code := report.ExitCode()
	if code != 0 {
		fmt.Fprintf(os.Stderr, "leankg doctor --deep found issues (exit %d); hints above suggest fixes.\n", code)
	}
	return code
}

func cmdIndex(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("index", flag.ContinueOnError)
	project := fs.String("project", "", "project directory (default cwd)")
	source := fs.String("source", "", "remote source URI: git+<url>, gs://bucket/prefix, or a local path")
	refName := fs.String("ref-name", "", "git ref for --source git+... (default: main)")
	authFlag := fs.String("auth", "", "credential for --source (git token or GCS access token)")
	auto := fs.Bool("auto", false, "first-run setup mode: index (and later embed) without asking again")
	manual := fs.Bool("manual", false, "first-run setup mode: never index or embed unless explicitly asked")
	rebase := fs.Bool("rebase-root", false, "adopt this directory as the store's index root (after moving a checkout); without it a store built from another root refuses the walk")
	hook := fs.Bool("hook", false, "SessionStart hook mode: exit 0 without indexing when HEAD exceeds LEANKG_HOOK_MAX_FILES or another index of the store is running; the embed is skipped when the backlog exceeds LEANKG_HOOK_MAX_EMBED")
	hookBudgetFlag := fs.Duration("hook-budget", 0, "with --hook: time budget for the index and embed (default LEANKG_HOOK_BUDGET, else 10s); once spent, the index stops before its next directory (a started directory always finishes) and the embed stops; prints one line and exits 0")
	noEmbed := fs.Bool("no-embed", false, "index only: skip the incremental embed that follows a successful index")
	positional, err := parseInterspersed("index", fs, args, 1)
	if err != nil {
		return err
	}
	if *auto && *manual {
		return errors.New("index: --auto and --manual are mutually exclusive")
	}
	if len(positional) == 0 && *source == "" {
		return errors.New("index requires a directory argument (or --source)")
	}
	target := ""
	if len(positional) == 1 {
		target = positional[0]
	}
	// The walk root must EXIST, and that is checked BEFORE the store dir is
	// created: the store lives at <target>/.leankg, so creating the store
	// creates the very path the caller named — and the check inside runIndex
	// then sees a directory that exists only because of this run. `leankg
	// index ./nope` printed the ordinary success line and exited 0, after
	// which every downstream signal (status cold, doctor empty) agreed that
	// the project was healthy and had indexed nothing.
	if *source == "" && target != "" {
		info, serr := os.Stat(target)
		if serr != nil {
			if os.IsNotExist(serr) {
				return fmt.Errorf("index: refusing to index %s: no such file or directory (an empty existing directory indexes fine — this is a path that does not exist)", target)
			}
			return fmt.Errorf("index: refusing to index %s: %v", target, serr)
		}
		if !info.IsDir() {
			return fmt.Errorf("index: refusing to index %s: not a directory", target)
		}
	}
	// The hook's time budget covers the size gate, the index and the embed. The
	// size gate and the embed stop at the hard deadline hardCtx; the index stops
	// softly, between directories, once the budget is spent (index.WithBudget).
	hardCtx, indexCtx := ctx, ctx
	var budget time.Duration
	if *hook {
		budget = hookBudget(*hookBudgetFlag)
		var cancel context.CancelFunc
		hardCtx, cancel = context.WithTimeout(ctx, budget)
		defer cancel()
		indexCtx = index.WithBudget(ctx, hardCtx.Done())
	}
	// The hook's size gate runs before any side effect: no setup-mode
	// persistence, no .leankg, no store.
	if *hook && *source == "" {
		if msg := hookBigRepoSkip(hardCtx, target); msg != "" {
			fmt.Println(msg)
			return nil
		}
	}
	// One index per store at a time. Hook mode gives up on a held lock so a
	// session start never queues behind a running index; a manual run waits.
	storeDir := indexStoreDir(*project, target)
	lockPath := filepath.Join(storeDir, ".leankg", "index.lock")
	if err := os.MkdirAll(filepath.Dir(lockPath), 0o755); err != nil {
		return fmt.Errorf("index: create .leankg: %v", err)
	}
	var unlock func()
	if *hook {
		unlock, err = flock.TryLock(lockPath)
		if errors.Is(err, flock.ErrHeld) {
			fmt.Printf("leankg index --hook: skipped %s: another index of this store is running\n", storeDir)
			return nil
		}
	} else {
		unlock, err = flock.Lock(lockPath)
	}
	if err != nil {
		return fmt.Errorf("index: lock %s: %v", lockPath, err)
	}
	defer unlock()

	// FR-ZCP-13 first-run contract: exactly one auto/manual question per user,
	// persisted. Precedence: flags > LEANKG_SETUP_MODE > the stored choice >
	// the interactive prompt (TTY only) > manual. Registration is `index` —
	// the Rust `add` verb this travelled with does not exist here.
	root := projectcfg.FindProjectRoot(*project)
	var flagMode *setupcfg.SetupMode
	if *auto {
		m := setupcfg.ModeAuto
		flagMode = &m
	} else if *manual {
		m := setupcfg.ModeManual
		flagMode = &m
	}
	envMode, envOK := setupcfg.ModeFromEnv()
	var envPtr *setupcfg.SetupMode
	if envOK {
		envPtr = &envMode
	}
	stored, _ := setupcfg.Load(root)
	decision := setupcfg.ResolveMode(flagMode, envPtr, stored.Setup, setupcfg.Interactive(), promptSetupMode)
	if decision.Source == setupcfg.SourcePrompt {
		log.Printf("setup mode: %s (asked once, stored in %s)", decision.Mode, setupcfg.PathFor(root))
	} else if decision.Source != setupcfg.SourceStored {
		log.Printf("setup mode: %s (%s)", decision.Mode, decision.Source)
	}
	mode := decision.Mode
	if err := setupcfg.Save(root, setupcfg.Config{Setup: &mode, Embed: stored.Embed}); err != nil {
		log.Printf("setup mode: cannot persist choice: %v", err)
	}
	// An explicit `index` always indexes, whatever the mode: manual mode governs
	// the AUTOMATIC paths (serve's auto-index gate, the setup pipeline), not a
	// command the user typed.
	_ = mode

	if err := runIndex(indexCtx, *project, target, *source, *refName, sourceAuth(*authFlag), *rebase); err != nil {
		switch {
		case *hook && errors.Is(err, index.ErrBudget):
			fmt.Printf("leankg index --hook: paused %s after its %s time budget: every directory it started is finished and kept, none was rolled back, and the next session start indexes the remaining directories\n", storeDir, budget)
			return nil
		case errors.Is(err, context.Canceled):
			return fmt.Errorf("index: interrupted; the directories finished so far are kept, the one in progress is rolled back; rerun to finish: %w", err)
		}
		return fmt.Errorf("index: %v", err)
	}
	if !*noEmbed {
		indexEmbed(hardCtx, storeDir, *hook)
	}
	return nil
}

// promptSetupMode asks the one first-run question (Rust setup_config prompt):
// empty input keeps the Rust default (manual).
func promptSetupMode() *setupcfg.SetupMode {
	fmt.Print("First-run setup: index and embed automatically in the background? [y/N] ")
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil {
		return nil
	}
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes", "auto", "a":
		m := setupcfg.ModeAuto
		return &m
	case "n", "no", "manual", "m", "":
		m := setupcfg.ModeManual
		return &m
	default:
		return nil
	}
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// sourceAuth resolves the credential for --source: --auth, then GITLAB_TOKEN,
// then GIT_TOKEN (Rust CLI chain). The GCS source also reads GCS_ACCESS_TOKEN
// itself; the Rust watch verb was the only caller that added it to the chain.
func sourceAuth(flagValue string) string {
	if flagValue != "" {
		return flagValue
	}
	if v := os.Getenv("GITLAB_TOKEN"); v != "" {
		return v
	}
	return os.Getenv("GIT_TOKEN")
}

// storeErrText renders a store-open failure through the FR-ZCP-12 catalog:
// a Postgres URL the driver cannot parse — or a missing one — is
// PG_URL_MALFORMED, every other Postgres open failure is PG_UNREACHABLE (the
// Rust engine rendered both at startup); other engines keep the driver's own
// message, which the catalog has no code for.
func storeErrText(engine string, err error) string {
	if engine != store.EnginePostgres {
		return err.Error()
	}
	msg := err.Error()
	if strings.Contains(msg, "parse pg dsn") || strings.Contains(msg, "requires LEANKG_PG_URL") {
		return errs.PGURLMalformed.Render(msg, errs.PGURLMalformed.Fix)
	}
	return errs.PGUnreachable.Render(msg, errs.PGUnreachable.Fix)
}
