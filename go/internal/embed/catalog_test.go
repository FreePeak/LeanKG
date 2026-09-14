package embed

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/FreePeak/LeanKG/go/internal/store"
)

// --- catalog pins (issue #279 part 1) ---------------------------------------

func TestCatalogLookupCarriesPinsAndPrefixes(t *testing.T) {
	cases := []struct {
		lookup      string
		wantID      string
		wantDims    int
		wantQuery   string
		wantDoc     string
		wantRevLike string // prefix of the pinned revision
	}{
		{"bge-small-en-v1.5", DefaultModelID, 384, "", "", "ea104dac"},
		{DefaultModelID, DefaultModelID, 384, "", "", "ea104dac"},
		{"Xenova/bge-small-en-v1.5", DefaultModelID, 384, "", "", "ea104dac"},
		{"all-MiniLM-L6-v2", "all-minilm-l6-v2-384", 384, "", "", "751bff37"},
		{"Qwen/Qwen3-Embedding-4B", "qwen3-emb-4b-2560", 2560,
			"Instruct: Given a web search query, retrieve relevant passages that answer the query\nQuery:",
			"", "5cf2132a"},
		{"jina-embeddings-v3", "jina-embeddings-v3-1024", 1024,
			"Represent the query for retrieving evidence documents: ",
			"Represent the document for retrieval: ", "ab036b02"},
		{"JINA-EMBEDDINGS-V3  ", "jina-embeddings-v3-1024", 1024, // trimmed + case-insensitive
			"Represent the query for retrieving evidence documents: ",
			"Represent the document for retrieval: ", "ab036b02"},
	}
	for _, tc := range cases {
		m, ok := Lookup(tc.lookup)
		if !ok {
			t.Errorf("Lookup(%q): not found", tc.lookup)
			continue
		}
		if m.ID != tc.wantID || m.Dims != tc.wantDims {
			t.Errorf("Lookup(%q) = %s/%d, want %s/%d", tc.lookup, m.ID, m.Dims, tc.wantID, tc.wantDims)
		}
		if m.QueryPrefix != tc.wantQuery || m.DocumentPrefix != tc.wantDoc {
			t.Errorf("Lookup(%q) prefixes = %q/%q, want %q/%q",
				tc.lookup, m.QueryPrefix, m.DocumentPrefix, tc.wantQuery, tc.wantDoc)
		}
		if !strings.HasPrefix(m.Revision, tc.wantRevLike) {
			t.Errorf("Lookup(%q) revision = %q, want prefix %q", tc.lookup, m.Revision, tc.wantRevLike)
		}
	}
	if _, ok := Lookup("text-embedding-3-small"); ok {
		t.Fatal("an uncatalogued model must not resolve to a catalog row")
	}
}

// TestCatalogPinPrecedence pins the documented precedence: an explicit
// LEANKG_EMBED_REVISION override beats the catalog pin, the catalog pin beats
// the derived fallback, and an uncatalogued model still gets a revision that
// changes when the model does.
func TestCatalogPinPrecedence(t *testing.T) {
	model, ok := Lookup("jina-embeddings-v3")
	if !ok {
		t.Fatal("jina row missing")
	}
	if got := Pin("jina-embeddings-v3", "openai"); got != model.Revision {
		t.Fatalf("catalog pin = %q, want %q", got, model.Revision)
	}
	t.Setenv("LEANKG_EMBED_REVISION", "operator-pin-1")
	if got := Pin("jina-embeddings-v3", "openai"); got != "operator-pin-1" {
		t.Fatalf("explicit override = %q, want operator-pin-1", got)
	}
	t.Setenv("LEANKG_EMBED_REVISION", "")
	if got := Pin("text-embedding-3-small", "openai"); got != "openai:text-embedding-3-small" {
		t.Fatalf("uncatalogued fallback = %q", got)
	}
	if got := Pin("local", "local"); got != "local:local" {
		t.Fatalf("local sidecar fallback = %q", got)
	}
}

func TestCatalogDimsPrecedence(t *testing.T) {
	if got, err := ResolveDims("jina-embeddings-v3", 0, 384); err != nil || got != 1024 {
		t.Fatalf("catalog dims = %d, %v; want 1024", got, err)
	}
	if got, err := ResolveDims("jina-embeddings-v3", 768, 384); err != nil || got != 768 {
		t.Fatalf("explicit dims = %d, %v; want 768 (env wins)", got, err)
	}
	if got, err := ResolveDims("text-embedding-3-small", 0, 384); err != nil || got != 384 {
		t.Fatalf("uncatalogued fallback = %d, %v; want 384", got, err)
	}
	if _, err := ResolveDims("text-embedding-3-small", 0, 0); err == nil {
		t.Fatal("uncatalogued model with no dims and no fallback must error")
	}
}

