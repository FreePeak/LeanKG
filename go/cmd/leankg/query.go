package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/FreePeak/LeanKG/go/internal/graph"
	"github.com/FreePeak/LeanKG/go/internal/store"
)

// cmdQuery is the direct CLI query path (Rust `leankg query` parity): name
// lookup with fuzzy fallback, plus the impact verb. Results print as JSON.
func cmdQuery(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "query requires a query string")
		os.Exit(2)
	}
	q := args[0]
	rest := args[1:]
	fs := flag.NewFlagSet("query", flag.ExitOnError)
	kind := fs.String("kind", "name", "query type: name (exact+fuzzy fallback) | impact")
	depth := fs.Int("depth", 2, "impact traversal depth")
	compress := fs.Bool("compress", false, "RTK-style compact output (one line per result)")
	project := fs.String("project", "", "project directory (default cwd, or LEANKG_PROJECT)")
	if err := fs.Parse(rest); err != nil {
		os.Exit(2)
	}

	dir := *project
	if dir == "" {
		dir = envOr("LEANKG_PROJECT", ".")
	}
	st, err := store.OpenBackend(context.Background(), dir, envOr("LEANKG_DB_ENGINE", "sqlite"), os.Getenv("LEANKG_PG_URL"), store.RO)
	if err != nil {
		fatalJSON(err)
	}
	defer st.Close()

	switch *kind {
	case "impact":
		hits, err := impactFrom(st, q, *depth)
		if err != nil {
			fatalJSON(err)
		}
		if *compress {
			// RTK-style: one line per affected node — "qn depth"
			for _, h := range hits {
				fmt.Printf("%s %d\n", h.QN, h.Depth)
			}
			return
		}
		printJSON(hits)
	case "name", "":
		els, err := st.FindExact(q)
		if err != nil {
			fatalJSON(err)
		}
		if len(els) == 0 {
			matches, ferr := st.FindFuzzy(q, 10)
			if ferr != nil {
				fatalJSON(ferr)
			}
			for _, m := range matches {
				els = append(els, m.Element)
			}
		}
		if *compress {
			for _, el := range els {
				fmt.Printf("%s (%s)\n", el.QualifiedName, el.ElementType)
			}
			return
		}
		printJSON(els)
	default:
		fmt.Fprintf(os.Stderr, "query: unknown kind %q (want name|impact)\n", *kind)
		os.Exit(2)
	}
}

func printJSON(v any) {
	b, _ := json.MarshalIndent(v, "", "  ")
	fmt.Println(string(b))
}

func fatalJSON(err error) {
	b, _ := json.Marshal(map[string]string{"error": err.Error()})
	fmt.Fprintln(os.Stderr, string(b))
	os.Exit(1)
}

// impactFrom runs graph.Impact from an element QN seed, or — Rust
// `impact <FILE>` parity — aggregates the impact of every element in a file
// when the seed is a path with no matching element. The file path resolves
// through the element census (O(n) scan; documented ceiling: fine for
// CLI-sized indexes).
func impactFrom(st store.Backend, seed string, depth int) ([]graph.Hit, error) {
	if _, err := graph.Impact(st, seed, depth); err == nil {
		return graph.Impact(st, seed, depth)
	}
	els, err := st.Elements()
	if err != nil {
		return nil, err
	}
	prefix := seed + "::"
	seen := map[string]int{}
	for _, el := range els {
		if !seedMatchesFile(el, seed, prefix) {
			continue
		}
		hits, err := graph.Impact(st, el.QualifiedName, depth)
		if err != nil {
			continue
		}
		for _, h := range hits {
			if d, ok := seen[h.QN]; !ok || h.Depth < d {
				seen[h.QN] = h.Depth
			}
		}
	}
	if len(seen) == 0 {
		return nil, fmt.Errorf("graph: unknown node %q (not an element QN or indexed file)", seed)
	}
	var out []graph.Hit
	for qn, d := range seen {
		out = append(out, graph.Hit{QN: qn, Depth: d})
	}
	return out, nil
}

// seedMatchesFile decides whether an element belongs to the seed when the seed
// is a file path rather than a QN: the stored QN prefix, the stored relative
// FilePath, or the seed being an absolute path ending in that FilePath.
func seedMatchesFile(el store.Element, seed, prefix string) bool {
	if strings.HasPrefix(el.QualifiedName, prefix) || el.FilePath == seed {
		return true
	}
	if filepath.IsAbs(seed) {
		rel := filepath.ToSlash(el.FilePath)
		return strings.HasSuffix(seed, "/"+rel)
	}
	return false
}
