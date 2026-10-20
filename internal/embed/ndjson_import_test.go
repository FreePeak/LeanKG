package embed

import (
	"bufio"
	"bytes"
	"context"
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"testing"

	"github.com/FreePeak/LeanKG/internal/store"
)

// gemmaRevision is the catalog pin of embeddinggemma-2: the revision an offsite
// import must stamp so the live local provider finds no drift.
const gemmaRevision = "914f7f89142e33e77833254d9c9b90c3cef7303b"

// rawGemmaVector is a non-trivial native-width (768-d) vector: the stand-in for
// the raw vector the sidecar or the Hugging Face job returns for one text.
func rawGemmaVector() []float32 {
	v := make([]float32, 768)
	for i := range v {
		v[i] = float32(i%7) + 0.5
	}
	return v
}

// exportAsVectorFile rewrites an export into the offsite vector file: each line
// keeps qualified_name and content_hash and carries vec in place of text.
func exportAsVectorFile(t *testing.T, export *bytes.Buffer, vec []float32) *bytes.Buffer {
	t.Helper()
	var out bytes.Buffer
	sc := bufio.NewScanner(export)
	for sc.Scan() {
		var e exportLine
		if err := json.Unmarshal(sc.Bytes(), &e); err != nil {
			t.Fatal(err)
		}
		line, err := json.Marshal(map[string]any{"qualified_name": e.QualifiedName, "content_hash": e.ContentHash, "vec": vec})
		if err != nil {
			t.Fatal(err)
		}
		out.Write(line)
		out.WriteByte('\n')
	}
	return &out
}

// exportGemma exports st for the embeddinggemma-2 row.
func exportGemma(t *testing.T, st *store.Store) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	if err := ExportNDJSON(context.Background(), st, "embeddinggemma-2", &buf); err != nil {
		t.Fatalf("ExportNDJSON: %v", err)
	}
	return &buf
}

// importGemma imports a vector file for embeddinggemma-2 at the configured
// width the tests use (512).
func importGemma(t *testing.T, st *store.Store, in *bytes.Buffer) (Report, error) {
	t.Helper()
	return ImportNDJSON(context.Background(), st, "embeddinggemma-2", gemmaRevision, "cosine", 512, in)
}

// liveIncrementalRun is `leankg-embed run`: the live provider from the environment.
func liveIncrementalRun(t *testing.T, st *store.Store) Report {
	t.Helper()
	p, err := FromEnv()
	if err != nil {
		t.Fatalf("live provider: %v", err)
	}
	return mustRun(t, st, p, "incremental")
}

// storedVector reads the vector st holds for qn under modelID.
func storedVector(t *testing.T, st *store.Store, modelID, qn string) []float32 {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+st.Path()+"?_pragma=query_only(ON)")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()
	var b []byte
	if err := db.QueryRow(`SELECT vec FROM embedding_vectors WHERE model_id = ? AND qualified_name = ?`, modelID, qn).Scan(&b); err != nil {
		t.Fatalf("read vector %s: %v", qn, err)
	}
	v := make([]float32, len(b)/4)
	for i := range v {
		v[i] = math.Float32frombits(binary.LittleEndian.Uint32(b[i*4:]))
	}
	return v
}

// TestImportThenIncrementalRunHasNoStampDrift: an offsite import of an
// embeddinggemma-2 collection must leave a store that the live local provider
// (same environment) accepts without a rebuild: the stored stamp equals the
// live provider's stamp, and the next incremental run embeds nothing.
func TestImportThenIncrementalRunHasNoStampDrift(t *testing.T) {
	setGemmaEnv(t, newFixedVectorServer(t, rawGemmaVector()).URL, "512")
	st := testStore(t)
	seed(t, st, el("a::f1", "func f1() {}"), el("a::f2", "func f2() {}"), el("b::g", "def g(): pass"))

	export := exportGemma(t, st)
	rep, err := importGemma(t, st, exportAsVectorFile(t, export, rawGemmaVector()))
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if rep.Embedded != 3 {
		t.Fatalf("import: got %+v, want Embedded=3", rep)
	}

	live, err := FromEnv()
	if err != nil {
		t.Fatalf("live provider: %v", err)
	}
	cur, err := st.Stamp("embeddinggemma-2")
	if err != nil || cur == nil {
		t.Fatalf("stamp after import: %v %v", cur, err)
	}
	if *cur != StampOf(live) {
		t.Fatalf("imported stamp = %+v, want the live stamp %+v", *cur, StampOf(live))
	}

	rep = liveIncrementalRun(t, st)
	if rep.Dirty != 0 || rep.Embedded != 0 {
		t.Fatalf("incremental after import: got %+v, want Dirty=0 Embedded=0", rep)
	}
}

