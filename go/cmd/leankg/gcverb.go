package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/FreePeak/LeanKG/go/internal/projectcfg"
	"github.com/FreePeak/LeanKG/go/internal/store"
)

// cmdGC purges orphaned relationship edges — the repair `doctor --deep`
// names for the documented reconcile ceiling: Store.DeleteByFile removes
// edges SOURCED by a file's elements, but edges pointing INTO a deleted file
// from other files survive until those sources are re-indexed, which
// incremental index never revisits (internal/index package doc). A mass
// deletion (the v4.10.1 hygiene sweep) therefore leaves dangling edges that
// break graph traversals until gc runs.
func cmdGC(args []string) {
	fs := flag.NewFlagSet("gc", flag.ExitOnError)
	project := fs.String("project", ".", "project directory whose store is purged (default cwd)")
	engine := fs.String("engine", "", "storage engine: sqlite|postgres (default: LEANKG_DB_ENGINE, else sqlite)")
	positional := parseInterspersed("gc", fs, args, 1)
	if len(positional) == 1 {
		*project = positional[0]
	}
	eng := *engine
	if eng == "" {
		eng = os.Getenv("LEANKG_DB_ENGINE")
	}
	st, err := store.OpenBackend(context.Background(), *project,
		eng, projectcfg.PGURL(*project), store.RW)
	if err != nil {
		fatalText(fmt.Errorf("gc: open store: %w", err))
	}
	defer st.Close()
	if err := st.Migrate(); err != nil {
		fatalText(fmt.Errorf("gc: migrate: %w", err))
	}
	n, err := st.DeleteOrphanRelationships()
	if err != nil {
		fatalText(fmt.Errorf("gc: %w", err))
	}
	fmt.Printf("gc: purged %d orphan relationship(s) from %s\n", n, *project)
}
