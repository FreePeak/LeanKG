package embed

import (
	"context"
	"errors"
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
//
// v3 (RS-11): the embedded text is documentText — a "<type> <name> — <file>"
// header plus content (content now carries the leading doc comment, RS-10).
const ChunkerVersion = 3

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
	NoiseFloor  float64 `json:"NoiseFloor,omitempty"` // measured off-topic similarity ceiling (calibrate.go)
	Coverage    float64
	Duration    time.Duration
	Notes       []string `json:"notes,omitempty"` // one-line operator notes (e.g. a kept stored text cap)
}

// dirtyItem is one element queued for embedding: the file it came from (the
// atomic write unit), its qualified name, the (possibly truncated) text to
// send, and the full-content hash to record.
type dirtyItem struct{ file, qn, text, hash string }

// TextCap is the rune budget for one text sent to p: the general cap, or for
// the local sidecar family the served model's real window when the sidecar
// reports it (RS-14), else the conservative 512-token default. Documents and
// queries use the same cap, so a long query is truncated instead of failing
// at the provider and degrading L3 (RS-15).
//
// QUERY/MEMORY CALLERS ONLY (internal/core: query truncation and the memory
// path). The write path must not use it: a flaky /props would silently drop the
// write cap to 1000 and flip the stamp guard. Writers use resolveWriteCap.
func TextCap(p Provider) int {
	return capForLabel(p.Provider(), sidecarBudget(p))
}

// capForLabel is the rune cap for provider label `label` given the sidecar's
// reported budget (0 = unknown): the general cap, narrowed to the local
// family's budget for label "local". The single home of that rule; the
// incremental stamp guard applies it to the stored label as well.
func capForLabel(label string, budget int) int {
	if label != "local" {
		return maxContentChars
	}
	if budget <= 0 {
		budget = maxLocalTextChars
	}
	return min(maxContentChars, budget)
}

// resolveWriteCap is the rune cap a writer embeds with, decided by rule order
// and never by a silent fallback:
//
//  1. a cap already recorded in the stamp (stored > 0) wins, whatever the budget;
//  2. a non-local label takes the general cap (capForLabel);
//  3. the local label with a live sidecar budget takes min(8000, budget);
//  4. the local label with an operator pin (LEANKG_EMBED_TEXT_CAP) takes the pin;
//  5. otherwise the cap is undetermined and the error names both fixes.
//
// Pure: the caller reads /props (budget), the pin and the stamp. Unlike TextCap
// there is no 1000 default here.
func resolveWriteCap(label string, budget, pin, stored int) (int, error) {
	if stored > 0 {
		return stored, nil
	}
	if label != "local" || budget > 0 {
		return capForLabel(label, budget), nil
	}
	if pin > 0 {
		return pin, nil
	}
	return 0, fmt.Errorf("embed: cannot determine the text cap for provider %q: the sidecar /props reported no budget; make the sidecar /props reachable, or set LEANKG_EMBED_TEXT_CAP (256-8000)", label)
}

// sidecarBudget is the text budget the local sidecar reports from /props, in
// runes; 0 when p has none or the sidecar cannot be asked.
func sidecarBudget(p Provider) int {
	if tb, ok := unwrapProvider(p).(interface{ TextBudget() int }); ok {
		return tb.TextBudget()
	}
	return 0
}

// planText is the text a live run sends for one element: documentText cut to
// TextCap(p) runes, and whether it was cut. Run embeds it and ExportNDJSON
// writes it, so an offsite embedder receives the string a live run would. The
// provider prepends the document prefix on the wire (openaiCompatible.Embed),
// so an export line is that prefix followed by this text.
func planText(p Provider, e elementText) (text string, truncated bool) {
	text = documentText(e)
	limit := TextCap(p)
	if utf8.RuneCountInString(text) > limit {
		return truncateRunes(text, limit), true
	}
	return text, false
}

// documentInput is the exact string a live run's provider receives for one
// element: planText with the model's document prefix, the same prefix
// openaiCompatible.Embed prepends on the wire (prefixTexts). ExportNDJSON writes
// it as the line's text, so the offsite embedder and the live run cannot drift.
func documentInput(p Provider, e elementText) string {
	text, _ := planText(p, e)
	_, document := Prefixes(p.ModelID())
	return prefixTexts(document, []string{text})[0]
}

