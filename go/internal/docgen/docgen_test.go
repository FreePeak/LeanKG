package docgen

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/FreePeak/LeanKG/go/internal/store"
)

type fixtureSpec struct {
	Modules int
	Files   int
	Funcs   int
	Classes int
	// RelCounts maps relationship type -> number of edges inserted. Each type
	// must stay at or below Funcs (edge endpoints are generated as distinct
	// pairs while a type's occurrence index stays under Funcs).
	RelCounts map[string]int
}

// newFixture opens a migrated SQLite store in a temp dir and loads the spec.
func newFixture(t *testing.T, spec fixtureSpec) store.Backend {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), ".leankg", "leankg.db"), store.RW)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.Migrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	var els []store.Element
	for i := 0; i < spec.Files; i++ {
		qn := fmt.Sprintf("src/f%02d.go", i)
		els = append(els, store.Element{QualifiedName: qn, Name: qn, ElementType: "file", FilePath: qn, Language: "go"})
	}
	for i := 0; i < spec.Modules; i++ {
		fp := fmt.Sprintf("src/mod%02d.go", i)
		els = append(els, store.Element{QualifiedName: fmt.Sprintf("mod%02d", i), Name: fp, ElementType: "module", FilePath: fp, Language: "go"})
	}
	for i := 0; i < spec.Funcs; i++ {
		els = append(els, store.Element{
			QualifiedName: fmt.Sprintf("fn%02d", i),
			Name:          fmt.Sprintf("fn%02d", i),
			ElementType:   "function",
			FilePath:      "src/lib.rs",
			LineStart:     100 + i,
		})
	}
	for i := 0; i < spec.Classes; i++ {
		els = append(els, store.Element{
			QualifiedName: fmt.Sprintf("Class%02d", i),
			Name:          fmt.Sprintf("Class%02d", i),
			ElementType:   "class",
			FilePath:      "src/lib.rs",
			LineStart:     500 + i,
		})
	}
	if len(els) > 0 {
		if err := st.UpsertElements(els); err != nil {
			t.Fatalf("upsert elements: %v", err)
		}
	}

	var rels []store.Relationship
	k := 0 // global edge counter keeps (source,target) pairs unique per type
	for typ, count := range spec.RelCounts {
		for j := 0; j < count; j++ {
			rels = append(rels, store.Relationship{
				Source:     fmt.Sprintf("fn%02d", k%max(spec.Funcs, 1)),
				Target:     fmt.Sprintf("fn%02d", (k+1)%max(spec.Funcs, 1)),
				RelType:    typ,
				Confidence: 0.9,
			})
			k++
		}
	}
	if len(rels) > 0 {
		if err := st.UpsertRelationships(rels); err != nil {
			t.Fatalf("upsert relationships: %v", err)
		}
	}
	return st
}

// between returns the body text between start and the next occurrence of end,
// so bucket assertions cannot bleed into neighboring sections.
func between(t *testing.T, body, start, end string) string {
	t.Helper()
	i := strings.Index(body, start)
	if i < 0 {
		t.Fatalf("start marker %q missing", start)
	}
	rest := body[i+len(start):]
	j := strings.Index(rest, end)
	if j < 0 {
		t.Fatalf("end marker %q missing after %q", end, start)
	}
	return rest[:j]
}

func countLines(s, prefix string) int {
	n := 0
	for _, ln := range strings.Split(s, "\n") {
		if strings.HasPrefix(ln, prefix) {
			n++
		}
	}
	return n
}

// baseRelCounts: 12 types with count-desc ordering and equal-count ties, so
// top-10 selection and the tie-break are both exercised.
var baseRelCounts = map[string]int{
	"calls": 9, "imports": 7, "references": 7, "defines": 6, "uses": 6,
	"extends": 5, "implements": 5, "contains": 4, "returns": 4, "awaits": 3,
	"throws": 2, "annotates": 1,
}

