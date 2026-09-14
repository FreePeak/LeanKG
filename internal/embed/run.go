package embed

import (
	"context"
	"fmt"
	"time"
	"unicode/utf8"

	"github.com/FreePeak/LeanKG/internal/store"
)

// ChunkerVersion versions the pipeline that turns a stored element into the
// text a model embeds: per-element chunks keyed on qualified_name, the
// maxContentChars rune cap, and the catalog's query/document prefix pair
// applied at the provider boundary.
//
// Bump it on ANY change to that construction. The value is carried on every
// collection stamp, so a bump is a rebuild directive (incremental writers hard
// fail, `leankg-embed full` clears and re-embeds) rather than a silent mix of
// vectors built from differently-cut text — the same rule the reference applies
// (src/embeddings/text_blob.rs CHUNKER_VERSION), enforced through the stamp
// here instead of the content hash because Go's content_hash is also the
// NDJSON export/resume key and must stay stable.
//
// v2: the local provider family embeds under a model-context budget
// (maxLocalTextChars) instead of the general cap — text a 512-token sidecar
// can actually accept. Bumping the version rebuilds collections stamped v1,
// where over-budget elements had no vectors at all (the run died on them).
const ChunkerVersion = 2

// batchSize is the number of texts sent to the provider per call.
const batchSize = 32

// Report summarizes one embedding run.
type Report struct {
	Mode        string
	Backend     string `json:"backend,omitempty"` // engine that produced the run
	Dirty       int
	Embedded    int
	Skipped     int
	Failed      int
	Truncations int
	Orphans     int
	Coverage    float64
	Duration    time.Duration
}

// dirtyItem is one element queued for embedding: the file it came from (the
// atomic write unit), its qualified name, the (possibly truncated) text to
// send, and the full-content hash to record.
type dirtyItem struct{ file, qn, text, hash string }

// StampOf composes the collection identity provider p writes with: p's five
// identity fields plus the two pipeline components a provider does not carry
// itself — the chunker version and the catalog prefix pair resolved from the
// model id. Every vector writer AND the L3 query guard compare this value, so
// chunker or prefix drift is caught exactly like a revision change.
func StampOf(p Provider) store.ModelStamp {
	q, d := Prefixes(p.ModelID())
	return store.ModelStamp{
		ModelID:        p.ModelID(),
		Revision:       p.Revision(),
		Dimensions:     p.Dimensions(),
		Distance:       p.Distance(),
		Provider:       p.Provider(),
		ChunkerVersion: ChunkerVersion,
		QueryPrefix:    q,
		DocumentPrefix: d,
	}
}

// stampFor is the writer-side stamp for an explicit identity (the NDJSON
// import path, which has no live provider but must produce the identical stamp
// a provider run would for the same model).
func stampFor(modelID, revision, distance, provider string, dims int) store.ModelStamp {
	q, d := Prefixes(modelID)
	return store.ModelStamp{
		ModelID:        modelID,
		Revision:       revision,
		Dimensions:     dims,
		Distance:       distance,
		Provider:       provider,
		ChunkerVersion: ChunkerVersion,
		QueryPrefix:    q,
		DocumentPrefix: d,
	}
}

// stampDrift names the stamp components that differ, so the rebuild directive
// says WHY the collection is unusable rather than dumping two structs.
func stampDrift(cur, want store.ModelStamp) string {
	var why []string
	if cur.Revision != want.Revision {
		why = append(why, fmt.Sprintf("revision %q -> %q", cur.Revision, want.Revision))
	}
	if cur.Dimensions != want.Dimensions {
		why = append(why, fmt.Sprintf("dimensions %d -> %d", cur.Dimensions, want.Dimensions))
	}
	if cur.Distance != want.Distance {
		why = append(why, fmt.Sprintf("distance %q -> %q", cur.Distance, want.Distance))
	}
	if cur.Provider != want.Provider {
		why = append(why, fmt.Sprintf("provider %q -> %q", cur.Provider, want.Provider))
	}
	if cur.ChunkerVersion != want.ChunkerVersion {
		why = append(why, fmt.Sprintf("chunker_version %d -> %d", cur.ChunkerVersion, want.ChunkerVersion))
	}
	if cur.QueryPrefix != want.QueryPrefix || cur.DocumentPrefix != want.DocumentPrefix {
		why = append(why, "query/document prefix pair")
	}
	if len(why) == 0 {
		return "identity"
	}
	return fmt.Sprintf("%s", join(why, ", "))
}

func join(parts []string, sep string) string {
	out := ""
	for i, p := range parts {
		if i > 0 {
			out += sep
		}
		out += p
	}
	return out
}