// TruncateText cuts s to at most n runes.
func TruncateText(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return truncateRunes(s, n)
}

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
	// Provider is a transport label, not part of identity (ModelStamp.Matches).
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
	// dims, distance, chunker version and prefix pair. Provider is a
	// transport label, not identity (ModelStamp.Matches), but it selects the
	// text cap (capForLabel), so a label change that changes the cap is drift
	// too: vectors cut at two caps must not share one collection.
	//   full        + mismatch ⇒ clear + re-stamp (rebuild, never mixed)
	//   incremental + mismatch ⇒ HARD FAIL with a rebuild directive — a
	//                 flag slip (provider A full, provider B incremental)
	//                 must not silently wipe A's vectors
	//   no stamp    ⇒ write (first build, both modes)
	cur, err := st.Stamp(modelID)
	if err != nil {
		return rep, err
	}
	budget := sidecarBudget(p)
	capChanged := cur != nil && capForLabel(cur.Provider, budget) != capForLabel(want.Provider, budget)
	if cur != nil && (!cur.Matches(want) || capChanged) {
		if mode != "full" {
			if !cur.Matches(want) {
				return rep, fmt.Errorf(
					"embed: stamp mismatch for %s (%s) — run `leankg-embed full` to rebuild the collection",
					modelID, stampDrift(*cur, want))
			}
			return rep, fmt.Errorf(
				"embed: provider label %q (text cap %d runes) differs from the stored label %q (text cap %d runes) for %s; "+
					"the label changes the text cap, so new vectors would be cut differently — set LEANKG_EMBED_PROVIDER=%s to keep the stored cap, "+
					"or run `leankg-embed full` only if a rebuild at the new cap is intended",
				want.Provider, capForLabel(want.Provider, budget), cur.Provider, capForLabel(cur.Provider, budget),
				modelID, cur.Provider)
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
		if mode == "incremental" && !needsVector(e.QN, h, states) {
			rep.Skipped++
			continue
		}
		text, cut := planText(p, e)
		if cut {
			rep.Truncations++
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

	// Reclaim vectors whose element is gone before measuring coverage: the
	// run used to report `Orphans: N` and leave them to `leankg gc` while L3
	// served them as hits (RS-05).
	if _, err := st.DeleteOrphanVectors(); err != nil {
		rep.Duration = time.Since(start)
		_ = st.FinishEmbedRun(runID, "failed", rep.Embedded, rep.Skipped, rep.Failed, rep.Truncations, rep.Orphans)
		return rep, fmt.Errorf("embed: reclaim orphan vectors: %w", err)
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
	// Re-measure the noise floor whenever the collection changed (or was
	// never measured for this identity). A calibration failure costs only the
	// low-confidence flag and dense memory recall, never the run.
	if _, ok := ReadCalibration(st, StampOf(p)); rep.Embedded > 0 || !ok {
		if floor, ok, err := Calibrate(ctx, st, p); err == nil && ok {
			rep.NoiseFloor = floor
		}
	} else if floor, ok := ReadCalibration(st, StampOf(p)); ok {
		rep.NoiseFloor = floor
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

// needsVector is the incremental selection, shared by Run and IncrementalBacklog
// so the backlog a hook gate counts is the set a run embeds: an element needs a
// vector when its content hash differs from the state stored for it, and an
// element with no stored state at all always needs one. states is the
// EmbeddingStateMap for the provider's model.
func needsVector(qn, hash string, states map[string]string) bool {
	return states[qn] != hash
}

// IncrementalBacklog counts the elements an incremental Run over st with provider
// p would embed. It reads the store and never calls the provider, so a caller can
// size the embed before it starts one.
func IncrementalBacklog(st store.Backend, p Provider) (int, error) {
	elems, err := readElements(st)
	if err != nil {
		return 0, err
	}
	states, err := st.EmbeddingStateMap(p.ModelID())
	if err != nil {
		return 0, err
	}
	n := 0
	for _, e := range elems {
		if needsVector(e.QN, contentHashHex(e.Content), states) {
			n++
		}
	}
	return n, nil
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
//
// A permanent configuration error (ErrEndpointConfig: wrong base URL, refused
// API key, a reply that is not an embeddings object) is not a poison element:
// embedFiles returns it at once, before any retry or shrink, so a misconfigured
// endpoint costs one request and writes nothing from the failing group.
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
				if errors.Is(err, ErrEndpointConfig) {
					return err
				}
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
							if errors.Is(ierr, ErrEndpointConfig) {
								return ierr
							}
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
