package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/FreePeak/LeanKG/go/internal/projectcfg"
	"github.com/FreePeak/LeanKG/go/internal/refresh"
	"github.com/FreePeak/LeanKG/go/internal/registry"
	"github.com/FreePeak/LeanKG/go/internal/setup"
	"github.com/FreePeak/LeanKG/go/internal/setupcfg"
	"github.com/FreePeak/LeanKG/go/internal/store"
)

// cmdSetup is the FR-ZCP-13 first-run setup contract CLI half: no stage flag
// (or --status) reports the resolved repo list; --clone/--index/--embed run
// the pipeline; --reset clears the stored auto/manual choice so the next
// registration re-asks. (The Rust `leankg add` verb is superseded by
// `leankg index` in the Go engine — the Rust `add`-side mode resolution and
// summary belongs on the registration verb, hunk 6.)
func cmdSetup(args []string) {
	fs := flag.NewFlagSet("setup", flag.ExitOnError)
	reset := fs.Bool("reset", false, "clear the stored setup choice so the next registration re-asks")
	clone := fs.Bool("clone", false, "clone the LEANKG_REPOS list into LEANKG_CLONE_ROOT")
	index := fs.Bool("index", false, "run a full index per repo dir")
	embed := fs.Bool("embed", false, "run the embedding build per repo dir")
	status := fs.Bool("status", false, "report the resolved repo list without running")
	parseInterspersed("setup", fs, args, 0)

	if *reset {
		root := projectcfg.FindProjectRoot(".")
		cleared, err := setupcfg.ResetSetupChoice(root)
		if err != nil {
			log.Fatalf("setup --reset: %v", err)
		}
		if cleared {
			fmt.Printf("Cleared stored setup choice in %s; the next `leankg index` re-asks.\n", setupcfg.PathFor(root))
		} else {
			fmt.Printf("No setup choice stored at %s — nothing to reset.\n", setupcfg.PathFor(root))
		}
		if !*clone && !*index && !*embed && !*status {
			return
		}
	}

	res, err := setup.RunSetup(context.Background(), setup.Options{
		Clone: *clone, Index: *index, Embed: *embed, Status: *status,
		Env:    os.Getenv("LEANKG_ENV"),
		Stages: cliStages{},
		Logf:   log.Printf,
	})
	if err != nil {
		log.Fatalf("setup: %v", err)
	}

	// An empty resolution used to print an empty table — indistinguishable from
	// success. The report is not an error (exit 0), but it must say so and name
	// the knobs that fill it (internal/setup's ResolveRepos reads exactly these
	// three, in this precedence order).
	if (*status || !(*clone || *index || *embed)) && len(res.Specs) == 0 {
		fmt.Println("No repositories resolved. Set LEANKG_WORKSPACE_DIR, LEANKG_PROJECT_DIRS or LEANKG_REPOS, then re-run.")
	}
	// Registry bookkeeping the pipeline deliberately left to the caller.
	for _, repo := range res.IndexedRepos {
		if err := registry.Register(repo.Name, repo.Dir); err != nil {
			log.Printf("setup: register %s: %v", repo.Name, err)
		}
	}
}

// cliStages adapts the existing CLI flows to setup.Stages. IndexOne reuses
// runIndex verbatim (identical to `leankg index <dir>`); EmbedOne mirrors
// the refresh verb's embed stage.
type cliStages struct{}

func (cliStages) IndexOne(_ context.Context, dir, _env string, _verbose bool) (int, error) {
	if err := runIndex("", dir, "", "", ""); err != nil {
		return 0, err
	}
	st, err := store.Open(filepath.Join(dir, ".leankg", "leankg.db"), store.RO)
	if err != nil {
		return 0, nil // indexed, but the count is unavailable
	}
	defer st.Close()
	n, err := st.ElementCount()
	if err != nil {
		return 0, nil
	}
	return n, nil
}

func (cliStages) EmbedOne(ctx context.Context, dir string) error {
	res, err := refresh.Run(ctx, refresh.Options{Project: dir, Path: dir})
	if err != nil {
		return err
	}
	if res.EmbedSkipped != "" {
		// Rust parity: an unavailable embedder is a reported skip, not a
		// failed pipeline — the preference is stored, embeddings arrive later.
		return fmt.Errorf("embed skipped: %s", res.EmbedSkipped)
	}
	return nil
}
