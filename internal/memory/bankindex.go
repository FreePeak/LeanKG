package memory

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// indexVersion versions the derived index.db layout. On open, a different
// stored version rebuilds the whole index from the Markdown files and JSONL
// banks (the sources of truth), so an upgrade never needs a migration.
const indexVersion = "2"

// ensureIndex rebuilds index.db when it was built by another version.
func (m *Memory) ensureIndex() error {
	var v string
	err := m.fts.QueryRow(`SELECT value FROM memory_meta WHERE key = 'index_version'`).Scan(&v)
	if err == nil && v == indexVersion {
		return nil
	}
	if err != nil && err != sql.ErrNoRows {
		return fmt.Errorf("memory: read index version: %w", err)
	}
	return m.rebuildIndex()
}

// rebuildIndex re-derives every index row and cursor from the source files.
func (m *Memory) rebuildIndex() error {
	m.bankMu.Lock()
	defer m.bankMu.Unlock()
	tx, err := m.fts.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	for _, stmt := range []string{`DELETE FROM memory_fts`, `DELETE FROM memory_cursors`} {
		if _, err := tx.Exec(stmt); err != nil {
			return fmt.Errorf("memory: clear index: %w", err)
		}
	}
	keys := []string{"MEMORY.md", "USER.md"}
	if topics, err := os.ReadDir(filepath.Join(m.real, "topics")); err == nil {
		for _, t := range topics {
			if !t.IsDir() && strings.HasSuffix(t.Name(), ".md") {
				keys = append(keys, "topics/"+t.Name())
			}
		}
	}
	for _, key := range keys {
		data, err := os.ReadFile(filepath.Join(m.real, filepath.FromSlash(key)))
		if err != nil {
			continue
		}
		if err := insertFileRows(tx, key, string(data)); err != nil {
			return err
		}
	}
	banks, _ := os.ReadDir(filepath.Join(m.root, "banks"))
	for _, b := range banks {
		if b.IsDir() || !strings.HasSuffix(b.Name(), ".jsonl") {
			continue
		}
		bank := strings.TrimSuffix(b.Name(), ".jsonl")
		entries, err := m.readBankRows(bank)
		if err != nil {
			return err
		}
		if err := insertBankRows(tx, bank, entries); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(`INSERT OR REPLACE INTO memory_meta (key, value) VALUES ('index_version', ?)`, indexVersion); err != nil {
		return err
	}
	return tx.Commit()
}

// insertBankRows indexes entries of one bank and advances the cursors.
func insertBankRows(tx *sql.Tx, bank string, entries []Entry) error {
	bank = sanitizeBank(bank)
	for _, e := range entries {
		doc, err := json.Marshal(e)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT INTO memory_fts(kind, path, bank, entry_id, doc, body) VALUES ('bank', '', ?, ?, ?, ?)`,
			bank, e.ID, string(doc), e.Content); err != nil {
			return fmt.Errorf("memory: index bank row: %w", err)
		}
		turn, ok := intOf(e.Metadata["retained_through_user_turn"])
		if !ok {
			continue
		}
		if err := raiseCursor(tx, "bank", bank, turn); err != nil {
			return err
		}
		if sid, _ := e.Metadata["session_id"].(string); sid != "" {
			if err := raiseCursor(tx, "session", sid, turn); err != nil {
				return err
			}
		}
	}
	return nil
}

func raiseCursor(tx *sql.Tx, kind, key string, v int) error {
	_, err := tx.Exec(`INSERT INTO memory_cursors (kind, key, value) VALUES (?, ?, ?)
		ON CONFLICT (kind, key) DO UPDATE SET value = MAX(value, excluded.value)`, kind, key, v)
	return err
}

// intOf reads a cursor value: int in memory, float64 after a JSON round trip.
func intOf(v any) (int, bool) {
	switch n := v.(type) {
	case int:
		return n, true
	case int64:
		return int(n), true
	case float64:
		return int(n), true
	}
	return 0, false
}

// indexAppendLocked indexes freshly appended rows. Callers hold bankMu.
func (m *Memory) indexAppendLocked(bank string, entries []Entry) error {
	tx, err := m.fts.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err := insertBankRows(tx, bank, entries); err != nil {
		return err
	}
	return tx.Commit()
}

// reindexBankLocked re-derives one bank's rows after a rewrite (document
// replace/delete) and recomputes the cursors it can have changed. Callers
// hold bankMu.
func (m *Memory) reindexBankLocked(bank string) error {
	entries, err := m.readBankRows(bank)
	if err != nil {
		return err
	}
	key := sanitizeBank(bank)
	tx, err := m.fts.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	for _, stmt := range []struct {
		q    string
		args []any
	}{
		{`DELETE FROM memory_fts WHERE kind = 'bank' AND bank = ?`, []any{key}},
		{`DELETE FROM memory_cursors WHERE kind = 'bank' AND key = ?`, []any{key}},
	} {
		if _, err := tx.Exec(stmt.q, stmt.args...); err != nil {
			return err
		}
	}
	if err := insertBankRows(tx, bank, entries); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM memory_vectors WHERE bank = ? AND entry_id NOT IN
		(SELECT entry_id FROM memory_fts WHERE kind = 'bank' AND bank = ?)`, key, key); err != nil {
		return err
	}
	// Session cursors span banks: recompute them all from the indexed rows.
	if _, err := tx.Exec(`DELETE FROM memory_cursors WHERE kind = 'session'`); err != nil {
		return err
	}
	if _, err := tx.Exec(`INSERT INTO memory_cursors (kind, key, value)
		SELECT 'session', json_extract(doc, '$.metadata.session_id'),
		       MAX(CAST(json_extract(doc, '$.metadata.retained_through_user_turn') AS INTEGER))
		FROM memory_fts
		WHERE kind = 'bank'
		  AND json_extract(doc, '$.metadata.session_id') IS NOT NULL
		  AND json_extract(doc, '$.metadata.retained_through_user_turn') IS NOT NULL
		GROUP BY 2`); err != nil {
		return fmt.Errorf("memory: recompute session cursors: %w", err)
	}
	return tx.Commit()
}

// cursor reads one stored cursor.
func (m *Memory) cursor(kind, key string) (int, bool) {
	var v int
	if err := m.fts.QueryRow(`SELECT value FROM memory_cursors WHERE kind = ? AND key = ?`, kind, key).Scan(&v); err != nil {
		return 0, false
	}
	return v, true
}

// recallIndexed ranks bank rows by bm25 over the requested banks. Rows are
// deduped by id in score order (a tie keeps bank order), filtered by keep
// BEFORE the limit (the hindsight tag filter contract), and only rows that
// match at least one query term surface.
func (m *Memory) recallIndexed(banks []string, query string, limit int, keep func(Entry) bool) ([]Entry, error) {
	match := matchQuery(query)
	if match == "" || len(banks) == 0 {
		return nil, nil
	}
	order := make(map[string]int, len(banks))
	args := []any{match}
	marks := make([]string, 0, len(banks))
	for i, b := range banks {
		key := sanitizeBank(b)
		if _, dup := order[key]; !dup {
			order[key] = i
			args = append(args, key)
			marks = append(marks, "?")
		}
	}
	rows, err := m.fts.Query(`SELECT bank, entry_id, doc, bm25(memory_fts) FROM memory_fts
		WHERE memory_fts MATCH ? AND kind = 'bank' AND bank IN (`+strings.Join(marks, ",")+`)`, args...)
	if err != nil {
		return nil, fmt.Errorf("memory: recall: %w", err)
	}
	defer rows.Close()
	type hit struct {
		e     Entry
		score float64
		bank  int
	}
	var hits []hit
	for rows.Next() {
		var bank, id, doc string
		var score float64
		if err := rows.Scan(&bank, &id, &doc, &score); err != nil {
			return nil, err
		}
		var e Entry
		if json.Unmarshal([]byte(doc), &e) != nil {
			continue
		}
		hits = append(hits, hit{e: e, score: score, bank: order[bank]})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sort.SliceStable(hits, func(i, j int) bool {
		if hits[i].score != hits[j].score {
			return hits[i].score < hits[j].score // bm25: more negative = better
		}
		return hits[i].bank < hits[j].bank
	})
	seen := map[string]bool{}
	var lexical []Entry
	for _, h := range hits {
		if seen[h.e.ID] {
			continue
		}
		seen[h.e.ID] = true
		lexical = append(lexical, h.e)
	}
	ranked := lexical
	if v := m.vectorizer(); v != nil {
		if dense := m.denseCandidates(v, banks, query); len(dense) > 0 {
			ranked = fuseRRF(lexical, dense)
		}
	}
	var out []Entry
	for _, e := range ranked {
		if keep != nil && !keep(e) {
			continue
		}
		out = append(out, e)
		if len(out) == limit {
			break
		}
	}
	return out, nil
}
