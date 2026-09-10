// cmd/leankg-embed — the embedding data pipeline as its own binary (issue
// #368). Independent process, independent resource profile, same store
// layer: scan → plan → invoke provider → validate → write vectors+stamp →
// report. The serving binary links internal/embed for the shared stamp
// guard but performs zero inference.
//
// Subcommands:
//
//	run       incremental embed of content-hash-diffed QNs
//	full      rebuild everything (stamp-guarded; mismatch ⇒ clear + rebuild)
//	export    NDJSON {qualified_name, content_hash, text} for offsite batching
//	import    NDJSON {qualified_name, vec} from offsite batching (resumable)
//	status    last run, coverage, stamp (reads DB only)
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"syscall"

	"github.com/FreePeak/LeanKG/go/internal/embed"
	"github.com/FreePeak/LeanKG/go/internal/store"
)

func main() {
	log.SetFlags(0)
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	switch os.Args[1] {
	case "run":
		cmdRun(os.Args[2:], "incremental")
	case "full":
		cmdRun(os.Args[2:], "full")
	case "export":
		cmdExport(os.Args[2:])
	case "import":
		cmdImport(os.Args[2:])
	case "status":
		cmdStatus(os.Args[2:])
	default:
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `leankg-embed — LeanKG embedding pipeline (independent binary)

Usage:
  leankg-embed run    [--project DIR]   incremental (hash-diffed) run
  leankg-embed full   [--project DIR]   full rebuild (stamp-guarded)
  leankg-embed export [--project DIR] [--model M] [--out FILE]   NDJSON export for offsite batching
  leankg-embed import [--project DIR] [--model M] [--revision R] [--dims N] [--in FILE]
  leankg-embed status [--project DIR]                  last run / stamps / element count

Providers (env): LEANKG_EMBED_PROVIDER=local|openai|deterministic (default local,
llama.cpp sidecar shape http://127.0.0.1:8080/v1), LEANKG_EMBED_BASE_URL,
LEANKG_EMBED_API_KEY, LEANKG_EMBED_MODEL, LEANKG_EMBED_DIMS.
`)
}

// projectDir resolves the --project flag (empty = cwd).
func projectDir(project string) string {
	if project != "" {
		return project
	}
	dir, err := os.Getwd()
	if err != nil {
		log.Fatalf("resolve cwd: %v", err)
	}
	return dir
}

func openStore(project string) (*store.Store, error) {
	dir := projectDir(project)
	st, err := store.Open(filepath.Join(dir, ".leankg", "leankg.db"), store.RW)
	if err != nil {
		return nil, err
	}
	if err := st.Migrate(); err != nil {
		st.Close()
		return nil, err
	}
	return st, nil
}

// lock takes the single-flight flock for the PROJECT dir (not the process
// cwd): concurrent invocations against one project serialize; abandoned
// locks release with the process.
func lock(dir string) (func(), error) {
	lockPath := filepath.Join(dir, ".leankg", "embed.lock")
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, fmt.Errorf("another leankg-embed run holds %s (single-flight per project)", lockPath)
	}
	return func() { _ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN); f.Close() }, nil
}

func cmdRun(args []string, mode string) {
	fs := flag.NewFlagSet(mode, flag.ExitOnError)
	project := fs.String("project", "", "project directory (default cwd)")
	if err := fs.Parse(args); err != nil {
		log.Fatal(err)
	}
	unlock, err := lock(projectDir(*project))
	if err != nil {
		log.Fatal(err)
	}
	defer unlock()
	st, err := openStore(*project)
	if err != nil {
		log.Fatalf("store: %v", err)
	}
	defer st.Close()
	p, err := embed.FromEnv()
	if err != nil {
		log.Fatalf("provider: %v", err)
	}
	report, err := embed.Run(context.Background(), st, p, mode)
	if err != nil {
		log.Fatalf("run: %v (status recorded)", err)
	}
	b, _ := json.Marshal(report)
	fmt.Println(string(b))
}

func cmdExport(args []string) {
	fs := flag.NewFlagSet("export", flag.ExitOnError)
	project := fs.String("project", "", "project directory (default cwd)")
	out := fs.String("out", "", "output file (default stdout)")
	modelID := fs.String("model", "", "model to export (required)")
	if err := fs.Parse(args); err != nil {
		log.Fatal(err)
	}
	if *modelID == "" {
		log.Fatal("export requires --model")
	}
	st, err := openStore(*project)
	if err != nil {
		log.Fatalf("store: %v", err)
	}
	defer st.Close()
	w := os.Stdout
	if *out != "" {
		f, err := os.Create(*out)
		if err != nil {
			log.Fatal(err)
		}
		defer f.Close()
		w = f
	}
	if err := embed.ExportNDJSON(context.Background(), st, *modelID, w); err != nil {
		log.Fatalf("export: %v", err)
	}
}

func cmdImport(args []string) {
	fs := flag.NewFlagSet("import", flag.ExitOnError)
	project := fs.String("project", "", "project directory (default cwd)")
	in := fs.String("in", "", "input file (default stdin)")
	modelID := fs.String("model", "", "model being imported (required)")
	revision := fs.String("revision", "", "model revision pin (required)")
	dims := fs.Int("dims", 0, "vector dimensions (required)")
	distance := fs.String("distance", "cosine", "distance metric")
	if err := fs.Parse(args); err != nil {
		log.Fatal(err)
	}
	if *modelID == "" || *revision == "" || *dims == 0 {
		log.Fatal("import requires --model, --revision, --dims")
	}
	unlock, err := lock(projectDir(*project))
	if err != nil {
		log.Fatal(err)
	}
	defer unlock()
	st, err := openStore(*project)
	if err != nil {
		log.Fatalf("store: %v", err)
	}
	defer st.Close()
	r := os.Stdin
	if *in != "" {
		f, err := os.Open(*in)
		if err != nil {
			log.Fatal(err)
		}
		defer f.Close()
		r = f
	}
	report, err := embed.ImportNDJSON(context.Background(), st, *modelID, *revision, *distance, *dims, r)
	if err != nil {
		log.Fatalf("import: %v", err)
	}
	b, _ := json.Marshal(report)
	fmt.Println(string(b))
}

func cmdStatus(args []string) {
	fs := flag.NewFlagSet("status", flag.ExitOnError)
	project := fs.String("project", "", "project directory (default cwd)")
	if err := fs.Parse(args); err != nil {
		log.Fatal(err)
	}
	st, err := openStore(*project)
	if err != nil {
		log.Fatalf("store: %v", err)
	}
	defer st.Close()
	out := map[string]any{}
	if stamps, err := st.Stamps(); err != nil {
		log.Fatalf("status: stamps: %v", err)
	} else {
		out["stamps"] = stamps
	}
	if run, err := st.LastEmbedRunAny(); err != nil {
		log.Fatalf("status: last run: %v", err)
	} else if run != nil {
		out["last_run"] = run
	}
	if els, err := st.ElementCount(); err != nil {
		log.Fatalf("status: elements: %v", err)
	} else {
		out["elements"] = els
	}
	b, _ := json.Marshal(out)
	fmt.Println(string(b))
}