// TestImportMatchesLiveVectors: for the same element and the same raw native
// vector, the vector import stores equals the vector a live run stores, so an
// imported collection is indistinguishable from one the live provider built.
func TestImportMatchesLiveVectors(t *testing.T) {
	raw := rawGemmaVector()
	setGemmaEnv(t, newFixedVectorServer(t, raw).URL, "512")

	live := testStore(t)
	seed(t, live, el("a::f1", "func f1() {}"))
	liveIncrementalRun(t, live)

	imported := testStore(t)
	seed(t, imported, el("a::f1", "func f1() {}"))
	if _, err := importGemma(t, imported, exportAsVectorFile(t, exportGemma(t, imported), raw)); err != nil {
		t.Fatalf("import: %v", err)
	}

	want := storedVector(t, live, "embeddinggemma-2", "a::f1")
	got := storedVector(t, imported, "embeddinggemma-2", "a::f1")
	if len(want) != 512 || len(got) != len(want) {
		t.Fatalf("widths: live %d, imported %d, want 512 both", len(want), len(got))
	}
	for i := range want {
		if math.Abs(float64(got[i])-float64(want[i])) > 1e-6 {
			t.Fatalf("component %d: imported %v, live %v", i, got[i], want[i])
		}
	}
}

// TestImportKeepsExportContentHash: the export's content_hash is the state the
// import records. An element edited between export and import stays dirty, so
// the next incremental run re-embeds exactly that element.
func TestImportKeepsExportContentHash(t *testing.T) {
	setGemmaEnv(t, newFixedVectorServer(t, rawGemmaVector()).URL, "512")
	st := testStore(t)
	seed(t, st, el("a::f1", "func f1() {}"), el("a::f2", "func f2() {}"))

	export := exportGemma(t, st)
	seed(t, st, el("a::f1", "func f1() { return 2 }"))

	if _, err := importGemma(t, st, exportAsVectorFile(t, export, rawGemmaVector())); err != nil {
		t.Fatalf("import: %v", err)
	}
	rep := liveIncrementalRun(t, st)
	if rep.Embedded != 1 {
		t.Fatalf("incremental after edit: got %+v, want Embedded=1 (only the edited element)", rep)
	}
}

// TestImportStaleLineKeepsFresherVector: a line exported before an edit must not
// replace the vector the live provider already wrote for the element's current
// content.
func TestImportStaleLineKeepsFresherVector(t *testing.T) {
	setGemmaEnv(t, newFixedVectorServer(t, rawGemmaVector()).URL, "512")
	st := testStore(t)
	seed(t, st, el("a::f1", "func f1() {}"))
	export := exportGemma(t, st)

	seed(t, st, el("a::f1", "func f1() { return 2 }"))
	liveIncrementalRun(t, st)
	before := storedVector(t, st, "embeddinggemma-2", "a::f1")

	other := make([]float32, 768)
	for i := range other {
		other[i] = float32(i%5) + 1
	}
	rep, err := importGemma(t, st, exportAsVectorFile(t, export, other))
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if rep.Embedded != 0 || rep.Skipped != 1 {
		t.Fatalf("stale import: got %+v, want Embedded=0 Skipped=1", rep)
	}
	after := storedVector(t, st, "embeddinggemma-2", "a::f1")
	for i := range before {
		if before[i] != after[i] {
			t.Fatalf("component %d replaced by stale import: %v -> %v", i, before[i], after[i])
		}
	}
}

// TestImportRejectsWrongWidth: a line must carry the native width (768) or the
// configured width (512). A 300-d line is rejected and nothing is written.
func TestImportRejectsWrongWidth(t *testing.T) {
	setGemmaEnv(t, newFixedVectorServer(t, rawGemmaVector()).URL, "512")

	ok := testStore(t)
	seed(t, ok, el("a::f1", "func f1() {}"))
	if _, err := importGemma(t, ok, exportAsVectorFile(t, exportGemma(t, ok), rawGemmaVector())); err != nil {
		t.Fatalf("native-width line must import: %v", err)
	}

	bad := testStore(t)
	seed(t, bad, el("a::f1", "func f1() {}"))
	var in bytes.Buffer
	fmt.Fprintf(&in, `{"qualified_name":"a::f1","content_hash":%q,"vec":[%s]}`+"\n",
		contentHashHex("func f1() {}"), strings.TrimSuffix(strings.Repeat("0.1,", 300), ","))
	if _, err := importGemma(t, bad, &in); err == nil {
		t.Fatal("want error for a 300-d line (neither 768 nor 512)")
	}
	if vectorCount(t, bad, "embeddinggemma-2") != 0 {
		t.Fatal("wrong-width import must write nothing")
	}
}

