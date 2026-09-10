package embed

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/FreePeak/LeanKG/go/internal/store"
)

// exportLine is one NDJSON export record. content_hash lets a store resume
// an import: unchanged QNs are skipped instead of rewritten.
type exportLine struct {
	QualifiedName string `json:"qualified_name"`
	ContentHash   string `json:"content_hash"`
	Text          string `json:"text"`
}

// importLine is one NDJSON import record: a pre-computed vector.
type importLine struct {
	QualifiedName string    `json:"qualified_name"`
	Vec           []float32 `json:"vec"`
}

// ExportNDJSON writes every element as one JSON line
// {"qualified_name":..., "content_hash":..., "text":...} — the offsite
// embedding workflow's input format (export → embed elsewhere → import).
func ExportNDJSON(ctx context.Context, st store.Backend, modelID string, w io.Writer) error {
	elems, err := readElements(st)
	if err != nil {
		return err
	}
	bw := bufio.NewWriter(w)
	enc := json.NewEncoder(bw)
	for _, e := range elems {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := enc.Encode(exportLine{QualifiedName: e.QN, ContentHash: contentHashHex(e.Content), Text: e.Content}); err != nil {
			return fmt.Errorf("embed: export %s: %w", e.QN, err)
		}
	}
	if err := bw.Flush(); err != nil {
		return fmt.Errorf("embed: export flush: %w", err)
	}
	return nil
}

// ImportNDJSON reads {"qualified_name":..., "vec":[floats]} lines and writes
// them as the given model's vectors with the given stamp (a mismatched
// collection is cleared first — same guard as a full Run). Dim-guard: a line
// whose vector length differs from dims is a hard error for that line.
// Resume: a line is skipped when the stored embedding_state content_hash for
// its QN already equals the element's current content hash. A line whose QN
// has no live element counts as an orphan and is not written.
func ImportNDJSON(ctx context.Context, st store.Backend, modelID, revision, distance string, dims int, r io.Reader) (Report, error) {
	start := time.Now()
	rep := Report{Mode: "import", Backend: st.Engine()}

	want := store.ModelStamp{ModelID: modelID, Revision: revision, Dimensions: dims, Distance: distance, Provider: "ndjson"}
	cur, err := st.Stamp(modelID)
	if err != nil {
		return rep, err
	}
	if cur != nil && *cur != want {
		if err := st.ClearVectors(modelID); err != nil {
			return rep, err
		}
		cur = nil
	}
	if cur == nil {
		if err := st.WriteStamp(want); err != nil {
			return rep, err
		}
	}

	elems, err := readElements(st)
	if err != nil {
		return rep, err
	}
	elementByQN := make(map[string]string, len(elems)) // qn -> current content hash
	for _, e := range elems {
		elementByQN[e.QN] = contentHashHex(e.Content)
	}
	resume, err := st.EmbeddingStateMap(modelID)
	if err != nil {
		return rep, err
	}

	runID, err := st.StartEmbedRun(modelID, "import", 0)
	if err != nil {
		return rep, err
	}
	fail := func(err error) (Report, error) {
		_ = st.FinishEmbedRun(runID, "failed", rep.Embedded, rep.Skipped, rep.Failed, rep.Truncations, rep.Orphans)
		return rep, err
	}

	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	rows := make([]store.VectorRow, 0, batchSize)
	states := make(map[string]string)
	flush := func() error {
		if len(rows) == 0 {
			return nil
		}
		if err := st.UpsertVectors(modelID, rows); err != nil {
			return err
		}
		if err := st.SetEmbeddingStates(modelID, states); err != nil {
			return err
		}
		rows = rows[:0]
		states = make(map[string]string)
		return nil
	}

	for sc.Scan() {
		if err := ctx.Err(); err != nil {
			return fail(err)
		}
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var in importLine
		if err := json.Unmarshal(line, &in); err != nil {
			return fail(fmt.Errorf("embed: import %q: %w", snippet(line), err))
		}
		if in.QualifiedName == "" {
			return fail(fmt.Errorf("embed: import line with empty qualified_name"))
		}
		if len(in.Vec) != dims {
			return fail(fmt.Errorf("embed: import %s: %d dims, want %d", in.QualifiedName, len(in.Vec), dims))
		}
		for _, x := range in.Vec {
			if x != x || x > 1e38 || x < -1e38 { // NaN/Inf guard
				return fail(fmt.Errorf("embed: import %s: vector contains NaN/Inf", in.QualifiedName))
			}
		}
		curHash, live := elementByQN[in.QualifiedName]
		if !live {
			// No live element for this QN: the vector cannot be written.
			rep.Orphans++
			continue
		}
		if resume[in.QualifiedName] == curHash {
			rep.Skipped++
			continue
		}
		rows = append(rows, store.VectorRow{QualifiedName: in.QualifiedName, Vec: in.Vec})
		states[in.QualifiedName] = curHash
		rep.Embedded++
		if len(rows) >= batchSize {
			if err := flush(); err != nil {
				return fail(err)
			}
		}
	}
	if err := sc.Err(); err != nil {
		return fail(err)
	}
	if err := flush(); err != nil {
		return fail(err)
	}

	// Add stored orphans (vectors whose element no longer exists) to any
	// input orphans counted above — the two sets are disjoint.
	covered, dbOrphans, err := readVectorCounts(st, modelID)
	if err != nil {
		return fail(err)
	}
	rep.Orphans += dbOrphans
	rep.Coverage = coverage(covered, len(elems))
	rep.Duration = time.Since(start)
	if err := st.FinishEmbedRun(runID, "ok", rep.Embedded, rep.Skipped, rep.Failed, rep.Truncations, rep.Orphans); err != nil {
		return rep, err
	}
	return rep, nil
}
