// PostgreSQL+pgvector backend (W4). One schema per project inside a shared
// database; the schema name is derived from the canonical project dir so two
// stores never share rows. Every Backend method mirrors the SQLite
// implementation's semantics exactly; divergences are called out per method.
package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pgvector/pgvector-go"
)

// pgCtx backs Backend methods, which carry no context parameter.
var pgCtx = context.Background()

// pgNow mirrors sqlite's strftime('%Y-%m-%dT%H:%M:%fZ','now') format so
// timestamps stored by either engine render identically.
const pgNow = `to_char(now() AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS.MS"Z"')`

// PGStore is a handle to one project's PostgreSQL schema (pgvector enabled).
type PGStore struct {
	pool   *pgxpool.Pool
	mode   Mode
	dsn    string
	schema string
}

// OpenPG opens the PostgreSQL+pgvector backend for one project directory.
// Each project lives in its own schema (leankg_<sha256(dir)[:16]>); RW mode
// creates the schema, RO mode requires it to exist (parity with sqlite's
// read-only open of a missing store).
func OpenPG(ctx context.Context, pgURL, projectDir string, mode Mode) (Backend, error) {
	cfg, err := pgxpool.ParseConfig(pgURL)
	if err != nil {
		return nil, fmt.Errorf("store: parse pg dsn: %w", err)
	}
	schema, err := pgSchemaForDir(projectDir)
	if err != nil {
		return nil, err
	}
	if mode == RO {
		// Reader role: every transaction (implicit single-statement ones
		// included) becomes read-only, mirroring sqlite's query_only pragma.
		cfg.ConnConfig.RuntimeParams["default_transaction_read_only"] = "on"
	}
	// Every acquired connection — readers included — resolves unqualified
	// tables inside the project's schema.
	cfg.BeforeAcquire = func(ctx context.Context, conn *pgx.Conn) bool {
		_, err := conn.Exec(ctx, `SET search_path TO `+schema+`, public`)
		return err == nil
	}
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("store: connect postgres: %w", err)
	}
	s := &PGStore{pool: pool, mode: mode, dsn: pgURL, schema: schema}
	if mode == RW {
		if _, err := pool.Exec(ctx, `CREATE SCHEMA IF NOT EXISTS `+schema); err != nil {
			pool.Close()
			return nil, fmt.Errorf("store: create schema %s: %w", schema, err)
		}
		return s, nil
	}
	var exists bool
	if err := pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM information_schema.schemata WHERE schema_name = $1)`, schema).
		Scan(&exists); err != nil {
		pool.Close()
		return nil, fmt.Errorf("store: probe schema %s: %w", schema, err)
	}
	if !exists {
		pool.Close()
		return nil, fmt.Errorf("store: read-only open of missing store schema %s", schema)
	}
	return s, nil
}

// pgSchemaForDir derives the per-project schema name: leankg_ + first 16 hex
// chars of sha256(canonical absolute projectDir) — the same keying scheme as
// the Rust engine's schema-per-project layout.
func pgSchemaForDir(projectDir string) (string, error) {
	abs, err := filepath.Abs(projectDir)
	if err != nil {
		return "", fmt.Errorf("store: resolve project dir: %w", err)
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		abs = resolved
	}
	h := sha256.Sum256([]byte(abs))
	return "leankg_" + hex.EncodeToString(h[:])[:16], nil
}

// Close closes the connection pool.
func (s *PGStore) Close() error { s.pool.Close(); return nil }

// Path returns the redacted DSN (for status display).
func (s *PGStore) Path() string { return redactDSN(s.dsn) }

// Engine names the storage engine of this handle.
func (s *PGStore) Engine() string { return EnginePostgres }

// pgMarshalMeta canonicalizes a metadata map for JSONB storage.
func pgMarshalMeta(m map[string]any) string {
	if m == nil {
		return "{}"
	}
	if b, err := json.Marshal(m); err == nil {
		return string(b)
	}
	return "{}"
}

// UpsertElements writes a batch of elements in one transaction. Qualified
// names already present are replaced. (No FTS sync needed: PostgreSQL L2
// fuzzy search uses ILIKE + pg_trgm, not a shadow table.)
func (s *PGStore) UpsertElements(els []Element) error {
	if len(els) == 0 {
		return nil
	}
	tx, err := s.pool.Begin(pgCtx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(pgCtx) }()
	for _, e := range els {
		if _, err := tx.Exec(pgCtx, `INSERT INTO code_elements
			(qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, content, metadata)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10::jsonb)
			ON CONFLICT (qualified_name) DO UPDATE SET
				element_type=excluded.element_type, name=excluded.name, file_path=excluded.file_path,
				line_start=excluded.line_start, line_end=excluded.line_end, language=excluded.language,
				parent_qualified=excluded.parent_qualified, content=excluded.content, metadata=excluded.metadata`,
			e.QualifiedName, e.ElementType, e.Name, e.FilePath, e.LineStart, e.LineEnd, e.Language,
			nullIfEmpty(e.ParentQualified), e.Content, pgMarshalMeta(e.Metadata)); err != nil {
			return fmt.Errorf("store: upsert element %s: %w", e.QualifiedName, err)
		}
	}
	if err := tx.Commit(pgCtx); err != nil {
		return err
	}
	return s.BumpWatermark()
}

// UpsertRelationships writes a batch of relationships in one transaction.
// Duplicate (source, target, rel_type) rows are overwritten, not duplicated.
func (s *PGStore) UpsertRelationships(rels []Relationship) error {
	if len(rels) == 0 {
		return nil
	}
	tx, err := s.pool.Begin(pgCtx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(pgCtx) }()
	for _, r := range rels {
		if _, err := tx.Exec(pgCtx, `INSERT INTO relationships (source_qualified, target_qualified, rel_type, confidence, metadata)
			VALUES ($1,$2,$3,$4,$5::jsonb)
			ON CONFLICT (source_qualified, target_qualified, rel_type) DO UPDATE SET
				confidence=excluded.confidence, metadata=excluded.metadata`,
			r.Source, r.Target, r.RelType, r.Confidence, pgMarshalMeta(r.Metadata)); err != nil {
			return fmt.Errorf("store: upsert relationship %s->%s: %w", r.Source, r.Target, err)
		}
	}
	if err := tx.Commit(pgCtx); err != nil {
		return err
	}
	return s.BumpWatermark()
}

// UpsertFiles records per-file index state.
func (s *PGStore) UpsertFiles(files []FileRecord) error {
	if len(files) == 0 {
		return nil
	}
	tx, err := s.pool.Begin(pgCtx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(pgCtx) }()
	for _, f := range files {
		if _, err := tx.Exec(pgCtx, `INSERT INTO code_files (path, size, mtime_ns, content_hash, indexed_at)
			VALUES ($1,$2,$3,$4,`+pgNow+`)
			ON CONFLICT (path) DO UPDATE SET size=excluded.size, mtime_ns=excluded.mtime_ns,
				content_hash=excluded.content_hash, indexed_at=excluded.indexed_at`,
			f.Path, f.Size, f.MtimeNS, f.ContentHash); err != nil {
			return fmt.Errorf("store: upsert file %s: %w", f.Path, err)
		}
	}
	if err := tx.Commit(pgCtx); err != nil {
		return err
	}
	return s.BumpWatermark()
}

// Files returns all indexed file records.
func (s *PGStore) Files() ([]FileRecord, error) {
	rows, err := s.pool.Query(pgCtx, `SELECT path, size, mtime_ns, content_hash FROM code_files`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []FileRecord
	for rows.Next() {
		var f FileRecord
		if err := rows.Scan(&f.Path, &f.Size, &f.MtimeNS, &f.ContentHash); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// DeleteByFile removes all elements and relationships belonging to a file.
// Used by incremental re-index before a file is re-inserted.
func (s *PGStore) DeleteByFile(path string) error {
	tx, err := s.pool.Begin(pgCtx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(pgCtx) }()
	if _, err := tx.Exec(pgCtx, `DELETE FROM relationships
		WHERE source_qualified IN (SELECT qualified_name FROM code_elements WHERE file_path = $1)`, path); err != nil {
		return err
	}
	if _, err := tx.Exec(pgCtx, `DELETE FROM code_elements WHERE file_path = $1`, path); err != nil {
		return err
	}
	if err := tx.Commit(pgCtx); err != nil {
		return err
	}
	return s.BumpWatermark()
}

// DeleteFileRecord removes a file's row from code_files (file deleted from disk).
func (s *PGStore) DeleteFileRecord(path string) error {
	if _, err := s.pool.Exec(pgCtx, `DELETE FROM code_files WHERE path = $1`, path); err != nil {
		return err
	}
	return s.BumpWatermark()
}

const pgElementCols = `ce.qualified_name, ce.element_type, ce.name, ce.file_path, ce.line_start, ce.line_end, ce.language, COALESCE(ce.parent_qualified,''), ce.content, COALESCE(ce.metadata::text,'{}')`

func pgScanElements(rows pgx.Rows) ([]Element, error) {
	defer rows.Close()
	var out []Element
	for rows.Next() {
		var e Element
		var meta string
		if err := rows.Scan(&e.QualifiedName, &e.ElementType, &e.Name, &e.FilePath, &e.LineStart, &e.LineEnd,
			&e.Language, &e.ParentQualified, &e.Content, &meta); err != nil {
			return nil, err
		}
		if meta != "" && meta != "{}" {
			_ = json.Unmarshal([]byte(meta), &e.Metadata)
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// FindExact implements the L1 rung: exact (case-insensitive) match on element
// name or qualified name. sqlite relies on COLLATE NOCASE; PostgreSQL uses
// lower() comparisons (backed by expression indexes when available).
func (s *PGStore) FindExact(name string) ([]Element, error) {
	rows, err := s.pool.Query(pgCtx, `SELECT `+pgElementCols+` FROM code_elements ce
		WHERE lower(ce.name) = lower($1) OR lower(ce.qualified_name) = lower($1)
		ORDER BY length(ce.qualified_name) LIMIT 200`, name)
	if err != nil {
		return nil, err
	}
	return pgScanElements(rows)
}

// FindFuzzy implements the L2 rung.
// Divergence from sqlite (documented): FTS5 provides bm25 ranking, which
// PostgreSQL has no equivalent for over a plain LIKE search. Hits rank by
// ascending qualified-name length and Score = -length(qualified_name) so
// shorter (more specific) names come first; the ordering parity is kept, but
// absolute Score values are not comparable across engines.
func (s *PGStore) FindFuzzy(query string, limit int) ([]FuzzyMatch, error) {
	if strings.TrimSpace(query) == "" {
		return nil, nil
	}
	if limit <= 0 {
		limit = 50
	}
	pattern := "%" + escapeLike(query) + "%"
	rows, err := s.pool.Query(pgCtx, `SELECT `+pgElementCols+`, -length(ce.qualified_name) AS score
		FROM code_elements ce
		WHERE ce.name ILIKE $1 ESCAPE '\' OR ce.qualified_name ILIKE $1 ESCAPE '\'
		ORDER BY length(ce.qualified_name) LIMIT $2`, pattern, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []FuzzyMatch
	for rows.Next() {
		var m FuzzyMatch
		var meta string
		if err := rows.Scan(&m.Element.QualifiedName, &m.Element.ElementType, &m.Element.Name, &m.Element.FilePath,
			&m.Element.LineStart, &m.Element.LineEnd, &m.Element.Language, &m.Element.ParentQualified,
			&m.Element.Content, &meta, &m.Score); err != nil {
			return nil, err
		}
		if meta != "" && meta != "{}" {
			_ = json.Unmarshal([]byte(meta), &m.Element.Metadata)
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// escapeLike escapes LIKE wildcards so user input matches literally
// (backslash is PostgreSQL's default LIKE escape character).
func escapeLike(q string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(q)
}

// ElementCount returns the total number of indexed elements.
func (s *PGStore) ElementCount() (int, error) {
	var n int
	err := s.pool.QueryRow(pgCtx, `SELECT COUNT(*) FROM code_elements`).Scan(&n)
	return n, err
}

// RelationshipCount returns the total number of indexed relationships.
func (s *PGStore) RelationshipCount() (int, error) {
	var n int
	err := s.pool.QueryRow(pgCtx, `SELECT COUNT(*) FROM relationships`).Scan(&n)
	return n, err
}

// ElementsByType returns element counts grouped by element_type.
func (s *PGStore) ElementsByType() (map[string]int, error) {
	rows, err := s.pool.Query(pgCtx, `SELECT element_type, COUNT(*) FROM code_elements GROUP BY element_type`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var t string
		var n int
		if err := rows.Scan(&t, &n); err != nil {
			return nil, err
		}
		out[t] = n
	}
	return out, rows.Err()
}

// FileCount returns the number of indexed files.
func (s *PGStore) FileCount() (int, error) {
	var n int
	err := s.pool.QueryRow(pgCtx, `SELECT COUNT(*) FROM code_files`).Scan(&n)
	return n, err
}

// Elements enumerates every indexed element (ordered by qualified_name).
func (s *PGStore) Elements() ([]Element, error) {
	rows, err := s.pool.Query(pgCtx, `SELECT `+pgElementCols+` FROM code_elements ce ORDER BY ce.qualified_name`)
	if err != nil {
		return nil, err
	}
	return pgScanElements(rows)
}

// Outgoing returns relationships sourced at `source`.
func (s *PGStore) Outgoing(source string) ([]Relationship, error) {
	rows, err := s.pool.Query(pgCtx,
		`SELECT source_qualified, target_qualified, rel_type, confidence, metadata::text FROM relationships WHERE source_qualified = $1`, source)
	if err != nil {
		return nil, err
	}
	return pgScanRelationships(rows)
}

// Incoming returns relationships targeting `target`.
func (s *PGStore) Incoming(target string) ([]Relationship, error) {
	rows, err := s.pool.Query(pgCtx,
		`SELECT source_qualified, target_qualified, rel_type, confidence, metadata::text FROM relationships WHERE target_qualified = $1`, target)
	if err != nil {
		return nil, err
	}
	return pgScanRelationships(rows)
}

// RelationshipsAll returns up to limit relationships ordered by id.
func (s *PGStore) RelationshipsAll(limit int) ([]Relationship, error) {
	if limit <= 0 {
		limit = 1000
	}
	rows, err := s.pool.Query(pgCtx,
		`SELECT source_qualified, target_qualified, rel_type, confidence, metadata::text FROM relationships ORDER BY id LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	return pgScanRelationships(rows)
}