// TestCatalogRowsAreImmutable pins that Models() hands out a copy: a caller
// mutating the slice must not be able to rewrite the pinned catalog.
func TestCatalogRowsAreImmutable(t *testing.T) {
	rows := Models()
	if len(rows) != len(catalogRows) {
		t.Fatalf("Models() = %d rows, want %d", len(rows), len(catalogRows))
	}
	rows[0].Revision = "tampered"
	if again := Models()[0].Revision; again == "tampered" {
		t.Fatal("Models() exposed the backing array")
	}
}

// --- prefixes at embed time AND query time (issue #279 part 2) ---------------

// TestPrefixesAppliedForBothTextKinds is the wire-level contract: the pinned
// asymmetric prefix reaches the provider request for documents AND for
// queries, and a symmetric model's text is sent untouched.
func TestPrefixesAppliedForBothTextKinds(t *testing.T) {
	model, ok := Lookup("jina-embeddings-v3")
	if !ok {
		t.Fatal("jina row missing")
	}
	var got []string
	srv := newOpenAITestServer(t, "", 4, func(body embeddingsRequest) {
		got = append([]string(nil), body.Input...)
	})
	p := OpenAICompatible(srv.URL, "", model.Name, 4, model.Revision)

	if _, err := p.Embed(context.Background(), Document, []string{"func f()"}); err != nil {
		t.Fatalf("document embed: %v", err)
	}
	if len(got) != 1 || got[0] != model.DocumentPrefix+"func f()" {
		t.Fatalf("document text = %q, want document prefix applied", got)
	}
	if _, err := p.Embed(context.Background(), Query, []string{"where is f"}); err != nil {
		t.Fatalf("query embed: %v", err)
	}
	if len(got) != 1 || got[0] != model.QueryPrefix+"where is f" {
		t.Fatalf("query text = %q, want query prefix applied", got)
	}

	// Symmetric model: no prefix, no rewrite.
	sym := OpenAICompatible(srv.URL, "", "all-MiniLM-L6-v2", 4, "rev")
	if _, err := sym.Embed(context.Background(), Document, []string{"raw text"}); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != "raw text" {
		t.Fatalf("symmetric model text = %q, want untouched", got)
	}
}

// --- stamp coupling for the chunker + prefixes (issue #279 parts 2 & 3) ------

// TestRunStampRecordsPipelineIdentity pins that a writer stamps the chunker
// version and the catalog prefix pair, and that drift in EITHER is a hard
// rebuild on the incremental path and a clear+restamp on the full path —
// never a mixed collection.
func TestRunStampRecordsPipelineIdentity(t *testing.T) {
	model, ok := Lookup("jina-embeddings-v3")
	if !ok {
		t.Fatal("jina row missing")
	}
	srv := newOpenAITestServer(t, "", 4, func(embeddingsRequest) {})
	p := OpenAICompatible(srv.URL, "", model.Name, 4, model.Revision)

	st := testStore(t)
	seed(t, st, el("a::f1", "func f1() {}"))
	mustRun(t, st, p, "full")

	got, err := st.Stamp(p.ModelID())
	if err != nil {
		t.Fatal(err)
	}
	if got == nil {
		t.Fatal("no stamp written")
	}
	if got.ChunkerVersion != ChunkerVersion {
		t.Fatalf("stamp chunker = %d, want %d", got.ChunkerVersion, ChunkerVersion)
	}
	if got.QueryPrefix != model.QueryPrefix || got.DocumentPrefix != model.DocumentPrefix {
		t.Fatalf("stamp prefixes = %q/%q, want %q/%q",
			got.QueryPrefix, got.DocumentPrefix, model.QueryPrefix, model.DocumentPrefix)
	}

	for _, tc := range []struct {
		name   string
		break_ func(*store.ModelStamp)
		want   string
	}{
		{"prefix pair", func(s *store.ModelStamp) { s.QueryPrefix = "" }, "prefix"},
		{"chunker version", func(s *store.ModelStamp) { s.ChunkerVersion-- }, "chunker_version"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			drifted := *got
			tc.break_(&drifted)
			if err := st.WriteStamp(drifted); err != nil {
				t.Fatal(err)
			}
			_, err := Run(context.Background(), st, p, "incremental")
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("incremental drift error = %v, want %q named", err, tc.want)
			}
			if !strings.Contains(err.Error(), "leankg-embed full") {
				t.Fatalf("drift error must name the runnable rebuild: %v", err)
			}
			// Full mode clears and re-stamps the drifted collection.
			mustRun(t, st, p, "full")
			after, err := st.Stamp(p.ModelID())
			if err != nil || after == nil || *after != *got {
				t.Fatalf("stamp after rebuild = %+v, %v; want %+v", after, err, got)
			}
		})
	}
}