// TestImportFlagsMustAgreeWithEnvironment: the import flags are overrides. Unset
// flags take the live environment's stamp; a flag that disagrees is an error,
// and a local model with no pin is refused because its revision is only known
// from a running sidecar.
func TestImportFlagsMustAgreeWithEnvironment(t *testing.T) {
	setGemmaEnv(t, newFixedVectorServer(t, rawGemmaVector()).URL, "512")
	st := testStore(t)
	seed(t, st, el("a::f1", "func f1() {}"))
	line := func() *bytes.Buffer {
		return exportAsVectorFile(t, exportGemma(t, st), rawGemmaVector())
	}

	for _, bad := range []struct {
		name             string
		model, rev, dist string
		dims             int
	}{
		{"model", "embeddinggemma-x", "", "", 0},
		{"revision", "", "other-revision", "", 0},
		{"distance", "", "", "euclidean", 0},
		{"dims", "", "", "", 768},
	} {
		if _, err := ImportNDJSON(context.Background(), st, bad.model, bad.rev, bad.dist, bad.dims, line()); err == nil {
			t.Fatalf("%s conflict: want an error", bad.name)
		}
	}
	if vectorCount(t, st, "embeddinggemma-2") != 0 {
		t.Fatal("a rejected import must write nothing")
	}

	if _, err := ImportNDJSON(context.Background(), st, "", "", "", 0, line()); err != nil {
		t.Fatalf("all flags unset must take the environment: %v", err)
	}

	pending := line() // exported under the catalogued environment, before the switch
	t.Setenv("LEANKG_EMBED_MODEL", "my-local-gguf")
	t.Setenv("LEANKG_EMBED_REVISION", "")
	if _, err := ImportNDJSON(context.Background(), st, "", "", "", 0, pending); err == nil || !strings.Contains(err.Error(), "LEANKG_EMBED_REVISION") {
		t.Fatalf("uncatalogued local model without a pin: err = %v, want a refusal naming LEANKG_EMBED_REVISION", err)
	}
}

// TestImportAcceptsHFJobOutputFormat: scripts/hf_embed_job.py writes one
// {"qualified_name","content_hash","vec"} line per export line, with no header.
// Lines in exactly that shape must import with Embedded == number of lines.
func TestImportAcceptsHFJobOutputFormat(t *testing.T) {
	setGemmaEnv(t, newFixedVectorServer(t, rawGemmaVector()).URL, "512")
	st := testStore(t)
	seed(t, st, el("a::f1", "func f1() {}"), el("a::f2", "func f2() {}"))

	vec := rawGemmaVector()
	var in bytes.Buffer
	for _, e := range []struct{ qn, content string }{{"a::f1", "func f1() {}"}, {"a::f2", "func f2() {}"}} {
		line, err := json.Marshal(map[string]any{
			"qualified_name": e.qn,
			"content_hash":   contentHashHex(e.content),
			"vec":            vec,
		})
		if err != nil {
			t.Fatal(err)
		}
		in.Write(line)
		in.WriteByte('\n')
	}
	rep, err := importGemma(t, st, &in)
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if rep.Embedded != 2 {
		t.Fatalf("import of HF job output: got %+v, want Embedded=2", rep)
	}
}

// TestImportProviderLabelAloneKeepsVectors: Provider is a transport label
// (ModelStamp.Matches). A collection stamped "openai" that an offsite import
// under the "local" environment provider lands in must keep its vectors: the
// new line is added, the originals survive, and the stored label is unchanged.
func TestImportProviderLabelAloneKeepsVectors(t *testing.T) {
	setGemmaEnv(t, newFixedVectorServer(t, rawGemmaVector()).URL, "512")
	st := testStore(t)
	seed(t, st, el("a::f1", "func f1() {}"), el("a::f2", "func f2() {}"))
	if _, err := importGemma(t, st, exportAsVectorFile(t, exportGemma(t, st), rawGemmaVector())); err != nil {
		t.Fatalf("first import: %v", err)
	}
	stored, err := st.Stamp("embeddinggemma-2")
	if err != nil || stored == nil {
		t.Fatalf("stamp after first import: %v %v", stored, err)
	}
	stored.Provider = "openai"
	if err := st.WriteStamp(*stored); err != nil {
		t.Fatal(err)
	}
	before := storedVector(t, st, "embeddinggemma-2", "a::f1")
	const original = 2

	seed(t, st, el("a::f3", "func f3() {}"))
	rep, err := importGemma(t, st, exportAsVectorFile(t, exportGemma(t, st), rawGemmaVector()))
	if err != nil {
		t.Fatalf("import with provider label local over openai stamp: %v", err)
	}
	if rep.Embedded != 1 {
		t.Fatalf("import: got %+v, want Embedded=1 (only the new element)", rep)
	}
	if n := vectorCount(t, st, "embeddinggemma-2"); n != original+1 {
		t.Fatalf("vectors: got %d, want %d (originals kept, new one added)", n, original+1)
	}
	after := storedVector(t, st, "embeddinggemma-2", "a::f1")
	for i := range before {
		if before[i] != after[i] {
			t.Fatalf("component %d of a::f1 changed: %v -> %v", i, before[i], after[i])
		}
	}
	if cur, err := st.Stamp("embeddinggemma-2"); err != nil || cur == nil || cur.Provider != "openai" {
		t.Fatalf("stored stamp = %+v, %v; want the original provider openai kept", cur, err)
	}
}

