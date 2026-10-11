package main

import (
	"context"

	"github.com/FreePeak/LeanKG/internal/projectcfg"
	"github.com/FreePeak/LeanKG/internal/store"
)

// openStore opens the store for one project directory, resolving the
// standalone path (FR-P2): an explicit dbPath wins, else the configured
// standalone path (LEANKG_DB_PATH or leankg.yaml db.standalone_db_path), else
// the project-scoped <project>/.leankg/leankg.db.
//
// This is the ONE place that resolution happens. Before it existed, `serve`
// resolved the standalone path and ten other verbs passed a bare "" — so
// `LEANKG_DB_PATH=<p> leankg index .` followed by a serve against the same path
// split the corpus in two, with every verb but serve silently reading an empty
// store. One guard per caller would have been a smaller diff and a bigger
// maintenance cost.
func openStore(ctx context.Context, dir, dbPath string, mode store.Mode) (store.Backend, error) {
	if dbPath == "" {
		dbPath = projectcfg.StandaloneDBPath(dir)
	}
	return store.OpenBackend(ctx, dir,
		envOr("LEANKG_DB_ENGINE", "sqlite"), projectcfg.PGURL(dir), dbPath, mode)
}