// Run executes the embedding pipeline against st with provider p.
// mode is "incremental" (embed elements whose content SHA-256 differs from
// the stored embedding state) or "full" (embed everything, after the stamp
// guard clears a mismatched collection). A provider transport error fails
// the run; a batch failing validation (count/dims/finiteness) is counted in
// Report.Failed and the run continues to status partial. Vectors and state
// land per file in one transaction (see embedFiles).
func Run(ctx context.Context, st store.Backend, p Provider, mode string) (Report, error) {
	start := time.Now()
	rep := Report{Mode: mode, Backend: st.Engine()}
	if mode != "incremental" && mode != "full" {
		return rep, fmt.Errorf("embed: unknown mode %q (want incremental|full)", mode)
	}
	modelID := p.ModelID()
	want := StampOf(p)
	// Stamp guard (FR-ZCP-11 contract): every vector writer enforces the
	// same hard-rebuild rule over the WHOLE identity — model, revision,
	// dims, distance, provider, chunker version and prefix pair.
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
				"embed: stamp mismatch for %s (%s) — run `leankg-embed full` to rebuild the collection",
				modelID, stampDrift(*cur, want))
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
	elems, err := readElements(st)
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
		textCap := maxContentChars
		if p.Provider() == "local" {
			textCap = min(textCap, maxLocalTextChars)
		}
		if utf8.RuneCountInString(text) > textCap {
			rep.Truncations++
			text = truncateRunes(text, textCap)
		}
		dirty = append(dirty, dirtyItem{file: e.File, qn: e.QN, text: text, hash: h})
	}
	rep.Dirty = len(dirty)

	runID, err := st.StartEmbedRun(modelID, mode, rep.Dirty)
	if err != nil {
		return rep, err
	}

	status := "ok"
	if err := embedFiles(ctx, st, p, modelID, groupByFile(dirty), &rep); err != nil {
		rep.Duration = time.Since(start)
		_ = st.FinishEmbedRun(runID, "failed", rep.Embedded, rep.Skipped, rep.Failed, rep.Truncations, rep.Orphans)
		return rep, err
	}

	covered, orphans, err := readVectorCounts(st, modelID)
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
	// The inventory snapshot carries TotalVectors, and this writer just moved
	// the watermark past it: without the refresh a fully embedded project is
	// reported possibly_stale until the next index. (Found by dogfooding the
	// embedding pipeline on this repo itself.)
	if _, err := store.RefreshInventory(st); err != nil {
		return rep, fmt.Errorf("embed: inventory snapshot: %w", err)
	}
	return rep, nil
}

// groupByFile partitions the dirty plan into per-file write units, preserving
// the element order (Elements() is ordered by qualified_name, so a file's
// elements are contiguous). readElements drops elements with no file (a
// malformed row) into the "" group, which the store still writes atomically.
func groupByFile(dirty []dirtyItem) [][]dirtyItem {
	var groups [][]dirtyItem
	for i := 0; i < len(dirty); {
		j := i + 1
		for j < len(dirty) && dirty[j].file == dirty[i].file {
			j++
		}
		groups = append(groups, dirty[i:j])
		i = j
	}
	return groups
}

// embedFiles embeds each file group and commits it in ONE transaction
// (store.ReplaceFileVectors): a crash mid-run can never leave a file with new
// vectors and stale state, or half its elements written. Provider calls are
// still capped at batchSize texts; a group larger than that is embedded in
// sub-batches and committed as one unit. A batch failing validation
// (count/dims/finiteness) is counted in rep.Failed and its items are simply
// not committed, so the file stays dirty and the run continues to partial.
//
// A batch that fails at the PROVIDER (e.g. an element that slipped the local
// text budget and is over the model's context — llama.cpp answers 500, it
// does not truncate) retries the batch item by item: the poison element is
// counted in rep.Failed and stays dirty, its healthy siblings are still
// committed. An all-failed run is an unavailable provider — infra, not data —
// and aborts with the provider's own error, preserving the pre-existing
// fail-loud contract (found dogfooding `leankg-embed` on this repository).
func embedFiles(ctx context.Context, st store.Backend, p Provider, modelID string, groups [][]dirtyItem, rep *Report) error {
	var lastErr error
	for _, group := range groups {
		rows := make([]store.VectorRow, 0, len(group))
		states := make(map[string]string, len(group))
		for i := 0; i < len(group); i += batchSize {
			if err := ctx.Err(); err != nil {
				return err
			}
			batch := group[i:min(i+batchSize, len(group))]
			texts := make([]string, len(batch))
			for j, d := range batch {
				texts[j] = d.text
			}
			vecs, err := p.Embed(ctx, Document, texts)
			if err != nil {
				// Per-item retry with shrink: a batch the provider rejected
				// is retried item by item (healthy siblings still commit),
				// and an item the provider rejects is HALVED and retried
				// (up to 3 times) — the character budget is a heuristic and
				// dense content can exceed the model's token context even at
				// the cap (measured 523 tokens in 1000 runes on real Godot/
				// TS). Shrunk successes count as truncations; an item that
				// fails at every size counts in Failed and stays dirty.
				lastErr = err
				for _, d := range batch {
					text := d.text
					var vec []float32
					shrunk := false
					for range 4 {
						v, ierr := p.Embed(ctx, Document, []string{text})
						if ierr == nil && validateBatch(v, 1, p.Dimensions()) == nil {
							vec = v[0]
							break
						}
						if ierr != nil {
							lastErr = ierr
						}
						n := len([]rune(text)) / 2
						if n == 0 {
							break
						}
						text = truncateRunes(text, n)
						shrunk = true
					}
					if vec == nil {
						rep.Failed++
						continue
					}
					if shrunk {
						rep.Truncations++
					}
					rows = append(rows, store.VectorRow{QualifiedName: d.qn, Vec: vec})
					states[d.qn] = d.hash
				}
				continue
			}
			if err := validateBatch(vecs, len(texts), p.Dimensions()); err != nil {
				rep.Failed += len(batch)
				continue
			}
			for j, d := range batch {
				rows = append(rows, store.VectorRow{QualifiedName: d.qn, Vec: vecs[j]})
				states[d.qn] = d.hash
			}
		}
		if len(rows) == 0 {
			continue
		}
		if err := st.ReplaceFileVectors(modelID, rows, states); err != nil {
			return err
		}
		rep.Embedded += len(rows)
	}
	// Nothing embedded and everything failed: that is a dead/misconfigured
	// provider, not a poison element — surface it loudly (wrapping the
	// provider's own error) exactly as a batch failure used to.
	if rep.Dirty > 0 && rep.Failed == rep.Dirty && rep.Embedded == 0 && lastErr != nil {
		return fmt.Errorf("embed: all %d item(s) failed: %w", rep.Failed, lastErr)
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
