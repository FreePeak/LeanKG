package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/FreePeak/LeanKG/internal/projectcfg"
	"github.com/FreePeak/LeanKG/internal/store"
)

// verbEngine reports the engine the next store open will use — the same
// expression cmd/leankg/store.go's openStore resolves — so the checks below
// always ask about the store the verb is ABOUT to open, never about a
// different one.
func verbEngine() string {
	return envOr("LEANKG_DB_ENGINE", store.EngineSQLite)
}

// requireStore refuses a verb whose project has no store, before the verb
// opens one.
//
// Opening a MISSING store read-write CREATES it (file, schema and all). So a
// verb that opens without asking materialises a store for a project nobody
// indexed — and a stray <dir>/.leankg is enough to make every project-root
// walk above it latch onto the wrong project
// (internal/projectcfg.FindProjectRoot walks up and takes the first ancestor
// that owns a .leankg entry or a leankg.yaml).
//
// The refusal is the diagnosis `leankg doctor` prints for the same state, and
// exits with doctor's FAIL code (2).
func requireStore(verb, dir string) {
	eng := verbEngine()
	if verbStoreExists(context.Background(), dir, eng) {
		return
	}
	refuseNotIndexed(verb, dir, eng, storeHint(verb))
}

// storeHint is the recovery sentence for each refusing verb: the command that
// creates the store. A refusal the caller cannot act on is not a diagnosis.
func storeHint(verb string) string {
	switch verb {
	case "migrate":
		return "run `leankg index .` in this project first — it creates the store and applies every migration, so `leankg migrate` then reports them up to date"
	default:
		return fmt.Sprintf("run `leankg index .` in this project, then `leankg %s` again", verb)
	}
}

// verbStoreExists reports whether the project at dir already HAS a store.
//
// It is the shared guard (maintenanceStoreExists, cmd/leankg/main.go) — the
// CLI's one definition of "this project has a store", so the maintenance
// fleet and the verbs can never disagree about a project.
//
// Two resolutions the shared guard does not model are answered here, because
// they ask about a store that is NOT at <dir>/.leankg/leankg.db:
//
//   - A standalone store (FR-P2 — LEANKG_DB_PATH / leankg.yaml
//     db.standalone_db_path) is the configured FILE, and the shared guard's
//     stat of the project-scoped path would report a project that IS indexed
//     as unindexed.
//   - The postgres DSN a verb opens is the PROJECT's (projectcfg.PGURL: the
//     environment first, then leankg.yaml db.url), while the shared guard
//     probes with the environment alone — so a project whose DSN lives only
//     in its config would read as unindexed.
//
// A postgres probe that fails for any reason OTHER than a missing schema
// (a bad DSN, a refused connection, an auth failure) is not a verdict about
// the project: it is the verb's own open that must report it, so this returns
// true and lets the open fail with the real error.
func verbStoreExists(ctx context.Context, dir, engine string) bool {
	if engine == store.EngineSQLite {
		if p := projectcfg.StandaloneDBPath(dir); p != "" {
			_, err := os.Stat(p)
			return err == nil
		}
		return maintenanceStoreExists(ctx, dir, engine)
	}
	probe, err := store.OpenBackend(ctx, dir, engine, projectcfg.PGURL(dir), "", store.RO)
	if err != nil {
		return !errors.Is(err, store.ErrSchemaMissing)
	}
	_ = probe.Close()
	return true
}

// verbStoreSubject names the store a verb would open for dir: the file for
// sqlite, the project schema for postgres. A refusal that names the store it
// looked for is one the caller can act on.
func verbStoreSubject(dir, engine string) string {
	if engine == store.EngineSQLite {
		if p := projectcfg.StandaloneDBPath(dir); p != "" {
			return p
		}
		return filepath.Join(dir, ".leankg", "leankg.db")
	}
	return fmt.Sprintf("%s (postgres schema)", dir)
}

// refuseNotIndexed refuses a verb whose project has no store, in the shape
// `leankg doctor` reports the same state, and exits with doctor's FAIL code
// (2).
//
// The message names the store that is missing AND the command that creates
// it, because a refusal the caller cannot act on is not a diagnosis.
//
// It goes to stderr because these verbs print their payload on stdout —
// doctor's own report IS its stdout, but a metrics summary, a dashboard table
// and a migrate result are all stdout contracts, and a text line in the
// middle of one corrupts it.
func refuseNotIndexed(verb, dir, engine, hint string) {
	fmt.Fprintf(os.Stderr, "FAIL not indexed: no store at %s (project %s)\n",
		verbStoreSubject(dir, engine), dir)
	fmt.Fprintf(os.Stderr, "     %s\n", hint)
	os.Exit(2)
}