func TestAgentsMDCountsAndCaps(t *testing.T) {
	st := newFixture(t, fixtureSpec{Modules: 25, Files: 35, Funcs: 55, Classes: 35, RelCounts: baseRelCounts})
	body, err := AgentsMD(st)
	if err != nil {
		t.Fatalf("AgentsMD: %v", err)
	}

	wantCount := fmt.Sprintf("This codebase contains %d elements and %d relationships.\n\n", 25+35+55+35, 59)
	if !strings.Contains(body, wantCount) {
		t.Errorf("counts line missing: want %q", wantCount)
	}

	// Modules render 20 quietly: Rust emits no "... more modules" line.
	mods := between(t, body, "### Key Modules\n\n", "### Files\n\n")
	if n := countLines(mods, "- `mod"); n != maxModules {
		t.Errorf("modules bucket = %d lines, want %d", n, maxModules)
	}
	if strings.Contains(mods, "... and") {
		t.Error("modules bucket must truncate silently")
	}
	if !strings.Contains(mods, "- `mod00` (src/mod00.go)") || !strings.Contains(mods, "- `mod19` (src/mod19.go)") {
		t.Error("modules bucket missing its sorted head")
	}
	if strings.Contains(mods, "mod20") {
		t.Error("modules bucket must stop after 20 entries")
	}

	files := between(t, body, "### Files\n\n", "### Functions\n\n")
	if n := countLines(files, "- `src/f"); n != maxFiles {
		t.Errorf("files bucket = %d lines, want %d", n, maxFiles)
	}
	if want := "- ... and 5 more files\n"; !strings.Contains(files, want) {
		t.Errorf("files truncation line missing (want %q)", want)
	}
	if !strings.Contains(files, "- `src/f29.go`") || strings.Contains(files, "src/f30.go") {
		t.Error("files bucket must list the 30 lowest file_paths")
	}

	funcs := between(t, body, "### Functions\n\n", "### Classes/Structs\n\n")
	if n := countLines(funcs, "- `fn"); n != maxFuncs {
		t.Errorf("functions bucket = %d lines, want %d", n, maxFuncs)
	}
	if want := "- ... and 5 more functions\n"; !strings.Contains(funcs, want) {
		t.Errorf("functions truncation line missing (want %q)", want)
	}
	if !strings.Contains(funcs, "- `fn00` (src/lib.rs:100)") || !strings.Contains(funcs, "- `fn49` (src/lib.rs:149)") {
		t.Error("functions bucket missing its sorted head")
	}

	classes := between(t, body, "### Classes/Structs\n\n", "---\n\n## Relationship Types\n\n")
	if n := countLines(classes, "- `Class"); n != maxClasses {
		t.Errorf("classes bucket = %d lines, want %d", n, maxClasses)
	}
	if want := "- ... and 5 more classes\n"; !strings.Contains(classes, want) {
		t.Errorf("classes truncation line missing (want %q)", want)
	}
	if !strings.Contains(classes, "- `Class29` (src/lib.rs:529)") || strings.Contains(classes, "Class30") {
		t.Error("classes bucket must stop after 30 entries")
	}

	order := []string{
		"## Project Overview\n", "## Build Commands\n", "### Key Modules\n\n",
		"### Files\n\n", "### Functions\n\n", "### Classes/Structs\n\n",
		"## Relationship Types\n", "## Testing Guidelines\n",
	}
	last := -1
	for _, h := range order {
		i := strings.Index(body, h)
		if i < 0 {
			t.Fatalf("heading %q missing", h)
		}
		if i < last {
			t.Errorf("heading %q out of order", h)
		}
		last = i
	}
}

func TestAgentsMDRelationshipHistogram(t *testing.T) {
	st := newFixture(t, fixtureSpec{Modules: 1, Files: 1, Funcs: 55, RelCounts: baseRelCounts})
	body, err := AgentsMD(st)
	if err != nil {
		t.Fatalf("AgentsMD: %v", err)
	}
	hist := between(t, body, "---\n\n## Relationship Types\n\n", "---\n\n## Testing Guidelines\n\n")
	// count DESC, then rel_type ASC for ties; only the top 10 of 12 types.
	want := strings.Join([]string{
		"- `calls`: 9 occurrences",
		"- `imports`: 7 occurrences",
		"- `references`: 7 occurrences",
		"- `defines`: 6 occurrences",
		"- `uses`: 6 occurrences",
		"- `extends`: 5 occurrences",
		"- `implements`: 5 occurrences",
		"- `contains`: 4 occurrences",
		"- `returns`: 4 occurrences",
		"- `awaits`: 3 occurrences",
		"",
		"",
	}, "\n")
	if hist != want {
		t.Errorf("histogram mismatch:\n got: %q\nwant: %q", hist, want)
	}
}

