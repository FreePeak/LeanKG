package main

import (
	"context"
	"path/filepath"

	"github.com/FreePeak/LeanKG/internal/projectcfg"
	"github.com/FreePeak/LeanKG/internal/store"
)

// openStore opens the store for one project directory, resolving the
// standalone path (FR-P2): an explicit dbPath wins, else the configured
// standalone path (LEANKG_DB_PATH or leankg.yaml db.standalone_db_path), else
// the project-scoped <project>/.leankg/leankg.db. The engine is
// LEANKG_DB_ENGINE.
func openStore(ctx context.Context, dir, dbPath string, mode store.Mode) (store.Backend, error) {
	return openStoreWith(ctx, dir, "", dbPath, mode)
}

// openStoreWith is the ONE place a cmd/leankg store is opened. An explicit
// engine (a --engine flag) wins; "" takes LEANKG_DB_ENGINE, else sqlite. An
// explicit dbPath (a --db flag) wins; "" takes the standalone path. The
// PostgreSQL DSN is projectcfg.PGURL(dir) in every case, so the flag, the
// environment and leankg.yaml resolve through the same precedence.
//
// Before this existed, `serve` and `writer` opened their stores through
// different calls: the writer ignored LEANKG_DB_PATH, the leankg.yaml anchor
// and the PostgreSQL engine, and gc and vacuum read --engine and then dropped it.
func openStoreWith(ctx context.Context, dir, engine, dbPath string, mode store.Mode) (store.Backend, error) {
	if engine == "" {
		engine = envOr("LEANKG_DB_ENGINE", "sqlite")
	}
	if dbPath == "" {
		dbPath = projectcfg.StandaloneDBPath(dir)
	}
	return store.OpenBackend(ctx, dir, engine, projectcfg.PGURL(dir), dbPath, mode)
}

// anchorProjectDir applies the leankg.yaml project.project_path / project.root
// anchor that serve applies before it opens its store, so the writer and serve
// resolve the same project directory. A standalone store names its own file
// (LEANKG_DB_PATH or leankg.yaml db.standalone_db_path), so its directory is
// not anchored.
func anchorProjectDir(dir string) string {
	if projectcfg.StandaloneDBPath(dir) != "" {
		return dir
	}
	if dbDir := filepath.Join(dir, ".leankg"); projectcfg.ResolveProjectDBDir(dbDir) != dbDir {
		return filepath.Dir(projectcfg.ResolveProjectRoot(dbDir))
	}
	return dir
}
