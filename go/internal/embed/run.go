package embed

import (
	"context"
	"fmt"
	"time"
	"unicode/utf8"

	"github.com/FreePeak/LeanKG/go/internal/store"
)

// batchSize is the number of texts sent to the provider per call.
const batchSize = 32

// Report summarizes one embedding run.
type Report struct {
	Mode        string
	Dirty       int
	Embedded    int
	Skipped     int
	Failed      int
	Truncations int
	Orphans     int
	Coverage    float64
	Duration    time.Duration
}

// dirtyItem is one element queued for embedding: qualified name, the
// (possibly truncated) text to send, and the full-content hash to record.
type dirtyItem struct{ qn, text, hash string }

// Run executes the embedding pipeline against st with provider p.
// mode is "incremental" (embed elements whose content SHA-256 differs from
// the stored embedding state) or "full" (embed everything, after the stamp
// guard clears a mismatched collection). A provider transport error fails
// the run; a batch failing validation (count/dims/finiteness) is counted in
// Report.Failed and the run continues to status partial.
func Run(ctx context.Context, st *store.Store, p Provider, mode string) (Report, error) {
	start := time.Now()
	rep := Report{Mode: mode}
	if mode != "incremental" && mode != "full" {
		return rep, fmt.Errorf("embed: unknown mode %q (want incremental|full)", mode)
	}
	modelID := p.ModelID()
	want := store.ModelStamp{ModelID: modelID, Revision: p.Revision(), Dimensions: p.Dimensions(), Distance: p.Distance(), Provider: p.Provider()}
	// Stamp guard (FR-ZCP-11 contract): every vector writer enforces the
	// same hard-rebuild rule.
	//   full        + mismatch ⇒ clear + re-stamp (rebuild, never mixed)
	//   incremental + mismatch ⇒ HARD FAIL with a rebuild directive — a
	//                 flag slip (provider A full, provider B incremental)
	//                 must not silently wipe A's vectors
	//   no stamp    ⇒ write (first build, both modes)
	cur, err := st.Stamp(modelID)
	if err != nil {
		return rep, err
	}
	if cur != nil && *cur != want {
		if mode != "full" {
			return rep, fmt.Errorf(
				"embed: stamp mismatch for %s (stored revision %q, provider stamp revision %q) — run `leankg-embed full` to rebuild the collection",
				modelID, cur.Revision, want.Revision)
		}
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

	// Plan: dirty = elements whose content hash differs from embedding_state
	// (incremental) or all elements (full).
	elems, err := readElements(ctx, st.Path())
	if err != nil {
		return rep, err
	}
	states, err := st.EmbeddingStateMap(modelID)
	if err != nil {
		return rep, err
	}
	dirty := make([]dirtyItem, 0, len(elems))
	for _, e := range elems {
		h := contentHashHex(e.Content)
		if mode == "incremental" && states[e.QN] == h {
			rep.Skipped++
			continue
		}
		text := e.Content
		if utf8.RuneCountInString(text) > maxContentChars {
			rep.Truncations++
			text = truncateRunes(text, maxContentChars)
		}
		dirty = append(dirty, dirtyItem{qn: e.QN, text: text, hash: h})
	}
	rep.Dirty = len(dirty)

	runID, err := st.StartEmbedRun(modelID, mode, rep.Dirty)
	if err != nil {
		return rep, err
	}

	status := "ok"
	if err := embedBatches(ctx, st, p, modelID, dirty, &rep); err != nil {
		rep.Duration = time.Since(start)
		_ = st.FinishEmbedRun(runID, "failed", rep.Embedded, rep.Skipped, rep.Failed, rep.Truncations, rep.Orphans)
		return rep, err
	}

	covered, orphans, err := readVectorCounts(ctx, st.Path(), modelID)
	if err != nil {
		rep.Duration = time.Since(start)
		_ = st.FinishEmbedRun(runID, "failed", rep.Embedded, rep.Skipped, rep.Failed, rep.Truncations, rep.Orphans)
		return rep, err
	}
	rep.Orphans = orphans
	rep.Coverage = coverage(covered, len(elems))
	if rep.Failed > 0 {
		status = "partial"
	}
	rep.Duration = time.Since(start)
	if err := st.FinishEmbedRun(runID, status, rep.Embedded, rep.Skipped, rep.Failed, rep.Truncations, rep.Orphans); err != nil {
		return rep, err
	}
	return rep, nil
}

// embedBatches invokes the provider in batches of 32 and writes each
// successful batch (vectors + embedding state) crash-consistently. A batch
// failing validation is counted in rep.Failed and the run continues.
func embedBatches(ctx context.Context, st *store.Store, p Provider, modelID string, dirty []dirtyItem, rep *Report) error {
	for i := 0; i < len(dirty); i += batchSize {
		if err := ctx.Err(); err != nil {
			return err
		}
		end := min(i+batchSize, len(dirty))
		batch := dirty[i:end]

		texts := make([]string, len(batch))
		for j, d := range batch {
			texts[j] = d.text
		}
		vecs, err := p.Embed(ctx, Document, texts)
		if err != nil {
			return fmt.Errorf("embed: provider %s: %w", modelID, err)
		}
		if err := validateBatch(vecs, len(texts), p.Dimensions()); err != nil {
			rep.Failed += len(batch)
			continue
		}

		rows := make([]store.VectorRow, len(batch))
		states := make(map[string]string, len(batch))
		for j, d := range batch {
			rows[j] = store.VectorRow{QualifiedName: d.qn, Vec: vecs[j]}
			states[d.qn] = d.hash
		}
		if err := st.UpsertVectors(modelID, rows); err != nil {
			return err
		}
		if err := st.SetEmbeddingStates(modelID, states); err != nil {
			return err
		}
		rep.Embedded += len(batch)
	}
	return nil
}

func truncateRunes(s string, n int) string {
	r := []rune(s)
	return string(r[:n])
}

// coverage is the fraction of live elements holding vectors; a store with
// zero elements is vacuously fully covered.
func coverage(covered, total int) float64 {
	if total == 0 {
		return 1
	}
	return float64(covered) / float64(total)
}
