package pack

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/FreePeak/LeanKG/go/internal/store"
)

// snapshotElement/snapshotRelationship mirror the on-disk snapshot shape; the
// pointer fields must round-trip as null (no store column, empty sentinel).
type snapshotElement struct {
	QualifiedName   string  `json:"qualified_name"`
	ElementType     string  `json:"element_type"`
	Name            string  `json:"name"`
	FilePath        string  `json:"file_path"`
	LineStart       int     `json:"line_start"`
	LineEnd         int     `json:"line_end"`
	Language        string  `json:"language"`
	ClusterID       *string `json:"cluster_id"`
	ClusterLabel    *string `json:"cluster_label"`
	ParentQualified *string `json:"parent_qualified"`
}

type snapshotRelationship struct {
	Source     string  `json:"source"`
	Target     string  `json:"target"`
	RelType    string  `json:"rel_type"`
	Confidence float64 `json:"confidence"`
}

type snapshotDoc struct {
	Version       int                    `json:"version"`
	Kind          string                 `json:"kind"`
	Elements      []snapshotElement      `json:"elements"`
	Relationships []snapshotRelationship `json:"relationships"`
}

// newStore seeds a real SQLite store (the internal/graph fixture pattern) so
// the pack path exercises the actual store reads.
func newStore(t *testing.T, root string, els []store.Element, rels []store.Relationship) store.Backend {
	t.Helper()
	s, err := store.Open(filepath.Join(root, ".leankg", "leankg.db"), store.RW)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if err := s.Migrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if len(els) > 0 {
		if err := s.UpsertElements(els); err != nil {
			t.Fatalf("upsert elements: %v", err)
		}
	}
	if len(rels) > 0 {
		if err := s.UpsertRelationships(rels); err != nil {
			t.Fatalf("upsert relationships: %v", err)
		}
	}
	return s
}

// fixture: two in-scope elements under src/, one out-of-scope under tests/, and
// one edge that stays in scope (src->src) plus one that must be dropped
// (src->tests) once the scope excludes tests/.
func fixture(t *testing.T) (store.Backend, string) {
	t.Helper()
	root := t.TempDir()
	els := []store.Element{
		{QualifiedName: "src/a.go::alpha", ElementType: "function", Name: "alpha", FilePath: "src/a.go", LineStart: 1, LineEnd: 3, Language: "go", ParentQualified: "src/a.go::Widget"},
		{QualifiedName: "src/b.go::beta", ElementType: "function", Name: "beta", FilePath: "src/b.go", LineStart: 5, LineEnd: 7, Language: "go"},
		{QualifiedName: "tests/t.go::gamma", ElementType: "function", Name: "gamma", FilePath: "tests/t.go", LineStart: 1, LineEnd: 2, Language: "go"},
	}
	rels := []store.Relationship{
		{Source: "src/a.go::alpha", Target: "src/b.go::beta", RelType: "calls", Confidence: 0.75},
		{Source: "src/a.go::alpha", Target: "tests/t.go::gamma", RelType: "calls", Confidence: 0.5},
	}
	return newStore(t, root, els, rels), root
}

func readSnapshot(t *testing.T, dir string) (snapshotDoc, []byte) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, "snapshot.json"))
	if err != nil {
		t.Fatalf("read snapshot: %v", err)
	}
	var doc snapshotDoc
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("decode snapshot: %v", err)
	}
	return doc, raw
}

func readFile(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return b
}

