package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/FreePeak/LeanKG/go/internal/store"
	"github.com/FreePeak/LeanKG/go/internal/web"
)

// cmdDetectClusters prints the project's detected communities as JSON. Ported
// from the Rust `leankg detect-clusters` verb (cli::CLICommand::DetectClusters
// + graph::clustering::CommunityDetector); the community detection itself is
// internal/web's ported Louvain detector (web.ClusterAssignments).
func cmdDetectClusters(args []string) {
	fs := flag.NewFlagSet("detect-clusters", flag.ExitOnError)
	path := fs.String("path", "", "project directory (default cwd, or LEANKG_PROJECT)")
	// --min-hub-edges is registered for Rust CLI parity only: the Rust
	// detector destructured `min_hub_edges: _` and never filtered on it.
	fs.Int("min-hub-edges", 5, "minimum edges for a node to be considered a hub (Rust flag parity; the detector does not filter on it)")
	parseInterspersed("detect-clusters", fs, args, 0)
	dir := resolveProjectDir(*path)
	engine, err := openEngine(dir, store.RO)
	if err != nil {
		fatalJSON(err)
	}
	defer engine.Store().Close()

	clusters, err := web.ClusterAssignments(engine.Store())
	if err != nil {
		fatalJSON(err)
	}
	printJSON(clusters)
}

// cmdGods prints the most-connected elements for a project as JSON. Ported
// from the Rust `leankg gods` verb (cli::CLICommand::Gods +
// GraphEngine::get_god_nodes).
func cmdGods(args []string) {
	fs := flag.NewFlagSet("gods", flag.ExitOnError)
	project := fs.String("project", "", "project directory (default cwd, or LEANKG_PROJECT)")
	limit := fs.Int("limit", 20, "number of god nodes to list")
	excludeHubs := fs.Int("exclude-hubs-percentile", 0, "exclude the top N% super-hubs (0 disables, matching the Rust Option::None default)")
	parseInterspersed("gods", fs, args, 0)
	dir := resolveProjectDir(*project)
	engine, err := openEngine(dir, store.RO)
	if err != nil {
		fatalJSON(err)
	}
	defer engine.Store().Close()

	nodes, err := web.GodNodes(engine, *limit, *excludeHubs)
	if err != nil {
		fatalJSON(err)
	}
	printJSON(nodes)
}

// cmdReport prints the graph report as JSON and writes its markdown body. Ported
// from the Rust `leankg report` verb (cli::CLICommand::Report +
// GraphEngine::generate_graph_report): the file side effect (default
// <project>/.leankg/GRAPH_REPORT.md, overridable with --out) is kept, the
// structured report goes to stdout so the verb is scriptable.
func cmdReport(args []string) {
	fs := flag.NewFlagSet("report", flag.ExitOnError)
	project := fs.String("project", "", "project directory (default cwd, or LEANKG_PROJECT)")
	projectName := fs.String("project-name", "", "project display name (default: directory name)")
	out := fs.String("out", "", "markdown output file (default <project>/.leankg/GRAPH_REPORT.md)")
	parseInterspersed("report", fs, args, 0)
	dir := resolveProjectDir(*project)
	engine, err := openEngine(dir, store.RO)
	if err != nil {
		fatalJSON(err)
	}
	defer engine.Store().Close()

	report, err := web.GraphReportFor(engine, dir, *projectName)
	if err != nil {
		fatalJSON(err)
	}
	outPath := *out
	if outPath == "" {
		outPath = filepath.Join(dir, ".leankg", "GRAPH_REPORT.md")
	}
	if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
		fatalJSON(err)
	}
	if err := os.WriteFile(outPath, []byte(web.GraphReportMarkdown(report)), 0o644); err != nil {
		fatalJSON(err)
	}
	fmt.Fprintf(os.Stderr, "wrote graph report to %s\n", outPath)
	printJSON(report)
}
