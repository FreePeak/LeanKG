// Package docgen renders repository documentation from the indexed graph. It is
// the Go port of the Rust `doc::generator::DocGenerator::generate_agents_md`.
package docgen

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/FreePeak/LeanKG/go/internal/store"
)

// List truncation limits, mirroring the Rust generator.
const (
	maxModules  = 20
	maxFiles    = 30
	maxFuncs    = 50
	maxClasses  = 30
	maxRelTypes = 10
)

// allRelationshipsLimit caps the relationship read well above any realistic
// graph. store.Backend.RelationshipsAll treats limit <= 0 as 1000 rows, so the
// full edge set must be requested explicitly (same convention as web/api.go
// and internal/export).
const allRelationshipsLimit = 1 << 20

// AgentsMD renders the AGENTS.md body from the store (Rust
// DocGenerator::generate_agents_md).
func AgentsMD(st store.Backend) (string, error) {
	elements, err := st.Elements()
	if err != nil {
		return "", fmt.Errorf("docgen: list elements: %w", err)
	}
	relationships, err := allRelationships(st)
	if err != nil {
		return "", fmt.Errorf("docgen: list relationships: %w", err)
	}

	var b strings.Builder
	b.WriteString("# Agent Guidelines for LeanKG\n\n")
	b.WriteString("## Project Overview\n\n")
	b.WriteString("LeanKG is a Go knowledge graph system that indexes codebases with tree-sitter parsers, stores the graph in SQLite (WAL + FTS5) or PostgreSQL with pgvector, and serves it over the CLI, MCP, REST, and ConnectRPC.\n\n")
	b.WriteString("**Tech Stack**: Go 1.25, SQLite (modernc) or PostgreSQL + pgvector, smacker/go-tree-sitter, MCP go-sdk, ConnectRPC\n\n")
	b.WriteString("---\n\n## Build Commands\n\n### Standard Build\n```bash\ncd go && go build ./...        # Build every package\ncd go && CGO_ENABLED=0 go build -o bin/ ./cmd/leankg ./cmd/leankg-embed\n                               # The two binaries (CGO-free build)\n```\n\n### Testing\n```bash\ncd go && go test ./... -count=1        # Run all tests\ncd go && go test ./internal/store/     # Test one package\ncd go && go test ./internal/store/ -run TestWatermark -v\n                                       # Run one test (regex match)\ncd go && go test -tags tstree ./internal/tstree/ -count=1\n                                       # CGO tree-sitter tier\n```\n\n### Code Quality\n```bash\ngofmt -l go                     # List files that need formatting\ncd go && go vet ./...           # Static analysis\ncd go && CGO_ENABLED=0 go build ./...  # The default build stays CGO-free\n```\n\n### Codebase Indexing & Server\n```bash\nleankg index ./src             # Index codebase\nleankg serve --http :9699 --rest :8080   # MCP HTTP + REST server\nleankg query \"where is X\"      # Query the graph\nleankg impact <file> --depth 3 # Calculate impact radius\nleankg status                  # Show index status\n```\n\n---\n\n## Code Structure Overview\n\n")
	fmt.Fprintf(&b, "This codebase contains %d elements and %d relationships.\n\n", len(elements), len(relationships))

	var files, modules, funcs, classes []store.Element
	for _, e := range elements {
		switch e.ElementType {
		case "file":
			files = append(files, e)
		case "module":
			modules = append(modules, e)
		case "function":
			funcs = append(funcs, e)
		case "class":
			classes = append(classes, e)
		}
	}
	// Rust sorts files by file_path with a stable sort and every other bucket by
	// qualified_name (unique, so order is total).
	sortBy := func(els []store.Element, key func(store.Element) string) {
		sort.SliceStable(els, func(i, j int) bool { return key(els[i]) < key(els[j]) })
	}
	sortBy(files, func(e store.Element) string { return e.FilePath })
	sortBy(modules, func(e store.Element) string { return e.QualifiedName })
	sortBy(funcs, func(e store.Element) string { return e.QualifiedName })
	sortBy(classes, func(e store.Element) string { return e.QualifiedName })

	b.WriteString("### Key Modules\n\n")
	if len(modules) == 0 {
		b.WriteString("```\nleankg/\n├── go/\n│   ├── cmd/leankg/          # CLI + MCP/REST/ConnectRPC server\n│   ├── cmd/leankg-embed/    # Embedding pipeline binary\n│   ├── internal/store/      # SQLite (WAL + FTS5) and PostgreSQL backends\n│   ├── internal/index/      # Extractors and 3-signal change detection\n│   ├── internal/langs/      # Language registry and lazy activation tiers\n│   ├── internal/graph/      # Graph queries and traversal\n│   ├── internal/mcp/        # MCP tool surface\n│   ├── internal/web/        # Dashboard API and embedded UI\n│   └── benchmark/ab/        # A/B benchmark harness\n├── proto/leankg/v1/         # ConnectRPC service definitions\n├── ui-v2/                   # Dashboard front end\n└── e2e/                     # Browser-level acceptance specs\n```\n\n")
	} else {
		for _, m := range modules[:min(len(modules), maxModules)] {
			fmt.Fprintf(&b, "- `%s` (%s)\n", m.QualifiedName, m.FilePath)
		}
		b.WriteByte('\n')
	}

	b.WriteString("### Files\n\n")
	for _, e := range files[:min(len(files), maxFiles)] {
		fmt.Fprintf(&b, "- `%s`\n", e.FilePath)
	}
	if len(files) > maxFiles {
		fmt.Fprintf(&b, "- ... and %d more files\n", len(files)-maxFiles)
	}
	b.WriteByte('\n')

	b.WriteString("### Functions\n\n")
	for _, e := range funcs[:min(len(funcs), maxFuncs)] {
		fmt.Fprintf(&b, "- `%s` (%s:%d)\n", e.QualifiedName, e.FilePath, e.LineStart)
	}
	if len(funcs) > maxFuncs {
		fmt.Fprintf(&b, "- ... and %d more functions\n", len(funcs)-maxFuncs)
	}
	b.WriteByte('\n')

	b.WriteString("### Classes/Structs\n\n")
	for _, e := range classes[:min(len(classes), maxClasses)] {
		fmt.Fprintf(&b, "- `%s` (%s:%d)\n", e.QualifiedName, e.FilePath, e.LineStart)
	}
	if len(classes) > maxClasses {
		fmt.Fprintf(&b, "- ... and %d more classes\n", len(classes)-maxClasses)
	}
	b.WriteByte('\n')

	b.WriteString("---\n\n## Relationship Types\n\n")
	counts := make(map[string]int, len(relationships))
	for _, r := range relationships {
		counts[r.RelType]++
	}
	types := make([]string, 0, len(counts))
	for t := range counts {
		types = append(types, t)
	}
	// Deviation from Rust: the Rust tie-break came from unordered HashMap
	// iteration (which was then `.reverse()`d), so equal-count relationship
	// types were emitted in a nondeterministic order. Sort by count descending,
	// then rel_type ascending, so the document is reproducible.
	sort.Slice(types, func(i, j int) bool {
		if counts[types[i]] != counts[types[j]] {
			return counts[types[i]] > counts[types[j]]
		}
		return types[i] < types[j]
	})
	for _, t := range types[:min(len(types), maxRelTypes)] {
		fmt.Fprintf(&b, "- `%s`: %d occurrences\n", t, counts[t])
	}
	b.WriteByte('\n')

	b.WriteString("---\n\n## Testing Guidelines\n\n")
	b.WriteString("1. Unit tests live in `_test.go` files beside the package they cover\n")
	b.WriteString("2. Integration fixtures live under `go/testdata/` and the top-level `e2e/` specs\n")
	b.WriteString("3. Use `t.TempDir()` for tests that need filesystem access\n")
	b.WriteString("4. Prefer table-driven tests; the CGO tree-sitter tier is built with `-tags tstree`\n")
	b.WriteString("5. Follow the Arrange-Act-Assert pattern in all tests\n\n")

	return b.String(), nil
}

// Write renders the body and writes it to <docsDir>/AGENTS.md, creating
// docsDir; it returns the written path.
func Write(st store.Backend, docsDir string) (string, error) {
	body, err := AgentsMD(st)
	if err != nil {
		return "", err
	}
	return WriteFile(docsDir, body)
}

// WriteFile writes an already-rendered body to <docsDir>/AGENTS.md, creating
// docsDir; it returns the written path. It exists so a caller that prints the
// document (the `generate` verb prints it before saving) does not have to read
// the graph twice.
func WriteFile(docsDir, body string) (string, error) {
	if err := os.MkdirAll(docsDir, 0o755); err != nil {
		return "", fmt.Errorf("docgen: create docs dir: %w", err)
	}
	path := filepath.Join(docsDir, "AGENTS.md")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		return "", fmt.Errorf("docgen: write %s: %w", path, err)
	}
	return path, nil
}

func allRelationships(st store.Backend) ([]store.Relationship, error) {
	return st.RelationshipsAll(allRelationshipsLimit)
}