func TestManifestAndSnapshotWritten(t *testing.T) {
	st, root := fixture(t)
	out := filepath.Join(root, "pack")

	m, err := WritePack(st, out, Options{})
	if err != nil {
		t.Fatalf("WritePack: %v", err)
	}
	for _, name := range []string{"manifest.json", "snapshot.json"} {
		if _, err := os.Stat(filepath.Join(out, name)); err != nil {
			t.Fatalf("%s not written: %v", name, err)
		}
	}

	if m.SchemaVersion != 1 {
		t.Errorf("schema_version = %d, want 1", m.SchemaVersion)
	}
	if m.Kind != "leankg.context.pack" {
		t.Errorf("kind = %q, want leankg.context.pack", m.Kind)
	}
	if m.Elements != 3 || m.Relationships != 2 {
		t.Errorf("counts = %d elements / %d rels, want 3/2", m.Elements, m.Relationships)
	}
	if len(m.ContentHash) != 64 {
		t.Errorf("content_hash = %q, want 64 hex chars", m.ContentHash)
	}
	if m.SourceRevision != nil || m.PathScope != nil {
		t.Errorf("unset options must manifest as null, got %v / %v", m.SourceRevision, m.PathScope)
	}

	doc, snapBytes := readSnapshot(t, out)
	sum := sha256.Sum256(snapBytes)
	if m.ContentHash != hex.EncodeToString(sum[:]) {
		t.Errorf("content_hash %s does not cover the snapshot bytes", m.ContentHash)
	}
	if doc.Version != 1 || doc.Kind != "leankg.context.pack.snapshot" {
		t.Errorf("snapshot header = %d/%q", doc.Version, doc.Kind)
	}
	if len(doc.Elements) != 3 || len(doc.Relationships) != 2 {
		t.Fatalf("snapshot holds %d elements / %d rels, want 3/2", len(doc.Elements), len(doc.Relationships))
	}

	// Exact element key set (Rust snapshot shape) and null cluster columns.
	var raw struct {
		Elements []map[string]any `json:"elements"`
	}
	if err := json.Unmarshal(snapBytes, &raw); err != nil {
		t.Fatalf("decode raw elements: %v", err)
	}
	keys := make([]string, 0, len(raw.Elements[0]))
	for k := range raw.Elements[0] {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	wantKeys := "cluster_id cluster_label element_type file_path language line_end line_start name parent_qualified qualified_name"
	if got := strings.Join(keys, " "); got != wantKeys {
		t.Errorf("element keys = %q, want %q", got, wantKeys)
	}
	if raw.Elements[0]["cluster_id"] != nil || raw.Elements[0]["cluster_label"] != nil {
		t.Errorf("cluster columns must be null, got %v / %v", raw.Elements[0]["cluster_id"], raw.Elements[0]["cluster_label"])
	}

	// Relative paths only, and no trailing newline (serde_json::to_vec_pretty parity).
	for _, e := range doc.Elements {
		if strings.HasPrefix(e.FilePath, "/") {
			t.Errorf("element %s carries absolute path %q", e.QualifiedName, e.FilePath)
		}
	}
	var alpha snapshotElement
	for _, e := range doc.Elements {
		if e.QualifiedName == "src/a.go::alpha" {
			alpha = e
		}
	}
	if alpha.ParentQualified == nil || *alpha.ParentQualified != "src/a.go::Widget" {
		t.Errorf("parent_qualified = %v, want src/a.go::Widget", alpha.ParentQualified)
	}
	if alpha.ClusterID != nil || alpha.ClusterLabel != nil {
		t.Errorf("cluster_id/label = %v/%v, want null", alpha.ClusterID, alpha.ClusterLabel)
	}
	for _, name := range []string{"manifest.json", "snapshot.json"} {
		b := readFile(t, filepath.Join(out, name))
		if bytes.HasSuffix(b, []byte("\n")) {
			t.Errorf("%s has a trailing newline", name)
		}
	}

	var manifest map[string]any
	if err := json.Unmarshal(readFile(t, filepath.Join(out, "manifest.json")), &manifest); err != nil {
		t.Fatalf("decode manifest: %v", err)
	}
	for _, k := range []string{"schema_version", "kind", "elements", "relationships", "content_hash", "source_revision", "path_scope"} {
		if _, ok := manifest[k]; !ok {
			t.Errorf("manifest is missing key %q", k)
		}
	}
	if manifest["source_revision"] != nil || manifest["path_scope"] != nil {
		t.Errorf("manifest should carry explicit nulls, got %v / %v", manifest["source_revision"], manifest["path_scope"])
	}
}

func TestSameGraphSameHash(t *testing.T) {
	st, root := fixture(t)
	out1 := filepath.Join(root, "p1")
	out2 := filepath.Join(root, "p2")

	m1, err := WritePack(st, out1, Options{})
	if err != nil {
		t.Fatalf("pack 1: %v", err)
	}
	m2, err := WritePack(st, out2, Options{})
	if err != nil {
		t.Fatalf("pack 2: %v", err)
	}
	if m1.ContentHash != m2.ContentHash {
		t.Errorf("deterministic content hash: %s != %s", m1.ContentHash, m2.ContentHash)
	}
	s1 := readFile(t, filepath.Join(out1, "snapshot.json"))
	s2 := readFile(t, filepath.Join(out2, "snapshot.json"))
	if !bytes.Equal(s1, s2) {
		t.Error("snapshots are not byte-identical across runs")
	}
}

func TestManifestIsByteIdenticalAcrossRuns(t *testing.T) {
	// Two packs of one graph into two directories must be byte-identical: no
	// wall-clock leak, no absolute project_root, no out-dir echo.
	st, root := fixture(t)
	out1 := filepath.Join(root, "p1")
	out2 := filepath.Join(root, "p2")

	for _, out := range []string{out1, out2} {
		if _, err := WritePack(st, out, Options{SourceRevision: "abc1234", Path: "src"}); err != nil {
			t.Fatalf("pack %s: %v", out, err)
		}
	}
	m1 := readFile(t, filepath.Join(out1, "manifest.json"))
	m2 := readFile(t, filepath.Join(out2, "manifest.json"))
	if !bytes.Equal(m1, m2) {
		t.Errorf("manifest must be byte-identical across runs:\n%s\n%s", m1, m2)
	}
	if !bytes.Equal(readFile(t, filepath.Join(out1, "snapshot.json")), readFile(t, filepath.Join(out2, "snapshot.json"))) {
		t.Error("snapshot must be byte-identical across runs")
	}

	for _, name := range []string{"manifest.json", "snapshot.json"} {
		b := readFile(t, filepath.Join(out1, name))
		if bytes.Contains(b, []byte(root)) {
			t.Errorf("%s leaks the absolute filesystem root %s", name, root)
		}
		if bytes.Contains(b, []byte(".leankg")) {
			t.Errorf("%s leaks the store path", name)
		}
	}
}

func TestPathScopeFiltersElements(t *testing.T) {
	st, root := fixture(t)
	cases := []struct {
		name     string
		scope    string
		elements int
		rels     int
		elNames  []string
	}{
		{name: "whole graph", scope: "", elements: 3, rels: 2,
			elNames: []string{"src/a.go::alpha", "src/b.go::beta", "tests/t.go::gamma"}},
		{name: "dir prefix", scope: "src", elements: 2, rels: 1,
			elNames: []string{"src/a.go::alpha", "src/b.go::beta"}},
		{name: "dotslash prefix", scope: "./src", elements: 2, rels: 1,
			elNames: []string{"src/a.go::alpha", "src/b.go::beta"}},
		{name: "single file", scope: "src/a.go", elements: 1, rels: 0,
			elNames: []string{"src/a.go::alpha"}},
		{name: "out of scope dir", scope: "tests", elements: 1, rels: 0,
			elNames: []string{"tests/t.go::gamma"}},
		{name: "no match", scope: "nomatch", elements: 0, rels: 0},
	}

	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := filepath.Join(root, fmt.Sprintf("scope-%d", i))
			m, err := WritePack(st, out, Options{Path: tc.scope})
			if err != nil {
				t.Fatalf("WritePack(%q): %v", tc.scope, err)
			}
			if m.Elements != tc.elements || m.Relationships != tc.rels {
				t.Errorf("counts = %d/%d, want %d/%d", m.Elements, m.Relationships, tc.elements, tc.rels)
			}
			// Scope is recorded verbatim, including a "./" form.
			if tc.scope == "" {
				if m.PathScope != nil {
					t.Errorf("path_scope = %v, want null", *m.PathScope)
				}
			} else if m.PathScope == nil || *m.PathScope != tc.scope {
				t.Errorf("path_scope = %v, want %q", m.PathScope, tc.scope)
			}

			doc, snapBytes := readSnapshot(t, out)
			var got []string
			for _, e := range doc.Elements {
				got = append(got, e.QualifiedName)
			}
			if strings.Join(got, ",") != strings.Join(tc.elNames, ",") {
				t.Errorf("elements = %v, want %v", got, tc.elNames)
			}
			// An empty slice must render as [] (never null), like the Rust Vec.
			if tc.elements == 0 && !bytes.Contains(snapBytes, []byte(`"elements": []`)) {
				t.Errorf("empty slice must render as []: %s", snapBytes)
			}
			// The cross-scope edge (src/a.go::alpha -> tests/t.go::gamma) must
			// not survive a scope that excludes either endpoint.
			if tc.scope != "" {
				for _, r := range doc.Relationships {
					if r.Target == "tests/t.go::gamma" {
						t.Errorf("cross-scope relationship survived scope %q", tc.scope)
					}
				}
			}
			// Scope lives in the manifest; the snapshot must stay relative.
			if bytes.Contains(snapBytes, []byte(root)) {
				t.Errorf("snapshot leaks the absolute root %s", root)
			}
		})
	}
}

