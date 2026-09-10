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
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/FreePeak/LeanKG/go/internal/auth"
	"github.com/FreePeak/LeanKG/go/internal/core"
	"github.com/FreePeak/LeanKG/go/internal/embed"
	leankgmcp "github.com/FreePeak/LeanKG/go/internal/mcp"
	"github.com/FreePeak/LeanKG/go/internal/memory"
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
	case "writer":
		cmdWriter(os.Args[2:])
	case "connect":
		cmdConnect(os.Args[2:])
	case "install":
		cmdInstall(os.Args[2:])
	case "version":
		fmt.Println("leankg " + Version())
	case "doctor":
		cmdDoctor(os.Args[2:])
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
  leankg query <text> [--kind name|impact] [--depth N]
  leankg serve  [--project DIR] [--stdio] [--http ADDR] [--rest ADDR] [--read-only] [--memory] [--embed-provider P]
  leankg index  [--project DIR] <dir>
  leankg doctor [--project DIR]

Defaults: --http :9699 (MCP streamable HTTP) and --rest :8080 (REST) when
neither --stdio nor addresses are given; --project defaults to cwd.
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
		log.Fatalf("open store: %v", err)
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
		p, perr := embed.FromEnv()
		if perr != nil {
			log.Fatalf("embed provider: %v", perr)
		}
		embedder = core.QueryEmbedderFromProvider(p)
	}

	engine := core.New(st, mem, embedder)

	if *stdio {
		if *httpAddr != "" || *restAddr != "" || *rpcAddr != "" || *uiAddr != "" {
			log.Fatal("serve: --stdio is exclusive of --http/--rest/--rpc/--ui")
		}
		srv := leankgmcp.New(engine)
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
		srv := leankgmcp.New(engine)
		mux := httpMux(srv.HTTPHandler())
		log.Printf("leankg serve (MCP HTTP) on %s project=%s engine=%s", addr, dir, st.Engine())
		go serveHTTP(ctx, mux, addr)
	}
	if *restAddr != "" {
		log.Printf("leankg serve (REST) on %s", *restAddr)
		go serveHTTP(ctx, auth.Middleware(rest.Handler(engine, mem)), *restAddr)
	}
	if *rpcAddr != "" {
		svc := rpc.NewLeanKGService(engine)
		path, handler := leankgv1connect.NewLeanKGHandler(svc)
		mux := http.NewServeMux()
		mux.Handle(path, handler)
		log.Printf("leankg serve (ConnectRPC: gRPC+gRPC-Web+JSON) on %s", *rpcAddr)
		go serveHTTP(ctx, auth.Middleware(mux), *rpcAddr)
	}
	if *uiAddr != "" {
		log.Printf("leankg serve (UI) on %s", *uiAddr)
		go serveHTTP(ctx, web.Handler(), *uiAddr)
	}
	<-ctx.Done()
}

func cmdIndex(args []string) {
	fs := flag.NewFlagSet("index", flag.ExitOnError)
	project := fs.String("project", "", "project directory (default cwd)")
	if err := fs.Parse(args); err != nil {
		log.Fatal(err)
	}
	if fs.NArg() != 1 {
		log.Fatal("index requires exactly one directory argument")
	}
	if err := runIndex(*project, fs.Arg(0)); err != nil {
		log.Fatalf("index: %v", err)
	}
}

func cmdDoctor(args []string) {
	fs := flag.NewFlagSet("doctor", flag.ExitOnError)
	project := fs.String("project", "", "project directory (default cwd)")
	if err := fs.Parse(args); err != nil {
		log.Fatal(err)
	}
	dir := *project
	if dir == "" {
		dir, _ = os.Getwd()
	}
	st, err := store.Open(filepath.Join(dir, ".leankg", "leankg.db"), store.RO)
	if err != nil {
		fmt.Printf("FAIL store: %v\n", err)
		os.Exit(2)
	}
	defer st.Close()
	els, err1 := st.ElementCount()
	files, err2 := st.FileCount()
	seq, at, err3 := st.Watermark()
	if err1 != nil || err2 != nil || err3 != nil {
		fmt.Println("FAIL store queries:", err1, err2, err3)
		os.Exit(2)
	}
	fmt.Printf("ok store=%s elements=%d files=%d watermark(seq=%d at=%d)\n", st.Path(), els, files, seq, at)
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
