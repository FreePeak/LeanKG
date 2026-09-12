// cmd/leankg — the Go engine serving binary: MCP (stdio + HTTP) and REST
// from one core. Role flags:
//
//	leankg serve --stdio                       MCP over stdio (coding tools)
//	leankg serve --http :9699 --rest :8080     MCP HTTP + REST API
//	leankg serve --read-only ...               RO store handle (reader role)
//	leankg index <dir>                         one-shot index run
//	leankg doctor                              store diagnostics
//
// The serving binary performs ZERO embedding inference (issue #368): query-
// time embedding is an HTTP client call (sidecar or API) wired through
// internal/embed providers; the pipeline itself is cmd/leankg-embed.
package main

import (
	"context"
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

	"github.com/FreePeak/LeanKG/go/internal/auth"
	"github.com/FreePeak/LeanKG/go/internal/core"
	"github.com/FreePeak/LeanKG/go/internal/doctor"
	"github.com/FreePeak/LeanKG/go/internal/embed"
	"github.com/FreePeak/LeanKG/go/internal/errs"
	"github.com/FreePeak/LeanKG/go/internal/langs"
	leankgmcp "github.com/FreePeak/LeanKG/go/internal/mcp"
	"github.com/FreePeak/LeanKG/go/internal/memory"
	"github.com/FreePeak/LeanKG/go/internal/projects"
	"github.com/FreePeak/LeanKG/go/internal/rest"
	"github.com/FreePeak/LeanKG/go/internal/rpc"
	leankgv1connect "github.com/FreePeak/LeanKG/go/internal/rpc/leankg/v1/leankgv1connect"
	"github.com/FreePeak/LeanKG/go/internal/store"
	"github.com/FreePeak/LeanKG/go/internal/web"
)

func main() {
	log.SetFlags(0)
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	switch os.Args[1] {
	case "serve":
		cmdServe(os.Args[2:])
	case "index":
		cmdIndex(os.Args[2:])
	case "query":
		cmdQuery(os.Args[2:])
	case "impact":
		cmdQuery(append([]string{os.Args[2], "--kind", "impact"}, os.Args[3:]...))
	case "writer":
		cmdWriter(os.Args[2:])
	case "connect":
		cmdConnect(os.Args[2:])
	case "install":
		cmdInstall(os.Args[2:])
	case "version":
		fmt.Println("leankg " + Version())
	case "obsidian":
		cmdObsidian(os.Args[2:])
	case "status":
		cmdStatus(os.Args[2:])
	case "doctor":
		os.Exit(cmdDoctor(os.Args[2:]))
	case "run":
		cmdRun(os.Args[2:])
	case "detect-clusters":
		cmdDetectClusters(os.Args[2:])
	case "report":
		cmdReport(os.Args[2:])
	case "gods":
		cmdGods(os.Args[2:])
	case "ctags":
		cmdCtags(os.Args[2:])
	case "cost":
		cmdCost(os.Args[2:])
	case "migrate":
		cmdMigrate(os.Args[2:])
	case "audit":
		cmdAudit(os.Args[2:])
	case "metrics":
		cmdMetrics(os.Args[2:])
	case "dashboard":
		cmdDashboard(os.Args[2:])
	case "auth":
		cmdAuth(os.Args[2:])
	case "tunnels":
		cmdTunnels(os.Args[2:])
	case "quality":
		cmdQuality(os.Args[2:])
	case "reflect":
		cmdReflect(os.Args[2:])
	case "refresh":
		cmdRefresh(os.Args[2:])
	case "register":
		cmdRegister(os.Args[2:])
	case "unregister":
		cmdUnregister(os.Args[2:])
	case "list":
		cmdListRepos(os.Args[2:])
	case "status-repo":
		cmdStatusRepo(os.Args[2:])
	case "export":
		cmdExport(os.Args[2:])
	case "pack":
		cmdPack(os.Args[2:])
	case "generate":
		cmdGenerate(os.Args[2:])
	case "annotate":
		cmdAnnotate(os.Args[2:])
	case "prd":
		cmdPRD(os.Args[2:])
	case "prd-trace":
		cmdPRDTrace(os.Args[2:])
	case "link":
		cmdLink(os.Args[2:])
	case "search-annotations":
		cmdSearchAnnotations(os.Args[2:])
	case "show-annotations":
		cmdShowAnnotations(os.Args[2:])
	case "mine-conversations":
		cmdMineConversations(os.Args[2:])
	case "incident":
		cmdIncident(os.Args[2:])
	case "note":
		cmdNote(os.Args[2:])
	case "env-conflicts":
		cmdEnvConflicts(os.Args[2:])
	case "service-context":
		cmdServiceContext(os.Args[2:])
	case "team-map":
		cmdTeamMap(os.Args[2:])
	case "-h", "--help", "help":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "leankg: unknown command %q\n\n", os.Args[1])
		usage()
		os.Exit(2)
	}
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
  leankg migrate [--project DIR] [--engine sqlite|postgres]
  leankg audit export [--project DIR] [--since T] [--until T] [--format jsonl] [--out FILE]
  leankg audit verify [--project DIR] [--since T] [--until T]
  leankg metrics [--project DIR] [--since N|Nd] [--tool NAME] [--json|-j] [--session]
                 [--reset] [--cleanup] [--retention DAYS] [--seed]
  leankg dashboard [--project DIR] [--since 24h|7d|30d|2w] [--format text|json]
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
  leankg obsidian <init|push|pull|watch|status> [--project DIR] [--vault PATH] [--debounce-ms N]
  leankg export [--output FILE] [--format json|dot|mermaid] [--markdown] [--out FILE]
                [--file F] [--depth N] [--path P] [--community C] [--max-nodes N]
  leankg pack   [--output DIR] [--path P] [--max-nodes N] [--revision REV] [--project DIR]
  leankg generate [--project DIR]
  leankg annotate <element> --description TEXT [--user-story ID] [--feature ID]
  leankg link <element> <id> [--kind story|feature]
  leankg search-annotations <query> | show-annotations <element>
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
  leankg index <dir> [--source URI] [--ref-name REF] [--auth TOKEN]
  leankg prd [--source docs/prd.md] [--environment local] [--project DIR]
  leankg prd-trace [FEATURE_ID] [--project DIR]
  leankg push --remote URL --token TOKEN [--env local] [--project DIR]
  leankg pull --remote URL --token TOKEN [--env production]
  leankg doctor [--project DIR] [--deep] [--format text|json]
  leankg status [--project DIR]
  leankg version