func TestPathScopeRefusesToTruncateWhenOversize(t *testing.T) {
	// Contract: advertised as "refuses to truncate". If the slice matches more
	// than max_nodes, the call errors instead of silently dropping rows.
	st, root := fixture(t)
	out := filepath.Join(root, "oversize")

	m, err := WritePack(st, out, Options{MaxNodes: 2})
	if err == nil {
		t.Fatalf("oversize pack succeeded with %d elements, want refusal", m.Elements)
	}
	want := "pack slice 3 elements exceeds max_nodes=2 (refusing to truncate)"
	if err.Error() != want {
		t.Errorf("error = %q, want %q", err.Error(), want)
	}
	if _, statErr := os.Stat(filepath.Join(out, "snapshot.json")); !os.IsNotExist(statErr) {
		t.Errorf("oversize pack must not write a partial snapshot.json (err=%v)", statErr)
	}
}

func TestNonPositiveMaxNodesUsesDefault(t *testing.T) {
	root := t.TempDir()
	els := make([]store.Element, 0, DefaultMaxNodes+1)
	for i := 0; i <= DefaultMaxNodes; i++ {
		qn := fmt.Sprintf("src/f%04d.go::f%04d", i, i)
		els = append(els, store.Element{QualifiedName: qn, ElementType: "function", Name: qn, FilePath: fmt.Sprintf("src/f%04d.go", i), LineStart: 1, LineEnd: 1, Language: "go"})
	}
	st := newStore(t, root, els, nil)

	_, err := WritePack(st, filepath.Join(root, "default-cap"), Options{MaxNodes: 0})
	if err == nil {
		t.Fatalf("MaxNodes=0 must fall back to DefaultMaxNodes=%d and refuse %d elements", DefaultMaxNodes, len(els))
	}
	want := fmt.Sprintf("pack slice %d elements exceeds max_nodes=%d (refusing to truncate)", len(els), DefaultMaxNodes)
	if err.Error() != want {
		t.Errorf("error = %q, want %q", err.Error(), want)
	}
}