func pgScanRelationships(rows pgx.Rows) ([]Relationship, error) {
	defer rows.Close()
	var out []Relationship
	for rows.Next() {
		var r Relationship
		var meta string
		if err := rows.Scan(&r.Source, &r.Target, &r.RelType, &r.Confidence, &meta); err != nil {
			return nil, err
		}
		if meta != "" && meta != "{}" {
			_ = json.Unmarshal([]byte(meta), &r.Metadata)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// KVSet writes a namespaced key (upsert). Namespacing is folded into the key
// exactly like the sqlite implementation.
func (s *PGStore) KVSet(namespace, key, value string) error {
	if _, err := s.pool.Exec(pgCtx, `INSERT INTO kv (key, value) VALUES ($1, $2)
		ON CONFLICT (key) DO UPDATE SET value=excluded.value`, namespace+"\x1f"+key, value); err != nil {
		return err
	}
	return s.BumpWatermark()
}

// KVGet reads a namespaced key.
func (s *PGStore) KVGet(namespace, key string) (string, bool, error) {
	var v string
	err := s.pool.QueryRow(pgCtx, `SELECT value FROM kv WHERE key = $1`, namespace+"\x1f"+key).Scan(&v)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return v, true, nil
}

// sanitizeModelID folds a model id into a safe identifier fragment: lowercase
// alphanumerics and underscores pass through; anything else (or an empty id)
// falls back to a sha256 hex prefix so exotic ids stay deterministic. Note
// the ceiling: two distinct ids can collide on the same fragment (e.g. "a-b"
// and "a_b" both hash nowhere, but "AB" and "ab" map to the same tables);
// real model ids are lowercase and safe.
func sanitizeModelID(modelID string) string {
	lower := strings.ToLower(modelID)
	ok := lower != ""
	for _, r := range lower {
		if !((r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_') {
			ok = false
			break
		}
	}
	if ok {
		return lower
	}
	h := sha256.Sum256([]byte(modelID))
	return hex.EncodeToString(h[:])[:16]
}

func (s *PGStore) stateTable(modelID string) string {
	return "embedding_state_" + sanitizeModelID(modelID)
}
func (s *PGStore) vecTable(modelID string) string {
	return "embedding_vectors_" + sanitizeModelID(modelID)
}

// WriteStamp upserts the stamp for a model (writer side, after validation)
// and ensures the per-model tables exist.
func (s *PGStore) WriteStamp(st ModelStamp) error {
	if err := s.ensureModelTables(st); err != nil {
		return err
	}
	if _, err := s.pool.Exec(pgCtx, `INSERT INTO emb_stamp (model_id, revision, dimensions, distance, provider)
		VALUES ($1,$2,$3,$4,$5)
		ON CONFLICT (model_id) DO UPDATE SET revision=excluded.revision,
			dimensions=excluded.dimensions, distance=excluded.distance, provider=excluded.provider`,
		st.ModelID, st.Revision, st.Dimensions, st.Distance, st.Provider); err != nil {
		return fmt.Errorf("store: write stamp %s: %w", st.ModelID, err)
	}
	return s.BumpWatermark()
}

// ensureModelTables creates the per-model state + vector tables. A PG vector
// column is fixed-dimension (unlike sqlite BLOBs), so a re-stamp with a
// different dimension count drops and recreates the tables — the physical
// analogue of the stamp-guard full rebuild.
func (s *PGStore) ensureModelTables(st ModelStamp) error {
	state, vec := s.stateTable(st.ModelID), s.vecTable(st.ModelID)
	var cur int
	err := s.pool.QueryRow(pgCtx, `SELECT dimensions FROM emb_stamp WHERE model_id = $1`, st.ModelID).Scan(&cur)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		// fresh model
	case err != nil:
		return err
	case cur != st.Dimensions:
		if _, err := s.pool.Exec(pgCtx, `DROP TABLE IF EXISTS `+vec+`, `+state); err != nil {
			return fmt.Errorf("store: drop resized model tables %s: %w", st.ModelID, err)
		}
	}
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS ` + state + ` (
			qualified_name TEXT PRIMARY KEY,
			content_hash   TEXT NOT NULL,
			state          TEXT NOT NULL,
			embedded_at    TIMESTAMPTZ
		)`,
		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s (
			qualified_name TEXT PRIMARY KEY,
			vec            vector(%d) NOT NULL
		)`, vec, st.Dimensions),
		`CREATE INDEX IF NOT EXISTS idx_` + vec + `_hnsw ON ` + vec + ` USING hnsw (vec vector_cosine_ops)`,
	}
	for _, stmt := range stmts {
		if _, err := s.pool.Exec(pgCtx, stmt); err != nil {
			return fmt.Errorf("store: ensure model tables %s: %w", st.ModelID, err)
		}
	}
	return nil
}

// Stamp returns the persisted stamp for a model; nil when none exists.
func (s *PGStore) Stamp(modelID string) (*ModelStamp, error) {
	var st ModelStamp
	err := s.pool.QueryRow(pgCtx,
		`SELECT model_id, revision, dimensions, distance, provider FROM emb_stamp WHERE model_id = $1`, modelID).
		Scan(&st.ModelID, &st.Revision, &st.Dimensions, &st.Distance, &st.Provider)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &st, nil
}

// Stamps returns all persisted model stamps.
func (s *PGStore) Stamps() ([]ModelStamp, error) {
	rows, err := s.pool.Query(pgCtx, `SELECT model_id, revision, dimensions, distance, provider FROM emb_stamp`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ModelStamp
	for rows.Next() {
		var st ModelStamp
		if err := rows.Scan(&st.ModelID, &st.Revision, &st.Dimensions, &st.Distance, &st.Provider); err != nil {
			return nil, err
		}
		out = append(out, st)
	}
	return out, rows.Err()
}

// EmbeddingStateMap returns qualified_name -> content_hash for a model.
func (s *PGStore) EmbeddingStateMap(modelID string) (map[string]string, error) {
	rows, err := s.pool.Query(pgCtx, `SELECT qualified_name, content_hash FROM `+s.stateTable(modelID))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var qn, h string
		if err := rows.Scan(&qn, &h); err != nil {
			return nil, err
		}
		out[qn] = h
	}
	return out, rows.Err()
}

// SetEmbeddingStates records embedding_state rows for freshly embedded QNs.
func (s *PGStore) SetEmbeddingStates(modelID string, states map[string]string) error {
	if len(states) == 0 {
		return nil
	}
	tx, err := s.pool.Begin(pgCtx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(pgCtx) }()
	for qn, h := range states {
		if _, err := tx.Exec(pgCtx, `INSERT INTO `+s.stateTable(modelID)+` (qualified_name, content_hash, state, embedded_at)
			VALUES ($1,$2,'embedded', now())
			ON CONFLICT (qualified_name) DO UPDATE SET content_hash=excluded.content_hash,
				state='embedded', embedded_at=excluded.embedded_at`, qn, h); err != nil {
			return fmt.Errorf("store: set embedding state %s: %w", qn, err)
		}
	}
	if err := tx.Commit(pgCtx); err != nil {
		return err
	}
	return s.BumpWatermark()
}

// UpsertVectors writes a batch of vectors for a model in ONE transaction:
// a crash mid-run leaves the previous state consistent and reruns resume
// (issue #368 AC: batch upsert semantics).
func (s *PGStore) UpsertVectors(modelID string, rows []VectorRow) error {
	if len(rows) == 0 {
		return nil
	}
	tx, err := s.pool.Begin(pgCtx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(pgCtx) }()
	for _, r := range rows {
		if _, err := tx.Exec(pgCtx, `INSERT INTO `+s.vecTable(modelID)+` (qualified_name, vec) VALUES ($1, $2::vector)
			ON CONFLICT (qualified_name) DO UPDATE SET vec=excluded.vec`,
			r.QualifiedName, pgvector.NewVector(r.Vec)); err != nil {
			return fmt.Errorf("store: upsert vector %s: %w", r.QualifiedName, err)
		}
	}
	if err := tx.Commit(pgCtx); err != nil {
		return err
	}
	return s.BumpWatermark()
}

// ClearVectors drops all vectors + state for a model (stamp-guard rebuild).
func (s *PGStore) ClearVectors(modelID string) error {
	for _, stmt := range []string{
		`DELETE FROM ` + s.vecTable(modelID),
		`DELETE FROM ` + s.stateTable(modelID),
	} {
		if _, err := s.pool.Exec(pgCtx, stmt); err != nil {
			return err
		}
	}
	return s.BumpWatermark()
}

// VectorCount returns the vector count for a model.
func (s *PGStore) VectorCount(modelID string) (int, error) {
	var n int
	err := s.pool.QueryRow(pgCtx, `SELECT COUNT(*) FROM `+s.vecTable(modelID)).Scan(&n)
	return n, err
}

// VectorCoverage reports (covered, orphans): vector rows whose QN still has a
// live element vs. rows whose QN disappeared (anti-join via LEFT JOIN).
func (s *PGStore) VectorCoverage(modelID string) (covered, orphans int, err error) {
	err = s.pool.QueryRow(pgCtx, `SELECT
			COUNT(*) FILTER (WHERE ce.qualified_name IS NOT NULL),
			COUNT(*) FILTER (WHERE ce.qualified_name IS NULL)
		FROM `+s.vecTable(modelID)+` v
		LEFT JOIN code_elements ce ON ce.qualified_name = v.qualified_name`).Scan(&covered, &orphans)
	return covered, orphans, err
}

// SearchVectors performs the single ANN-equivalent shape: the model's vectors
// ranked by cosine distance (HNSW index when present), hydrated against
// code_elements. similarity = 1 - distance (the <=> operator's cosine distance).
func (s *PGStore) SearchVectors(modelID string, q []float32, k int) ([]VectorSearchHit, error) {
	if len(q) == 0 {
		return nil, nil
	}
	if k < 0 {
		k = 0
	}
	rows, err := s.pool.Query(pgCtx, `SELECT v.qualified_name,
		COALESCE(ce.element_type,''), COALESCE(ce.name, v.qualified_name), COALESCE(ce.file_path,''),
		COALESCE(ce.line_start,0), COALESCE(ce.line_end,0), COALESCE(ce.language,''),
		COALESCE(ce.parent_qualified,''), COALESCE(ce.content,''), COALESCE(ce.metadata::text,'{}'),
		1 - (v.vec <=> $1::vector) AS similarity
		FROM `+s.vecTable(modelID)+` v
		LEFT JOIN code_elements ce ON ce.qualified_name = v.qualified_name
		ORDER BY v.vec <=> $1::vector
		LIMIT $2`, pgvector.NewVector(q), k)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []VectorSearchHit
	for rows.Next() {
		var h VectorSearchHit
		var meta string
		if err := rows.Scan(&h.Element.QualifiedName, &h.Element.ElementType, &h.Element.Name, &h.Element.FilePath,
			&h.Element.LineStart, &h.Element.LineEnd, &h.Element.Language, &h.Element.ParentQualified,
			&h.Element.Content, &meta, &h.Similarity); err != nil {
			return nil, err
		}
		if meta != "" && meta != "{}" {
			_ = json.Unmarshal([]byte(meta), &h.Element.Metadata)
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

// Watermark returns the current freshness sequence number.
func (s *PGStore) Watermark() (int64, int64, error) {
	var seq, at int64
	err := s.pool.QueryRow(pgCtx, `SELECT seq, at FROM write_watermark WHERE id = 1`).Scan(&seq, &at)
	return seq, at, err
}

// BumpWatermark advances the freshness sequence (one call per commit batch).
func (s *PGStore) BumpWatermark() error {
	if s.mode == RO {
		return nil // readers never bump
	}
	_, err := s.pool.Exec(pgCtx, `UPDATE write_watermark SET seq = seq + 1, at = $1 WHERE id = 1`, time.Now().Unix())
	return err
}

// SaveInventory persists the inventory under kv with the current watermark.
func (s *PGStore) SaveInventory(inv Inventory) error {
	seq, _, err := s.Watermark()
	if err != nil {
		return err
	}
	inv.LastInventorySeq = seq
	inv.ComputedAt = time.Now().Unix()
	b, err := json.Marshal(inv)
	if err != nil {
		return err
	}
	_, err = s.pool.Exec(pgCtx, `INSERT INTO kv (key, value) VALUES ('inventory', $1)
		ON CONFLICT (key) DO UPDATE SET value=excluded.value`, string(b))
	return err
}

// LoadInventory returns the persisted inventory; nil when never computed.
func (s *PGStore) LoadInventory() (*Inventory, error) {
	var val string
	err := s.pool.QueryRow(pgCtx, `SELECT value FROM kv WHERE key = 'inventory'`).Scan(&val)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var inv Inventory
	if err := json.Unmarshal([]byte(val), &inv); err != nil {
		return nil, err
	}
	return &inv, nil
}

// StartEmbedRun records a running pipeline run and returns its id.
func (s *PGStore) StartEmbedRun(modelID, mode string, dirty int) (int64, error) {
	var id int64
	err := s.pool.QueryRow(pgCtx, `INSERT INTO embed_runs (model_id, mode, started_at, dirty, status)
		VALUES ($1,$2,`+pgNow+`,$3,'running') RETURNING id`, modelID, mode, dirty).Scan(&id)
	return id, err
}

// FinishEmbedRun closes a run record with the final counters.
func (s *PGStore) FinishEmbedRun(id int64, status string, embedded, skipped, failed, truncations, orphans int) error {
	_, err := s.pool.Exec(pgCtx, `UPDATE embed_runs SET finished_at=`+pgNow+`,
		embedded=$1, skipped=$2, failed=$3, truncations=$4, orphans=$5, status=$6 WHERE id=$7`,
		embedded, skipped, failed, truncations, orphans, status, id)
	return err
}

const pgEmbedRunCols = `id, model_id, mode, started_at, finished_at, dirty, embedded, skipped, failed, truncations, orphans, status`

func pgScanEmbedRun(row pgx.Row) (*EmbedRun, error) {
	var r EmbedRun
	err := row.Scan(&r.ID, &r.ModelID, &r.Mode, &r.StartedAt, &r.FinishedAt, &r.Dirty, &r.Embedded,
		&r.Skipped, &r.Failed, &r.Truncations, &r.Orphans, &r.Status)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &r, nil
}

// LastEmbedRunAny returns the most recent embed run across all models.
func (s *PGStore) LastEmbedRunAny() (*EmbedRun, error) {
	return pgScanEmbedRun(s.pool.QueryRow(pgCtx,
		`SELECT `+pgEmbedRunCols+` FROM embed_runs ORDER BY id DESC LIMIT 1`))
}

// LastEmbedRun returns the most recent run for a model; nil when none.
func (s *PGStore) LastEmbedRun(modelID string) (*EmbedRun, error) {
	return pgScanEmbedRun(s.pool.QueryRow(pgCtx,
		`SELECT `+pgEmbedRunCols+` FROM embed_runs WHERE model_id = $1 ORDER BY id DESC LIMIT 1`, modelID))
}

// AppendAudit writes one hash-chained record. The chain head is the row with
// the highest seq. sqlite serialized appends through its single write
// connection; the pool's concurrent writers need an explicit transaction-scoped
// advisory lock so the hash chain can never fork.
func (s *PGStore) AppendAudit(e *AuditEntry) error {
	tx, err := s.pool.Begin(pgCtx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(pgCtx) }()
	if _, err := tx.Exec(pgCtx, `SELECT pg_advisory_xact_lock(hashtext($1))`, s.schema); err != nil {
		return err
	}
	var seq int64
	var prev string
	if err := tx.QueryRow(pgCtx,
		`SELECT COALESCE(MAX(seq),0), COALESCE((SELECT hash FROM audit_ledger ORDER BY seq DESC LIMIT 1),'') FROM audit_ledger`).
		Scan(&seq, &prev); err != nil {
		return err
	}
	e.Seq = seq + 1
	e.PrevHash = prev
	if e.At == 0 {
		e.At = time.Now().Unix()
	}
	e.Hash = auditHash(e.PrevHash, e)
	if _, err := tx.Exec(pgCtx, `INSERT INTO audit_ledger (seq, at, actor, action, target, details, prev_hash, hash)
		VALUES ($1,$2,$3,$4,$5,$6::jsonb,$7,$8)`, e.Seq, e.At, e.Actor, e.Action, e.Target, auditDetailsJSON(e), e.PrevHash, e.Hash); err != nil {
		return err
	}
	if err := tx.Commit(pgCtx); err != nil {
		return err
	}
	return s.BumpWatermark()
}

// AuditTail returns the last `limit` entries in ascending seq order.
func (s *PGStore) AuditTail(limit int) ([]AuditEntry, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.pool.Query(pgCtx, `SELECT seq, at, actor, action, target, details::text, prev_hash, hash FROM audit_ledger
		ORDER BY seq DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AuditEntry
	for rows.Next() {
		var e AuditEntry
		var details string
		if err := rows.Scan(&e.Seq, &e.At, &e.Actor, &e.Action, &e.Target, &details, &e.PrevHash, &e.Hash); err != nil {
			return nil, err
		}
		if details != "" && details != "{}" {
			_ = json.Unmarshal([]byte(details), &e.Details)
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out, nil
}

// VerifyAuditChain walks the whole ledger recomputing hashes; the first
// mismatch (prev-link or entry hash) reports brokenAt. Details are unmarshaled
// into the entry BEFORE recompute: jsonb normalizes key order on storage, and
// the hash is defined over the canonical Go marshal of the details map, so
// only the map round-trip is stable — not the stored byte string.
func (s *PGStore) VerifyAuditChain() (ok bool, brokenAt int64, err error) {
	rows, err := s.pool.Query(pgCtx, `SELECT seq, at, actor, action, target, details::text, prev_hash, hash FROM audit_ledger ORDER BY seq`)
	if err != nil {
		return false, 0, err
	}
	defer rows.Close()
	prev := ""
	for rows.Next() {
		var e AuditEntry
		var details string
		if err := rows.Scan(&e.Seq, &e.At, &e.Actor, &e.Action, &e.Target, &details, &e.PrevHash, &e.Hash); err != nil {
			return false, 0, err
		}
		if details != "" && details != "{}" {
			_ = json.Unmarshal([]byte(details), &e.Details)
		}
		if e.PrevHash != prev {
			return false, e.Seq, nil
		}
		if e.Hash != auditHash(e.PrevHash, &e) {
			return false, e.Seq, nil
		}
		prev = e.Hash
	}
	return true, 0, rows.Err()
}
