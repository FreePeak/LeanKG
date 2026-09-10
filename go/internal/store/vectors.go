package store

import (
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
	"time"
)

// ModelStamp pins a vector collection to one embedding model. Ported from
// FR-ZCP-11: dimension-only checks are insufficient; the full stamp must
// match or the query side degrades to L2 and writers hard-fail.
type ModelStamp struct {
	ModelID    string `json:"model_id"`
	Revision   string `json:"revision"`
	Dimensions int    `json:"dimensions"`
	Distance   string `json:"distance"` // cosine
	Provider   string `json:"provider"`
}

// VectorRow is one vector to persist: qualified name plus float32 components.
type VectorRow struct {
	QualifiedName string
	Vec           []float32
}

// WriteStamp upserts the stamp for a model (writer side, after validation).
func (s *Store) WriteStamp(st ModelStamp) error {
	if _, err := s.db.Exec(`INSERT INTO emb_stamp (model_id, revision, dimensions, distance, provider)
		VALUES (?,?,?,?,?)
		ON CONFLICT(model_id) DO UPDATE SET revision=excluded.revision,
			dimensions=excluded.dimensions, distance=excluded.distance, provider=excluded.provider`,
		st.ModelID, st.Revision, st.Dimensions, st.Distance, st.Provider); err != nil {
		return fmt.Errorf("store: write stamp %s: %w", st.ModelID, err)
	}
	return s.BumpWatermark()
}

// Stamp returns the persisted stamp for a model; nil when none exists.
func (s *Store) Stamp(modelID string) (*ModelStamp, error) {
	var st ModelStamp
	err := s.db.QueryRow(`SELECT model_id, revision, dimensions, distance, provider FROM emb_stamp WHERE model_id = ?`, modelID).
		Scan(&st.ModelID, &st.Revision, &st.Dimensions, &st.Distance, &st.Provider)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &st, nil
}

