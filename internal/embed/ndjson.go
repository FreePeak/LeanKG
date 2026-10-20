package embed

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/FreePeak/LeanKG/internal/store"
)

// exportLine is one NDJSON export record. content_hash lets a store resume
// an import: unchanged QNs are skipped instead of rewritten.
type exportLine struct {
	QualifiedName string `json:"qualified_name"`
	ContentHash   string `json:"content_hash"`
	Text          string `json:"text"`
}

// importLine is one NDJSON import record: a pre-computed vector. ContentHash is
// the export's content_hash; a line without it is taken as embedded from the
// element's current content (see ImportNDJSON).
type importLine struct {
	QualifiedName string    `json:"qualified_name"`
	ContentHash   string    `json:"content_hash,omitempty"`
	Vec           []float32 `json:"vec"`
}

// importTarget is what an offline import writes: the stamp the live provider
// would write for the same environment, and the native width the offsite
// embedder returns. The stamp's Dimensions is the configured width.
type importTarget struct {
	stamp    store.ModelStamp
	native   int
	provider Provider
}

// resolveImport resolves the import identity from the same LEANKG_EMBED_*
// environment the live constructors read. It builds the provider the way
// FromEnv does, which needs no network for a catalogued model or an operator
// pin (see localPinned), so an import never needs a running provider. The
// arguments are the import flags: an empty string or zero means "not given",
// and a given value must equal the resolved one. A conflict is an error, so the
// import never writes a stamp the live provider would not match.
func resolveImport(model, revision, distance string, dims int) (importTarget, error) {
	name, err := providerName()
	if err != nil {
		return importTarget{}, err
	}
	// resolved is the model the environment selects; model is the flag, which
	// is only compared against it.
	var resolved string
	var native, width int
	var p Provider
	switch name {
	case "deterministic":
		resolved = "deterministic"
		if native, width, err = providerDims(resolved, defaultDims); err != nil {
			return importTarget{}, err
		}
		p = Deterministic(width)
	case "openai":
		resolved = os.Getenv("LEANKG_EMBED_MODEL")
		if native, width, err = providerDims(resolved, defaultDims); err != nil {
			return importTarget{}, err
		}
		if p, err = openAIProvider("openai", "https://api.openai.com/v1"); err != nil {
			return importTarget{}, err
		}
	case "local":
		resolved = localModel()
		if !localPinned(resolved) {
			return importTarget{}, fmt.Errorf("embed: import cannot name the revision a running local sidecar reports for %q: "+
				"set LEANKG_EMBED_REVISION, or name a catalogued model in LEANKG_EMBED_MODEL", resolved)
		}
		if native, width, err = providerDims(resolved, defaultDims); err != nil {
			return importTarget{}, err
		}
		if p, err = newLocalProvider(os.Getenv("LEANKG_EMBED_BASE_URL")); err != nil {
			return importTarget{}, err
		}
	default:
		return importTarget{}, fmt.Errorf("embed: unknown LEANKG_EMBED_PROVIDER %q (want local|openai|deterministic)", name)
	}
	stamp := StampOf(p)
	for _, check := range []error{
		matchImportFlag("--model", model, stamp.ModelID),
		matchImportFlag("--revision", revision, stamp.Revision),
		matchImportFlag("--distance", distance, stamp.Distance),
		matchImportFlag("--dims", dims, stamp.Dimensions),
	} {
		if check != nil {
			return importTarget{}, check
		}
	}
	return importTarget{stamp: stamp, native: native, provider: p}, nil
}

// matchImportFlag accepts an import flag that is unset or equal to the value the
// live provider environment resolves to.
func matchImportFlag[T comparable](flag string, given, resolved T) error {
	var unset T
	if given == unset || given == resolved {
		return nil
	}
	return fmt.Errorf("embed: import %s %v conflicts with the live provider environment (%v); unset the flag or fix LEANKG_EMBED_*",
		flag, given, resolved)
}

