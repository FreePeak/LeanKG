package store

import (
	"encoding/json"
	"errors"
	"fmt"
)

// Engine-neutral logic shared by the SQLite and PostgreSQL stores (CX-51).
// Dialect SQL, placeholders and time expressions stay in the engine files;
// everything here behaves the same on both engines. Callers own closing rows.

// scanRow is one row read with Scan: *sql.Row and pgx.Row both satisfy it.
type scanRow interface {
	Scan(dest ...any) error
}

// scanRows is a row cursor: *sql.Rows and pgx.Rows both satisfy it. Close is
// left out because its signature differs (database/sql returns an error).
type scanRows interface {
	Next() bool
	Scan(dest ...any) error
	Err() error
}

// schemaMigrationsDDL is the migration ledger, identical on both engines.
const schemaMigrationsDDL = `CREATE TABLE IF NOT EXISTS schema_migrations (
		version INTEGER PRIMARY KEY, name TEXT NOT NULL, applied_at TEXT NOT NULL)`

// migrateModeError refuses a migration run on a read-only handle.
func migrateModeError(mode Mode) error {
	if mode == RO {
		return fmt.Errorf("store: migrate requires RW mode")
	}
	return nil
}

// encodeJSONObject canonicalizes a metadata map for JSON storage: nil and any
// value that fails to marshal both store "{}".
func encodeJSONObject(m map[string]any) string {
	if m == nil {
		return "{}"
	}
	if b, err := json.Marshal(m); err == nil {
		return string(b)
	}
	return "{}"
}

// decodeJSONObject reads a stored metadata object. An empty object and an
// empty string both read back as nil; a malformed value reads back as nil.
func decodeJSONObject(s string) map[string]any {
	if s == "" || s == "{}" {
		return nil
	}
	var m map[string]any
	_ = json.Unmarshal([]byte(s), &m)
	return m
}

// embedRunCols is the embed_runs column list read by every run reader.
const embedRunCols = `id, model_id, mode, started_at, finished_at, dirty, embedded, skipped, failed, truncations, orphans, status`

// scanEmbedRun reads one run row; noRows is the engine's no-rows sentinel,
// which maps to (nil, nil).
func scanEmbedRun(row scanRow, noRows error) (*EmbedRun, error) {
	var r EmbedRun
	err := row.Scan(&r.ID, &r.ModelID, &r.Mode, &r.StartedAt, &r.FinishedAt, &r.Dirty, &r.Embedded,
		&r.Skipped, &r.Failed, &r.Truncations, &r.Orphans, &r.Status)
	if errors.Is(err, noRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &r, nil
}

// scanStamp reads the emb_stamp columns in the order stampSelectFor emits them.
func scanStamp(row scanRow) (ModelStamp, error) {
	var st ModelStamp
	err := row.Scan(&st.ModelID, &st.Revision, &st.Dimensions, &st.Distance, &st.Provider,
		&st.ChunkerVersion, &st.QueryPrefix, &st.DocumentPrefix, &st.TextCap)
	return st, err
}

// scanAuditEntry reads one audit_ledger row, details included.
func scanAuditEntry(row scanRow) (AuditEntry, error) {
	var e AuditEntry
	var details string
	err := row.Scan(&e.Seq, &e.At, &e.Actor, &e.Action, &e.Target, &details, &e.PrevHash, &e.Hash)
	e.Details = decodeJSONObject(details)
	return e, err
}

// scanElements reads the element columns (elementCols or pgElementCols).
func scanElements(rows scanRows) ([]Element, error) {
	var out []Element
	for rows.Next() {
		var e Element
		var meta string
		if err := rows.Scan(&e.QualifiedName, &e.ElementType, &e.Name, &e.FilePath, &e.LineStart, &e.LineEnd,
			&e.Language, &e.ParentQualified, &e.Content, &meta); err != nil {
			return nil, err
		}
		e.Metadata = decodeJSONObject(meta)
		out = append(out, e)
	}
	return out, rows.Err()
}

// scanFuzzyMatches reads the element columns plus a trailing score column.
func scanFuzzyMatches(rows scanRows) ([]FuzzyMatch, error) {
	var out []FuzzyMatch
	for rows.Next() {
		var m FuzzyMatch
		var meta string
		if err := rows.Scan(&m.Element.QualifiedName, &m.Element.ElementType, &m.Element.Name, &m.Element.FilePath,
			&m.Element.LineStart, &m.Element.LineEnd, &m.Element.Language, &m.Element.ParentQualified,
			&m.Element.Content, &meta, &m.Score); err != nil {
			return nil, err
		}
		m.Element.Metadata = decodeJSONObject(meta)
		out = append(out, m)
	}
	return out, rows.Err()
}

// scanRelationships reads source, target, rel_type, confidence and metadata.
func scanRelationships(rows scanRows) ([]Relationship, error) {
	var out []Relationship
	for rows.Next() {
		var r Relationship
		var meta string
		if err := rows.Scan(&r.Source, &r.Target, &r.RelType, &r.Confidence, &meta); err != nil {
			return nil, err
		}
		r.Metadata = decodeJSONObject(meta)
		out = append(out, r)
	}
	return out, rows.Err()
}

// relationshipKey is the identity of an edge for sameRelationshipSet.
type relationshipKey struct {
	source, target, relType string
	confidence              float64
}

// sameRelationshipSet reports whether a and b hold the same edges, in any
// order, on (source, target, rel_type, confidence). Metadata is not compared.
func sameRelationshipSet(a, b []Relationship) bool {
	keys := func(rels []Relationship) map[relationshipKey]bool {
		out := make(map[relationshipKey]bool, len(rels))
		for _, r := range rels {
			out[relationshipKey{r.Source, r.Target, r.RelType, r.Confidence}] = true
		}
		return out
	}
	ka, kb := keys(a), keys(b)
	if len(ka) != len(kb) {
		return false
	}
	for k := range kb {
		if !ka[k] {
			return false
		}
	}
	return true
}

// keepRareNameTokens drops every query token that appears in more than bound
// symbol names (counts holds each token's capped count). It filters toks in
// place, as the callers always did.
func keepRareNameTokens(toks []string, counts map[string]int, bound int) []string {
	out := toks[:0]
	for _, t := range toks {
		if counts[t] > bound {
			continue // too common to discriminate
		}
		out = append(out, t)
	}
	return out
}

// prepareStaleSweep applies the rules every stale-collection purge starts
// with: the handle must be writable, and the keep list is cleaned. An empty
// result means the purge is a no-op by contract.
func prepareStaleSweep(mode Mode, keep []string) ([]string, error) {
	if err := requireWritable(mode); err != nil {
		return nil, err
	}
	return cleanModelIDs(keep), nil
}

// fuseHybridHits fuses the arms' ranked lists and returns at most limit hits.
// hit supplies each document's per-arm fields; the fused score and ranks are
// set here.
func fuseHybridHits(lists []RankList, w FuseRRFWeights, limit int, hit func(qn string) HybridHit) []HybridHit {
	fused := FuseRRFWeighted(lists, w)
	if len(fused) > limit {
		fused = fused[:limit]
	}
	out := make([]HybridHit, 0, len(fused))
	for _, f := range fused {
		h := hit(f.Key)
		h.Score, h.Ranks = f.Score, f.Ranks
		out = append(out, h)
	}
	return out
}
