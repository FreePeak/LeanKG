// Portfolio registry verbs (issue #376): `leankg register-project` and
// `leankg projects`. The policy lives in internal/portfolioreg; these two
// commands are the CLI surface over it, and `leankg index` additionally
// registers a project on completion (helpers.go, report-only hunk).
//
// Both verbs resolve the registry the way `serve` resolves its store —
// --engine, then LEANKG_DB_ENGINE, then sqlite; LEANKG_PG_URL for the shared
// Postgres — so the CLI writes to the registry the server actually reads.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/FreePeak/LeanKG/internal/portfolioreg"
	"github.com/FreePeak/LeanKG/internal/projectcfg"
	"github.com/FreePeak/LeanKG/internal/store"
)

// portfolioOptions builds the registry location for a CLI verb: the deployment
// engine selection, with the dsn resolved the way every other verb resolves it.
func portfolioOptions(engineFlag, dir string) portfolioreg.Options {
	o := portfolioreg.Options{Engine: engineFlag, PGURL: projectcfg.PGURL(dir)}
	if o.Engine == "" {
		o.Engine = envOr("LEANKG_DB_ENGINE", "")
	}
	return o
}

// cmdRegisterProject implements `leankg register-project <dir> [--name N]`.
// The element/file counts and the last_indexed stamp come from the project's
// own store when it has one; a directory that was never indexed is still
// registrable — it appears in the manifest with no stamp and no counts, which
// is exactly what "registered, not indexed" should look like.
func cmdRegisterProject(args []string) {
	fs := flag.NewFlagSet("register-project", flag.ExitOnError)
	name := fs.String("name", "", "display name (default: the directory base name)")
	project := fs.String("project", "", "project whose registry is written (default cwd, or LEANKG_PROJECT)")
	engine := fs.String("engine", "", "registry engine: sqlite|postgres (default: LEANKG_DB_ENGINE, else sqlite)")
	positional := parseInterspersed("register-project", fs, args, 1)
	if len(positional) != 1 {
		logUsageErr("usage: leankg register-project <dir> [--name N] [--project DIR] [--engine sqlite|postgres]")
	}
	target := positional[0]

	ctx := context.Background()
	opts := portfolioOptions(*engine, resolveProjectDir(*project))

	var elements, files int
	var indexedAt *time.Time
	if st, err := store.OpenBackend(ctx, target, opts.Engine, opts.PGURL, "", store.RO); err == nil {
		// A readable store means this project HAS been indexed: stamp it.
		elements, _ = st.ElementCount()
		files, _ = st.FileCount()
		now := time.Now()
		indexedAt = &now
		_ = st.Close()
	}

	rec, err := portfolioreg.Register(ctx, opts, target, *name, elements, files, indexedAt)
	if err != nil {
		fatalJSON(err)
	}
	stamp := "never indexed"
	if rec.LastIndexed != nil {
		stamp = *rec.LastIndexed
	}
	fmt.Printf("registered %s (%s): elements=%d files=%d last_indexed=%s\n",
		rec.Name, rec.Dir, rec.ElementCount, rec.FileCount, stamp)
	if indexedAt == nil {
		fmt.Fprintf(os.Stderr, "note: %s has no readable store yet — run `leankg index %s` and it will be stamped.\n", target, target)
	}
}

// cmdProjects implements `leankg projects`, the T0 manifest: one row per
// registered project with the counts the registry last stamped and whether the
// project's store is on disk. It opens NO project store and indexes nothing —
// that is what makes it safe on a portfolio of eighty repos. `--forget DIR`
// removes a registration instead of listing.
func cmdProjects(args []string) {
	fs := flag.NewFlagSet("projects", flag.ExitOnError)
	project := fs.String("project", "", "project whose registry is read (default cwd, or LEANKG_PROJECT)")
	engine := fs.String("engine", "", "registry engine: sqlite|postgres (default: LEANKG_DB_ENGINE, else sqlite)")
	jsonOut := fs.Bool("json", false, "print the manifest as JSON")
	fs.BoolVar(jsonOut, "j", false, "shorthand for --json")
	forget := fs.String("forget", "", "remove DIR from the registry instead of listing")
	parseInterspersed("projects", fs, args, 0)

	ctx := context.Background()
	opts := portfolioOptions(*engine, resolveProjectDir(*project))

	if *forget != "" {
		gone, err := portfolioreg.Forget(ctx, opts, *forget)
		if err != nil {
			fatalJSON(err)
		}
		if !gone {
			fmt.Fprintf(os.Stderr, "not registered: %s\n", *forget)
			os.Exit(1)
		}
		fmt.Printf("forgot %s\n", *forget)
		return
	}

	hot := portfolioreg.NewHotSet(0)
	entries, err := portfolioreg.Manifest(ctx, opts, hot)
	if err != nil {
		if errors.Is(err, portfolioreg.ErrNoRegistry) {
			// No registry is a normal state for a single-repo deployment, and
			// an empty listing is the honest answer.
			if *jsonOut {
				printJSON(map[string]any{"projects": []any{}, "count": 0, "hot_limit": hot.Limit()})
				return
			}
			fmt.Println("no projects registered (no portfolio registry at this location)")
			return
		}
		fatalJSON(err)
	}
	if *jsonOut {
		printJSON(map[string]any{
			"projects": entries, "count": len(entries),
			"hot_limit": hot.Limit(), "max_repos_env": portfolioreg.MaxReposEnv,
		})
		return
	}
	if len(entries) == 0 {
		fmt.Println("no projects registered")
		return
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "PROJECT\tDIR\tELEMENTS\tFILES\tLAST INDEXED\tSTORE\tHOT")
	for _, e := range entries {
		stamp := "never"
		if e.LastIndexed != nil {
			stamp = *e.LastIndexed
		}
		presence := "missing"
		if e.StoreOnDisk {
			presence = "on-disk"
		}
		hotMark := "-"
		if e.Hot {
			hotMark = fmt.Sprintf("hot (%d)", hot.Limit())
		}
		fmt.Fprintf(w, "%s\t%s\t%d\t%d\t%s\t%s\t%s\n",
			e.Project, e.Dir, e.Elements, e.Files, stamp, presence, hotMark)
	}
	_ = w.Flush()
	if len(entries) > hot.Limit() {
		fmt.Printf("\n%d of %d projects are in the hot set (%s=%d); the rest stay registered and are listed as not-hot by portfolio queries.\n",
			hot.Limit(), len(entries), portfolioreg.MaxReposEnv, hot.Limit())
	}
}

// logUsageErr reports a CLI misuse without the log package's timestamp, which
// would corrupt machine-readable output (the same contract run.go's parser
// keeps).
func logUsageErr(msg string) {
	fmt.Fprintln(os.Stderr, strings.TrimSpace(msg))
	os.Exit(2)
}
