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

	"github.com/FreePeak/LeanKG/internal/cliexit"
	"github.com/FreePeak/LeanKG/internal/convo"
	"github.com/FreePeak/LeanKG/internal/graph"
	"github.com/FreePeak/LeanKG/internal/registry"
	"github.com/FreePeak/LeanKG/internal/session"
	"github.com/FreePeak/LeanKG/internal/store"
	"github.com/FreePeak/LeanKG/internal/web"
)

// cmdTunnels lists cross-cluster relationships (Rust `tunnels` verb, US-MP-06).
func cmdTunnels(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("tunnels", flag.ContinueOnError)
	path := fs.String("path", "", "project directory (default cwd, or LEANKG_PROJECT)")
	limit := fs.Int("limit", 50, "maximum tunnels to print (0 = no cap)")
	if _, err := parseInterspersed("tunnels", fs, args, 0); err != nil {
		return err
	}
	dir := resolveProjectDir(*path)
	engine, err := openEngine(ctx, dir, store.RO)
	if err != nil {
		return verbFatal(err)
	}
	defer engine.Store().Close()
	defer engine.Close()
	ts, err := web.Tunnels(engine.Store())
	if err != nil {
		return verbFatal(err)
	}
	if *limit > 0 && len(ts) > *limit {
		ts = ts[:*limit]
	}
	fmt.Print(web.RenderTunnels(ts))
	return nil
}

// cmdQuality finds oversized functions (Rust `quality` verb).
func cmdQuality(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("quality", flag.ContinueOnError)
	path := fs.String("path", "", "project directory (default cwd, or LEANKG_PROJECT)")
	minLines := fs.Int("min-lines", 50, "minimum line count")
	lang := fs.String("lang", "", "filter by language (e.g. go, python)")
	if _, err := parseInterspersed("quality", fs, args, 0); err != nil {
		return err
	}
	dir := resolveProjectDir(*path)
	engine, err := openEngine(ctx, dir, store.RO)
	if err != nil {
		return verbFatal(err)
	}
	defer engine.Store().Close()
	defer engine.Close()
	els, err := graph.OversizedFunctions(context.Background(), engine.Store(), *minLines, *lang)
	if err != nil {
		return verbFatal(err)
	}
	fmt.Print(graph.RenderOversized(els, *minLines))
	return nil
}

// cmdReflect records a query-outcome lesson (Rust `reflect` verb, US-GF-09).
func cmdReflect(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("reflect", flag.ContinueOnError)
	path := fs.String("path", "", "project directory (default cwd, or LEANKG_PROJECT)")
	nodes := fs.String("nodes", "", "comma-separated qualified names that were returned")
	note := fs.String("note", "", "free-form note")
	positional, err := parseInterspersed("reflect", fs, args, 2)
	if err != nil {
		return err
	}
	if len(positional) < 2 {
		fmt.Fprintln(os.Stderr, "usage: leankg reflect <question> <outcome> [--nodes a,b] [--note text]")
		return cliexit.Code(2)
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
		return verbFatal(err)
	}
	fmt.Printf("Recorded reflection for outcome '%s'\n", outcome)
	return nil
}

// cmdRegister registers the current directory in the global registry
// (Rust `register` verb).
func cmdRegister(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("register", flag.ContinueOnError)
	positional, err := parseInterspersed("register", fs, args, 1)
	if err != nil {
		return err
	}
	if len(positional) != 1 {
		fmt.Fprintln(os.Stderr, "usage: leankg register <name>")
		return cliexit.Code(2)
	}
	cwd, err := os.Getwd()
	if err != nil {
		return verbFatal(err)
	}
	if err := registry.Register(positional[0], cwd); err != nil {
		return verbFatal(err)
	}
	fmt.Print(registry.ConfirmRegister(positional[0], cwd))
	return nil
}

// cmdUnregister removes a repository from the global registry (Rust
// `unregister` verb).
func cmdUnregister(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("unregister", flag.ContinueOnError)
	positional, err := parseInterspersed("unregister", fs, args, 1)
	if err != nil {
		return err
	}
	if len(positional) != 1 {
		fmt.Fprintln(os.Stderr, "usage: leankg unregister <name>")
		return cliexit.Code(2)
	}
	was, err := registry.Unregister(positional[0])
	if err != nil {
		return verbFatal(err)
	}
	fmt.Print(registry.ConfirmUnregister(positional[0], was))
	return nil
}

// cmdListRepos lists globally registered repositories (Rust `list` verb).
func cmdListRepos(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("list", flag.ContinueOnError)
	if _, err := parseInterspersed("list", fs, args, 0); err != nil {
		return err
	}
	entries, err := registry.List()
	if err != nil {
		return verbFatal(err)
	}
	fmt.Print(registry.RenderList(entries))
	return nil
}

// cmdStatusRepo shows bookkeeping + live store counts for a registered
// repository (Rust `status-repo` verb).
func cmdStatusRepo(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("status-repo", flag.ContinueOnError)
	positional, err := parseInterspersed("status-repo", fs, args, 1)
	if err != nil {
		return err
	}
	if len(positional) != 1 {
		fmt.Fprintln(os.Stderr, "usage: leankg status-repo <name>")
		return cliexit.Code(2)
	}
	st, err := registry.Status(positional[0])
	if errors.Is(err, registry.ErrNotFound) {
		fmt.Printf("Repository '%s' not found in registry\n", positional[0])
		return nil
	}
	if err != nil {
		return verbFatal(err)
	}
	fmt.Print(st.Render())
	return nil
}

// cmdMineConversations mines Claude / ChatGPT / Slack export JSON into typed
// conversation nodes (Rust `mine-conversations` verb, US-MP-03 / FR-MP-09..13).
func cmdMineConversations(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("mine-conversations", flag.ContinueOnError)
	format := fs.String("format", "", "export format: claude | chatgpt | slack")
	project := fs.String("project", ".", "project root whose .leankg graph receives the mined nodes")
	input := fs.String("input", "", "input file or directory of export JSON files")
	if _, err := parseInterspersed("mine-conversations", fs, args, 0); err != nil {
		return err
	}
	f := convo.ParseFormat(*format)
	if f == convo.UnknownFormat {
		fmt.Fprintf(os.Stderr, "Unknown format '%s'; use --format claude|chatgpt|slack\n", *format)
		return cliexit.Code(2)
	}
	if *input == "" {
		fmt.Fprintln(os.Stderr, "usage: leankg mine-conversations --format claude|chatgpt|slack --input FILE_OR_DIR [--project DIR]")
		return cliexit.Code(2)
	}
	result, err := convo.MineIntoProject(ctx, *project, *input, f)
	if err != nil {
		fmt.Fprintf(os.Stderr, "mine-conversations failed: %v\n", err)
		return cliexit.Code(1)
	}
	fmt.Println(result.Summary())
	for _, item := range result.Items {
		fmt.Printf("  [%s] %s: %s\n", item.Kind.String(), item.QualifiedName(*project), truncateRunes(item.Verbatim, 120))
	}
	fmt.Println("Done.")
	return nil
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
