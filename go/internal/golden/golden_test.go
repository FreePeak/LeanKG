package golden

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/FreePeak/LeanKG/go/internal/core"
	"github.com/FreePeak/LeanKG/go/internal/embed"
	"github.com/FreePeak/LeanKG/go/internal/memory"
	"github.com/FreePeak/LeanKG/go/internal/store"
)

var update = flag.Bool("update", false, "rewrite the golden files in ../../testdata/golden")

const goldenDir = "../../testdata/golden"

// TestGolden pins the envelope wire shapes against go/testdata/golden/*.json.
// The fixture store is deterministic: 2 elements, 1 calls edge, 1 embedding
// (dims 8 via the hash-seeded provider). Nondeterminism (temp paths,
// watermark seq/at) is redacted to fixed strings BEFORE comparison, so the
// goldens are byte-stable across runs and diffable by the W7 cutover.
func TestGolden(t *testing.T) {
	if skipGoldensUnderTstree() {
		t.Skip("goldens capture the regex tier; tree-sitter extraction differs by design")
	}
	ctx := context.Background()
	root := t.TempDir()
	project := filepath.Join(root, "proj")
	lk := filepath.Join(project, ".leankg")
	if err := os.MkdirAll(lk, 0o755); err != nil {
		t.Fatal(err)
	}

	st := openStore(t, filepath.Join(lk, "leankg.db"))
	defer st.Close()
	seed(t, ctx, st)

	mem, err := memory.Open(project, false)
	if err != nil {
		t.Fatal(err)
	}
	defer mem.Close()
	seedMemory(t, mem)

	eng := core.New(st, mem, core.QueryEmbedderFromProvider(embed.Deterministic(8)))

	status, err := eng.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	memSnap, err := eng.MemoryRead("snapshot", "", "", 0)
	if err != nil {
		t.Fatal(err)
	}

	q := func(req core.QueryRequest) map[string]any {
		r, err := eng.Query(ctx, req)
		if err != nil {
			t.Fatal(err)
		}
		return r
	}

	cases := []struct {
		name string
		v    any
	}{
		{"status", status},
		{"query_l0", l0Response(t, ctx, lk)},
		{"query_l1_exact", q(core.QueryRequest{Query: "Alpha"})},
		{"query_l2_fuzzy", q(core.QueryRequest{Action: "fuzzy", Query: "beta"})},
		{"query_l3_semantic", q(core.QueryRequest{Action: "semantic", Query: "compute the answer", Limit: 1})},
		{"query_impact", q(core.QueryRequest{Action: "impact", Query: "src/beta.go::Beta"})},
		{"memory_snapshot", memSnap},
	}

	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			got := canon(t, c.v, project)
			path := filepath.Join(goldenDir, c.name+".json")
			if *update {
				if err := os.WriteFile(path, pretty(t, got), 0o644); err != nil {
					t.Fatal(err)
				}
				return
			}
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("golden missing (run with -update): %v", err)
			}
			if want := compact(t, raw); !bytes.Equal(want, got) {
				t.Fatalf("golden mismatch for %s\n--- got ---\n%s\n--- want (run with -update) ---\n%s",
					c.name, pretty(t, got), string(raw))
			}
		})
	}
}

// l0Response builds a second, never-seeded store in the same project so the
// ladder router hits the L0 cold rung.
func l0Response(t *testing.T, ctx context.Context, lk string) map[string]any {
	t.Helper()
	empty := openStore(t, filepath.Join(lk, "empty.db"))
	defer empty.Close()
	eng := core.New(empty, nil, nil)
	r, err := eng.Query(ctx, core.QueryRequest{Query: "anything"})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func openStore(t *testing.T, path string) *store.Store {
	t.Helper()
	st, err := store.Open(path, store.RW)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Migrate(); err != nil {
		t.Fatal(err)
	}
	return st
}

// seed writes the fixed corpus: two elements, one calls edge, one stamped
// collection with a single deterministic vector.
func seed(t *testing.T, ctx context.Context, st *store.Store) {
	t.Helper()
	els := []store.Element{
		{
			QualifiedName: "src/alpha.go::Alpha", ElementType: "function", Name: "Alpha",
			FilePath: "src/alpha.go", LineStart: 1, LineEnd: 3, Language: "go",
			Content: "func Alpha() int {\n\treturn Beta() + 1\n}",
		},
		{
			QualifiedName: "src/beta.go::Beta", ElementType: "function", Name: "Beta",
			FilePath: "src/beta.go", LineStart: 6, LineEnd: 8, Language: "go",
			Content: "func Beta() int {\n\treturn 41\n}",
		},
	}
	if err := st.UpsertElements(els); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertRelationships([]store.Relationship{{
		Source: "src/alpha.go::Alpha", Target: "src/beta.go::Beta",
		RelType: "calls", Confidence: 0.5,
	}}); err != nil {
		t.Fatal(err)
	}

	prov := embed.Deterministic(8)
	if err := st.WriteStamp(store.ModelStamp{
		ModelID: prov.ModelID(), Revision: prov.Revision(),
		Dimensions: prov.Dimensions(), Distance: prov.Distance(), Provider: prov.Provider(),
	}); err != nil {
		t.Fatal(err)
	}
	vecs, err := prov.Embed(ctx, embed.Document, []string{els[0].Content})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertVectors(prov.ModelID(), []store.VectorRow{
		{QualifiedName: els[0].QualifiedName, Vec: vecs[0]},
	}); err != nil {
		t.Fatal(err)
	}
}

func seedMemory(t *testing.T, mem *memory.Memory) {
	t.Helper()
	p := filepath.Join(mem.Root(), "MEMORY.md")
	if err := os.WriteFile(p, []byte("# Project Memory\n\n- Golden fixture corpus pins the L0-L3 ladder.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

var (
	reWatermarkSeq = regexp.MustCompile(`"seq":\s*-?\d+`)
	reWatermarkAt  = regexp.MustCompile(`"at":\s*-?\d+`)
)

// canon marshals v, redacts nondeterminism to fixed strings, and re-marshals
// compact (sorted keys) as the comparison form.
func canon(t *testing.T, v any, project string) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	s = strings.ReplaceAll(s, project, "<PROJECT>")
	s = reWatermarkSeq.ReplaceAllLiteralString(s, `"seq":"<SEQ>"`)
	s = reWatermarkAt.ReplaceAllLiteralString(s, `"at":"<AT>"`)
	var out any
	if err := json.Unmarshal([]byte(s), &out); err != nil {
		t.Fatalf("redaction produced invalid JSON: %v\n%s", err, s)
	}
	return marshalCompact(t, out)
}

func compact(t *testing.T, raw []byte) []byte {
	t.Helper()
	var out any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("golden file is not valid JSON: %v", err)
	}
	return marshalCompact(t, out)
}

// marshalCompact emits sorted-key compact JSON without HTML escaping so
// placeholders like <PROJECT> stay literal and diffable.
func marshalCompact(t *testing.T, v any) []byte {
	t.Helper()
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		t.Fatal(err)
	}
	return bytes.TrimRight(buf.Bytes(), "\n")
}

func pretty(t *testing.T, compactJSON []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := json.Indent(&buf, compactJSON, "", "  "); err != nil {
		t.Fatal(err)
	}
	buf.WriteByte('\n')
	return buf.Bytes()
}
