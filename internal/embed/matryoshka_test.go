package embed

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

// newFixedVectorServer is a fake OpenAI-compatible sidecar that answers every
// text with vec, the wire vector at the model's native width. /props reports
// n_ctx 2048 (the local text budget source).
func newFixedVectorServer(t *testing.T, vec []float32) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/props":
			_, _ = w.Write([]byte(`{"model_path":"/cache/embeddinggemma-2.gguf","default_generation_settings":{"n_ctx":2048}}`))
		case "/embeddings":
			var body embeddingsRequest
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				http.Error(w, "bad json", http.StatusBadRequest)
				return
			}
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
	return srv
}

// newEmbeddingGemmaServer serves the native 768-d width with a known vector per
// text: components 0..511 = 2.0 and 512..767 = 7.0.
func newEmbeddingGemmaServer(t *testing.T) *httptest.Server {
	t.Helper()
	vec := make([]float32, 768)
	for i := range vec {
		vec[i] = 7
		if i < 512 {
			vec[i] = 2
		}
	}
	return newFixedVectorServer(t, vec)
}

// gemmaCfg is the configuration for the local EmbeddingGemma 2 row against
// baseURL; dims "" leaves the width unset (the native 768-d width).
func gemmaCfg(t *testing.T, baseURL, dims string) Config {
	t.Helper()
	cfg := Config{Provider: "local", BaseURL: baseURL, Model: "embeddinggemma-2"}
	if dims != "" {
		n, err := strconv.Atoi(dims)
		if err != nil {
			t.Fatal(err)
		}
		cfg.Dims = n
	}
	return cfg
}

// gemmaEntryPoints are the three constructors a configuration reaches. Each
// must apply Config.Dims the same way.
var gemmaEntryPoints = []struct {
	name  string
	build func(Config) (Provider, error)
}{
	{"Attach", func(cfg Config) (Provider, error) {
		p, release, err := Attach(cfg)
		release()
		return p, err
	}},
	{"Start", func(cfg Config) (Provider, error) {
		p, release, err := Start(context.Background(), cfg)
		release()
		return p, err
	}},
	{"AttachHook", func(cfg Config) (Provider, error) {
		p, release, err := AttachHook(cfg)
		release()
		return p, err
	}},
}

func TestMatryoshkaTruncatesAndRenormalizes(t *testing.T) {
	const wantComponent = 0.0441941738 // 1/sqrt(512)
	srv := newEmbeddingGemmaServer(t)
	for _, ep := range gemmaEntryPoints {
		t.Run(ep.name, func(t *testing.T) {
			p, err := ep.build(gemmaCfg(t, srv.URL, "512"))
			if err != nil {
				t.Fatalf("construct: %v", err)
			}
			vecs, err := p.Embed(context.Background(), Document, []string{"func f()"})
			if err != nil {
				t.Fatalf("Embed: %v", err)
			}
			if len(vecs) != 1 || len(vecs[0]) != 512 {
				t.Fatalf("shape: %d x %d, want 1 x 512", len(vecs), len(vecs[0]))
			}
			for i, x := range vecs[0] {
				if math.Abs(float64(x)-wantComponent) > 1e-6 {
					t.Fatalf("component %d = %v, want %v (2.0 renormalized over 512 components)", i, x, wantComponent)
				}
			}
			if p.Identity().Dims != 512 {
				t.Fatalf("Dimensions() = %d, want 512", p.Identity().Dims)
			}

			native, _, err := Attach(gemmaCfg(t, srv.URL, ""))
			if err != nil {
				t.Fatalf("native Attach: %v", err)
			}
			want := StampOf(native)
			want.Dimensions = 512
			if got := StampOf(p); got != want {
				t.Fatalf("stamp = %+v, want the native stamp with Dimensions 512: %+v", got, want)
			}
		})
	}
}

func TestMatryoshkaRejectsUnsupportedWidth(t *testing.T) {
	srv := newEmbeddingGemmaServer(t)
	for _, ep := range gemmaEntryPoints {
		t.Run(ep.name, func(t *testing.T) {
			_, err := ep.build(gemmaCfg(t, srv.URL, "300"))
			if err == nil {
				t.Fatal("want a configuration error for dims 300")
			}
			for _, width := range []string{"512", "256", "128"} {
				if !strings.Contains(err.Error(), width) {
					t.Fatalf("error %q does not name allowed width %s", err, width)
				}
			}
		})
	}
}

func TestMatryoshkaNativeWidthUnchanged(t *testing.T) {
	srv := newEmbeddingGemmaServer(t)
	p, _, err := Attach(gemmaCfg(t, srv.URL, "768"))
	if err != nil {
		t.Fatalf("Attach: %v", err)
	}
	vecs, err := p.Embed(context.Background(), Document, []string{"func f()"})
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if p.Identity().Dims != 768 || len(vecs[0]) != 768 {
		t.Fatalf("width: Dimensions() = %d, vector = %d, want 768", p.Identity().Dims, len(vecs[0]))
	}
	if vecs[0][0] != 2 || vecs[0][767] != 7 {
		t.Fatalf("vector altered: [0]=%v [767]=%v, want 2 and 7 as sent", vecs[0][0], vecs[0][767])
	}
}

// TestMatryoshkaKeepsTextBudget guards TextCap: the local sidecar's n_ctx
// budget must still reach TextCap through the truncation wrapper (2048 tokens
// -> 2*2048-64 = 4032 runes), not fall back to maxLocalTextChars (1000).
func TestMatryoshkaKeepsTextBudget(t *testing.T) {
	srv := newEmbeddingGemmaServer(t)
	p, _, err := Attach(gemmaCfg(t, srv.URL, "512"))
	if err != nil {
		t.Fatalf("Attach: %v", err)
	}
	if err := p.Probe(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := TextCap(p); got != 4032 {
		t.Fatalf("TextCap under truncation = %d, want 4032 from the sidecar n_ctx", got)
	}
}

func TestTruncateNormalize(t *testing.T) {
	cases := []struct {
		name string
		in   []float32
		n    int
		want []float32
	}{
		{"keeps the prefix and normalizes it", []float32{3, 4, 100}, 2, []float32{0.6, 0.8}},
		{"full width only normalizes", []float32{3, 4}, 2, []float32{0.6, 0.8}},
		{"constant block", []float32{2, 2, 2, 2, 7}, 4, []float32{0.5, 0.5, 0.5, 0.5}},
		{"zero prefix stays zero, no NaN", []float32{0, 0, 5}, 2, []float32{0, 0}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := TruncateNormalize(tc.in, tc.n)
			if len(got) != len(tc.want) {
				t.Fatalf("len = %d, want %d", len(got), len(tc.want))
			}
			for i := range tc.want {
				if math.IsNaN(float64(got[i])) || math.Abs(float64(got[i]-tc.want[i])) > 1e-6 {
					t.Fatalf("component %d = %v, want %v", i, got[i], tc.want[i])
				}
			}
		})
	}

	in := []float32{3, 4, 100}
	_ = TruncateNormalize(in, 2)
	if in[0] != 3 || in[1] != 4 || in[2] != 100 {
		t.Fatalf("input mutated: %v", in)
	}
}