// TestImportRealStampDifferenceRewritesCollection pins the behaviour a real
// identity difference (revision, dims or distance) keeps: the stored vectors
// are cleared, the stamp is rewritten to the environment's, and every line is
// embedded again.
func TestImportRealStampDifferenceRewritesCollection(t *testing.T) {
	for _, tc := range []struct {
		name   string
		break_ func(*store.ModelStamp)
	}{
		{"revision", func(s *store.ModelStamp) { s.Revision += "-drift" }},
		{"dimensions", func(s *store.ModelStamp) { s.Dimensions++ }},
		{"distance", func(s *store.ModelStamp) { s.Distance = "dot" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			setGemmaEnv(t, newFixedVectorServer(t, rawGemmaVector()).URL, "512")
			st := testStore(t)
			seed(t, st, el("a::f1", "func f1() {}"), el("a::f2", "func f2() {}"))
			if _, err := importGemma(t, st, exportAsVectorFile(t, exportGemma(t, st), rawGemmaVector())); err != nil {
				t.Fatalf("first import: %v", err)
			}
			stored, err := st.Stamp("embeddinggemma-2")
			if err != nil || stored == nil {
				t.Fatalf("stamp after first import: %v %v", stored, err)
			}
			tc.break_(stored)
			if err := st.WriteStamp(*stored); err != nil {
				t.Fatal(err)
			}

			rep, err := importGemma(t, st, exportAsVectorFile(t, exportGemma(t, st), rawGemmaVector()))
			if err != nil {
				t.Fatalf("import over real %s drift: %v", tc.name, err)
			}
			if rep.Embedded != 2 {
				t.Fatalf("import: got %+v, want Embedded=2 (collection rewritten)", rep)
			}
			if n := vectorCount(t, st, "embeddinggemma-2"); n != 2 {
				t.Fatalf("vectors: got %d, want 2", n)
			}
			live, err := FromEnv()
			if err != nil {
				t.Fatalf("live provider: %v", err)
			}
			if cur, _ := st.Stamp("embeddinggemma-2"); cur == nil || *cur != StampOf(live) {
				t.Fatalf("stamp = %+v, want rewritten to the live stamp %+v", cur, StampOf(live))
			}
		})
	}
}

// TestImportNeverOverwritesStoredTextCap: an import never knows the cap the
// producer cut its text at, so it must not change the stored one. A fresh stamp
// is written with text_cap 0 (the first run fills it in); a stamp that already
// holds a cap keeps it through a re-import at the same identity.
func TestImportNeverOverwritesStoredTextCap(t *testing.T) {
	setGemmaEnv(t, newFixedVectorServer(t, rawGemmaVector()).URL, "512")
	st := testStore(t)
	seed(t, st, el("a::f1", "func f1() {}"))
	if _, err := importGemma(t, st, exportAsVectorFile(t, exportGemma(t, st), rawGemmaVector())); err != nil {
		t.Fatalf("first import: %v", err)
	}
	if got := storedStamp(t, st, "embeddinggemma-2").TextCap; got != 0 {
		t.Fatalf("fresh import stamp TextCap = %d, want 0 (not recorded)", got)
	}

	stored := storedStamp(t, st, "embeddinggemma-2")
	stored.TextCap = 4032
	if err := st.WriteStamp(stored); err != nil {
		t.Fatal(err)
	}
	seed(t, st, el("a::f2", "func f2() {}"))
	rep, err := importGemma(t, st, exportAsVectorFile(t, exportGemma(t, st), rawGemmaVector()))
	if err != nil {
		t.Fatalf("second import: %v", err)
	}
	if rep.Embedded != 1 {
		t.Fatalf("second import: got %+v, want Embedded=1 (only the new element)", rep)
	}
	if got := storedStamp(t, st, "embeddinggemma-2").TextCap; got != 4032 {
		t.Fatalf("import overwrote the stored text cap: TextCap = %d, want 4032", got)
	}
}
