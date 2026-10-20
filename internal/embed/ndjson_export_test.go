package embed

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"unicode/utf8"
)

// firstDiff is the byte offset of the first difference between a and b (the
// shorter length when one is a prefix of the other).
func firstDiff(a, b string) int {
	n := min(len(a), len(b))
	for i := 0; i < n; i++ {
		if a[i] != b[i] {
			return i
		}
	}
	return n
}

// TestExportTextEqualsLiveRunInput: the text an offsite export writes for an
// element must be byte-for-byte the string a live incremental run sends the
// provider for that element. The fake sidecar reports a small n_ctx, so the long
// element is over the text budget and both paths must cut it the same way; the
// export's text is compared with the input the server recorded for the same
// element (matched by a marker only that element's content carries).
func TestExportTextEqualsLiveRunInput(t *testing.T) {
	const nCtx = 300 // text budget max(2*300-64, 256) = 536 runes

	var mu sync.Mutex
	var received []string
	vec := make([]float32, 768) // native EmbeddingGemma width
	for i := range vec {
		vec[i] = 1
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/props":
			fmt.Fprintf(w, `{"model_path":"/cache/embeddinggemma-2.gguf","default_generation_settings":{"n_ctx":%d}}`, nCtx)
		case "/embeddings":
			var body embeddingsRequest
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				http.Error(w, "bad json", http.StatusBadRequest)
				return
			}
			mu.Lock()
			received = append(received, body.Input...)
			mu.Unlock()
			data := make([]map[string]any, len(body.Input))
			for i := range data {
				data[i] = map[string]any{"embedding": vec}
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	setGemmaEnv(t, srv.URL, "512")

	st := testStore(t)
	seed(t,
		st,
		el("a::short", "marker-short\nfunc short() {}"),
		el("a::long", "marker-long\n"+strings.Repeat("func long() { return 1 }\n", 200)),
	)

	var export bytes.Buffer
	if err := ExportNDJSON(context.Background(), st, "embeddinggemma-2", &export); err != nil {
		t.Fatalf("ExportNDJSON: %v", err)
	}
	live, err := FromEnv()
	if err != nil {
		t.Fatalf("live provider: %v", err)
	}
	mustRun(t, st, live, "incremental")

	exported := map[string]string{}
	sc := bufio.NewScanner(&export)
	for sc.Scan() {
		var line exportLine
		if err := json.Unmarshal(sc.Bytes(), &line); err != nil {
			t.Fatalf("decode export line: %v", err)
		}
		exported[line.QualifiedName] = line.Text
	}

	mu.Lock()
	sent := append([]string(nil), received...)
	mu.Unlock()

	for qn, marker := range map[string]string{"a::short": "marker-short", "a::long": "marker-long"} {
		var hits []string
		for _, in := range sent {
			if strings.Contains(in, marker) {
				hits = append(hits, in)
			}
		}
		if len(hits) != 1 {
			t.Fatalf("%s: live run sent %d inputs carrying %q, want exactly 1", qn, len(hits), marker)
		}
		if got := exported[qn]; got != hits[0] {
			t.Errorf("%s: export text != live input: export %d runes, live %d runes, first differing byte %d",
				qn, utf8.RuneCountInString(got), utf8.RuneCountInString(hits[0]), firstDiff(got, hits[0]))
		}
	}

	// The long element must really be over the budget, or the test proves nothing
	// about truncation.
	if n := utf8.RuneCountInString(exported["a::long"]); n > 536+len("title: none | text: ") {
		t.Fatalf("long export text is %d runes; the budget should have cut it to <= 536 (+prefix)", n)
	}
}

// exportLocalEnv points the export's environment at sc, the local sidecar the
// run and the export both read.
func exportLocalEnv(t *testing.T, sc *localSidecar) {
	t.Helper()
	t.Setenv("LEANKG_EMBED_PROVIDER", "local")
	t.Setenv("LEANKG_EMBED_BASE_URL", sc.URL+"/v1")
}

// TestExportCutsAtStoredCapWhenPropsDown: a store whose stamp records a text cap
// exports at that cap even when the sidecar's /props is down. The export text
// must be the string the incremental run (also /props down) sends for the element.
func TestExportCutsAtStoredCapWhenPropsDown(t *testing.T) {
	hermeticLocalEnv(t)
	st := testStore(t)
	seed(t, st, el("a::f1", "func f1() {}"))
	sc := newLocalSidecar(t, 2048) // budget 4032 runes, recorded in the stamp
	if _, err := localRun(t, st, sc, "full"); err != nil {
		t.Fatalf("full: %v", err)
	}
	sc.takeInputs()

	sc.setNCtx(0) // /props now 404
	seed(t, st, el("a::big", "marker-big\n"+strings.Repeat("x", 9000)))
	exportLocalEnv(t, sc)
	var export bytes.Buffer
	if err := ExportNDJSON(context.Background(), st, localModel(), &export); err != nil {
		t.Fatalf("export with /props down and a stored cap: %v", err)
	}
	exported := map[string]string{}
	sc2 := bufio.NewScanner(&export)
	for sc2.Scan() {
		var line exportLine
		if err := json.Unmarshal(sc2.Bytes(), &line); err != nil {
			t.Fatalf("decode export line: %v", err)
		}
		exported[line.QualifiedName] = line.Text
	}
	if n := utf8.RuneCountInString(exported["a::big"]); n != 4032 {
		t.Fatalf("export text for a::big is %d runes, want the stored cap 4032", n)
	}

	if _, err := localRun(t, st, sc, "incremental"); err != nil {
		t.Fatalf("incremental with /props down: %v", err)
	}
	var hits []string
	for _, in := range sc.takeInputs() {
		if strings.Contains(in, "marker-big") {
			hits = append(hits, in)
		}
	}
	if len(hits) != 1 || exported["a::big"] != hits[0] {
		t.Fatalf("export text != live input for a::big: %d live inputs carry the marker", len(hits))
	}
}

// TestExportRefusesWhenNoCapIsDeterminable: no stored cap, no /props budget and
// no LEANKG_EMBED_TEXT_CAP pin leave the write cap undetermined, so the export
// fails before writing and names the pin.
func TestExportRefusesWhenNoCapIsDeterminable(t *testing.T) {
	hermeticLocalEnv(t)
	st := testStore(t)
	seed(t, st, el("a::f1", "func f1() {}"))
	sc := newLocalSidecar(t, 0) // /props 404, no pin, no stamp
	exportLocalEnv(t, sc)

	var buf bytes.Buffer
	err := ExportNDJSON(context.Background(), st, localModel(), &buf)
	if err == nil || !strings.Contains(err.Error(), "LEANKG_EMBED_TEXT_CAP") {
		t.Fatalf("export with nothing to determine the cap: err = %v, want a refusal naming LEANKG_EMBED_TEXT_CAP", err)
	}
	if buf.Len() != 0 {
		t.Fatalf("refused export wrote %d bytes", buf.Len())
	}
}

// TestExportUsesPinWhenNoBudgetOrStoredCap: with /props down and no stored cap,
// the operator pin is the export's cut.
func TestExportUsesPinWhenNoBudgetOrStoredCap(t *testing.T) {
	hermeticLocalEnv(t)
	t.Setenv("LEANKG_EMBED_TEXT_CAP", "1000")
	st := testStore(t)
	seed(t, st, el("a::big", strings.Repeat("x", 9000)))
	sc := newLocalSidecar(t, 0) // /props 404
	exportLocalEnv(t, sc)

	var buf bytes.Buffer
	if err := ExportNDJSON(context.Background(), st, localModel(), &buf); err != nil {
		t.Fatalf("export with the pin and no /props: %v", err)
	}
	var line exportLine
	if err := json.Unmarshal(buf.Bytes(), &line); err != nil {
		t.Fatalf("decode export line: %v", err)
	}
	if n := utf8.RuneCountInString(line.Text); n != 1000 {
		t.Fatalf("export text is %d runes, want the pinned 1000", n)
	}
}
