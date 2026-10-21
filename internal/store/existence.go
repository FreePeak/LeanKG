package store

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// SQLitePath resolves the SQLite store path for a project directory — the
// SAME resolution OpenBackend uses (the standalone override first, then the
// project's own .leankg/leankg.db). A caller that wants to talk about a
// project's store asks here, so the path it names is the path that is
// actually opened.
func SQLitePath(projectDir string) string {
	if p := StandaloneDBPath(projectDir); p != "" {
		return p
	}
	return filepath.Join(projectDir, ".leankg", "leankg.db")
}

// Exists reports whether the project's store is ALREADY THERE.
//
// It answers the question a READ has to ask before it opens anything:
// is there a store to read? Opening a missing store read-write CREATES it —
// the file, the schema, every table — so a server that opens first and asks
// later turns the first read of an unindexed project into a write. That is
// the same rule the maintenance fleet already follows (it skips a project
// with no store) and the same rule the CLI verbs now refuse on.
//
// The check never writes: a sqlite store is stat()ed, a postgres project is
// probed read-only (a schema that does not exist yet is reported as "no
// store", not as an error).
func Exists(ctx context.Context, projectDir, engine, pgURL string) (bool, error) {
	switch engine {
	case "", EngineSQLite:
		path := SQLitePath(projectDir)
		if _, err := os.Stat(path); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return false, nil
			}
			return false, err
		}
		return true, nil
	case EnginePostgres:
		if pgURL == "" {
			return false, errors.New("store: postgres engine requires LEANKG_PG_URL")
		}
		probe, err := OpenPG(ctx, pgURL, projectDir, RO)
		if err != nil {
			if errors.Is(err, ErrSchemaMissing) {
				return false, nil
			}
			return false, err
		}
		_ = probe.Close()
		return true, nil
	default:
		return false, fmt.Errorf("store: unknown engine %q", engine)
	}
}
