package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/FreePeak/LeanKG/go/internal/projectcfg"
	"github.com/FreePeak/LeanKG/go/internal/store"
	"github.com/FreePeak/LeanKG/go/internal/summarize"
)

// cmdSummarize runs graft's two-pass LLM-meaning pipeline over an
// already-indexed project (issue #297): per-file prose summaries, then a
// curated system/file/concept node set written as graph elements plus markdown
// nodes under <project>/.leankg/summarize/.
//
// It is explicit on purpose — the meaning tier spends tokens, so nothing here
// happens implicitly (see summarize.AfterIndexEnv for the opt-in index hook).
// Exit code 1 covers both "the run could not start" (no LEANKG_LLM_MODEL, an
// unreadable store) and a fatal gate reason: a quota-dead provider must never
// look like a green run (graft #127). A completed run with a few failed files
// prints them and exits 0 — the tier is partial but the pass did its job.
func cmdSummarize(args []string) {
	fs := flag.NewFlagSet("summarize", flag.ExitOnError)
	project := fs.String("project", ".", "project root whose .leankg store receives the meaning tier")
	force := fs.Bool("force", false, "ignore the content-hash resume and re-summarize every indexed file")
	dryRun := fs.Bool("dry-run", false, "report what the run would spend without calling the provider or writing anything")
	concurrency := fs.Int("concurrency", summarize.Concurrency, "how many files pass 1 summarizes at once")
	parseInterspersed("summarize", fs, args, 0)

	dir := *project
	if dir == "" {
		dir = "."
	}
	ctx := context.Background()
	st, err := store.OpenBackend(ctx, dir, os.Getenv("LEANKG_DB_ENGINE"), projectcfg.PGURL(dir), store.RW)
	if err != nil {
		fmt.Fprintf(os.Stderr, "summarize: open store: %v\n", err)
		os.Exit(1)
	}
	defer st.Close()
	if err := st.Migrate(); err != nil {
		fmt.Fprintf(os.Stderr, "summarize: migrate: %v\n", err)
		os.Exit(1)
	}

	res, err := summarize.Run(ctx, st, summarize.Options{
		ProjectDir:  dir,
		Force:       *force,
		DryRun:      *dryRun,
		Concurrency: *concurrency,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "summarize: %v\n", err)
		os.Exit(1)
	}
	fmt.Println(res.Summary())
	const shownErrors = 10
	for i, e := range res.Errors {
		if i == shownErrors {
			fmt.Fprintf(os.Stderr, "  … and %d more failure(s)\n", len(res.Errors)-i)
			break
		}
		fmt.Fprintf(os.Stderr, "  failed: %s\n", e)
	}
	// The pass writes elements and relationships; refresh the snapshot so
	// status/doctor don't call the project stale after a successful run.
	if !*dryRun {
		if _, ierr := store.RefreshInventory(st); ierr != nil {
			fmt.Fprintf(os.Stderr, "summarize: inventory snapshot: %v\n", ierr)
		}
	}
	if res.Fatal != "" {
		os.Exit(1)
	}
}
