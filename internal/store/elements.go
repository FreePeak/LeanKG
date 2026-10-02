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
		e = e.sanitize()
		if e.Metadata != nil {
			if bb, err := json.Marshal(e.Metadata); err == nil {
				meta = ValidText(string(bb))
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
	c := ClipUTF8(e.Content, maxContent)
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

// DeleteOrphanRelationships removes every edge whose endpoint is no longer a
// live element. The watermark bumps only when something actually went.
func (s *Store) DeleteOrphanRelationships() (int, error) {
	res, err := s.db.Exec(`DELETE FROM relationships
		WHERE source_qualified NOT IN (SELECT qualified_name FROM code_elements)
		   OR target_qualified NOT IN (SELECT qualified_name FROM code_elements)`)
	if err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, err
	}
	if n == 0 {
		return 0, nil
	}
	return int(n), s.BumpWatermark()
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

// FindByNameToken ranks elements whose SYMBOL NAME contains an identifier-shaped
// token of the query, shortest (most specific) name first. It is the ArmName
// ranking arm, and it reads the name alone: the FTS arm reads
// name + qualified_name + content but only for words that MATCH, and a vector
// embedding reads the body — so a question that paraphrases ("cut a string to
// n runes") or names a two-line helper whose body says nothing useful
// (`coverage`) reaches neither.
//
// The tokens become quoted LIKE patterns, so user input can never inject SQL,
// and a token must be identifier-shaped (see nameTokens) so ordinary prose never
// summons this arm and fills the result with every element that has a common
// word in its name. Matching is case-insensitive on both sides because Go symbol
// names and an agent's question do not agree about case.
//
// The score is 0 for every hit: this arm does not rank by RELEVANCE, it ranks by
// specificity (shortest name first), and the fusion only needs the order.
func (s *Store) FindByNameToken(query string, limit int) ([]FuzzyMatch, error) {
	freq, distinct, err := s.nameTokenFreq()
	if err != nil {
		return nil, err
	}
	toks := nameTokens(query, freq, distinct)
	if len(toks) == 0 {
		return nil, nil
	}
	if limit <= 0 {
		limit = 50
	}
	var where []string
	var args []any
	for _, t := range toks {
		pat := "%" + t + "%"
		where = append(where, "(ce.name LIKE ? COLLATE NOCASE OR ce.qualified_name LIKE ? COLLATE NOCASE)")
		args = append(args, pat, pat)
	}
	args = append(args, limit)
	rows, err := s.db.Query(`SELECT `+elementCols+`, 0.0 AS score
		FROM code_elements ce
		WHERE `+strings.Join(where, " OR ")+`
		ORDER BY length(ce.name) ASC, length(ce.qualified_name) ASC
		LIMIT ?`, args...)
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

// nameTokens extracts the tokens of a query that are worth searching for in a
// SYMBOL NAME. It is the gate that keeps the name arm from being a noise
// machine, and it had to get much more permissive than a shape test: measured
// against the 30 labelled questions in docs/retrieval-label-set.md, a
// capital/underscore/digit shape test let the arm fire on 2 of 30 — because
// agents ask in PROSE ("take the single-flight lock for embedding") and the
// informative token is lowercase (`lock`, `stamp`, `runes`).
//
// Two filters, in order:
//
//  1. length and duplicates — a token must be at least 3 characters, so "go"
//     and "a" never become name searches;
//  2. RARITY IN THIS CORPUS. A token that appears inside a large number of
//     symbol names ("read" in 142 of them, "text" in 210) discriminates nothing,
//     and searching on it would return a wall of near-identical names. A token
//     that is rare is the one an agent typed because it is the answer's name.
//
// The rarity bound is what replaced the stop-list, and it is strictly better:
// a stop-list of English words can never be complete, and every word it omits is
// a real symbol name somewhere (`lock`, `stamp`, `list`, `run`). Rarity is
// measured from the store itself, so it adapts to the project rather than to
// the author's guesses about English.
// total is the ELEMENT count (not the distinct-name count): see the bound below.
func nameTokens(query string, freq map[string]int, total int) []string {
	stop := stopWords
	fields := strings.FieldsFunc(query, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsNumber(r) && r != '_' && r != '.'
	})
	// A token must be rare among symbol names to count. The bound is a
	// fraction of the ELEMENTS, not of the distinct names: a fixture corpus of
	// three elements would otherwise make every English word "rare" and the
	// arm would fire on prose, which is exactly the noise it must not produce.
	// total is the element count, so the bound is scale-free.
	bound := total / 500
	if bound < 2 {
		bound = 2
	}
	seen := map[string]bool{}
	var out []string
	for _, f := range fields {
		low := strings.ToLower(f)
		if len(f) < 3 || seen[low] {
			continue
		}
		seen[low] = true
		if stop[low] {
			continue
		}
		// Rare in this corpus. An UNSEEN token counts as rare (it is how "runes"
		// reaches an arm on a corpus that happens not to have it), which is why
		// the stop list above is not optional: rarity alone would let "the"
		// through on any project.
		if freq[low] > bound {
			continue
		}
		out = append(out, f)
	}
	return out
}

// stopWords is the SMALL set of function words that must never reach a name
// search. It is deliberately tiny and deliberately not "English": every word
// here is one a name arm must reject regardless of how rare it is in the
// corpus, and every word NOT here gets the rarity test instead. A larger list
// would re-introduce the failure this design exists to avoid — dropping a real
// symbol name because the author guessed it was an English word.
var stopWords = map[string]bool{
	"the": true, "and": true, "for": true, "not": true, "but": true,
	"you": true, "your": true, "its": true, "are": true, "was": true, "were": true,
	"can": true, "did": true, "has": true, "had": true, "out": true,
	"own": true, "same": true, "too": true, "very": true, "just": true, "into": true,
	"that": true, "this": true, "then": true, "than": true, "when": true,
	"how": true, "what": true, "where": true, "which": true, "who": true, "why": true,
	"with": true, "from": true, "use": true, "used": true, "using": true,
	"one": true, "two": true, "all": true, "any": true, "some": true, "each": true,
	"does": true, "get": true, "via": true, "per": true, "upon": true, "there": true, "here": true,
}

// nameTokenFreq returns how many elements carry each distinct symbol name, and
// the total ELEMENT count (the rarity bound's denominator). It is the table
// nameTokens consults, and it is one GROUP BY over the names it needs — cheap
// enough to run per scoped arm, and never on a miss (an arm with no tokens does
// no work at all).
func (s *Store) nameTokenFreq() (map[string]int, int, error) {
	rows, err := s.db.Query(`SELECT name, COUNT(*) FROM code_elements GROUP BY name`)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	freq := map[string]int{}
	elements := 0
	for rows.Next() {
		var name string
		var n int
		if err := rows.Scan(&name, &n); err != nil {
			return nil, 0, err
		}
		freq[strings.ToLower(name)] += n
		elements += n
	}
	return freq, elements, rows.Err()
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
