package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/FreePeak/LeanKG/go/internal/core"
	"github.com/FreePeak/LeanKG/go/internal/graph"
	"github.com/FreePeak/LeanKG/go/internal/store"
)

// cliQueryActions are the envelope actions the CLI can reach through the same
// wiring the MCP transports use. memory/session/ontology stay MCP-only: they
// need the memory subsystem and session roots the CLI does not construct.
var cliQueryActions = map[string]bool{
	"search": true, "exact": true, "fuzzy": true, "semantic": true, "element": true,
	"impact": true, "path": true, "callers": true, "callees": true, "context": true,
	"explain": true, "languages": true, "lsp": true, "pattern": true, "compress": true,
	"read":            true, // import{action:"read"}: the reader-mode compression path
	"prd":             true, // prdindex traceability rows (no extra subsystems needed)
	"incidents":       true, // orgknowledge reads
	"env_conflicts":   true,
	"service_context": true,
}

// cmdQuery is the direct CLI query path (Rust `leankg query` parity): name
// lookup with fuzzy fallback, plus the impact verb. Results print as JSON.
//
// Two forms:
//
//	leankg query <text> [--kind name|impact] [--depth N]        (local verbs)
//	leankg query <text> --action <a> [action flags]             (envelope passthrough)
//
// --action routes through core.Query/core.Import exactly like the MCP tool
// arguments do, so the CLI can reach path/callers/callees/context/explain/
// pattern/lsp/compress/read without a server. --kind is untouched.
func cmdQuery(args []string) {
	fs := flag.NewFlagSet("query", flag.ExitOnError)
	kind := fs.String("kind", "name", "query type: name (exact+fuzzy fallback) | impact")
	action := fs.String("action", "", "envelope action: "+queryActionList())
	depth := fs.Int("depth", 2, "traversal depth (--kind impact, or args.depth for --action)")
	compressOut := fs.Bool("compress", false, "RTK-style compact output (one line per result)")
	project := fs.String("project", "", "project directory (default cwd, or LEANKG_PROJECT)")
	to := fs.String("to", "", "action path: target qualified name")
	lang := fs.String("lang", "", "action pattern/lsp: language id")
	pattern := fs.String("pattern", "", "action pattern: ast-grep pattern")
	cmd := fs.String("cmd", "", "action compress: command string the output came from")
	command := fs.String("command", "", "action lsp: workspace (default) | document")
	path := fs.String("path", "", "action read: file to compress; action lsp: file to inspect")
	mode := fs.String("mode", "", "action read: reader mode (adaptive, full, map, signatures, diff, aggressive, entropy, lines)")
	lines := fs.String("lines", "", "action read: line spec for the lines mode")
	fresh := fs.Bool("fresh", false, "action read: bypass the session cache")
	service := fs.String("service", "", "action incidents/env_conflicts/service_context: service name")
	env := fs.String("env", "", "action incidents/service_context: environment (default production server-side)")
	limit := fs.Int("limit", 0, "result limit (args.limit for --action)")
	// clap semantics: flags may appear before or after the query text, and a
	// second positional is rejected rather than silently dropped (previously
	// `query --kind impact foo` looked up the literal "--kind").
	positional := parseInterspersed("query", fs, args, 1)
	q := ""
	if len(positional) == 1 {
		q = positional[0]
	}
	provided := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { provided[f.Name] = true })

	// A query string is required for the local verbs; the envelope actions may
	// take everything from flags (e.g. `query --action languages`).
	if q == "" && *action == "" && *path == "" {
		fmt.Fprintln(os.Stderr, "query requires a query string")
		os.Exit(2)
	}

	dir := resolveProjectDir(*project)

	if *action != "" {
		if !cliQueryActions[*action] {
			fmt.Fprintf(os.Stderr, "query: unknown --action %q\nvalid: %s\n", *action, queryActionList())
			os.Exit(2)
		}
		engine, err := openEngine(dir, store.RO)
		if err != nil {
			fatalJSON(err)
		}
		defer engine.Store().Close()
		argMap := map[string]any{}
		for _, kv := range []struct{ name, value string }{
			{"to", *to}, {"lang", *lang}, {"pattern", *pattern}, {"cmd", *cmd},
			{"command", *command}, {"path", *path}, {"mode", *mode}, {"lines", *lines},
			{"service", *service}, {"env", *env},
		} {
			if provided[kv.name] {
				argMap[kv.name] = kv.value
			}
		}
		if provided["depth"] {
			argMap["depth"] = *depth
		}
		if *fresh {
			argMap["fresh"] = "true"
		}
		ctx := context.Background()
		if *action == "read" {
			target := *path
			if target == "" {
				target = q
			}
			out, err := engine.Import(ctx, core.ImportRequest{Action: "read", Path: target, Args: argMap})
			if err != nil {
				fatalJSON(err)
			}
			printJSON(out)
			return
		}
		out, err := engine.Query(ctx, core.QueryRequest{
			Action: *action,
			Query:  q,
			Limit:  *limit,
			Args:   argMap,
		})
		if err != nil {
			fatalJSON(err)
		}
		printJSON(out)
		return
	}

	engine, err := openEngine(dir, store.RO)
	if err != nil {
		fatalJSON(err)
	}
	defer engine.Store().Close()
	st := engine.Store()

	switch *kind {
	case "impact":
		hits, err := impactFrom(st, q, *depth)
		if err != nil {
			fatalJSON(err)
		}
		if *compressOut {
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
		if *compressOut {
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

// queryActionList renders the sorted --action vocabulary for usage strings.
func queryActionList() string {
	out := make([]string, 0, len(cliQueryActions))
	for a := range cliQueryActions {
		out = append(out, a)
	}
	sort.Strings(out)
	return strings.Join(out, "|")
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
