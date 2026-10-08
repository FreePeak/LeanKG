package memory

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"math"
	"sort"
	"strings"
	"time"
	"unicode"
)

// Dense recall (RS-19). BM25 finds memories that share words with the
// question; it cannot find "We store embeddings in pgvector" for "which
// database holds the vectors". When the server has a query embedder, bank
// rows are embedded lazily and recall fuses the BM25 ranking with a cosine
// ranking by reciprocal rank fusion. A row that matches on meaning alone is
// admitted only above the collection's calibrated noise floor (the best score
// off-topic text reaches), so an unrelated question still recalls nothing —
// recalled rows are injected into agent prompts, where noise is a cost.

// Vectorizer is the dense arm a Memory can be given.
type Vectorizer interface {
	// Key identifies the vector space; rows embedded under another key are
	// ignored and re-embedded.
	Key() string
	EmbedDocuments(ctx context.Context, texts []string) ([][]float32, error)
	EmbedQuery(ctx context.Context, text string) ([]float32, error)
	// Floor is the calibrated noise floor; without one the dense arm is off.
	Floor() (float64, bool)
}

// SetVectorizer enables dense recall (nil disables it).
func (m *Memory) SetVectorizer(v Vectorizer) {
	m.vecMu.Lock()
	m.vec = v
	m.vecMu.Unlock()
}

func (m *Memory) vectorizer() Vectorizer {
	m.vecMu.Lock()
	defer m.vecMu.Unlock()
	return m.vec
}

// denseMinWords gates the dense arm to natural-language questions. The
// noise floor is calibrated on sentence-length probes; a one- or two-word
// query embeds close to almost everything (validation: "alpha" recalled
// "gamma deploy note"), while BM25 is already precise for keyword queries.
const denseMinWords = 3

func wordCount(q string) int {
	return len(strings.FieldsFunc(q, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }))
}

const (
	denseEmbedBatch  = 32
	denseEmbedPerRun = 256 // rows embedded per recall call; the rest catch up on later calls
	rrfK             = 60
)

// ensureVectors embeds bank rows of the given banks that have no vector in
// v's space yet, at most denseEmbedPerRun per call.
func (m *Memory) ensureVectors(ctx context.Context, v Vectorizer, banks []string) error {
	key := v.Key()
	args := make([]any, 0, len(banks)+2)
	marks := make([]string, 0, len(banks))
	for _, b := range banks {
		args = append(args, sanitizeBank(b))
		marks = append(marks, "?")
	}
	args = append(args, key, denseEmbedPerRun) // SQL order: banks…, model, limit
	rows, err := m.fts.Query(`SELECT f.bank, f.entry_id, f.body FROM memory_fts f
		WHERE f.kind = 'bank' AND f.bank IN (`+strings.Join(marks, ",")+`)
		  AND NOT EXISTS (SELECT 1 FROM memory_vectors v WHERE v.model = ? AND v.bank = f.bank AND v.entry_id = f.entry_id)
		LIMIT ?`, args...)
	if err != nil {
		return err
	}
	type pending struct{ bank, id, body string }
	var todo []pending
	for rows.Next() {
		var p pending
		if err := rows.Scan(&p.bank, &p.id, &p.body); err != nil {
			rows.Close()
			return err
		}
		todo = append(todo, p)
	}
	rows.Close()
	for i := 0; i < len(todo); i += denseEmbedBatch {
		batch := todo[i:min(i+denseEmbedBatch, len(todo))]
		texts := make([]string, len(batch))
		for j, p := range batch {
			texts[j] = p.body
		}
		vecs, err := v.EmbedDocuments(ctx, texts)
		if err != nil || len(vecs) != len(batch) {
			return err
		}
		for j, p := range batch {
			if _, err := m.fts.Exec(`INSERT OR REPLACE INTO memory_vectors (model, bank, entry_id, vec) VALUES (?, ?, ?, ?)`,
				key, p.bank, p.id, encodeVec(vecs[j])); err != nil {
				return err
			}
		}
	}
	return nil
}

// denseCandidates returns the bank rows whose similarity to the query beats
// the noise floor, best first.
func (m *Memory) denseCandidates(v Vectorizer, banks []string, query string) []Entry {
	floor, ok := v.Floor()
	if !ok || wordCount(query) < denseMinWords {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := m.ensureVectors(ctx, v, banks); err != nil {
		return nil // dense arm unavailable: BM25 still answers
	}
	qv, err := v.EmbedQuery(ctx, query)
	if err != nil || len(qv) == 0 {
		return nil
	}
	args := []any{v.Key()}
	marks := make([]string, 0, len(banks))
	for _, b := range banks {
		args = append(args, sanitizeBank(b))
		marks = append(marks, "?")
	}
	rows, err := m.fts.Query(`SELECT f.doc, v.vec FROM memory_vectors v
		JOIN memory_fts f ON f.kind = 'bank' AND f.bank = v.bank AND f.entry_id = v.entry_id
		WHERE v.model = ? AND v.bank IN (`+strings.Join(marks, ",")+`)`, args...)
	if err != nil {
		return nil
	}
	defer rows.Close()
	type scored struct {
		e   Entry
		sim float64
	}
	var out []scored
	for rows.Next() {
		var doc string
		var blob []byte
		if rows.Scan(&doc, &blob) != nil {
			continue
		}
		sim := cosine(qv, decodeVec(blob))
		if sim <= floor {
			continue
		}
		var e Entry
		if json.Unmarshal([]byte(doc), &e) == nil {
			out = append(out, scored{e, sim})
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].sim > out[j].sim })
	entries := make([]Entry, len(out))
	for i, s := range out {
		entries[i] = s.e
	}
	return entries
}

// fuseRRF merges ranked entry lists by reciprocal rank fusion (k=60), ties
// broken by first appearance; entries are keyed by id.
func fuseRRF(lists ...[]Entry) []Entry {
	score := map[string]float64{}
	first := map[string]Entry{}
	var order []string
	for _, l := range lists {
		for i, e := range l {
			if _, ok := first[e.ID]; !ok {
				first[e.ID] = e
				order = append(order, e.ID)
			}
			score[e.ID] += 1 / float64(rrfK+i+1)
		}
	}
	sort.SliceStable(order, func(i, j int) bool { return score[order[i]] > score[order[j]] })
	out := make([]Entry, len(order))
	for i, id := range order {
		out[i] = first[id]
	}
	return out
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
	if len(a) != len(b) || len(a) == 0 {
		return -1
	}
	var dot, na, nb float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		na += float64(a[i]) * float64(a[i])
		nb += float64(b[i]) * float64(b[i])
	}
	if na == 0 || nb == 0 {
		return -1
	}
	return dot / math.Sqrt(na*nb)
}