--action vocabulary: path|callers|callees|context|explain|pattern|lsp|compress|read|
search|exact|fuzzy|semantic|element|impact|languages|prd|incidents|env_conflicts|
service_context (queried through the same core wiring the MCP transports use).
Time filters (audit --since/--until): RFC3339 | epoch seconds | 90s|30m|24h|7d.

Defaults: --http :9699 (MCP streamable HTTP) and --rest :8080 (REST) when
neither --stdio nor addresses are given; --project defaults to cwd.
LEANKG_PROJECT_DIRS (comma-separated) registers extra projects; REST/MCP
requests select one with ?project= (dir path or name).
`)
}

func cmdServe(args []string) {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	project := fs.String("project", "", "project directory (default cwd)")
	stdio := fs.Bool("stdio", false, "serve MCP over stdio")
	httpAddr := fs.String("http", "", "MCP streamable HTTP address (e.g. :9699)")
	restAddr := fs.String("rest", "", "REST API address (e.g. :8080)")
	readOnly := fs.Bool("read-only", false, "open the store read-only (reader role)")
	withMemory := fs.Bool("memory", false, "enable the markdown memory layer")
	engineName := fs.String("engine", "", "storage engine: sqlite|postgres (default: LEANKG_DB_ENGINE, else sqlite; postgres needs LEANKG_PG_URL)")
	embedProvider := fs.String("embed-provider", "", "query-time embedder: openai|local|deterministic (env LEANKG_EMBED_* configure it; empty = L3 degrades with reason)")
	rpcAddr := fs.String("rpc", "", "ConnectRPC address (gRPC + gRPC-Web + JSON, e.g. :9090)")
	uiAddr := fs.String("ui", "", "dashboard address serving the embedded ui-v2 build (e.g. :8080)")
	if err := fs.Parse(args); err != nil {
		log.Fatal(err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	dir := *project
	if dir == "" {
		var err error
		dir, err = os.Getwd()
		if err != nil {
			log.Fatalf("resolve cwd: %v", err)
		}
	}

	mode := store.RW
	if *readOnly {
		mode = store.RO
	}
	eng := *engineName
	if eng == "" {
		eng = envOr("LEANKG_DB_ENGINE", "sqlite")
	}
	st, err := store.OpenBackend(ctx, dir, eng, os.Getenv("LEANKG_PG_URL"), mode)
	if err != nil {
		log.Fatalf("open store: %s", storeErrText(eng, err))
	}
	defer st.Close()
	if !*readOnly {
		if err := st.Migrate(); err != nil {
			log.Fatalf("migrate: %v", err)
		}
	}

	var mem *memory.Memory
	if *withMemory {
		if mem, err = memory.Open(dir, false); err != nil {
			log.Fatalf("memory: %v", err)
		}
	}

	var embedder core.QueryEmbedder
	if *embedProvider != "" {
		if err := os.Setenv("LEANKG_EMBED_PROVIDER", *embedProvider); err != nil {
			log.Fatalf("set provider env: %v", err)
		}
	}
	if os.Getenv("LEANKG_EMBED_PROVIDER") != "" {
		p, release, perr := embed.StartProvider(ctx)
		if perr != nil {
			log.Fatalf("embed provider: %v", perr)
		}
		defer release()
		embedder = core.QueryEmbedderFromProvider(p)
	}
	engine := core.New(st, mem, embedder)
	engine.SetProjectDir(dir) // enables the session actions

	// Lazy language activation: detect the languages this codebase (incl.
	// nested repos) actually uses; everything else stays idle.
	langReg := langs.DefaultRegistry()
	if roots, err := langReg.Activate(dir); err != nil {
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
	engine.SetLangsRegistry(langReg)

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

	if *stdio {
		if *httpAddr != "" || *restAddr != "" || *rpcAddr != "" || *uiAddr != "" {
			log.Fatal("serve: --stdio is exclusive of --http/--rest/--rpc/--ui")
		}
		// stdio is one process-bound MCP session; per-project selection
		// travels as the `project` tool argument, routed through the
		// router hook.
		srv := leankgmcp.New(engine)
		srv.SetProjectRouter(router)
		log.Printf("leankg serve (stdio) project=%s engine=%s", dir, st.Engine())
		if err := srv.RunStdio(ctx); err != nil {
			log.Fatalf("stdio: %v", err)
		}
		return
	}

	addr := *httpAddr
	if addr == "" && *restAddr == "" {
		addr = ":9699"
	}
	if addr != "" {
		mcpSrv := leankgmcp.New(engine)
		mcpSrv.SetProjectRouter(router)
		h := httpMux(mcpSrv.HTTPHandler())
		h = routeByProject(ctx, router, h, func(p *projects.Project) http.Handler {
			pSrv := leankgmcp.New(p.Engine)
			pSrv.SetProjectRouter(router)
			return httpMux(pSrv.HTTPHandler())
		})
		log.Printf("leankg serve (MCP HTTP) on %s project=%s engine=%s", addr, dir, st.Engine())
		go serveHTTP(ctx, h, addr)
	}
	if *restAddr != "" {
		// Routing sits INSIDE the auth boundary: token gating applies
		// before a selector is resolved or its name echoed back.
		h := routeByProject(ctx, router, rest.Handler(engine, mem), func(p *projects.Project) http.Handler {
			return rest.Handler(p.Engine, p.Memory)
		})
		// /api/v1/auth/* is public by design: register/login/token are the
		// bootstrap (the handlers enforce their own caller/account checks), and
		// Rust registered them outside the auth middleware for the same reason.
		// Everything else sits behind the bearer gate.
		root := http.NewServeMux()
		root.Handle("/api/v1/auth/", auth.Routes(st))
		root.Handle("/", auth.MiddlewareWithStore(st, h))
		log.Printf("leankg serve (REST) on %s", *restAddr)
		go serveHTTP(ctx, root, *restAddr)
	}
	if *rpcAddr != "" {
		svc := rpc.NewLeanKGService(engine)
		path, handler := leankgv1connect.NewLeanKGHandler(svc)
		mux := http.NewServeMux()
		mux.Handle(path, handler)
		log.Printf("leankg serve (ConnectRPC: gRPC+gRPC-Web+JSON) on %s", *rpcAddr)
		go serveHTTP(ctx, auth.MiddlewareWithStore(st, mux), *rpcAddr)
	}
	if *uiAddr != "" {
		// One address, two surfaces: /api/ wins, the SPA fallback serves
		// everything else (web.Handler returns JSON 404 for unknown
		// api/* paths). The API handler routes ?project= like the REST
		// and MCP mounts, so the dashboard follows the selected project.
		dash := routeByProject(ctx, router, web.APIHandler(engine, mem), func(p *projects.Project) http.Handler {
			return web.APIHandler(p.Engine, p.Memory)
		})
		uiMux := http.NewServeMux()
		uiMux.Handle("/api/", dash)
		uiMux.Handle("/", web.Handler())
		log.Printf("leankg serve (UI) on %s", *uiAddr)
		if host, _, err := net.SplitHostPort(*uiAddr); err == nil && host != "127.0.0.1" && host != "localhost" && host != "::1" {
			log.Printf("warning: dashboard bound to %s — its API (query/file/import) is unauthenticated; bind loopback or front it with a proxy", host)
		}
		go serveHTTP(ctx, uiMux, *uiAddr)
	}
	<-ctx.Done()
}

func cmdDoctor(args []string) int {
	fs := flag.NewFlagSet("doctor", flag.ExitOnError)
	project := fs.String("project", "", "project directory (default: cwd, discovered upward for --deep)")
	deep := fs.Bool("deep", false, "run the deep self-diagnosis suite (latency, migrations, freshness, embeddings, pool env, orphans, duplicates, .leankg dir)")
	format := fs.String("format", "text", "output format for --deep: text (default) or json")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *deep {
		return doctorDeep(*project, *format)
	}
	dir := *project
	if dir == "" {
		dir, _ = os.Getwd()
	}
	st, err := store.Open(filepath.Join(dir, ".leankg", "leankg.db"), store.RO)
	if err != nil {
		fmt.Printf("FAIL store: %v\n", err)
		return 2
	}
	defer st.Close()
	els, err1 := st.ElementCount()
	files, err2 := st.FileCount()
	seq, at, err3 := st.Watermark()
	if err1 != nil || err2 != nil || err3 != nil {
		fmt.Println("FAIL store queries:", err1, err2, err3)
		return 2
	}
	fmt.Printf("ok store=%s elements=%d files=%d watermark(seq=%d at=%d)\n", st.Path(), els, files, seq, at)
	return 0
}

// doctorDeep runs the H9 self-diagnosis suite (internal/doctor) and
// returns the CI exit code: 0 all-pass, 1 any warn, 2 any fail.
func doctorDeep(projectFlag, format string) int {
	dir := projectFlag
	if dir == "" {
		dir, _ = os.Getwd()
		// find_project_root parity: walk up to the nearest dir owning a
		// .leankg directory before giving up.
		for {
			if info, err := os.Stat(filepath.Join(dir, ".leankg")); err == nil && info.IsDir() {
				break
			}
			parent := filepath.Dir(dir)
			if parent == dir {
				break
			}
			dir = parent
		}
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	report, err := doctor.RunDeep(ctx, dir, envOr("LEANKG_DB_ENGINE", ""), os.Getenv("LEANKG_PG_URL"), nil)
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

func cmdIndex(args []string) {
	fs := flag.NewFlagSet("index", flag.ExitOnError)
	project := fs.String("project", "", "project directory (default cwd)")
	source := fs.String("source", "", "remote source URI: git+<url>, gs://bucket/prefix, or a local path")
	refName := fs.String("ref-name", "", "git ref for --source git+... (default: main)")
	authFlag := fs.String("auth", "", "credential for --source (git token or GCS access token)")
	if err := fs.Parse(args); err != nil {
		log.Fatal(err)
	}
	if fs.NArg() > 1 || (fs.NArg() == 0 && *source == "") {
		log.Fatal("index requires a directory argument (or --source)")
	}
	if err := runIndex(*project, fs.Arg(0), *source, *refName, sourceAuth(*authFlag)); err != nil {
		log.Fatalf("index: %v", err)
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
