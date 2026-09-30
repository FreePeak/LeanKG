package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/FreePeak/LeanKG/internal/maintain"
	"github.com/FreePeak/LeanKG/internal/projectcfg"
	"github.com/FreePeak/LeanKG/internal/store"
)

// cmdVacuum is the one-shot form of the maintenance pass internal/maintain
// runs on a ticker inside `leankg serve` and `leankg writer`.
//
// The periodic pass is the right answer for a long-lived server; this verb is
// for everything else — a one-shot CLI invocation, a cron entry, a CI job
// after a bulk index, or an operator who just ran `leankg gc` and wants the
// space back now instead of in an hour.
//
// --full escalates from the bounded incremental reclaim to a whole-file
// VACUUM, which returns every free page in one pass at the cost of holding the
// write lock for the whole rewrite. The default is deliberately the gentle
// one: `leankg vacuum` must be safe to run against a store a reader is
// serving.
func cmdVacuum(args []string) {
	fs := flag.NewFlagSet("vacuum", flag.ExitOnError)
	project := fs.String("project", ".", "project directory whose store is compacted (default cwd)")
	engine := fs.String("engine", "", "storage engine: sqlite|postgres (default: LEANKG_DB_ENGINE, else sqlite)")
	full := fs.Bool("full", false, "rewrite the whole store (whole-file VACUUM) instead of a bounded incremental reclaim")
	maxPages := fs.Int("max-pages", maintain.DefaultMaxPages, "free pages a bounded incremental pass returns (ignored by --full)")
	quiet := fs.Bool("quiet", false, "print only the bytes reclaimed")
	positional := parseInterspersed("vacuum", fs, args, 1)
	if len(positional) == 1 {
		*project = positional[0]
	}
	eng := *engine
	if eng == "" {
		eng = os.Getenv("LEANKG_DB_ENGINE")
	}
	st, err := store.OpenBackend(context.Background(), *project,
		eng, projectcfg.PGURL(*project), "", store.RW)
	if err != nil {
		fatalText(fmt.Errorf("vacuum: open store: %w", err))
	}
	defer st.Close()
	if err := st.Migrate(); err != nil {
		fatalText(fmt.Errorf("vacuum: migrate: %w", err))
	}

	if *full {
		before, err := st.Space()
		if err != nil {
			fatalText(fmt.Errorf("vacuum: %w", err))
		}
		if err := st.Vacuum(); err != nil {
			fatalText(fmt.Errorf("vacuum: %w", err))
		}
		if err := st.Checkpoint(); err != nil {
			fmt.Fprintf(os.Stderr, "vacuum: checkpoint: %v\n", err)
		}
		after, err := st.Space()
		if err != nil {
			fatalText(fmt.Errorf("vacuum: %w", err))
		}
		if *quiet {
			fmt.Printf("%d\n", before.SizeBytes-after.SizeBytes)
			return
		}
		fmt.Printf("vacuum: %s full rewrite %d -> %d bytes (freed %d) on %s\n",
			st.Engine(), before.SizeBytes, after.SizeBytes,
			before.SizeBytes-after.SizeBytes, before.Path)
		return
	}

	res, err := maintain.Pass(st, maintain.Options{
		MaxPages:     *maxPages,
		MinFreeBytes: 0,
		Logf: func(format string, a ...any) {
			if !*quiet {
				fmt.Fprintf(os.Stderr, format+"\n", a...)
			}
		},
	})
	if err != nil {
		fatalText(fmt.Errorf("vacuum: %w", err))
	}
	if *quiet {
		fmt.Printf("%d\n", res.Reclaimed)
		return
	}
	fmt.Printf("vacuum: %s reclaimed %d bytes (%d -> %d, freelist %d -> %d, wal %d)\n",
		res.Engine, res.Reclaimed, res.Before, res.After, res.FreeBefore, res.FreeAfter, res.WALBytes)
}