func TestPacksWholeEdgeSetBeyondStoreDefaultLimit(t *testing.T) {
	// store.Backend.RelationshipsAll treats limit<=0 as 1000 rows; a pack must
	// ask for the whole edge set, not the dashboard default cap.
	root := t.TempDir()
	els := []store.Element{
		{QualifiedName: "src/a.go::alpha", ElementType: "function", Name: "alpha", FilePath: "src/a.go", LineStart: 1, LineEnd: 1, Language: "go"},
		{QualifiedName: "src/b.go::beta", ElementType: "function", Name: "beta", FilePath: "src/b.go", LineStart: 1, LineEnd: 1, Language: "go"},
	}
	const edges = 1100
	rels := make([]store.Relationship, 0, edges)
	for i := 0; i < edges; i++ {
		rels = append(rels, store.Relationship{
			Source: "src/a.go::alpha", Target: "src/b.go::beta",
			RelType: fmt.Sprintf("r%04d", i), Confidence: 0.5,
		})
	}
	st := newStore(t, root, els, rels)

	out := filepath.Join(root, "wide")
	m, err := WritePack(st, out, Options{})
	if err != nil {
		t.Fatalf("WritePack: %v", err)
	}
	if m.Relationships != edges {
		t.Errorf("manifest relationships = %d, want %d (store default cap leaked)", m.Relationships, edges)
	}
	doc, _ := readSnapshot(t, out)
	if len(doc.Relationships) != edges {
		t.Errorf("snapshot relationships = %d, want %d", len(doc.Relationships), edges)
	}
}

func TestSnapshotOrderIsCanonical(t *testing.T) {
	root := t.TempDir()
	// Insertion order deliberately scrambles both sort keys.
	els := []store.Element{
		{QualifiedName: "z.go::z", ElementType: "function", Name: "z", FilePath: "z.go", LineStart: 1, LineEnd: 1, Language: "go"},
		{QualifiedName: "a.go::a", ElementType: "function", Name: "a", FilePath: "a.go", LineStart: 1, LineEnd: 1, Language: "go"},
		{QualifiedName: "m.go::m", ElementType: "function", Name: "m", FilePath: "m.go", LineStart: 1, LineEnd: 1, Language: "go"},
	}
	rels := []store.Relationship{
		{Source: "z.go::z", Target: "a.go::a", RelType: "calls", Confidence: 0.9},
		{Source: "a.go::a", Target: "z.go::z", RelType: "imports", Confidence: 0.9},
		{Source: "a.go::a", Target: "m.go::m", RelType: "calls", Confidence: 0.9},
		{Source: "a.go::a", Target: "z.go::z", RelType: "calls", Confidence: 0.9},
	}
	st := newStore(t, root, els, rels)

	out := filepath.Join(root, "ordered")
	if _, err := WritePack(st, out, Options{}); err != nil {
		t.Fatalf("WritePack: %v", err)
	}
	doc, _ := readSnapshot(t, out)

	var names []string
	for _, e := range doc.Elements {
		names = append(names, e.QualifiedName)
	}
	if want := "a.go::a,m.go::m,z.go::z"; strings.Join(names, ",") != want {
		t.Errorf("element order = %v, want %s", names, want)
	}

	var edges []string
	for _, r := range doc.Relationships {
		edges = append(edges, r.Source+"|"+r.RelType+"|"+r.Target)
	}
	want := "a.go::a|calls|m.go::m,a.go::a|calls|z.go::z,a.go::a|imports|z.go::z,z.go::z|calls|a.go::a"
	if strings.Join(edges, ",") != want {
		t.Errorf("relationship order = %v, want %s", edges, want)
	}
}

