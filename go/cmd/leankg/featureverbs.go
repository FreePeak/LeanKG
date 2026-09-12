// Graph feature verbs (Rust feature parity wave): tunnels, quality, reflect,
// register/list/status-repo/unregister. Each prints exactly what the Rust CLI
// printed (the render functions in the owning packages).
package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"

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
	if err := fs.Parse(args); err != nil {
		os.Exit(2)
	}
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
	if err := fs.Parse(args); err != nil {
		os.Exit(2)
	}
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
	// Rust/clap accepted flags in any position; Go's flag package stops at the
	// first positional, so the two required positionals come first by contract.
	if len(args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: leankg reflect <question> <outcome> [--nodes a,b] [--note text]")
		os.Exit(2)
	}
	question, outcome := args[0], args[1]
	if err := fs.Parse(args[2:]); err != nil {
		os.Exit(2)
	}
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
	if len(args) != 1 {
		fmt.Fprintln(os.Stderr, "usage: leankg register <name>")
		os.Exit(2)
	}
	cwd, err := os.Getwd()
	if err != nil {
		fatalText(err)
	}
	if err := registry.Register(args[0], cwd); err != nil {
		fatalText(err)
	}
	fmt.Print(registry.ConfirmRegister(args[0], cwd))
}

// cmdUnregister removes a repository from the global registry (Rust
// `unregister` verb).
func cmdUnregister(args []string) {
	if len(args) != 1 {
		fmt.Fprintln(os.Stderr, "usage: leankg unregister <name>")
		os.Exit(2)
	}
	was, err := registry.Unregister(args[0])
	if err != nil {
		fatalText(err)
	}
	fmt.Print(registry.ConfirmUnregister(args[0], was))
}

// cmdListRepos lists globally registered repositories (Rust `list` verb).
func cmdListRepos(args []string) {
	if len(args) != 0 {
		fmt.Fprintln(os.Stderr, "usage: leankg list")
		os.Exit(2)
	}
	entries, err := registry.List()
	if err != nil {
		fatalText(err)
	}
	fmt.Print(registry.RenderList(entries))
}

// cmdStatusRepo shows bookkeeping + live store counts for a registered
// repository (Rust `status-repo` verb).
func cmdStatusRepo(args []string) {
	if len(args) != 1 {
		fmt.Fprintln(os.Stderr, "usage: leankg status-repo <name>")
		os.Exit(2)
	}
	st, err := registry.Status(args[0])
	if errors.Is(err, registry.ErrNotFound) {
		fmt.Printf("Repository '%s' not found in registry\n", args[0])
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