// --- per-file atomic commit (issue #279 part 4) ------------------------------

// textFailProvider returns wrong-dimension vectors for any batch containing
// `bad`, and delegates otherwise.
type textFailProvider struct {
	inner Provider
	bad   string
}

func (p textFailProvider) ModelID() string  { return p.inner.ModelID() }
func (p textFailProvider) Revision() string { return p.inner.Revision() }
func (p textFailProvider) Dimensions() int  { return p.inner.Dimensions() }
func (p textFailProvider) Distance() string { return p.inner.Distance() }
func (p textFailProvider) Provider() string { return p.inner.Provider() }

func (p textFailProvider) Embed(ctx context.Context, kind TextKind, texts []string) ([][]float32, error) {
	for _, txt := range texts {
		if strings.Contains(txt, p.bad) {
			out := make([][]float32, len(texts))
			for i := range out {
				out[i] = make([]float32, p.inner.Dimensions()+1) // fails validateBatch
			}
			return out, nil
		}
	}
	return p.inner.Embed(ctx, kind, texts)
}

// TestRunCommitsPerFileAtomically pins the per-file write unit: a batch that
// fails validation for one file leaves that file with NEITHER vectors NOR
// state, while another file's elements are committed with both. Before the
// per-file transaction, vectors and state were separate writes and a failure
// could interleave them across files.
func TestRunCommitsPerFileAtomically(t *testing.T) {
	st := testStore(t)
	good := store.Element{
		QualifiedName: "a.go::fine", ElementType: "function", Name: "fine",
		FilePath: "a.go", LineStart: 1, LineEnd: 2, Language: "go", Content: "func fine() {}",
	}
	bad := store.Element{
		QualifiedName: "b.go::broken", ElementType: "function", Name: "broken",
		FilePath: "b.go", LineStart: 1, LineEnd: 2, Language: "go", Content: "func broken() {} // BOOM",
	}
	seed(t, st, good, bad)

	p := textFailProvider{inner: Deterministic(8), bad: "BOOM"}
	rep, err := Run(context.Background(), st, p, "full")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if rep.Failed != 1 || rep.Embedded != 1 {
		t.Fatalf("run report = %+v, want Failed=1 Embedded=1", rep)
	}

	states, err := st.EmbeddingStateMap(p.ModelID())
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := states["b.go::broken"]; ok {
		t.Fatal("failed file must have no embedding state")
	}
	if states["a.go::fine"] == "" {
		t.Fatal("sibling file must keep its embedding state")
	}
	covered, orphans, err := st.VectorCoverage(p.ModelID())
	if err != nil {
		t.Fatal(err)
	}
	if covered != 1 || orphans != 0 {
		t.Fatalf("coverage = (%d, %d), want (1, 0): the failed file must carry no vector", covered, orphans)
	}
}

// TestExportAppliesDocumentPrefix pins the offsite workflow's half of the
// prefix contract: the exported text is exactly what the live provider would
// send (document prefix applied), so vectors built elsewhere land in the same
// vector space as the collection's queries. The resume hash stays the raw
// content hash shared with Run.
func TestExportAppliesDocumentPrefix(t *testing.T) {
	model, ok := Lookup("jina-embeddings-v3")
	if !ok {
		t.Fatal("jina row missing")
	}
	st := testStore(t)
	seed(t, st, el("a::f1", "func f1() {}"))

	var buf bytes.Buffer
	if err := ExportNDJSON(context.Background(), st, model.Name, &buf); err != nil {
		t.Fatalf("ExportNDJSON: %v", err)
	}
	var line exportLine
	if err := json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &line); err != nil {
		t.Fatalf("decode export line: %v", err)
	}
	if line.Text != model.DocumentPrefix+"func f1() {}" {
		t.Fatalf("export text = %q, want document prefix applied", line.Text)
	}
	if line.ContentHash != contentHashHex("func f1() {}") {
		t.Fatalf("export content_hash = %q, want the raw-content hash (resume key)", line.ContentHash)
	}
}