func TestAgentsMDEmptyModulesFallsBackToTree(t *testing.T) {
	st := newFixture(t, fixtureSpec{Files: 2, Funcs: 1})
	body, err := AgentsMD(st)
	if err != nil {
		t.Fatalf("AgentsMD: %v", err)
	}
	if !strings.Contains(body, "```\nleankg/\n") || !strings.Contains(body, "cmd/leankg-embed/    # Embedding pipeline binary") {
		t.Errorf("static repository tree missing for empty module set:\n%s", body)
	}
	if strings.Contains(body, "- `mod") {
		t.Error("no module bucket expected")
	}
}

func TestAgentsMDNoTruncationUnderCaps(t *testing.T) {
	st := newFixture(t, fixtureSpec{Modules: 2, Files: 3, Funcs: 5, Classes: 1, RelCounts: map[string]int{"calls": 3}})
	body, err := AgentsMD(st)
	if err != nil {
		t.Fatalf("AgentsMD: %v", err)
	}
	if strings.Contains(body, "... and") {
		t.Errorf("no bucket may emit a truncation line under its cap:\n%s", body)
	}
	if !strings.Contains(body, "This codebase contains 11 elements and 3 relationships.\n\n") {
		t.Errorf("counts line wrong:\n%s", body)
	}
}

func TestAgentsMDDeterministic(t *testing.T) {
	st := newFixture(t, fixtureSpec{Modules: 25, Files: 35, Funcs: 55, Classes: 35, RelCounts: baseRelCounts})
	a, err := AgentsMD(st)
	if err != nil {
		t.Fatalf("AgentsMD: %v", err)
	}
	b, err := AgentsMD(st)
	if err != nil {
		t.Fatalf("AgentsMD (2nd): %v", err)
	}
	if a != b {
		t.Error("two renders of the same graph differ")
	}
}

func TestWriteCreatesAgentsMD(t *testing.T) {
	st := newFixture(t, fixtureSpec{Modules: 25, Files: 35, Funcs: 55, Classes: 35, RelCounts: baseRelCounts})
	docsDir := filepath.Join(t.TempDir(), "docs")

	path, err := Write(st, docsDir)
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if want := filepath.Join(docsDir, "AGENTS.md"); path != want {
		t.Fatalf("path = %q, want %q", path, want)
	}
	info, err := os.Stat(docsDir)
	if err != nil || !info.IsDir() {
		t.Fatalf("docs dir not created: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	body, err := AgentsMD(st)
	if err != nil {
		t.Fatalf("AgentsMD: %v", err)
	}
	if string(got) != body {
		t.Error("file bytes differ from AgentsMD output")
	}
	// The document contains only relative paths; the temp dir must not leak in.
	if strings.Contains(body, t.TempDir()) {
		t.Error("absolute temp path leaked into the document")
	}
}

func TestAgentsMDDescribesTheGoEngine(t *testing.T) {
	st := newFixture(t, fixtureSpec{Files: 1, Funcs: 1})
	body, err := AgentsMD(st)
	if err != nil {
		t.Fatalf("AgentsMD: %v", err)
	}
	for _, want := range []string{
		"LeanKG is a Go knowledge graph system",
		"**Tech Stack**: Go 1.25",
		"cd go && go test ./... -count=1",
		"-tags tstree",
		"t.TempDir()",
		"gofmt -l go",
		"cmd/leankg-embed/",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("generated guide must carry the Go toolchain claim %q", want)
		}
	}
	// The Rust-era claims must never come back: they were a false statement about
	// the shipped engine, not a formatting detail.
	for _, forbidden := range []string{"Rust-based", "cargo build", "cargo test", "#[cfg(test)]", "tokio::test", "Axum", "Clap", "src/main.rs"} {
		if strings.Contains(body, forbidden) {
			t.Errorf("generated guide still carries the Rust-era literal %q", forbidden)
		}
	}
}
