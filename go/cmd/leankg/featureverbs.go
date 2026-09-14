// Graph feature verbs (Rust feature parity wave): tunnels, quality, reflect,
// register/list/status-repo/unregister. Each prints exactly what the Rust CLI
// printed (the render functions in the owning packages).
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/FreePeak/LeanKG/go/internal/convo"
	"github.com/FreePeak/LeanKG/go/internal/graph"
	"github.com/FreePeak/LeanKG/go/internal/registry"
	"github.com/FreePeak/LeanKG/go/internal/session"
	"github.com/FreePeak/LeanKG/go/internal/store"
	"github.com/FreePeak/LeanKG/go/internal/web"
)

// cmdTunnels lists cross-cluster relationships (Rust `tunnels` verb, US-MP-06).
func cmdTunnels(args []string) {
	fs := flag.NewFlagSet("tunnels", flag.ExitOnError)
	path := fs.String("path", "", "project directory (default cwd, or LEANKG_PROJECT)")
	limit := fs.Int("limit", 50, "maximum tunnels to print (0 = no cap)")
	parseInterspersed("tunnels", fs, args, 0)
	dir := resolveProjectDir(*path)
	engine, err := openEngine(dir, store.RO)
	if err != nil {
		fatalText(err)
	}
	defer engine.Store().Close()
	ts, err := web.Tunnels(engine.Store())
	if err != nil {
		fatalText(err)
	}
	if *limit > 0 && len(ts) > *limit {
		ts = ts[:*limit]
	}
	fmt.Print(web.RenderTunnels(ts))
}

// cmdQuality finds oversized functions (Rust `quality` verb).
func cmdQuality(args []string) {
	fs := flag.NewFlagSet("quality", flag.ExitOnError)
	path := fs.String("path", "", "project directory (default cwd, or LEANKG_PROJECT)")
	minLines := fs.Int("min-lines", 50, "minimum line count")
	lang := fs.String("lang", "", "filter by language (e.g. go, python)")
	parseInterspersed("quality", fs, args, 0)
	dir := resolveProjectDir(*path)
	engine, err := openEngine(dir, store.RO)
	if err != nil {
		fatalText(err)
	}
	defer engine.Store().Close()
	els, err := graph.OversizedFunctions(engine.Store(), *minLines, *lang)
	if err != nil {
		fatalText(err)
	}
	fmt.Print(graph.RenderOversized(els, *minLines))
}

// cmdReflect records a query-outcome lesson (Rust `reflect` verb, US-GF-09).
func cmdReflect(args []string) {
	fs := flag.NewFlagSet("reflect", flag.ExitOnError)
	path := fs.String("path", "", "project directory (default cwd, or LEANKG_PROJECT)")
	nodes := fs.String("nodes", "", "comma-separated qualified names that were returned")
	note := fs.String("note", "", "free-form note")
	positional := parseInterspersed("reflect", fs, args, 2)
	if len(positional) < 2 {
		fmt.Fprintln(os.Stderr, "usage: leankg reflect <question> <outcome> [--nodes a,b] [--note text]")
		os.Exit(2)
	}
	question, outcome := positional[0], positional[1]
	dir := resolveProjectDir(*path)
	var nodeList []string
	if *nodes != "" {
		for _, n := range strings.Split(*nodes, ",") {
			if n = strings.TrimSpace(n); n != "" {
				nodeList = append(nodeList, n)
			}
		}
	}
	if _, err := session.ReflectOutcome(dir, question, nodeList, outcome, *note); err != nil {
		fatalText(err)
	}
	fmt.Printf("Recorded reflection for outcome '%s'\n", outcome)
}

// cmdRegister registers the current directory in the global registry
// (Rust `register` verb).
func cmdRegister(args []string) {
	fs := flag.NewFlagSet("register", flag.ExitOnError)
	positional := parseInterspersed("register", fs, args, 1)
	if len(positional) != 1 {
		fmt.Fprintln(os.Stderr, "usage: leankg register <name>")
		os.Exit(2)
	}
	cwd, err := os.Getwd()
	if err != nil {
		fatalText(err)
	}
	if err := registry.Register(positional[0], cwd); err != nil {
		fatalText(err)
	}
	fmt.Print(registry.ConfirmRegister(positional[0], cwd))
}