func TestRelativize(t *testing.T) {
	cases := []struct{ in, want string }{
		{"src/a.go", "src/a.go"},
		{"./src/a.go", "src/a.go"},
		{"././src/a.go", "src/a.go"},
		{"/src/a.go", "src/a.go"},
		{"//src/a.go", "src/a.go"},
		{"\\server\\share\\a.go", "server/share/a.go"},
		{"..\\src\\a.go", "../src/a.go"},
		{"", ""},
		{"../src/a.go", "../src/a.go"},
	}
	for _, tc := range cases {
		if got := relativize(tc.in); got != tc.want {
			t.Errorf("relativize(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestSnapshotBytesMatchRustShape pins the exact on-disk rendering ported from
// the Rust renderer: serde_json::Map key order (alphabetical), to_vec_pretty
// layout (two-space indent, one array item per line, no trailing newline),
// null for the absent cluster columns and for an absent parent_qualified, and
// relativized file paths. A migration artifact must stay byte-compatible.
func TestSnapshotBytesMatchRustShape(t *testing.T) {
	root := t.TempDir()
	els := []store.Element{
		{QualifiedName: "src/a.go::alpha", ElementType: "function", Name: "alpha", FilePath: "./src/a.go", LineStart: 1, LineEnd: 3, Language: "go", ParentQualified: "src/a.go::Widget"},
		{QualifiedName: "tests/t.go::gamma", ElementType: "function", Name: "gamma", FilePath: "tests/t.go", LineStart: 1, LineEnd: 2, Language: "go"},
	}
	rels := []store.Relationship{{Source: "src/a.go::alpha", Target: "tests/t.go::gamma", RelType: "calls", Confidence: 0.75}}
	st := newStore(t, root, els, rels)

	out := filepath.Join(root, "golden")
	if _, err := WritePack(st, out, Options{}); err != nil {
		t.Fatalf("WritePack: %v", err)
	}

	want := `{
  "elements": [
    {
      "cluster_id": null,
      "cluster_label": null,
      "element_type": "function",
      "file_path": "src/a.go",
      "language": "go",
      "line_end": 3,
      "line_start": 1,
      "name": "alpha",
      "parent_qualified": "src/a.go::Widget",
      "qualified_name": "src/a.go::alpha"
    },
    {
      "cluster_id": null,
      "cluster_label": null,
      "element_type": "function",
      "file_path": "tests/t.go",
      "language": "go",
      "line_end": 2,
      "line_start": 1,
      "name": "gamma",
      "parent_qualified": null,
      "qualified_name": "tests/t.go::gamma"
    }
  ],
  "kind": "leankg.context.pack.snapshot",
  "relationships": [
    {
      "confidence": 0.75,
      "rel_type": "calls",
      "source": "src/a.go::alpha",
      "target": "tests/t.go::gamma"
    }
  ],
  "version": 1
}`
	if got := string(readFile(t, filepath.Join(out, "snapshot.json"))); got != want {
		t.Errorf("snapshot bytes drifted from the Rust shape:\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}

	wantManifest := "{\n" +
		"  \"schema_version\": 1,\n" +
		"  \"kind\": \"leankg.context.pack\",\n" +
		"  \"elements\": 2,\n" +
		"  \"relationships\": 1,\n" +
		"  \"content_hash\": \"" + sha256Hex([]byte(want)) + "\",\n" +
		"  \"source_revision\": null,\n" +
		"  \"path_scope\": null\n" +
		"}"
	if got := string(readFile(t, filepath.Join(out, "manifest.json"))); got != wantManifest {
		t.Errorf("manifest bytes drifted from the Rust shape:\n--- got ---\n%s\n--- want ---\n%s", got, wantManifest)
	}
}

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
