package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"sort"

	"github.com/FreePeak/LeanKG/go/internal/store"
)

// migrateStep is one migration in the applied/skipped result.
type migrateStep struct {
	Version int    `json:"version"`
	Name    string `json:"name"`
}

// migrateResult is the machine-readable output of `leankg migrate`: the
// embedded plan plus the ledger diff of this run.
type migrateResult struct {
	Engine          string        `json:"engine"`
	Project         string        `json:"project"`
	Planned         int           `json:"planned"`
	Applied         []migrateStep `json:"applied"`
	UpToDate        []migrateStep `json:"up_to_date"`
	Unknown         []int         `json:"unknown"`
	PendingAfterRun []migrateStep `json:"pending_after_run"`
}

// cmdMigrate applies the embedded schema migrations to a project's store and
// prints the applied/skipped result as JSON (Rust `leankg migrate` parity:
// main.rs CLICommand::Migrate printed "applied: <id>" / "up to date: <id>").
func cmdMigrate(args []string) {
	fs := flag.NewFlagSet("migrate", flag.ExitOnError)
	project := fs.String("project", "", "project directory (default cwd, or LEANKG_PROJECT)")
	engineName := fs.String("engine", "", "storage engine: sqlite (default) | postgres")
	parseInterspersed("migrate", fs, args, 0)
	eng := *engineName
	if eng == "" {
		eng = envOr("LEANKG_DB_ENGINE", "sqlite")
	}
	if eng != store.EngineSQLite && eng != store.EnginePostgres {
		fmt.Fprintf(os.Stderr, "migrate: unknown engine %q (valid: sqlite | postgres)\n", eng)
		os.Exit(2)
	}
	dir := resolveProjectDir(*project)
	st, err := store.OpenBackend(context.Background(), dir, eng,
		os.Getenv("LEANKG_PG_URL"), store.RW)
	if err != nil {
		fatalJSON(err)
	}
	defer st.Close()

	// Pre-state: a never-migrated store has no ledger yet, so any read error
	// here means "nothing applied" — Migrate below still fails loudly if the
	// store is truly unreachable.
	before, beforeErr := store.AppliedMigrations(st)
	beforeSet := map[int]bool{}
	if beforeErr == nil {
		for _, v := range before {
			beforeSet[v] = true
		}
	}

	if err := st.Migrate(); err != nil {
		fatalJSON(err)
	}
	after, err := store.AppliedMigrations(st)
	if err != nil {
		fatalJSON(err)
	}
	afterSet := map[int]bool{}
	for _, v := range after {
		afterSet[v] = true
	}

	plan := store.Migrations()
	res := migrateResult{
		Engine:          eng,
		Project:         dir,
		Planned:         len(plan),
		Applied:         []migrateStep{},
		UpToDate:        []migrateStep{},
		Unknown:         []int{},
		PendingAfterRun: []migrateStep{},
	}
	planSet := map[int]bool{}
	for _, m := range plan {
		planSet[m.Version] = true
		switch {
		case afterSet[m.Version] && !beforeSet[m.Version]:
			res.Applied = append(res.Applied, migrateStep{m.Version, m.Name})
		case afterSet[m.Version]:
			res.UpToDate = append(res.UpToDate, migrateStep{m.Version, m.Name})
		default:
			res.PendingAfterRun = append(res.PendingAfterRun, migrateStep{m.Version, m.Name})
		}
	}
	for _, v := range after {
		if !planSet[v] {
			res.Unknown = append(res.Unknown, v)
		}
	}
	sort.Ints(res.Unknown)

	printJSON(res)
}
