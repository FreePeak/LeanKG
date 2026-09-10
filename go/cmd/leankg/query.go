package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"

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
		hits, err := graph.Impact(st, q, *depth)
		if err != nil {
			fatalJSON(err)
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
