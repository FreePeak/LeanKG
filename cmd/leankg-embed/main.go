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
//	import    NDJSON {qualified_name, content_hash, vec} from offsite batching (resumable)
//	status    last run, coverage, stamp (reads DB only)
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/FreePeak/LeanKG/internal/embed"
	"github.com/FreePeak/LeanKG/internal/store"
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
	case "version":
		fmt.Println("leankg-embed " + Version())
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
  leankg-embed import [--project DIR] [--in FILE] [--model M] [--revision R] [--dims N] [--distance D]
                                                     import vectors; the stamp is the live provider's
  leankg-embed status [--project DIR]                  last run / stamps / element count

Providers (env): LEANKG_EMBED_PROVIDER=local|openai|deterministic (default local).
local spawns the llama.cpp sidecar: LEANKG_EMBED_SIDECAR_CMD (default llama-server),
LEANKG_EMBED_SIDECAR_ARGS (shell-quoted; default serves the pinned bge-small-en-v1.5 GGUF
with --embeddings: -m ~/.leankg/models/bge-small-en-v1.5-f16.gguf when that file exists,
otherwise -hf CompendiumLabs/bge-small-en-v1.5-gguf:f16 which self-downloads on first use),
LEANKG_EMBED_SIDECAR_PORT (default 8080), LEANKG_EMBED_SIDECAR_READY_SECS (default 120).
Attach instead with LEANKG_EMBED_BASE_URL.
Remote: LEANKG_EMBED_BASE_URL, LEANKG_EMBED_API_KEY, LEANKG_EMBED_MODEL,
LEANKG_EMBED_DIMS, LEANKG_EMBED_REVISION.
LEANKG_EMBED_REQUEST_TIMEOUT_SECS (default 120) bounds one embeddings request; a hung endpoint fails the run.

import stamps the collection exactly as the live provider would for the same
LEANKG_EMBED_* environment (catalog pin, prefixes, width), so a later
'leankg-embed run' sees no drift. --model, --revision, --dims and --distance are
optional: unset takes the environment's value; a value that differs from it is
an error. A catalogued model needs no running sidecar. A line's vector may be
the model's native width (reduced to the configured width) or the configured
width. A line's content_hash, when present, is the state recorded for it.
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

func openStore(project string) (store.Backend, error) {
	dir := projectDir(project)
	st, err := store.OpenBackend(context.Background(), dir,
		envOr("LEANKG_DB_ENGINE", "sqlite"), os.Getenv("LEANKG_PG_URL"), "", store.RW)
	if err != nil {
		return nil, err
	}
	if err := st.Migrate(); err != nil {
		st.Close()
		return nil, err
	}
	return st, nil
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// bindPositional parses args into fs and binds a bare positional as the
// project dir (matching `leankg index <dir>`). Go's flag package stops at the
// first non-flag arg, so flags after the positional were silently dropped
// (`run <dir> --project X` ignored X). parseArgs re-parses the remainder, so
// flags work before, between and after the positional. A positional that
// conflicts with --project, or a second positional, is an error, never a
// silent drop.
func bindPositional(verb string, fs *flag.FlagSet, args []string, project *string) error {
	positional, err := parseArgs(fs, args)
	if err != nil {
		return fmt.Errorf("%s: %w", verb, err)
	}
	if len(positional) > 1 {
		return fmt.Errorf("%s: too many arguments (%s)", verb, strings.Join(positional, " "))
	}
	if len(positional) == 1 {
		if *project != "" && *project != positional[0] {
			return fmt.Errorf("%s: positional %q conflicts with --project %q", verb, positional[0], *project)
		}
		*project = positional[0]
	}
	return nil
}

// parseArgs parses args into fs and returns the positionals in order. After
// each positional it re-parses the remainder, so a flag may follow it; the
// flag package alone would stop at the first positional.
func parseArgs(fs *flag.FlagSet, args []string) ([]string, error) {
	var positional []string
	rest := args
	for {
		if err := fs.Parse(rest); err != nil {
			return nil, err
		}
		rest = fs.Args()
		if len(rest) == 0 {
			return positional, nil
		}
		positional = append(positional, rest[0])
		rest = rest[1:]
	}
}

// lock takes the single-flight flock for the PROJECT dir (not the process
// cwd): concurrent invocations against one project serialize; abandoned
// locks release with the process.
//
// The lock is keyed on the STORE, not on <project>/.leankg: a standalone store
// (FR-P2) has no .leankg dir, and the lock must not create one — that is what
// made `leankg-embed run` fail with "open .../.leankg/embed.lock: no such
// file or directory" the moment the store moved out of the project.
func lock(dir string) (func(), error) {
	lockDir := filepath.Dir(store.StandaloneDBPath(dir))
	if lockDir == "" || lockDir == "." {
		lockDir = filepath.Join(dir, ".leankg")
	}
	if err := os.MkdirAll(lockDir, 0o755); err != nil {
		return nil, err
	}
	lockPath := filepath.Join(lockDir, "embed.lock")
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, fmt.Errorf("another leankg-embed run holds %s (single-flight per project)", lockPath)
	}
	// Stamp the owning PID as bookkeeping (mutual exclusion is the flock
	// itself): `cat embed.lock` and `doctor --deep` can then name the holder.
	_ = f.Truncate(0)
	_, _ = fmt.Fprintf(f, "%d\n", os.Getpid())
	return func() { _ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN); f.Close() }, nil
}

func cmdRun(args []string, mode string) {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	err := runEmbed(ctx, args, mode)
	stop()
	if err != nil {
		log.Fatal(err)
	}
}

// runEmbed performs one run. It returns every error instead of exiting, so the
// deferred sidecar release, store close and lock release all run before the
// process exits; a log.Fatal after the sidecar starts orphaned it. A run
// stopped through ctx returns context.Canceled.
func runEmbed(ctx context.Context, args []string, mode string) error {
	fs := flag.NewFlagSet(mode, flag.ExitOnError)
	project := fs.String("project", "", "project directory (default cwd)")
	if err := bindPositional(mode, fs, args, project); err != nil {
		return err
	}
	unlock, err := lock(projectDir(*project))
	if err != nil {
		return err
	}
	defer unlock()
	st, err := openStore(*project)
	if err != nil {
		return fmt.Errorf("store: %w", err)
	}
	defer st.Close()
	p, release, err := embed.StartProvider(ctx)
	if err != nil {
		return fmt.Errorf("provider: %w", err)
	}
	defer release()
	report, err := embed.Run(ctx, st, p, mode)
	if err != nil {
		return fmt.Errorf("run: %w (status recorded)", err)
	}
	for _, note := range report.Notes {
		fmt.Fprintln(os.Stderr, note)
	}
	b, _ := json.Marshal(report)
	fmt.Println(string(b))
	return nil
}

func cmdExport(args []string) {
	fs := flag.NewFlagSet("export", flag.ExitOnError)
	project := fs.String("project", "", "project directory (default cwd)")
	out := fs.String("out", "", "output file (default stdout)")
	modelID := fs.String("model", "", "model to export (required)")
	if err := bindPositional("export", fs, args, project); err != nil {
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
	modelID := fs.String("model", "", "model id (default LEANKG_EMBED_MODEL; must agree with it)")
	revision := fs.String("revision", "", "model revision (default: the live provider's pin; must agree)")
	dims := fs.Int("dims", 0, "stored vector width (default: the live provider's width; must agree)")
	distance := fs.String("distance", "", "distance metric (default cosine; must agree)")
	if err := bindPositional("import", fs, args, project); err != nil {
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
	if err := bindPositional("status", fs, args, project); err != nil {
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
