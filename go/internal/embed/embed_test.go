package embed

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/FreePeak/LeanKG/go/internal/store"
)

// --- fixtures ---------------------------------------------------------------

func testStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), ".leankg", "leankg.db"), store.RW)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.Migrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return st
}

func el(qn, content string) store.Element {
	return store.Element{
		QualifiedName: qn,
		ElementType:   "function",
		Name:          qn,
		FilePath:      "src/f.go",
		LineStart:     1,
		LineEnd:       2,
		Language:      "go",
		Content:       content,
	}
}

func seed(t *testing.T, st *store.Store, els ...store.Element) {
	t.Helper()
	if err := st.UpsertElements(els); err != nil {
		t.Fatalf("seed: %v", err)
	}
}

func mustRun(t *testing.T, st *store.Store, p Provider, mode string) Report {
	t.Helper()
	rep, err := Run(context.Background(), st, p, mode)
	if err != nil {
		t.Fatalf("Run(%s): %v", mode, err)
	}
	return rep
}

func vectorCount(t *testing.T, st *store.Store, modelID string) int {
	t.Helper()
	n, err := st.VectorCount(modelID)
	if err != nil {
		t.Fatalf("VectorCount: %v", err)
	}
	return n
}

func stampRows(t *testing.T, st *store.Store) int {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+st.Path()+"?_pragma=query_only(ON)")
	if err != nil {
		t.Fatalf("sidecar open: %v", err)
	}
	defer db.Close()
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM emb_stamp`).Scan(&n); err != nil {
		t.Fatalf("count stamps: %v", err)
	}
	return n
}

// overrideProvider delegates to an inner provider but renames identity fields,
// to simulate a provider whose stamp drifted (same model id, new revision).
type overrideProvider struct {
	inner             Provider
	modelID, revision string
	dims              int
}

func (o overrideProvider) ModelID() string  { return o.modelID }
func (o overrideProvider) Revision() string { return o.revision }
func (o overrideProvider) Dimensions() int  { return o.dims }
func (o overrideProvider) Distance() string { return o.inner.Distance() }
func (o overrideProvider) Provider() string { return o.inner.Provider() }
func (o overrideProvider) Embed(ctx context.Context, kind TextKind, texts []string) ([][]float32, error) {
	return o.inner.Embed(ctx, kind, texts)
}

// wrongDimsProvider returns wrong-dimension vectors for its first N calls,
// then delegates. Exercises batch validation without failing the provider.
type wrongDimsProvider struct {
	inner            Provider
	calls, failFirst int
}

func (w *wrongDimsProvider) ModelID() string  { return w.inner.ModelID() }
func (w *wrongDimsProvider) Revision() string { return w.inner.Revision() }
func (w *wrongDimsProvider) Dimensions() int  { return w.inner.Dimensions() }
func (w *wrongDimsProvider) Distance() string { return w.inner.Distance() }
func (w *wrongDimsProvider) Provider() string { return w.inner.Provider() }

func (w *wrongDimsProvider) Embed(ctx context.Context, kind TextKind, texts []string) ([][]float32, error) {
	w.calls++
	if w.calls <= w.failFirst {
		out := make([][]float32, len(texts))
		for i := range out {
			out[i] = make([]float32, w.inner.Dimensions()+1)
		}
		return out, nil
	}
	return w.inner.Embed(ctx, kind, texts)
}

// errProvider always fails its Embed call: transport-level provider failure.
type errProvider struct{ inner Provider }

func (e errProvider) ModelID() string  { return e.inner.ModelID() }
func (e errProvider) Revision() string { return e.inner.Revision() }
func (e errProvider) Dimensions() int  { return e.inner.Dimensions() }
func (e errProvider) Distance() string { return e.inner.Distance() }
func (e errProvider) Provider() string { return e.inner.Provider() }

func (e errProvider) Embed(context.Context, TextKind, []string) ([][]float32, error) {
	return nil, errors.New("provider down")
}

// --- Run --------------------------------------------------------------------

func TestRunFullThenIncrementalSkipsUnchanged(t *testing.T) {
	st := testStore(t)
	seed(t, st, el("a::f1", "func f1() {}"), el("a::f2", "func f2() {}"), el("a::f3", "func f3() {}"))
	p := Deterministic(8)

	rep := mustRun(t, st, p, "full")
	if rep.Embedded != 3 || rep.Dirty != 3 || rep.Skipped != 0 {
		t.Fatalf("full run: got %+v, want Dirty=3 Embedded=3 Skipped=0", rep)
	}
	if rep.Coverage != 1 {
		t.Fatalf("full run coverage: got %v, want 1", rep.Coverage)
	}
	if vectorCount(t, st, p.ModelID()) != 3 {
		t.Fatalf("full run: want 3 vectors")
	}

	rep = mustRun(t, st, p, "incremental")
	if rep.Dirty != 0 || rep.Embedded != 0 || rep.Skipped != 3 {
		t.Fatalf("incremental run: got %+v, want Dirty=0 Embedded=0 Skipped=3", rep)
	}

	// Change one element's content: exactly that element re-embeds.
	seed(t, st, el("a::f2", "func f2(x int) {}"))
	rep = mustRun(t, st, p, "incremental")
	if rep.Dirty != 1 || rep.Embedded != 1 || rep.Skipped != 2 {
		t.Fatalf("incremental after edit: got %+v, want Dirty=1 Embedded=1 Skipped=2", rep)
	}
	if vectorCount(t, st, p.ModelID()) != 3 {
		t.Fatalf("after edit: want still 3 vectors")
	}
}

func TestRunStampMismatchClearsAndRebuilds(t *testing.T) {
	st := testStore(t)
	seed(t, st, el("a::f1", "func f1() {}"), el("a::f2", "func f2() {}"))
	p1 := Deterministic(8)
	mustRun(t, st, p1, "full")

	// Same model id, drifted revision: a full run must clear + rebuild.
	p2 := overrideProvider{inner: Deterministic(8), modelID: p1.ModelID(), revision: "test:deterministic-v2", dims: 8}
	rep := mustRun(t, st, p2, "full")
	if rep.Embedded != 2 {
		t.Fatalf("rebuild: got %+v, want Embedded=2", rep)
	}
	cur, err := st.Stamp(p1.ModelID())
	if err != nil {
		t.Fatal(err)
	}
	if cur == nil || cur.Revision != "test:deterministic-v2" {
		t.Fatalf("stamp after rebuild: got %+v, want revision test:deterministic-v2", cur)
	}
	if n := stampRows(t, st); n != 1 {
		t.Fatalf("want exactly 1 stamp row, got %d", n)
	}
	if vectorCount(t, st, p1.ModelID()) != 2 {
		t.Fatalf("want 2 vectors after rebuild (no mixed/stale rows)")
	}

	// Incremental against the rebuilt collection finds nothing dirty.
	rep = mustRun(t, st, p2, "incremental")
	if rep.Embedded != 0 || rep.Skipped != 2 {
		t.Fatalf("incremental after rebuild: got %+v, want Embedded=0 Skipped=2", rep)
	}
}

func TestRunValidationFailureCountedNotFatal(t *testing.T) {
	st := testStore(t)
	seed(t, st, el("a::f1", "func f1() {}"), el("a::f2", "func f2() {}"), el("a::f3", "func f3() {}"))
	p := &wrongDimsProvider{inner: Deterministic(8), failFirst: 1}

	rep, err := Run(context.Background(), st, p, "full")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if rep.Failed != 3 || rep.Embedded != 0 {
		t.Fatalf("got %+v, want Failed=3 Embedded=0", rep)
	}
	if rep.Coverage != 0 {
		t.Fatalf("coverage: got %v, want 0", rep.Coverage)
	}
	run, err := st.LastEmbedRun(p.ModelID())
	if err != nil || run == nil {
		t.Fatalf("LastEmbedRun: %v %v", run, err)
	}
	if run.Status != "partial" {
		t.Fatalf("run status: got %q, want partial", run.Status)
	}

	// Retry succeeds and converges.
	rep = mustRun(t, st, p, "full")
	if rep.Embedded != 3 || rep.Failed != 0 {
		t.Fatalf("retry: got %+v, want Embedded=3 Failed=0", rep)
	}
}

func TestRunProviderErrorSurfaces(t *testing.T) {
	st := testStore(t)
	seed(t, st, el("a::f1", "func f1() {}"))
	p := errProvider{inner: Deterministic(8)}

	_, err := Run(context.Background(), st, p, "full")
	if err == nil || !strings.Contains(err.Error(), "provider down") {
		t.Fatalf("Run error: got %v, want provider failure surfaced", err)
	}
	run, _ := st.LastEmbedRun(p.ModelID())
	if run == nil || run.Status != "failed" {
		t.Fatalf("run status: got %+v, want failed", run)
	}
}

func TestRunEmptyStoreOK(t *testing.T) {
	st := testStore(t)
	p := Deterministic(8)
	for _, mode := range []string{"full", "incremental"} {
		rep := mustRun(t, st, p, mode)
		if rep.Dirty != 0 || rep.Embedded != 0 || rep.Coverage != 1 {
			t.Fatalf("%s on empty store: got %+v", mode, rep)
		}
	}
	if vectorCount(t, st, p.ModelID()) != 0 {
		t.Fatal("empty store produced vectors")
	}
}

func TestRunTruncationCounted(t *testing.T) {
	st := testStore(t)
	seed(t, st, el("a::big", strings.Repeat("x", 8001)))
	p := Deterministic(8)

	rep := mustRun(t, st, p, "full")
	if rep.Truncations != 1 || rep.Embedded != 1 {
		t.Fatalf("got %+v, want Truncations=1 Embedded=1", rep)
	}
}

func TestRunUnknownModeRejected(t *testing.T) {
	st := testStore(t)
	if _, err := Run(context.Background(), st, Deterministic(8), "everything"); err == nil {
		t.Fatal("want error for unknown mode")
	}
}

// --- Deterministic provider ---------------------------------------------------

func TestDeterministicProviderIdentity(t *testing.T) {
	p := Deterministic(384)
	if p.ModelID() != "deterministic-384" || p.Revision() != "test:deterministic-v1" ||
		p.Dimensions() != 384 || p.Distance() != "cosine" || p.Provider() != "deterministic" {
		t.Fatalf("unexpected identity: %s %s %d %s %s",
			p.ModelID(), p.Revision(), p.Dimensions(), p.Distance(), p.Provider())
	}
	vecs, err := p.Embed(context.Background(), Document, []string{"hello", "hello"})
	if err != nil {
		t.Fatal(err)
	}
	if len(vecs) != 2 || len(vecs[0]) != 384 {
		t.Fatalf("shape: %d x %d", len(vecs), len(vecs[0]))
	}
	for i := range vecs[0] {
		if vecs[0][i] != vecs[1][i] {
			t.Fatal("same text must embed identically")
		}
	}
	var norm float64
	for _, x := range vecs[0] {
		norm += float64(x) * float64(x)
	}
	if norm < 0.999 || norm > 1.001 {
		t.Fatalf("vector not unit length: %v", norm)
	}
}

// --- OpenAICompatible (httptest only — no live network) -----------------------

func openAIResponse(t *testing.T, body embeddingsRequest, dims int) []byte {
	t.Helper()
	data := make([]map[string]any, len(body.Input))
	for i := range data {
		vec := make([]float32, dims)
		for j := range vec {
			vec[j] = 0.1
		}
		data[i] = map[string]any{"embedding": vec}
	}
	out, err := json.Marshal(map[string]any{"data": data})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func newOpenAITestServer(t *testing.T, wantAuth string, dims int, check func(body embeddingsRequest)) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != wantAuth {
			t.Errorf("Authorization header: got %q, want %q", got, wantAuth)
		}
		if r.Method != http.MethodPost || r.URL.Path != "/embeddings" {
			t.Errorf("request: got %s %s, want POST /embeddings", r.Method, r.URL.Path)
		}
		var body embeddingsRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode request body: %v", err)
			http.Error(w, "bad json", http.StatusBadRequest)
			return
		}
		check(body)
		w.Header().Set("Content-Type", "application/json")
		w.Write(openAIResponse(t, body, dims))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestOpenAICompatibleSendsAuthAndShape(t *testing.T) {
	srv := newOpenAITestServer(t, "Bearer sk-test", 4, func(body embeddingsRequest) {
		if body.Model != "m-1" {
			t.Errorf("model: got %q, want m-1", body.Model)
		}
		if len(body.Input) != 2 || body.Input[0] != "alpha" || body.Input[1] != "beta" {
			t.Errorf("input: got %v", body.Input)
		}
	})

	p := OpenAICompatible(srv.URL, "sk-test", "m-1", 4, "rev-1")
	vecs, err := p.Embed(context.Background(), Document, []string{"alpha", "beta"})
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if len(vecs) != 2 || len(vecs[0]) != 4 {
		t.Fatalf("shape: %d x %d", len(vecs), len(vecs[0]))
	}
}

func TestOpenAICompatibleNoAuthHeaderWhenKeyEmpty(t *testing.T) {
	srv := newOpenAITestServer(t, "", 4, func(embeddingsRequest) {})
	p := OpenAICompatible(srv.URL, "", "m-1", 4, "rev-1")
	if _, err := p.Embed(context.Background(), Document, []string{"alpha"}); err != nil {
		t.Fatalf("Embed: %v", err)
	}
}

func TestOpenAICompatibleErrorIncludesBodySnippet(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "model overloaded", http.StatusInternalServerError)
	}))
	defer srv.Close()

	p := OpenAICompatible(srv.URL, "", "m-1", 4, "rev-1")
	_, err := p.Embed(context.Background(), Document, []string{"alpha"})
	if err == nil || !strings.Contains(err.Error(), "model overloaded") || !strings.Contains(err.Error(), "500") {
		t.Fatalf("Embed error: got %v, want status 500 + body snippet", err)
	}
}

// --- FromEnv -------------------------------------------------------------------

func TestFromEnv(t *testing.T) {
	t.Run("deterministic", func(t *testing.T) {
		t.Setenv("LEANKG_EMBED_PROVIDER", "deterministic")
		t.Setenv("LEANKG_EMBED_DIMS", "16")
		p, err := FromEnv()
		if err != nil {
			t.Fatal(err)
		}
		if p.ModelID() != "deterministic-16" || p.Dimensions() != 16 {
			t.Fatalf("got %s/%d", p.ModelID(), p.Dimensions())
		}
	})

	t.Run("openai requires model", func(t *testing.T) {
		t.Setenv("LEANKG_EMBED_PROVIDER", "openai")
		if _, err := FromEnv(); err == nil {
			t.Fatal("want error when LEANKG_EMBED_MODEL unset for openai")
		}
		t.Setenv("LEANKG_EMBED_MODEL", "text-embedding-3-small")
		t.Setenv("LEANKG_EMBED_BASE_URL", "https://api.example.com/v1")
		t.Setenv("LEANKG_EMBED_API_KEY", "sk-x")
		t.Setenv("LEANKG_EMBED_DIMS", "1536")
		p, err := FromEnv()
		if err != nil {
			t.Fatal(err)
		}
		if p.ModelID() != "text-embedding-3-small" || p.Dimensions() != 1536 || p.Provider() != "openai" {
			t.Fatalf("got %s/%d/%s", p.ModelID(), p.Dimensions(), p.Provider())
		}
	})

	t.Run("local defaults to sidecar", func(t *testing.T) {
		t.Setenv("LEANKG_EMBED_PROVIDER", "local")
		p, err := FromEnv()
		if err != nil {
			t.Fatal(err)
		}
		o, ok := p.(*openaiCompatible)
		if !ok {
			t.Fatalf("want *openaiCompatible, got %T", p)
		}
		if o.baseURL != "http://127.0.0.1:8080/v1" {
			t.Fatalf("base URL: got %q", o.baseURL)
		}
	})

	t.Run("unknown provider", func(t *testing.T) {
		t.Setenv("LEANKG_EMBED_PROVIDER", "quantum")
		if _, err := FromEnv(); err == nil {
			t.Fatal("want error for unknown provider")
		}
	})
}

// --- NDJSON export/import ------------------------------------------------------

func TestNDJSONRoundTrip(t *testing.T) {
	st1 := testStore(t)
	els := []store.Element{el("a::f1", "func f1() {}"), el("a::f2", "func f2() {}"), el("b::g", "def g(): pass")}
	seed(t, st1, els...)

	var buf bytes.Buffer
	if err := ExportNDJSON(context.Background(), st1, "m", &buf); err != nil {
		t.Fatalf("ExportNDJSON: %v", err)
	}

	// Export shape: one JSON line per element with content_hash = sha256(content).
	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 3 {
		t.Fatalf("export: got %d lines, want 3", len(lines))
	}
	var line exportLine
	if err := json.Unmarshal([]byte(lines[0]), &line); err != nil {
		t.Fatalf("decode export line: %v", err)
	}
	// readElements orders by qualified_name: a::f1 < a::f2 < b::g.
	sum := sha256.Sum256([]byte("func f1() {}"))
	if line.Text != "func f1() {}" || line.ContentHash != hex.EncodeToString(sum[:]) || line.QualifiedName != "a::f1" {
		t.Fatalf("export line: got %+v", line)
	}
	// Simulate the offsite workflow: export → embed elsewhere → import.
	simulateOffsite := func(export *bytes.Buffer) *bytes.Buffer {
		var out bytes.Buffer
		sc := bufio.NewScanner(export)
		for sc.Scan() {
			var e exportLine
			if err := json.Unmarshal(sc.Bytes(), &e); err != nil {
				t.Fatal(err)
			}
			vecs, err := Deterministic(8).Embed(context.Background(), Document, []string{e.Text})
			if err != nil {
				t.Fatal(err)
			}
			b, err := json.Marshal(importLine{QualifiedName: e.QualifiedName, Vec: vecs[0]})
			if err != nil {
				t.Fatal(err)
			}
			out.Write(b)
			out.WriteByte('\n')
		}
		return &out
	}

	// Import into a fresh store: writes vectors + stamp; second import resumes.
	st2 := testStore(t)
	seed(t, st2, els...)
	import1 := simulateOffsite(&buf)
	rep, err := ImportNDJSON(context.Background(), st2, "deterministic-8", "test:deterministic-v1", "cosine", 8, import1)
	if err != nil {
		t.Fatalf("ImportNDJSON: %v", err)
	}
	if rep.Embedded != 3 || rep.Skipped != 0 {
		t.Fatalf("import: got %+v, want Embedded=3 Skipped=0", rep)
	}
	if vectorCount(t, st2, "deterministic-8") != 3 {
		t.Fatal("import: want 3 vectors")
	}
	cur, _ := st2.Stamp("deterministic-8")
	if cur == nil || cur.Revision != "test:deterministic-v1" || cur.Dimensions != 8 || cur.Distance != "cosine" || cur.Provider != "ndjson" {
		t.Fatalf("import stamp: got %+v", cur)
	}

	buf2 := bytes.Buffer{}
	if err := ExportNDJSON(context.Background(), st2, "m", &buf2); err != nil {
		t.Fatal(err)
	}
	rep, err = ImportNDJSON(context.Background(), st2, "deterministic-8", "test:deterministic-v1", "cosine", 8, simulateOffsite(&buf2))
	if err != nil {
		t.Fatalf("re-import: %v", err)
	}
	if rep.Embedded != 0 || rep.Skipped != 3 {
		t.Fatalf("re-import: got %+v, want Embedded=0 Skipped=3 (resume)", rep)
	}
}

func TestImportNDJSONDimGuardAndOrphans(t *testing.T) {
	st := testStore(t)
	seed(t, st, el("a::f1", "func f1() {}"))

	// One valid line (8 dims), one wrong-dim line: the import errors and
	// writes nothing (crash-consistent).
	r := strings.NewReader(`{"qualified_name":"a::f1","vec":[0.1,0.2,0.3,0.4,0.5,0.6,0.7,0.8]}` + "\n" +
		`{"qualified_name":"a::f2","vec":[0.1,0.2,0.3,0.4,0.5,0.6,0.7]}` + "\n")
	if _, err := ImportNDJSON(context.Background(), st, "m", "rev", "cosine", 8, r); err == nil {
		t.Fatal("want error for wrong-dim import line")
	}
	if vectorCount(t, st, "m") != 0 {
		t.Fatal("dim-guard: nothing must be written when a line fails")
	}

	// A line for a QN with no element counts as an orphan and is not written.
	r = strings.NewReader(`{"qualified_name":"gone::x","vec":[0.1,0.2,0.3,0.4,0.5,0.6,0.7,0.8]}` + "\n" +
		`{"qualified_name":"a::f1","vec":[0.1,0.2,0.3,0.4,0.5,0.6,0.7,0.8]}` + "\n")
	rep, err := ImportNDJSON(context.Background(), st, "m", "rev", "cosine", 8, r)
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if rep.Embedded != 1 || rep.Orphans != 1 {
		t.Fatalf("import: got %+v, want Embedded=1 Orphans=1", rep)
	}
}

// TestRunIncrementalOnFreshStampsCollection pins the FR-ZCP-11 first-build
// rule for incremental runs: a fresh store gets the provider stamp written
// on the FIRST run of any mode (without it, L3 stays dead — the live smoke
// caught exactly this hole).
func TestRunIncrementalOnFreshStampsCollection(t *testing.T) {
	st := testStore(t)
	seed(t, st, el("a::f1", "func f1() {}"), el("a::f2", "func f2() {}"))
	p := Deterministic(8)

	rep := mustRun(t, st, p, "incremental")
	if rep.Embedded != 2 {
		t.Fatalf("incremental fresh: got %+v, want Embedded=2", rep)
	}
	cur, err := st.Stamp(p.ModelID())
	if err != nil {
		t.Fatal(err)
	}
	if cur == nil {
		t.Fatal("incremental run on fresh store left NO stamp — L3 would be dead")
	}
	if cur.Revision != p.Revision() || cur.Provider != p.Provider() {
		t.Fatalf("stamp: got %+v", cur)
	}
}

// TestRunIncrementalMismatchHardFails pins the flag-slip safety: incremental
// against a drifted stamp must HARD-FAIL with a rebuild directive and write
// NOTHING — an accidental provider switch must not wipe the old collection.
func TestRunIncrementalMismatchHardFails(t *testing.T) {
	st := testStore(t)
	seed(t, st, el("a::f1", "func f1() {}"), el("a::f2", "func f2() {}"))
	p1 := Deterministic(8)
	mustRun(t, st, p1, "full")
	before := vectorCount(t, st, p1.ModelID())

	p2 := overrideProvider{inner: Deterministic(8), modelID: p1.ModelID(), revision: "test:deterministic-v2", dims: 8}
	if _, err := Run(context.Background(), st, p2, "incremental"); err == nil {
		t.Fatal("incremental against drifted stamp must hard-fail")
	} else if !strings.Contains(err.Error(), "leankg-embed full") {
		t.Fatalf("error must carry the rebuild directive: %v", err)
	}
	if got := vectorCount(t, st, p1.ModelID()); got != before {
		t.Fatalf("failed incremental run must not touch vectors: had %d, now %d", before, got)
	}
}