// ExportNDJSON writes every element as one JSON line
// {"qualified_name":..., "content_hash":..., "text":...} — the offsite
// embedding workflow's input format (export → embed elsewhere → import).
//
// text is the EXACT string the live provider receives for the element
// (documentInput): the capped document text (the write cap Run resolves)
// with the model's pinned document prefix (issue #279 part 2). Otherwise an
// offsite embedder would build vectors from different text, or unprefixed
// vectors that then land in a collection whose queries are embedded WITH the
// prefix. content_hash stays the hash of the raw element content: it is the
// resume key shared with Run, not a hash of the transmitted text.
//
// The provider is resolved from the LEANKG_EMBED_* environment exactly as the
// import is (resolveImport); modelID is the --model flag and must agree with
// it. A local sidecar must report its n_ctx from /props: when it cannot, the
// export fails rather than cap at a different length than the live run.
func ExportNDJSON(ctx context.Context, st store.Backend, modelID string, w io.Writer) error {
	target, err := resolveImport(modelID, "", "", 0)
	if err != nil {
		return err
	}
	p := target.provider
	if p.Provider() == "local" && sidecarBudget(p) == 0 {
		return fmt.Errorf("embed: export: the local sidecar at %q reported no n_ctx on /props, so the live text cap "+
			"cannot be known; start the sidecar and set LEANKG_EMBED_BASE_URL to it", os.Getenv("LEANKG_EMBED_BASE_URL"))
	}
	cur, err := st.Stamp(p.ModelID())
	if err != nil {
		return err
	}
	limit, _, err := writeCapFor(cur, p, "incremental")
	if err != nil {
		return err
	}
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
		if err := enc.Encode(exportLine{QualifiedName: e.QN, ContentHash: contentHashHex(e.Content), Text: documentInput(p, limit, e)}); err != nil {
			return fmt.Errorf("embed: export %s: %w", e.QN, err)
		}
	}
	if err := bw.Flush(); err != nil {
		return fmt.Errorf("embed: export flush: %w", err)
	}
	return nil
}

// ImportNDJSON reads {"qualified_name":..., "content_hash":..., "vec":[floats]}
// lines and writes them as the live provider's vectors for this environment.
//
// The stamp is the one the live provider writes (resolveImport): the model,
// revision, distance, width, provider and prefixes come from LEANKG_EMBED_*.
// modelID, revision, distance and dims are optional overrides that must agree
// with that resolution. A mismatched collection is cleared first (same guard
// as a full Run).
//
// Width: a line carries either the model's native width or the configured
// width. A native vector is reduced with TruncateNormalize, the same function
// the live matryoshka wrapper applies; any other length is a hard error for
// that line.
//
// content_hash: the state recorded for a line is the export's content_hash, so
// an element edited between export and import stays dirty and the next
// incremental run re-embeds it. A line without content_hash (a hand-built file)
// is taken as embedded from the element's current content, so an edit made
// before its import is not detected. Resume: a line is skipped when the store
// already holds a vector for the element's current content or for the content
// the line was embedded from. A line whose QN has no live element counts as an
// orphan and is not written.
func ImportNDJSON(ctx context.Context, st store.Backend, modelID, revision, distance string, dims int, r io.Reader) (Report, error) {
	start := time.Now()
	rep := Report{Mode: "import", Backend: st.Engine()}

	target, err := resolveImport(modelID, revision, distance, dims)
	if err != nil {
		return rep, err
	}
	want, native := target.stamp, target.native
	width := want.Dimensions
	cur, err := st.Stamp(want.ModelID)
	if err != nil {
		return rep, err
	}
	// Provider-label-only differences are never cleared here: the producer's
	// text cap is not recorded in the stamp, so import trusts the operator, and
	// clearing on a label change would lose vectors.
	if cur != nil && !cur.Matches(want) {
		if err := st.ClearVectors(want.ModelID); err != nil {
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
	resume, err := st.EmbeddingStateMap(want.ModelID)
	if err != nil {
		return rep, err
	}

	runID, err := st.StartEmbedRun(want.ModelID, "import", 0)
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
		if err := st.UpsertVectors(want.ModelID, rows); err != nil {
			return err
		}
		if err := st.SetEmbeddingStates(want.ModelID, states); err != nil {
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
		if n := len(in.Vec); n != native && n != width {
			return fail(fmt.Errorf("embed: import %s: %d dims, want %d (native) or %d (configured)", in.QualifiedName, n, native, width))
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
		embeddedFrom := in.ContentHash
		if embeddedFrom == "" {
			embeddedFrom = curHash
		}
		// Skip when the store already holds a vector for the current content (a
		// stale line must not replace a fresher live vector) or for exactly the
		// content this line was embedded from.
		if stored := resume[in.QualifiedName]; stored == curHash || stored == embeddedFrom {
			rep.Skipped++
			continue
		}
		rows = append(rows, store.VectorRow{QualifiedName: in.QualifiedName, Vec: TruncateNormalize(in.Vec, width)})
		states[in.QualifiedName] = embeddedFrom
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
	covered, dbOrphans, err := readVectorCounts(st, want.ModelID)
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
