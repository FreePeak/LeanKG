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

// ErrSchemaMissing is the read-only open of a project schema that does not
// exist, the one openPG failure that means "no store yet". A bad dsn, a refused
// connection or an auth failure is a different error and must not read as empty.
var ErrSchemaMissing = errors.New("store: read-only open of missing store schema")

// Mode reports how the store was opened — the PostgreSQL twin of
// (*Store).Mode, so a caller that must not write (the write path skips the
// migration on a reader) can ask either backend.
func (s *PGStore) Mode() Mode { return s.mode }

// openPG opens the PostgreSQL+pgvector backend for one project directory.
// Each project lives in its own schema (leankg_<sha256(dir)[:16]>); RW mode
// creates the schema, RO mode requires it to exist (parity with sqlite's
// read-only open of a missing store).
func openPG(ctx context.Context, pgURL, projectDir string, mode Mode) (Backend, error) {
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
	// pgvector applies a WHERE to an HNSW scan only after the index has
	// returned hnsw.ef_search candidates, so a selective filter (or a limit
	// above ef_search) would return fewer rows than asked. iterative_scan keeps
	// the index scan going until the LIMIT is met; it only continues when the
	// first pass came up short, so unfiltered queries within ef_search are
	// unchanged. Set per connection so no statement needs a transaction.
	cfg.ConnConfig.RuntimeParams["hnsw.iterative_scan"] = "strict_order"
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
		return nil, fmt.Errorf("%w %s", ErrSchemaMissing, schema)
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

// UpsertElements writes a batch of elements in one transaction. Qualified
// names already present are replaced. (No FTS sync needed: the keyword index
// is the STORED generated tsvector column of migration 012, which PostgreSQL
// recomputes per row; ILIKE + pg_trgm is its fallback, not a shadow table.)
func (s *PGStore) UpsertElements(ctx context.Context, els []Element) error {
	if len(els) == 0 {
		return nil
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	for _, e := range els {
		e = e.sanitize() // same write-boundary guard as sqlite: engines store identical bytes
		if _, err := tx.Exec(ctx, `INSERT INTO code_elements
			(qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, content, metadata)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10::jsonb)
			ON CONFLICT (qualified_name) DO UPDATE SET
				element_type=excluded.element_type, name=excluded.name, file_path=excluded.file_path,
				line_start=excluded.line_start, line_end=excluded.line_end, language=excluded.language,
				parent_qualified=excluded.parent_qualified, content=excluded.content, metadata=excluded.metadata`,
			e.QualifiedName, e.ElementType, e.Name, e.FilePath, e.LineStart, e.LineEnd, e.Language,
			nullIfEmpty(e.ParentQualified), e.Content, ValidText(encodeJSONObject(e.Metadata))); err != nil {
			return fmt.Errorf("store: upsert element %s: %w", e.QualifiedName, err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	return s.BumpWatermark(context.WithoutCancel(ctx))
}

// pgInsertRelationshipTx writes one edge inside tx. Duplicate (source, target,
// rel_type) rows are overwritten, not duplicated.
func pgInsertRelationshipTx(ctx context.Context, tx pgx.Tx, r Relationship) error {
	if _, err := tx.Exec(ctx, `INSERT INTO relationships (source_qualified, target_qualified, rel_type, confidence, metadata)
		VALUES ($1,$2,$3,$4,$5::jsonb)
		ON CONFLICT (source_qualified, target_qualified, rel_type) DO UPDATE SET
			confidence=excluded.confidence, metadata=excluded.metadata`,
		r.Source, r.Target, r.RelType, r.Confidence, encodeJSONObject(r.Metadata)); err != nil {
		return fmt.Errorf("store: upsert relationship %s->%s: %w", r.Source, r.Target, err)
	}
	return nil
}

// UpsertRelationships writes a batch of relationships in one transaction.
// Duplicate (source, target, rel_type) rows are overwritten, not duplicated.
func (s *PGStore) UpsertRelationships(ctx context.Context, rels []Relationship) error {
	if len(rels) == 0 {
		return nil
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	for _, r := range rels {
		if err := pgInsertRelationshipTx(ctx, tx, r); err != nil {
			return err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	return s.BumpWatermark(context.WithoutCancel(ctx))
}

// UpsertFiles records per-file index state.
func (s *PGStore) UpsertFiles(ctx context.Context, files []FileRecord) error {
	if len(files) == 0 {
		return nil
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	for _, f := range files {
		if _, err := tx.Exec(ctx, `INSERT INTO code_files (path, size, mtime_ns, content_hash, indexed_at)
			VALUES ($1,$2,$3,$4,`+pgNow+`)
			ON CONFLICT (path) DO UPDATE SET size=excluded.size, mtime_ns=excluded.mtime_ns,
				content_hash=excluded.content_hash, indexed_at=excluded.indexed_at`,
			f.Path, f.Size, f.MtimeNS, f.ContentHash); err != nil {
			return fmt.Errorf("store: upsert file %s: %w", f.Path, err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	return s.BumpWatermark(context.WithoutCancel(ctx))
}

// Files returns all indexed file records.
func (s *PGStore) Files(ctx context.Context) ([]FileRecord, error) {
	rows, err := s.pool.Query(ctx, `SELECT path, size, mtime_ns, content_hash FROM code_files`)
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

// ReplaceFileRelationships swaps the relTypes edges sourced at file path for
// rels (see IndexWriter). The compare and the write share one transaction.
func (s *PGStore) ReplaceFileRelationships(ctx context.Context, path string, relTypes []string, rels []Relationship) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	const where = `rel_type = ANY($1) AND source_qualified IN (SELECT qualified_name FROM code_elements WHERE file_path = $2)`
	rows, err := tx.Query(ctx, `SELECT source_qualified, target_qualified, rel_type, confidence, metadata::text
		FROM relationships WHERE `+where, relTypes, path)
	if err != nil {
		return err
	}
	stored, err := scanRelationships(rows)
	rows.Close()
	if err != nil {
		return err
	}
	if sameRelationshipSet(stored, rels) {
		return nil
	}
	if _, err := tx.Exec(ctx, `DELETE FROM relationships WHERE `+where, relTypes, path); err != nil {
		return err
	}
	for _, r := range rels {
		if err := pgInsertRelationshipTx(ctx, tx, r); err != nil {
			return err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	return s.BumpWatermark(context.WithoutCancel(ctx))
}

// DeleteByFile removes all elements and relationships belonging to a file.
// Used by incremental re-index before a file is re-inserted.
func (s *PGStore) DeleteByFile(ctx context.Context, path string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	if _, err := tx.Exec(ctx, `DELETE FROM relationships
		WHERE source_qualified IN (SELECT qualified_name FROM code_elements WHERE file_path = $1)`, path); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM code_elements WHERE file_path = $1`, path); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	return s.BumpWatermark(context.WithoutCancel(ctx))
}

// DeleteFileRecord removes a file's row from code_files (file deleted from disk).
func (s *PGStore) DeleteFileRecord(ctx context.Context, path string) error {
	if _, err := s.pool.Exec(ctx, `DELETE FROM code_files WHERE path = $1`, path); err != nil {
		return err
	}
	return s.BumpWatermark(context.WithoutCancel(ctx))
}

// DeleteOrphanRelationships removes every edge whose endpoint is neither a live
// element nor a file in code_files (leankg gc).
func (s *PGStore) DeleteOrphanRelationships(ctx context.Context) (int, error) {
	if err := requireWritable(s.mode); err != nil {
		return 0, err
	}
	tag, err := s.pool.Exec(ctx, `DELETE FROM relationships
		WHERE source_qualified NOT IN (SELECT qualified_name FROM code_elements UNION SELECT path FROM code_files)
		   OR target_qualified NOT IN (SELECT qualified_name FROM code_elements UNION SELECT path FROM code_files)`)
	if err != nil {
		return 0, err
	}
	n := int(tag.RowsAffected())
	if n == 0 {
		return 0, nil
	}
	return n, s.BumpWatermark(context.WithoutCancel(ctx))
}

const pgElementCols = `ce.qualified_name, ce.element_type, ce.name, ce.file_path, ce.line_start, ce.line_end, ce.language, COALESCE(ce.parent_qualified,''), ce.content, COALESCE(ce.metadata::text,'{}')`

// FindExact implements the L1 rung: exact (case-insensitive) match on element
// name or qualified name. sqlite relies on COLLATE NOCASE; PostgreSQL uses
// lower() comparisons (backed by expression indexes when available).
func (s *PGStore) FindExact(ctx context.Context, name string) ([]Element, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+pgElementCols+` FROM code_elements ce
		WHERE lower(ce.name) = lower($1) OR lower(ce.qualified_name) = lower($1)
		ORDER BY length(ce.qualified_name) LIMIT 200`, name)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanElements(rows)
}

// nameTokenCounts is the rarity table the name arm gates on, on PostgreSQL.
// Same query, same bound, same meaning as the SQLite one: a token must be rare
// among symbol names to be worth a name search, and the bound is a fraction of
// the ELEMENT count. The count is what the LIKE will MATCH (a SUBSTRING of the
// name), and it stops at bound+1 because only the comparison matters.
func (s *PGStore) nameTokenCounts(ctx context.Context, toks []string) (map[string]int, int, error) {
	var total int
	if err := s.pool.QueryRow(ctx, `SELECT COUNT(*) FROM code_elements`).Scan(&total); err != nil {
		return nil, 0, err
	}
	bound := nameArmBound(total)
	counts := map[string]int{}
	for _, t := range toks {
		pat := "%" + t + "%"
		var n int
		if err := s.pool.QueryRow(ctx,
			`SELECT COUNT(*) FROM (SELECT 1 FROM code_elements WHERE name ILIKE $1 OR qualified_name ILIKE $1 LIMIT $2)`,
			pat, bound+1).Scan(&n); err != nil {
			return nil, 0, err
		}
		counts[t] = n
	}
	return counts, total, nil
}

// rareNameTokens is the shared gate (see the SQLite implementation for the
// measurement that motivated it).
func (s *PGStore) rareNameTokens(ctx context.Context, query string) ([]string, error) {
	toks := nameTokens(query)
	if len(toks) == 0 {
		return nil, nil
	}
	counts, total, err := s.nameTokenCounts(ctx, toks)
	if err != nil {
		return nil, err
	}
	return keepRareNameTokens(toks, counts, nameArmBound(total)), nil
}

// FindByNameToken is the ArmName ranking arm on PostgreSQL: elements whose
// SYMBOL NAME contains a rare token of the query, shortest name first. Same
// contract as the SQLite arm; f is applied in the WHERE before the LIMIT.
func (s *PGStore) FindByNameToken(ctx context.Context, query string, limit int, f ElementFilter) ([]FuzzyMatch, error) {
	toks, err := s.rareNameTokens(ctx, query)
	if err != nil {
		return nil, err
	}
	if len(toks) == 0 {
		return nil, nil
	}
	if limit <= 0 {
		limit = 50
	}
	var where []string
	var args []any
	for i, t := range toks {
		where = append(where, fmt.Sprintf("(ce.name ILIKE $%d OR ce.qualified_name ILIKE $%d)", i+1, i+1))
		args = append(args, "%"+t+"%")
	}
	filter := andWhere(f.wherePG("ce.qualified_name", "ce.file_path", &args))
	args = append(args, limit)
	rows, err := s.pool.Query(ctx, `SELECT `+pgElementCols+` FROM code_elements ce
		WHERE (`+strings.Join(where, " OR ")+`)`+filter+`
		ORDER BY length(ce.name) ASC, length(ce.qualified_name) ASC
		LIMIT $`+fmt.Sprint(len(args)), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	els, err := scanElements(rows)
	if err != nil {
		return nil, err
	}
	out := make([]FuzzyMatch, 0, len(els))
	for _, e := range els {
		out = append(out, FuzzyMatch{Element: e})
	}
	return out, nil
}

// findElementsILIKE is the degraded arm of the L2 rung: literal-substring
// recall over name/qualified_name with no ranking signal at all.
// Divergence from sqlite (documented): FTS5 provides bm25 ranking, which a
// plain LIKE search has no equivalent for. Hits rank by ascending
// qualified-name length and Score = -length(qualified_name) so shorter (more
// specific) names come first; the ordering parity is kept, but absolute Score
// values are not comparable across engines. The PG keyword rung (FindFuzzy in
// pg_fts.go) prefers tsvector ranking, then pg_trgm similarity, and only lands
// here.
func (s *PGStore) findElementsILIKE(ctx context.Context, query string, limit int, f ElementFilter) ([]FuzzyMatch, error) {
	if strings.TrimSpace(query) == "" {
		return nil, nil
	}
	if limit <= 0 {
		limit = 50
	}
	pattern := "%" + escapeLike(query) + "%"
	args := []any{pattern}
	filter := andWhere(f.wherePG("ce.qualified_name", "ce.file_path", &args))
	args = append(args, limit)
	rows, err := s.pool.Query(ctx, `SELECT `+pgElementCols+`, -length(ce.qualified_name) AS score
		FROM code_elements ce
		WHERE (ce.name ILIKE $1 ESCAPE '\' OR ce.qualified_name ILIKE $1 ESCAPE '\')`+filter+`
		ORDER BY length(ce.qualified_name) LIMIT $`+fmt.Sprint(len(args)), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanFuzzyMatches(rows)
}

// escapeLike escapes LIKE wildcards so user input matches literally
// (backslash is PostgreSQL's default LIKE escape character).
func escapeLike(q string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(q)
}

// ElementCount returns the total number of indexed elements.
func (s *PGStore) ElementCount(ctx context.Context) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx, `SELECT COUNT(*) FROM code_elements`).Scan(&n)
	return n, err
}

// RelationshipCount returns the total number of indexed relationships.
func (s *PGStore) RelationshipCount(ctx context.Context) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx, `SELECT COUNT(*) FROM relationships`).Scan(&n)
	return n, err
}

// ElementsByType returns element counts grouped by element_type.
func (s *PGStore) ElementsByType(ctx context.Context) (map[string]int, error) {
	rows, err := s.pool.Query(ctx, `SELECT element_type, COUNT(*) FROM code_elements GROUP BY element_type`)
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
func (s *PGStore) FileCount(ctx context.Context) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx, `SELECT COUNT(*) FROM code_files`).Scan(&n)
	return n, err
}

// Elements enumerates every indexed element (ordered by qualified_name).
func (s *PGStore) Elements(ctx context.Context) ([]Element, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+pgElementCols+` FROM code_elements ce ORDER BY ce.qualified_name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanElements(rows)
}

// Outgoing returns relationships sourced at `source`.
func (s *PGStore) Outgoing(ctx context.Context, source string) ([]Relationship, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT source_qualified, target_qualified, rel_type, confidence, metadata::text FROM relationships WHERE source_qualified = $1`, source)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanRelationships(rows)
}

// Incoming returns relationships targeting `target`.
func (s *PGStore) Incoming(ctx context.Context, target string) ([]Relationship, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT source_qualified, target_qualified, rel_type, confidence, metadata::text FROM relationships WHERE target_qualified = $1`, target)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanRelationships(rows)
}

// RelationshipsAll returns up to limit relationships ordered by id.
func (s *PGStore) RelationshipsAll(ctx context.Context, limit int) ([]Relationship, error) {
	if limit <= 0 {
		limit = 1000
	}
	rows, err := s.pool.Query(ctx,
		`SELECT source_qualified, target_qualified, rel_type, confidence, metadata::text FROM relationships ORDER BY id LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanRelationships(rows)
}

// KVSet writes a namespaced key (upsert). Namespacing is folded into the key
// exactly like the sqlite implementation.
func (s *PGStore) KVSet(ctx context.Context, namespace, key, value string) error {
	if _, err := s.pool.Exec(ctx, `INSERT INTO kv (key, value) VALUES ($1, $2)
		ON CONFLICT (key) DO UPDATE SET value=excluded.value`, namespace+"\x1f"+key, value); err != nil {
		return err
	}
	return s.BumpWatermark(ctx)
}

// KVGet reads a namespaced key.
func (s *PGStore) KVGet(ctx context.Context, namespace, key string) (string, bool, error) {
	var v string
	err := s.pool.QueryRow(ctx, `SELECT value FROM kv WHERE key = $1`, namespace+"\x1f"+key).Scan(&v)
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
// and ensures the per-model tables exist. The full identity — model,
// revision, dims, distance, provider, chunker version and the
// query/document prefix pair — is persisted.
func (s *PGStore) WriteStamp(ctx context.Context, st ModelStamp) error {
	if err := s.ensureModelTables(ctx, st); err != nil {
		return err
	}
	shape, err := s.stampShape(ctx)
	if err != nil {
		return fmt.Errorf("store: stamp shape: %w", err)
	}
	if _, err := s.pool.Exec(ctx, stampUpsertFor(shape.cols, func(i int) string { return fmt.Sprintf("$%d", i) }), stampValues(st, shape.cols)...); err != nil {
		return fmt.Errorf("store: write stamp %s: %w", st.ModelID, err)
	}
	return s.BumpWatermark(context.WithoutCancel(ctx))
}

// ensureModelTables creates the per-model state + vector tables. A PG vector
// column is fixed-dimension (unlike sqlite BLOBs), so a re-stamp with a
// different dimension count drops and recreates the tables — the physical
// analogue of the stamp-guard full rebuild.
func (s *PGStore) ensureModelTables(ctx context.Context, st ModelStamp) error {
	state, vec := s.stateTable(st.ModelID), s.vecTable(st.ModelID)
	var cur int
	err := s.pool.QueryRow(ctx, `SELECT dimensions FROM emb_stamp WHERE model_id = $1`, st.ModelID).Scan(&cur)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		// fresh model
	case err != nil:
		return err
	case cur != st.Dimensions:
		if _, err := s.pool.Exec(ctx, `DROP TABLE IF EXISTS `+vec+`, `+state); err != nil {
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
		if _, err := s.pool.Exec(ctx, stmt); err != nil {
			return fmt.Errorf("store: ensure model tables %s: %w", st.ModelID, err)
		}
	}
	return nil
}

// stampColumns probes the emb_stamp columns this schema carries. A store
// that predates a stamp migration has no such column, and naming it would
// fail the whole read. A schema with no emb_stamp table at all yields no
// columns; the read then fails with PostgreSQL's own "relation
// \"emb_stamp\" does not exist", which is the truth about that store.
func (s *PGStore) stampColumns(ctx context.Context) (stampColumnSet, error) {
	rows, err := s.pool.Query(ctx, `SELECT a.attname::text FROM pg_attribute a
		WHERE a.attrelid = to_regclass('emb_stamp') AND a.attnum > 0 AND NOT a.attisdropped`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	cols := stampColumnSet{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		cols[name] = true
	}
	return cols, rows.Err()
}

// stampShape probes this schema's emb_stamp columns and builds the read
// shape from them.
func (s *PGStore) stampShape(ctx context.Context) (*stampShape, error) {
	cols, err := s.stampColumns(ctx)
	if err != nil {
		return nil, err
	}
	return &stampShape{cols: cols, selectSQL: stampSelectFor(cols)}, nil
}

// Stamp returns the persisted stamp for a model; nil when none exists. The
// read shape follows the schema the store actually has: an optional column
// the schema does not have AT ALL is projected as its documented default
// (text_cap 0 = "cap never recorded") instead of failing the read with
// "column \"text_cap\" does not exist".
func (s *PGStore) Stamp(ctx context.Context, modelID string) (*ModelStamp, error) {
	shape, err := s.stampShape(ctx)
	if err != nil {
		return nil, err
	}
	st, err := scanStamp(s.pool.QueryRow(ctx, shape.selectSQL+` WHERE model_id = $1`, modelID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &st, nil
}

// Stamps returns all persisted model stamps. The read shape follows the
// store's schema exactly like PGStore.Stamp's.
func (s *PGStore) Stamps(ctx context.Context) ([]ModelStamp, error) {
	shape, err := s.stampShape(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx, shape.selectSQL)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ModelStamp
	for rows.Next() {
		st, err := scanStamp(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, st)
	}
	return out, rows.Err()
}

// DeleteStaleStamps drops every vector collection that is not in keep.
//
// Postgres keeps one pair of tables per collection (embedding_vectors_<model>
// / embedding_state_<model>), so a stale collection is dropped by dropping
// its tables — the rows do not go to a freelist the way SQLite's do, but
// the space is released immediately. The stamp row goes with them.
//
// An empty keep is a NO-OP (see the Backend contract). A collection whose
// tables have already vanished is skipped, not fatal: the stamp is cleaned
// up either way. The watermark bumps only when rows actually go.
func (s *PGStore) DeleteStaleStamps(ctx context.Context, keep []string) (StaleStampSweep, error) {
	keep, err := prepareStaleSweep(s.mode, keep)
	if err != nil {
		return StaleStampSweep{}, err
	}
	if len(keep) == 0 {
		return StaleStampSweep{}, nil
	}
	keepSet := make(map[string]bool, len(keep))
	for _, k := range keep {
		keepSet[k] = true
	}
	// A store whose keep collection is ABSENT is not stale — it is BEHIND
	// (see the SQLite implementation for the reasoning). Dropping its only
	// collection would destroy embed work for no gain.
	have := false
	for _, k := range keep {
		var present bool
		if err := s.pool.QueryRow(ctx, `SELECT to_regclass($1) IS NOT NULL`, s.vecTable(k)).Scan(&present); err != nil {
			return StaleStampSweep{}, err
		}
		if !present {
			continue
		}
		var n int
		if err := s.pool.QueryRow(ctx, `SELECT 1 FROM `+s.vecTable(k)+` LIMIT 1`).Scan(&n); err != nil {
			if err == pgx.ErrNoRows {
				continue
			}
			return StaleStampSweep{}, err
		}
		have = true
		break
	}
	if !have {
		return StaleStampSweep{}, nil
	}
	stamps, err := s.Stamps(ctx)
	if err != nil {
		return StaleStampSweep{}, err
	}
	var out StaleStampSweep
	for _, ms := range stamps {
		if keepSet[ms.ModelID] {
			continue
		}
		var present bool
		if err := s.pool.QueryRow(ctx, `SELECT to_regclass($1) IS NOT NULL`, s.vecTable(ms.ModelID)).Scan(&present); err != nil {
			return out, err
		}
		if present {
			var n int
			if err := s.pool.QueryRow(ctx, `SELECT COUNT(*) FROM `+s.vecTable(ms.ModelID)).Scan(&n); err != nil {
				return out, err
			}
			out.Vectors += n
			if _, err := s.pool.Exec(ctx, `DROP TABLE IF EXISTS `+s.vecTable(ms.ModelID)); err != nil {
				return out, err
			}
		}
		if _, err := s.pool.Exec(ctx, `DROP TABLE IF EXISTS `+s.stateTable(ms.ModelID)); err != nil {
			return out, err
		}
		tag, err := s.pool.Exec(ctx, `DELETE FROM emb_stamp WHERE model_id = $1`, ms.ModelID)
		if err != nil {
			return out, err
		}
		out.Models += int(tag.RowsAffected())
	}
	if out.Models == 0 && out.Vectors == 0 {
		return out, nil
	}
	return out, s.BumpWatermark(context.WithoutCancel(ctx))
}

// EmbeddingStateMap returns qualified_name -> content_hash for a model.
func (s *PGStore) EmbeddingStateMap(ctx context.Context, modelID string) (map[string]string, error) {
	rows, err := s.pool.Query(ctx, `SELECT qualified_name, content_hash FROM `+s.stateTable(modelID))
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
func (s *PGStore) SetEmbeddingStates(ctx context.Context, modelID string, states map[string]string) error {
	if len(states) == 0 {
		return nil
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	for qn, h := range states {
		if _, err := tx.Exec(ctx, `INSERT INTO `+s.stateTable(modelID)+` (qualified_name, content_hash, state, embedded_at)
			VALUES ($1,$2,'embedded', now())
			ON CONFLICT (qualified_name) DO UPDATE SET content_hash=excluded.content_hash,
				state='embedded', embedded_at=excluded.embedded_at`, qn, h); err != nil {
			return fmt.Errorf("store: set embedding state %s: %w", qn, err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	return s.BumpWatermark(context.WithoutCancel(ctx))
}

// UpsertVectors writes a batch of vectors for a model in ONE transaction:
// a crash mid-run leaves the previous state consistent and reruns resume
// (issue #368 AC: batch upsert semantics).
func (s *PGStore) UpsertVectors(ctx context.Context, modelID string, rows []VectorRow) error {
	if len(rows) == 0 {
		return nil
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	for _, r := range rows {
		if _, err := tx.Exec(ctx, `INSERT INTO `+s.vecTable(modelID)+` (qualified_name, vec) VALUES ($1, $2::vector)
			ON CONFLICT (qualified_name) DO UPDATE SET vec=excluded.vec`,
			r.QualifiedName, pgvector.NewVector(r.Vec)); err != nil {
			return fmt.Errorf("store: upsert vector %s: %w", r.QualifiedName, err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	return s.BumpWatermark(context.WithoutCancel(ctx))
}

// ClearVectors drops all vectors + state for a model (stamp-guard rebuild).
func (s *PGStore) ClearVectors(ctx context.Context, modelID string) error {
	for _, stmt := range []string{
		`DELETE FROM ` + s.vecTable(modelID),
		`DELETE FROM ` + s.stateTable(modelID),
	} {
		if _, err := s.pool.Exec(ctx, stmt); err != nil {
			return err
		}
	}
	return s.BumpWatermark(context.WithoutCancel(ctx))
}

// VectorCount returns the vector count for a model.
func (s *PGStore) VectorCount(ctx context.Context, modelID string) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx, `SELECT COUNT(*) FROM `+s.vecTable(modelID)).Scan(&n)
	return n, err
}

// VectorCoverage reports (covered, orphans): vector rows whose QN still has a
// live element vs. rows whose QN disappeared (anti-join via LEFT JOIN).
func (s *PGStore) VectorCoverage(ctx context.Context, modelID string) (covered, orphans int, err error) {
	err = s.pool.QueryRow(ctx, `SELECT
			COUNT(*) FILTER (WHERE ce.qualified_name IS NOT NULL),
			COUNT(*) FILTER (WHERE ce.qualified_name IS NULL)
		FROM `+s.vecTable(modelID)+` v
		LEFT JOIN code_elements ce ON ce.qualified_name = v.qualified_name`).Scan(&covered, &orphans)
	return covered, orphans, err
}

// DeleteOrphanVectors purges vector + state rows for elements that no longer
// exist, per model collection (the per-stamp tables are PG's collection
// layout; a stamped model whose tables vanished is skipped, not fatal). The
// watermark bumps only when rows actually go.
func (s *PGStore) DeleteOrphanVectors(ctx context.Context) (int, error) {
	if err := requireWritable(s.mode); err != nil {
		return 0, err
	}
	stamps, err := s.Stamps(ctx)
	if err != nil {
		return 0, err
	}
	total := 0
	for _, ms := range stamps {
		var ok bool
		if err := s.pool.QueryRow(ctx, `SELECT to_regclass($1) IS NOT NULL`, s.vecTable(ms.ModelID)).Scan(&ok); err != nil {
			return total, err
		}
		if !ok {
			continue
		}
		tag, err := s.pool.Exec(ctx, `DELETE FROM `+s.vecTable(ms.ModelID)+` v
			WHERE v.qualified_name NOT IN (SELECT qualified_name FROM code_elements)`)
		if err != nil {
			return total, err
		}
		total += int(tag.RowsAffected())
		if err := s.pool.QueryRow(ctx, `SELECT to_regclass($1) IS NOT NULL`, s.stateTable(ms.ModelID)).Scan(&ok); err != nil {
			return total, err
		}
		if ok {
			if _, err := s.pool.Exec(ctx, `DELETE FROM `+s.stateTable(ms.ModelID)+` st
				WHERE st.qualified_name NOT IN (SELECT qualified_name FROM code_elements)`); err != nil {
				return total, err
			}
		}
	}
	if total == 0 {
		return 0, nil
	}
	return total, s.BumpWatermark(context.WithoutCancel(ctx))
}

// SearchVectors performs the single ANN-equivalent shape: the model's vectors
// ranked by cosine distance (HNSW index when present), hydrated against
// code_elements. similarity = 1 - distance (the <=> operator's cosine distance).
// The filter is part of the WHERE clause, before ORDER BY and LIMIT, and the
// connections run with hnsw.iterative_scan (see openPG), so K admitted rows come
// back whenever they exist.
func (s *PGStore) SearchVectors(ctx context.Context, q VectorQuery) ([]VectorSearchHit, error) {
	if len(q.Vec) == 0 {
		return nil, nil
	}
	k := max(q.K, 0)
	args := []any{pgvector.NewVector(q.Vec)}
	filter := andWhere(q.Filter.wherePG("ce.qualified_name", "ce.file_path", &args))
	args = append(args, k)
	rows, err := s.pool.Query(ctx, `SELECT v.qualified_name,
		COALESCE(ce.element_type,''), COALESCE(ce.name, v.qualified_name), COALESCE(ce.file_path,''),
		COALESCE(ce.line_start,0), COALESCE(ce.line_end,0), COALESCE(ce.language,''),
		COALESCE(ce.parent_qualified,''), COALESCE(ce.content,''), COALESCE(ce.metadata::text,'{}'),
		1 - (v.vec <=> $1::vector) AS similarity
		FROM `+s.vecTable(q.ModelID)+` v
		JOIN code_elements ce ON ce.qualified_name = v.qualified_name -- INNER: orphans are never hits (RS-05)
		WHERE TRUE`+filter+`
		ORDER BY v.vec <=> $1::vector
		LIMIT $`+fmt.Sprint(len(args)), args...)
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
		h.Element.Metadata = decodeJSONObject(meta)
		out = append(out, h)
	}
	return out, rows.Err()
}

// Watermark returns the current freshness sequence number.
func (s *PGStore) Watermark(ctx context.Context) (int64, int64, error) {
	var seq, at int64
	err := s.pool.QueryRow(ctx, `SELECT seq, at FROM write_watermark WHERE id = 1`).Scan(&seq, &at)
	return seq, at, err
}

// BumpWatermark advances the freshness sequence (one call per commit batch).
func (s *PGStore) BumpWatermark(ctx context.Context) error {
	if s.mode == RO {
		return nil // readers never bump
	}
	_, err := s.pool.Exec(ctx, `UPDATE write_watermark SET seq = seq + 1, at = $1 WHERE id = 1`, time.Now().Unix())
	return err
}

// SaveInventory persists the inventory under kv with the current watermark.
func (s *PGStore) SaveInventory(ctx context.Context, inv Inventory) error {
	seq, _, err := s.Watermark(ctx)
	if err != nil {
		return err
	}
	inv.LastInventorySeq = seq
	inv.ComputedAt = time.Now().Unix()
	b, err := json.Marshal(inv)
	if err != nil {
		return err
	}
	_, err = s.pool.Exec(ctx, `INSERT INTO kv (key, value) VALUES ('inventory', $1)
		ON CONFLICT (key) DO UPDATE SET value=excluded.value`, string(b))
	return err
}

// LoadInventory returns the persisted inventory; nil when never computed.
func (s *PGStore) LoadInventory(ctx context.Context) (*Inventory, error) {
	var val string
	err := s.pool.QueryRow(ctx, `SELECT value FROM kv WHERE key = 'inventory'`).Scan(&val)
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
func (s *PGStore) StartEmbedRun(ctx context.Context, modelID, mode string, dirty int) (int64, error) {
	var id int64
	err := s.pool.QueryRow(ctx, `INSERT INTO embed_runs (model_id, mode, started_at, dirty, status)
		VALUES ($1,$2,`+pgNow+`,$3,'running') RETURNING id`, modelID, mode, dirty).Scan(&id)
	return id, err
}

// FinishEmbedRun closes a run record with the final counters.
func (s *PGStore) FinishEmbedRun(ctx context.Context, id int64, status string, embedded, skipped, failed, truncations, orphans int) error {
	_, err := s.pool.Exec(ctx, `UPDATE embed_runs SET finished_at=`+pgNow+`,
		embedded=$1, skipped=$2, failed=$3, truncations=$4, orphans=$5, status=$6 WHERE id=$7`,
		embedded, skipped, failed, truncations, orphans, status, id)
	return err
}

// LastEmbedRunAny returns the most recent embed run across all models.
func (s *PGStore) LastEmbedRunAny(ctx context.Context) (*EmbedRun, error) {
	return scanEmbedRun(s.pool.QueryRow(ctx,
		`SELECT `+embedRunCols+` FROM embed_runs ORDER BY id DESC LIMIT 1`), pgx.ErrNoRows)
}

// PruneEmbedRuns trims the embed-run ledger: it closes out the runs still
// marked "running" past the cutoff and deletes the finished runs older than
// it. The newest row is kept — it names the store's current collection
// (CurrentModelID). Mirrors the SQLite implementation (vectors.go).
func (s *PGStore) PruneEmbedRuns(ctx context.Context, cutoff time.Time) (EmbedRunPrune, error) {
	var out EmbedRunPrune
	if err := requireWritable(s.mode); err != nil {
		return out, err
	}
	cut := embedRunCutoff(cutoff)
	tag, err := s.pool.Exec(ctx, `UPDATE embed_runs SET status='failed', finished_at=`+pgNow+`
		WHERE status='running' AND started_at < $1`, cut)
	if err != nil {
		return out, fmt.Errorf("store: close out stale embed runs: %w", err)
	}
	out.Closed = tag.RowsAffected()
	tag, err = s.pool.Exec(ctx, `DELETE FROM embed_runs
		WHERE started_at < $1 AND status <> 'running'
		  AND id <> (SELECT COALESCE(MAX(id), 0) FROM embed_runs)`, cut)
	if err != nil {
		return out, fmt.Errorf("store: prune embed runs: %w", err)
	}
	out.Pruned = tag.RowsAffected()
	return out, nil
}

// LastEmbedRun returns the most recent run for a model; nil when none.
func (s *PGStore) LastEmbedRun(ctx context.Context, modelID string) (*EmbedRun, error) {
	return scanEmbedRun(s.pool.QueryRow(ctx,
		`SELECT `+embedRunCols+` FROM embed_runs WHERE model_id = $1 ORDER BY id DESC LIMIT 1`, modelID), pgx.ErrNoRows)
}

// AppendAudit writes one hash-chained record. The chain head is the row with
// the highest seq. sqlite serialized appends through its single write
// connection; the pool's concurrent writers need an explicit transaction-scoped
// advisory lock so the hash chain can never fork.
func (s *PGStore) AppendAudit(ctx context.Context, e *AuditEntry) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, s.schema); err != nil {
		return err
	}
	var seq int64
	var prev string
	if err := tx.QueryRow(ctx,
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
	if _, err := tx.Exec(ctx, `INSERT INTO audit_ledger (seq, at, actor, action, target, details, prev_hash, hash)
		VALUES ($1,$2,$3,$4,$5,$6::jsonb,$7,$8)`, e.Seq, e.At, e.Actor, e.Action, e.Target, auditDetailsJSON(e), e.PrevHash, e.Hash); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	return s.BumpWatermark(context.WithoutCancel(ctx))
}

// AuditTail returns the last `limit` entries in ascending seq order.
func (s *PGStore) AuditTail(ctx context.Context, limit int) ([]AuditEntry, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.pool.Query(ctx, `SELECT seq, at, actor, action, target, details::text, prev_hash, hash FROM audit_ledger
		ORDER BY seq DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AuditEntry
	for rows.Next() {
		e, err := scanAuditEntry(rows)
		if err != nil {
			return nil, err
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
func (s *PGStore) VerifyAuditChain(ctx context.Context) (ok bool, brokenAt int64, err error) {
	rows, err := s.pool.Query(ctx, `SELECT seq, at, actor, action, target, details::text, prev_hash, hash FROM audit_ledger ORDER BY seq`)
	if err != nil {
		return false, 0, err
	}
	defer rows.Close()
	prev := ""
	for rows.Next() {
		e, err := scanAuditEntry(rows)
		if err != nil {
			return false, 0, err
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