// cmdUnregister removes a repository from the global registry (Rust
// `unregister` verb).
func cmdUnregister(args []string) {
	fs := flag.NewFlagSet("unregister", flag.ExitOnError)
	positional := parseInterspersed("unregister", fs, args, 1)
	if len(positional) != 1 {
		fmt.Fprintln(os.Stderr, "usage: leankg unregister <name>")
		os.Exit(2)
	}
	was, err := registry.Unregister(positional[0])
	if err != nil {
		fatalText(err)
	}
	fmt.Print(registry.ConfirmUnregister(positional[0], was))
}

// cmdListRepos lists globally registered repositories (Rust `list` verb).
func cmdListRepos(args []string) {
	fs := flag.NewFlagSet("list", flag.ExitOnError)
	parseInterspersed("list", fs, args, 0)
	entries, err := registry.List()
	if err != nil {
		fatalText(err)
	}
	fmt.Print(registry.RenderList(entries))
}

// cmdStatusRepo shows bookkeeping + live store counts for a registered
// repository (Rust `status-repo` verb).
func cmdStatusRepo(args []string) {
	fs := flag.NewFlagSet("status-repo", flag.ExitOnError)
	positional := parseInterspersed("status-repo", fs, args, 1)
	if len(positional) != 1 {
		fmt.Fprintln(os.Stderr, "usage: leankg status-repo <name>")
		os.Exit(2)
	}
	st, err := registry.Status(positional[0])
	if errors.Is(err, registry.ErrNotFound) {
		fmt.Printf("Repository '%s' not found in registry\n", positional[0])
		return
	}
	if err != nil {
		fatalText(err)
	}
	fmt.Print(st.Render())
}

// fatalText reports a verb failure on stderr and exits nonzero.
func fatalText(err error) {
	fmt.Fprintln(os.Stderr, "leankg:", err)
	os.Exit(1)
}

// cmdMineConversations mines Claude / ChatGPT / Slack export JSON into typed
// conversation nodes (Rust `mine-conversations` verb, US-MP-03 / FR-MP-09..13).
func cmdMineConversations(args []string) {
	fs := flag.NewFlagSet("mine-conversations", flag.ExitOnError)
	format := fs.String("format", "", "export format: claude | chatgpt | slack")
	project := fs.String("project", ".", "project root whose .leankg graph receives the mined nodes")
	input := fs.String("input", "", "input file or directory of export JSON files")
	parseInterspersed("mine-conversations", fs, args, 0)
	f := convo.ParseFormat(*format)
	if f == convo.UnknownFormat {
		fmt.Fprintf(os.Stderr, "Unknown format '%s'; use --format claude|chatgpt|slack\n", *format)
		os.Exit(2)
	}
	if *input == "" {
		fmt.Fprintln(os.Stderr, "usage: leankg mine-conversations --format claude|chatgpt|slack --input FILE_OR_DIR [--project DIR]")
		os.Exit(2)
	}
	result, err := convo.MineIntoProject(context.Background(), *project, *input, f)
	if err != nil {
		fmt.Fprintf(os.Stderr, "mine-conversations failed: %v\n", err)
		os.Exit(1)
	}
	fmt.Println(result.Summary())
	for _, item := range result.Items {
		fmt.Printf("  [%s] %s: %s\n", item.Kind.String(), item.QualifiedName(*project), truncateRunes(item.Verbatim, 120))
	}
	fmt.Println("Done.")
}

// truncateRunes bounds a CLI echo to n runes without splitting one.
func truncateRunes(s string, n int) string {
	count := 0
	for pos := range s {
		if count == n {
			return s[:pos]
		}
		count++
	}
	return s
}
