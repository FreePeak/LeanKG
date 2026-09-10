package embed

import (
	"context"
	"database/sql"
	"fmt"

	_ "modernc.org/sqlite"
)

// elementText is one embeddable code element: qualified name plus content.
type elementText struct {
	QN      string
	Content string
}

// readElements returns (qualified_name, content) for every indexed element,
// ordered deterministically by qualified_name.
//
// ponytail/store-gap: internal/store has no element-enumeration method
// (no Elements()/DirtyQNCount despite the brief), so the embed pipeline
// opens its own read-only sidecar handle to the same SQLite file. Upgrade
// path: integrator adds store.Elements() and this file collapses to a call.
func readElements(ctx context.Context, dbPath string) ([]elementText, error) {
	db, err := sql.Open("sqlite", "file:"+dbPath+"?_pragma=busy_timeout(10000)&_pragma=query_only(ON)")
	if err != nil {
		return nil, fmt.Errorf("embed: read elements open: %w", err)
	}
	defer db.Close()

	rows, err := db.QueryContext(ctx, `SELECT qualified_name, content FROM code_elements ORDER BY qualified_name`)
	if err != nil {
		return nil, fmt.Errorf("embed: read elements query: %w", err)
	}
	defer rows.Close()

	var out []elementText
	for rows.Next() {
		var e elementText
		if err := rows.Scan(&e.QN, &e.Content); err != nil {
			return nil, fmt.Errorf("embed: read elements scan: %w", err)
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// readVectorCounts reports (covered, orphans) for a model: vector rows whose
// QN matches a live code_elements row, vs. vector rows whose QN no longer
// exists (same store-gap as readElements — sidecar read).
func readVectorCounts(ctx context.Context, dbPath, modelID string) (covered, orphans int, err error) {
	db, err := sql.Open("sqlite", "file:"+dbPath+"?_pragma=busy_timeout(10000)&_pragma=query_only(ON)")
	if err != nil {
		return 0, 0, fmt.Errorf("embed: vector counts open: %w", err)
	}
	defer db.Close()

	err = db.QueryRowContext(ctx, `SELECT
			COALESCE(SUM(CASE WHEN e.qualified_name IS NOT NULL THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN e.qualified_name IS NULL     THEN 1 ELSE 0 END), 0)
		FROM embedding_vectors v
		LEFT JOIN code_elements e ON e.qualified_name = v.qualified_name
		WHERE v.model_id = ?`, modelID).Scan(&covered, &orphans)
	if err != nil {
		return 0, 0, fmt.Errorf("embed: vector counts: %w", err)
	}
	return covered, orphans, nil
}
