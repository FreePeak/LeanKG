package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"unicode"
)

// Element is one indexed code element (the index layer).
type Element struct {
	QualifiedName   string         `json:"qualified_name"`
	ElementType     string         `json:"element_type"`
	Name            string         `json:"name"`
	FilePath        string         `json:"file_path"`
	LineStart       int            `json:"line_start"`
	LineEnd         int            `json:"line_end"`
	Language        string         `json:"language"`
	ParentQualified string         `json:"parent_qualified,omitempty"`
	Content         string         `json:"content,omitempty"`
	Metadata        map[string]any `json:"metadata,omitempty"`
}

// Relationship is one directed edge between elements.
type Relationship struct {
	Source     string         `json:"source"`
	Target     string         `json:"target"`
	RelType    string         `json:"rel_type"`
	Confidence float64        `json:"confidence"`
	Metadata   map[string]any `json:"metadata,omitempty"`
}

// FileRecord is the per-file index state used by 3-signal change detection.
type FileRecord struct {
	Path        string
	Size        int64
	MtimeNS     int64
	ContentHash string
}

// UpsertElements writes a batch of elements in one transaction and keeps the
// FTS5 L2 index in sync. Qualified names already present are replaced.
func (s *Store) UpsertElements(els []Element) error {
	if len(els) == 0 {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	for _, e := range els {
		meta := "{}"
		if e.Metadata != nil {
			if b, err := json.Marshal(e.Metadata); err == nil {
				meta = string(b)
			}
		}
		if _, err := tx.Exec(`INSERT INTO code_elements
			(qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, content, metadata)
			VALUES (?,?,?,?,?,?,?,?,?,?)
			ON CONFLICT(qualified_name) DO UPDATE SET
				element_type=excluded.element_type, name=excluded.name, file_path=excluded.file_path,
				line_start=excluded.line_start, line_end=excluded.line_end, language=excluded.language,
				parent_qualified=excluded.parent_qualified, content=excluded.content, metadata=excluded.metadata`,
			e.QualifiedName, e.ElementType, e.Name, e.FilePath, e.LineStart, e.LineEnd, e.Language, nullIfEmpty(e.ParentQualified), e.Content, meta); err != nil {
			return fmt.Errorf("store: upsert element %s: %w", e.QualifiedName, err)
		}
		if err := ftsSync(tx, e); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	return s.BumpWatermark()
}

// ftsSync replaces the FTS row for one element. rowid must match the element
// id so L2 joins stay cheap.
func ftsSync(tx *sql.Tx, e Element) error {
	if _, err := tx.Exec(`DELETE FROM elements_fts WHERE rowid = (SELECT id FROM code_elements WHERE qualified_name = ?)`, e.QualifiedName); err != nil {
		return fmt.Errorf("store: fts delete %s: %w", e.QualifiedName, err)
	}
	if _, err := tx.Exec(`INSERT INTO elements_fts (rowid, name, qualified_name, content)
		VALUES ((SELECT id FROM code_elements WHERE qualified_name = ?), ?, ?, ?)`,
		e.QualifiedName, e.Name, e.QualifiedName, ftsContent(e)); err != nil {
		return fmt.Errorf("store: fts insert %s: %w", e.QualifiedName, err)
	}
	return nil
}

func ftsContent(e Element) string {
	// Bound the indexed text: content is the source snippet already; cap it so
	// the FTS index does not balloon on generated files.
	const maxContent = 8000
	c := e.Content
	if len(c) > maxContent {
		c = c[:maxContent]
	}
	return e.Name + " " + e.QualifiedName + " " + c
}

// UpsertRelationships writes a batch of relationships in one transaction.
// Duplicate (source, target, rel_type) rows are overwritten, not duplicated.
func (s *Store) UpsertRelationships(rels []Relationship) error {
	if len(rels) == 0 {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	for _, r := range rels {
		meta := "{}"
		if r.Metadata != nil {
			if b, err := json.Marshal(r.Metadata); err == nil {
				meta = string(b)
			}
		}
		if _, err := tx.Exec(`INSERT INTO relationships (source_qualified, target_qualified, rel_type, confidence, metadata)
			VALUES (?,?,?,?,?)
			ON CONFLICT(source_qualified, target_qualified, rel_type) DO UPDATE SET
				confidence=excluded.confidence, metadata=excluded.metadata`,
			r.Source, r.Target, r.RelType, r.Confidence, meta); err != nil {
			return fmt.Errorf("store: upsert relationship %s->%s: %w", r.Source, r.Target, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	return s.BumpWatermark()
}

// UpsertFiles records per-file index state.
func (s *Store) UpsertFiles(files []FileRecord) error {
	if len(files) == 0 {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	for _, f := range files {
		if _, err := tx.Exec(`INSERT INTO code_files (path, size, mtime_ns, content_hash, indexed_at)
			VALUES (?,?,?,?,strftime('%Y-%m-%dT%H:%M:%fZ','now'))
			ON CONFLICT(path) DO UPDATE SET size=excluded.size, mtime_ns=excluded.mtime_ns,
				content_hash=excluded.content_hash, indexed_at=excluded.indexed_at`,
			f.Path, f.Size, f.MtimeNS, f.ContentHash); err != nil {
			return fmt.Errorf("store: upsert file %s: %w", f.Path, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	return s.BumpWatermark()
}

// Files returns all indexed file records.
func (s *Store) Files() ([]FileRecord, error) {
	rows, err := s.db.Query(`SELECT path, size, mtime_ns, content_hash FROM code_files`)
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

// DeleteByFile removes all elements and relationships belonging to a file and
// their FTS rows. Used by incremental re-index before a file is re-inserted.
func (s *Store) DeleteByFile(path string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.Exec(`DELETE FROM elements_fts WHERE rowid IN (SELECT id FROM code_elements WHERE file_path = ?)`, path); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM relationships WHERE source_qualified IN (SELECT qualified_name FROM code_elements WHERE file_path = ?)`, path); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM code_elements WHERE file_path = ?`, path); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	return s.BumpWatermark()
}

// DeleteFileRecord removes a file's row from code_files (file deleted from disk).
func (s *Store) DeleteFileRecord(path string) error {
	if _, err := s.db.Exec(`DELETE FROM code_files WHERE path = ?`, path); err != nil {
		return err
	}
	return s.BumpWatermark()
}

// FindExact implements the L1 rung: exact (case-insensitive) match on element
// name or qualified name.
func (s *Store) FindExact(name string) ([]Element, error) {
	rows, err := s.db.Query(`SELECT `+elementCols+` FROM code_elements ce
		WHERE ce.name = ? COLLATE NOCASE OR ce.qualified_name = ? COLLATE NOCASE
		ORDER BY length(ce.qualified_name) LIMIT 200`, name, name)
	if err != nil {
		return nil, err
	}
	return scanElements(rows)
}

const elementCols = `ce.qualified_name, ce.element_type, ce.name, ce.file_path, ce.line_start, ce.line_end, ce.language, COALESCE(ce.parent_qualified,''), ce.content, COALESCE(ce.metadata,'{}')`

func scanElements(rows *sql.Rows) ([]Element, error) {
	defer rows.Close()
	var out []Element
	for rows.Next() {
		var e Element
		var meta string
		if err := rows.Scan(&e.QualifiedName, &e.ElementType, &e.Name, &e.FilePath, &e.LineStart, &e.LineEnd, &e.Language, &e.ParentQualified, &e.Content, &meta); err != nil {
			return nil, err
		}
		if meta != "" && meta != "{}" {
			_ = json.Unmarshal([]byte(meta), &e.Metadata)
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// FuzzyMatch is one L2 hit with its bm25 relevance score (lower is better).
type FuzzyMatch struct {
	Element Element
	Score   float64
}

// FindFuzzy implements the L2 rung over FTS5. The query is tokenized into
// quoted OR terms so user input can never inject FTS syntax.
func (s *Store) FindFuzzy(query string, limit int) ([]FuzzyMatch, error) {
	q := ftsQuery(query)
	if q == "" {
		return nil, nil
	}
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.db.Query(`SELECT `+elementCols+`, bm25(elements_fts) AS score
		FROM elements_fts JOIN code_elements ce ON ce.id = elements_fts.rowid
		WHERE elements_fts MATCH ?
		ORDER BY score LIMIT ?`, q, limit)
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

// ftsQuery turns free text into a safe FTS5 query. Plain identifier-ish terms
// pass through (so `parse*` stays a prefix query); anything else is quoted so
// user input can never inject FTS5 syntax.
func ftsQuery(query string) string {
	fields := strings.FieldsFunc(query, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsNumber(r) && r != '_' && r != '*'
	})
	var parts []string
	for _, f := range fields {
		if f == "*" || f == "" {
			continue
		}
		if isBareTerm(f) {
			parts = append(parts, f)
			continue
		}
		f = strings.ReplaceAll(f, `"`, "")
		if f == "" {
			continue
		}
		parts = append(parts, `"`+f+`"`)
	}
	return strings.Join(parts, " OR ")
}

// isBareTerm reports whether f is a safe FTS5 bare token (letters, digits,
// underscore, optional single trailing star).
func isBareTerm(f string) bool {
	// FTS5 query keywords must never pass through bare — a quoted "OR" is a
	// literal term, a bare OR changes query semantics.
	switch strings.ToUpper(f) {
	case "AND", "OR", "NOT", "NEAR":
		return false
	}
	f = strings.TrimSuffix(f, "*")
	if f == "" || strings.Contains(f, "*") {
		return false
	}
	for _, r := range f {
		if !unicode.IsLetter(r) && !unicode.IsNumber(r) && r != '_' {
			return false
		}
	}
	return true
}

// ElementCount returns the total number of indexed elements.
func (s *Store) ElementCount() (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM code_elements`).Scan(&n)
	return n, err
}

// RelationshipCount returns the total number of indexed relationships.
func (s *Store) RelationshipCount() (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM relationships`).Scan(&n)
	return n, err
}

// ElementsByType returns element counts grouped by element_type.
func (s *Store) ElementsByType() (map[string]int, error) {
	rows, err := s.db.Query(`SELECT element_type, COUNT(*) FROM code_elements GROUP BY element_type`)
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
func (s *Store) FileCount() (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM code_files`).Scan(&n)
	return n, err
}

func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// Outgoing returns relationships sourced at `source`.
func (s *Store) Outgoing(source string) ([]Relationship, error) {
	rows, err := s.db.Query(`SELECT source_qualified, target_qualified, rel_type, confidence, metadata FROM relationships WHERE source_qualified = ?`, source)
	if err != nil {
		return nil, err
	}
	return scanRelationships(rows)
}

// Incoming returns relationships targeting `target`.
func (s *Store) Incoming(target string) ([]Relationship, error) {
	rows, err := s.db.Query(`SELECT source_qualified, target_qualified, rel_type, confidence, metadata FROM relationships WHERE target_qualified = ?`, target)
	if err != nil {
		return nil, err
	}
	return scanRelationships(rows)
}

// RelationshipsAll returns up to limit relationships ordered by id.
func (s *Store) RelationshipsAll(limit int) ([]Relationship, error) {
	if limit <= 0 {
		limit = 1000
	}
	rows, err := s.db.Query(`SELECT source_qualified, target_qualified, rel_type, confidence, metadata FROM relationships ORDER BY id LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	return scanRelationships(rows)
}

func scanRelationships(rows *sql.Rows) ([]Relationship, error) {
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

// KVSet writes a namespaced key (upsert).
func (s *Store) KVSet(namespace, key, value string) error {
	_, err := s.db.Exec(`INSERT INTO kv (key, value) VALUES (?, ?)
		ON CONFLICT(key) DO UPDATE SET value=excluded.value`, namespace+"\x1f"+key, value)
	if err != nil {
		return err
	}
	return s.BumpWatermark()
}

// KVGet reads a namespaced key.
func (s *Store) KVGet(namespace, key string) (string, bool, error) {
	var v string
	err := s.db.QueryRow(`SELECT value FROM kv WHERE key = ?`, namespace+"\x1f"+key).Scan(&v)
	if err == sql.ErrNoRows {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return v, true, nil
}
