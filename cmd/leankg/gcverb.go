package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/FreePeak/LeanKG/internal/store"
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
	// --keep-model names the collection to KEEP; every other collection is
	// dropped. Without it every collection is kept (the pre-existing
	// behavior). The live collection is the one the query path searches —
	// the embedder's model id.
	var keepModels stringList
	fs.Var(&keepModels, "keep-model", "model id to keep (repeatable); without it every collection is kept")
	positional := parseInterspersed("gc", fs, args, 1)
	if len(positional) == 1 {
		*project = positional[0]
	}
	eng := *engine
	if eng == "" {
		eng = os.Getenv("LEANKG_DB_ENGINE")
	}
	st, err := openStore(context.Background(), *project, "", store.RW)
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
	v, err := st.DeleteOrphanVectors()
	if err != nil {
		fatalText(fmt.Errorf("gc: vectors: %w", err))
	}
	var stale store.StaleStampSweep
	if len(keepModels) > 0 {
		stale, err = st.DeleteStaleStamps(keepModels)
		if err != nil {
			fatalText(fmt.Errorf("gc: stale stamps: %w", err))
		}
	}
	if n > 0 || v > 0 || !stale.Empty() {
		// Same contract as every other CLI writer (index/pull/summarize):
		// the purge bumped the watermark, and the freshness comparison reads
		// the inventory snapshot — without this refresh a just-gc'd project
		// sits at possibly_stale until some later write (the bug #401's own
		// live run exposed: fleet leg read "leankg: current, possibly_stale").
		if _, ierr := store.RefreshInventory(st); ierr != nil {
			fmt.Fprintf(os.Stderr, "gc: inventory snapshot: %v\n", ierr)
		}
	}
	fmt.Printf("gc: purged %d orphan relationship(s), %d orphan vector(s) from %s\n", n, v, *project)
	if !stale.Empty() {
		fmt.Printf("gc: dropped %d stale collection(s) (%d vectors, %d state rows)\n",
			stale.Models, stale.Vectors, stale.State)
	}
}

// stringList is a repeatable string flag value.
type stringList []string

func (s *stringList) String() string { return strings.Join(*s, ",") }

func (s *stringList) Set(v string) error {
	*s = append(*s, v)
	return nil
}