// EmbeddingStateMap returns qualified_name -> content_hash for a model.
func (s *Store) EmbeddingStateMap(modelID string) (map[string]string, error) {
	rows, err := s.db.Query(`SELECT qualified_name, content_hash FROM embedding_state WHERE model_id = ?`, modelID)
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
func (s *Store) SetEmbeddingStates(modelID string, states map[string]string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	for qn, h := range states {
		if _, err := tx.Exec(`INSERT INTO embedding_state (model_id, qualified_name, content_hash, state, embedded_at)
			VALUES (?,?,?,'embedded',strftime('%Y-%m-%dT%H:%M:%fZ','now'))
			ON CONFLICT(model_id, qualified_name) DO UPDATE SET content_hash=excluded.content_hash,
				state='embedded', embedded_at=excluded.embedded_at`, modelID, qn, h); err != nil {
			return fmt.Errorf("store: set embedding state %s: %w", qn, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	return s.BumpWatermark()
}

// UpsertVectors writes a batch of vectors for a model in ONE transaction:
// a crash mid-run leaves the previous state consistent and reruns resume
// (issue #368 AC: batch upsert semantics).
func (s *Store) UpsertVectors(modelID string, rows []VectorRow) error {
	if len(rows) == 0 {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	for _, r := range rows {
		if _, err := tx.Exec(`INSERT INTO embedding_vectors (model_id, qualified_name, vec) VALUES (?,?,?)
			ON CONFLICT(model_id, qualified_name) DO UPDATE SET vec=excluded.vec`,
			modelID, r.QualifiedName, encodeVec(r.Vec)); err != nil {
			return fmt.Errorf("store: upsert vector %s: %w", r.QualifiedName, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	return s.BumpWatermark()
}

// ClearVectors drops all vectors + state for a model (stamp-guard rebuild).
func (s *Store) ClearVectors(modelID string) error {
	for _, stmt := range []string{
		`DELETE FROM embedding_vectors WHERE model_id = ?`,
		`DELETE FROM embedding_state WHERE model_id = ?`,
	} {
		if _, err := s.db.Exec(stmt, modelID); err != nil {
			return err
		}
	}
	return s.BumpWatermark()
}

// VectorCount returns the vector count for a model.
func (s *Store) VectorCount(modelID string) (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM embedding_vectors WHERE model_id = ?`, modelID).Scan(&n)
	return n, err
}

// VectorSearchHit is one L3 hit with cosine similarity.
type VectorSearchHit struct {
	Element    Element
	Similarity float64
}

// vectorSelectCols hydrates L3 hits: vector QN always present; element fields
// fall back to the QN when no code element matches (docs, memory blobs).
const vectorSelectCols = `embedding_vectors.qualified_name,
	COALESCE(ce.element_type,''), COALESCE(ce.name, embedding_vectors.qualified_name),
	COALESCE(ce.file_path,''), COALESCE(ce.line_start,0), COALESCE(ce.line_end,0),
	COALESCE(ce.language,''), COALESCE(ce.parent_qualified,''), COALESCE(ce.content,''),
	COALESCE(ce.metadata,'{}')`

// SearchVectors performs the single ANN-equivalent shape: in-process cosine
// over the model's vectors, hydrated against code_elements.
// ponytail: O(n) scan per query — fine to ~100k vectors at 384 dims (~0.15s
// on M2 Pro); upgrade path is sqlite-vec when scale demands it.
func (s *Store) SearchVectors(modelID string, q []float32, k int) ([]VectorSearchHit, error) {
	if len(q) == 0 {
		return nil, nil
	}
	rows, err := s.db.Query(`SELECT `+vectorSelectCols+`, embedding_vectors.vec
		FROM embedding_vectors LEFT JOIN code_elements ce ON ce.qualified_name = embedding_vectors.qualified_name
		WHERE embedding_vectors.model_id = ?`, modelID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	type scored struct {
		el  Element
		val float64
	}
	var hits []scored
	var buf []byte
	for rows.Next() {
		var el Element
		var meta string
		buf = buf[:0]
		if err := rows.Scan(&el.QualifiedName, &el.ElementType, &el.Name, &el.FilePath, &el.LineStart, &el.LineEnd,
			&el.Language, &el.ParentQualified, &el.Content, &meta, &buf); err != nil {
			return nil, err
		}
		if meta != "" && meta != "{}" {
			_ = json.Unmarshal([]byte(meta), &el.Metadata)
		}
		v := decodeVec(buf)
		if len(v) != len(q) {
			continue // stamped collection mismatch should never reach here; skip defensively
		}
		hits = append(hits, scored{el: el, val: cosine(q, v)})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// partial sort top-k (k is small; n scan already done)
	for i := 0; i < len(hits) && i < k; i++ {
		best := i
		for j := i + 1; j < len(hits); j++ {
			if hits[j].val > hits[best].val {
				best = j
			}
		}
		hits[i], hits[best] = hits[best], hits[i]
	}
	if len(hits) > k {
		hits = hits[:k]
	}
	out := make([]VectorSearchHit, len(hits))
	for i, h := range hits {
		out[i] = VectorSearchHit{Element: h.el, Similarity: h.val}
	}
	return out, nil
}

func encodeVec(v []float32) []byte {
	b := make([]byte, 4*len(v))
	for i, f := range v {
		binary.LittleEndian.PutUint32(b[i*4:], math.Float32bits(f))
	}
	return b
}

func decodeVec(b []byte) []float32 {
	if len(b)%4 != 0 {
		return nil
	}
	v := make([]float32, len(b)/4)
	for i := range v {
		v[i] = math.Float32frombits(binary.LittleEndian.Uint32(b[i*4:]))
	}
	return v
}

func cosine(a, b []float32) float64 {
	var dot, na, nb float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		na += float64(a[i]) * float64(a[i])
		nb += float64(b[i]) * float64(b[i])
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return dot / (math.Sqrt(na) * math.Sqrt(nb))
}

// Watermark returns the current freshness sequence number.
func (s *Store) Watermark() (int64, int64, error) {
	var seq, at int64
	err := s.db.QueryRow(`SELECT seq, at FROM write_watermark WHERE id = 1`).Scan(&seq, &at)
	return seq, at, err
}

// BumpWatermark advances the freshness sequence (one call per commit batch).
func (s *Store) BumpWatermark() error {
	if s.mode == RO {
		return nil // readers never bump
	}
	_, err := s.db.Exec(`UPDATE write_watermark SET seq = seq + 1, at = ? WHERE id = 1`, time.Now().Unix())
	return err
}

// Inventory is the index summary surfaced by the status tool.
type Inventory struct {
	TotalElements      int            `json:"total_elements"`
	TotalFiles         int            `json:"total_files"`
	TotalRelationships int            `json:"total_relationships"`
	TotalVectors       int            `json:"total_vectors"`
	ElementsByType     map[string]int `json:"elements_by_type"`
	// LastInventorySeq is the watermark seq at inventory-compute time; readers
	// compare it against Watermark() to derive fresh | possibly_stale.
	LastInventorySeq int64 `json:"last_inventory_seq"`
	ComputedAt       int64 `json:"computed_at"`
}

// SaveInventory persists the inventory under kv with the current watermark.
func (s *Store) SaveInventory(inv Inventory) error {
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
	_, err = s.db.Exec(`INSERT INTO kv (key, value) VALUES ('inventory', ?)
		ON CONFLICT(key) DO UPDATE SET value=excluded.value`, string(b))
	return err
}

// LoadInventory returns the persisted inventory; nil when never computed.
func (s *Store) LoadInventory() (*Inventory, error) {
	var val string
	err := s.db.QueryRow(`SELECT value FROM kv WHERE key = 'inventory'`).Scan(&val)
	if err == sql.ErrNoRows {
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

// EmbedRun is one recorded leankg-embed run (read by the status tool).
type EmbedRun struct {
	ID          int64   `json:"id"`
	ModelID     string  `json:"model_id"`
	Mode        string  `json:"mode"`
	StartedAt   string  `json:"started_at"`
	FinishedAt  *string `json:"finished_at"`
	Dirty       int     `json:"dirty"`
	Embedded    int     `json:"embedded"`
	Skipped     int     `json:"skipped"`
	Failed      int     `json:"failed"`
	Truncations int     `json:"truncations"`
	Orphans     int     `json:"orphans"`
	Status      string  `json:"status"`
}

// StartEmbedRun records a running pipeline run and returns its id.
func (s *Store) StartEmbedRun(modelID, mode string, dirty int) (int64, error) {
	res, err := s.db.Exec(`INSERT INTO embed_runs (model_id, mode, started_at, dirty, status) VALUES (?,?,strftime('%Y-%m-%dT%H:%M:%fZ','now'),?,'running')`, modelID, mode, dirty)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// FinishEmbedRun closes a run record with the final counters.
func (s *Store) FinishEmbedRun(id int64, status string, embedded, skipped, failed, truncations, orphans int) error {
	_, err := s.db.Exec(`UPDATE embed_runs SET finished_at=strftime('%Y-%m-%dT%H:%M:%fZ','now'),
		embedded=?, skipped=?, failed=?, truncations=?, orphans=?, status=? WHERE id=?`,
		embedded, skipped, failed, truncations, orphans, status, id)
	return err
}

// LastEmbedRun returns the most recent run for a model; nil when none.
func (s *Store) LastEmbedRun(modelID string) (*EmbedRun, error) {
	row := s.db.QueryRow(`SELECT id, model_id, mode, started_at, finished_at, dirty, embedded, skipped, failed, truncations, orphans, status
		FROM embed_runs WHERE model_id = ? ORDER BY id DESC LIMIT 1`, modelID)
	var r EmbedRun
	err := row.Scan(&r.ID, &r.ModelID, &r.Mode, &r.StartedAt, &r.FinishedAt, &r.Dirty, &r.Embedded, &r.Skipped, &r.Failed, &r.Truncations, &r.Orphans, &r.Status)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &r, nil
}
