// Rust CLI parity for the export/annotation family: export, pack, generate,
// annotate, link, search-annotations, show-annotations (src/main.rs arms at
// 1215-1405). Every verb is read-only except annotate/link, which need the
// writer role because annotations live in the store's KV.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/FreePeak/LeanKG/go/internal/annot"
	"github.com/FreePeak/LeanKG/go/internal/docgen"
	"github.com/FreePeak/LeanKG/go/internal/export"
	"github.com/FreePeak/LeanKG/go/internal/pack"
	"github.com/FreePeak/LeanKG/go/internal/store"
	"github.com/FreePeak/LeanKG/go/internal/web"
)

// verbFatal reports a verb failure on stderr and exits nonzero (the Rust
// binary returned the error to main, which printed it and exited 1).
func verbFatal(err error) {
	fmt.Fprintf(os.Stderr, "leankg: %v\n", err)
	os.Exit(1)
}

// openVerbStore opens the project store with the same flag/env convention as
// query/status: --project, else $LEANKG_PROJECT, else cwd.
func openVerbStore(project string, mode store.Mode) (store.Backend, string, error) {
	dir := project
	if dir == "" {
		dir = envOr("LEANKG_PROJECT", ".")
	}
	st, err := store.OpenBackend(context.Background(), dir,
		envOr("LEANKG_DB_ENGINE", "sqlite"), os.Getenv("LEANKG_PG_URL"), mode)
	if err != nil {
		return nil, dir, err
	}
	if mode == store.RW {
		// Writer verbs bring the store up to date first (the index verb does
		// the same); readers assume an already-indexed store, like query.
		if err := st.Migrate(); err != nil {
			_ = st.Close()
			return nil, dir, fmt.Errorf("migrate: %w", err)
		}
	}
	return st, dir, nil
}

// cmdExport ports `leankg export`: json (streaming when unscoped), dot,
// mermaid, and the git-committable --markdown document.
func cmdExport(args []string) {
	fs := flag.NewFlagSet("export", flag.ExitOnError)
	output := fs.String("output", "graph.json", "output file path")
	format := fs.String("format", "json", "export format: json | dot | mermaid")
	markdown := fs.Bool("markdown", false, "emit git-committable Markdown graph docs instead of a graph format")
	out := fs.String("out", "", "output file for --markdown (default: .leankg/graph-docs.md)")
	file := fs.String("file", "", "scope the export to a file's subgraph")
	depth := fs.Uint("depth", 3, "subgraph traversal depth (used with --file)")
	pathPrefix := fs.String("path", "", "scope the export to a path prefix (e.g. src)")
	community := fs.String("community", "", "scope the export to a community/cluster id")
	maxNodes := fs.Int("max-nodes", export.DefaultMaxNodes, "maximum nodes to include")
	project := fs.String("project", "", "project directory (default cwd, or LEANKG_PROJECT)")
	if err := fs.Parse(args); err != nil {
		os.Exit(2)
	}

	st, dir, err := openVerbStore(*project, store.RO)
	if err != nil {
		verbFatal(err)
	}
	defer st.Close()

	if *markdown {
		res, err := export.ExportMarkdown(st, dir, *out)
		if err != nil {
			verbFatal(err)
		}
		fmt.Printf("Exported %d elements and %d relationships to %s (format: markdown)\n",
			res.Elements, res.Relationships, res.Path)
		return
	}
	if *format == "html" {
		verbFatal(fmt.Errorf(`format "html" is not built into the Go engine — the interactive graph is served by the dashboard (leankg serve --ui)`))
	}

	scoped := *file != "" || *pathPrefix != "" || *community != ""
	if !scoped {
		// Unscoped: the Rust legacy path streamed the full graph without the
		// selector's dedupe/endpoint filter.
		switch *format {
		case "json":
			f, err := os.Create(*output)
			if err != nil {
				verbFatal(err)
			}
			if err := export.WriteJSONStreaming(f, st); err != nil {
				_ = f.Close()
				verbFatal(err)
			}
			if err := f.Close(); err != nil {
				verbFatal(err)
			}
			fmt.Printf("Exported streaming JSON to %s\n", *output)
			return
		case "dot", "mermaid":
			els, rels, err := export.All(st)
			if err != nil {
				verbFatal(err)
			}
			if err := writeExport(*output, *format, els, rels, ""); err != nil {
				verbFatal(err)
			}
			return
		default:
			verbFatal(fmt.Errorf("unknown format %q. Supported: json, dot, mermaid", *format))
		}
	}

	opts := export.Options{
		File:      *file,
		Depth:     uint32(*depth),
		Path:      *pathPrefix,
		Community: *community,
		MaxNodes:  *maxNodes,
	}
	if *community != "" {
		// The Go store persists no cluster column; the detector is the single
		// source of community membership.
		clustering, err := web.ClusterAssignments(st)
		if err != nil {
			verbFatal(err)
		}
		labels := make(map[string]string, len(clustering.Clusters))
		for _, c := range clustering.Clusters {
			labels[c.ID] = c.Label
		}
		opts.Clusters = export.ClusterResolver(clustering.Assignments, labels)
	}

	els, rels, meta, err := export.Select(st, opts)
	if err != nil {
		verbFatal(err)
	}
	if meta.Truncated {
		fmt.Fprintf(os.Stderr, "  WARNING: export truncated at max_nodes=%d (scope: %s); raise --max-nodes for the whole slice\n",
			meta.MaxNodes, meta.ScopeDesc)
	}
	if err := writeExport(*output, *format, els, rels, Version()); err != nil {
		verbFatal(err)
	}
}

// writeExport renders the requested format and writes it, printing the Rust
// summary line.
func writeExport(output, format string, els []store.Element, rels []store.Relationship, version string) error {
	var content []byte
	switch format {
	case "json":
		raw, err := export.JSON(els, rels, version, time.Now().Unix())
		if err != nil {
			return err
		}
		content = raw
	case "dot":
		content = []byte(export.Dot(els, rels))
	case "mermaid":
		content = []byte(export.Mermaid(rels))
	default:
		return fmt.Errorf("unknown format %q. Supported: json, dot, mermaid", format)
	}
	if err := os.WriteFile(output, content, 0o644); err != nil {
		return err
	}
	fmt.Printf("Exported %d nodes and %d edges to %s (format: %s)\n", len(els), len(rels), output, format)
	return nil
}

// cmdPack ports `leankg pack`: a deterministic, content-hashed context pack.
func cmdPack(args []string) {
	fs := flag.NewFlagSet("pack", flag.ExitOnError)
	output := fs.String("output", "leankg-pack", "output directory")
	pathPrefix := fs.String("path", "", "scope to a path prefix (e.g. src)")
	maxNodes := fs.Int("max-nodes", pack.DefaultMaxNodes, "max elements (a pack refuses to truncate)")
	revision := fs.String("revision", "", "source revision to record in the manifest (git sha / tag)")
	project := fs.String("project", "", "project directory (default cwd, or LEANKG_PROJECT)")
	if err := fs.Parse(args); err != nil {
		os.Exit(2)
	}

	st, _, err := openVerbStore(*project, store.RO)
	if err != nil {
		verbFatal(err)
	}
	defer st.Close()

	m, err := pack.WritePack(st, *output, pack.Options{
		Path:           *pathPrefix,
		MaxNodes:       *maxNodes,
		SourceRevision: *revision,
	})
	if err != nil {
		verbFatal(err)
	}
	scope := ""
	if m.PathScope != nil {
		scope = fmt.Sprintf(", scope=%s", *m.PathScope)
	}
	hash := m.ContentHash
	if len(hash) > 16 {
		hash = hash[:16]
	}
	fmt.Printf("context pack -> %s/  (%d elements, %d rels%s, hash %s)\n",
		*output, m.Elements, m.Relationships, scope, hash)
}

// cmdGenerate ports `leankg generate`: render AGENTS.md from the graph and
// write it to docs/AGENTS.md (the Rust destination).
func cmdGenerate(args []string) {
	fs := flag.NewFlagSet("generate", flag.ExitOnError)
	template := fs.String("template", "", "template name (accepted for CLI parity; the Rust generator never read it)")
	project := fs.String("project", "", "project directory (default cwd, or LEANKG_PROJECT)")
	if err := fs.Parse(args); err != nil {
		os.Exit(2)
	}
	_ = template

	st, _, err := openVerbStore(*project, store.RO)
	if err != nil {
		verbFatal(err)
	}
	defer st.Close()

	body, err := docgen.AgentsMD(st)
	if err != nil {
		verbFatal(err)
	}
	fmt.Printf("Generated documentation:\n%s", body)
	path, err := docgen.WriteFile("docs", body)
	if err != nil {
		verbFatal(err)
	}
	fmt.Printf("\nSaved to %s\n", path)
}

// cmdAnnotate ports `leankg annotate <element> --description ...`.
func cmdAnnotate(args []string) {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "usage: leankg annotate <element> --description TEXT [--user-story ID] [--feature ID]")
		os.Exit(2)
	}
	element := args[0]
	fs := flag.NewFlagSet("annotate", flag.ExitOnError)
	description := fs.String("description", "", "business logic description")
	fs.StringVar(description, "d", "", "business logic description (shorthand)")
	userStory := fs.String("user-story", "", "user story id")
	feature := fs.String("feature", "", "feature id")
	project := fs.String("project", "", "project directory (default cwd, or LEANKG_PROJECT)")
	if err := fs.Parse(args[1:]); err != nil {
		os.Exit(2)
	}

	st, _, err := openVerbStore(*project, store.RW)
	if err != nil {
		verbFatal(err)
	}
	defer st.Close()

	created, err := annot.Annotate(st, element, *description, optString(*userStory), optString(*feature))
	if err != nil {
		verbFatal(err)
	}
	if created {
		fmt.Printf("Created annotation for '%s'\n", element)
	} else {
		fmt.Printf("Updated annotation for '%s'\n", element)
	}
	fmt.Printf("  Description: %s\n", *description)
	if *userStory != "" {
		fmt.Printf("  User Story: %s\n", *userStory)
	}
	if *feature != "" {
		fmt.Printf("  Feature: %s\n", *feature)
	}
}

// cmdLink ports `leankg link <element> <id> [--kind story|feature]`.
func cmdLink(args []string) {
	if len(args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: leankg link <element> <id> [--kind story|feature]")
		os.Exit(2)
	}
	element, id := args[0], args[1]
	fs := flag.NewFlagSet("link", flag.ExitOnError)
	kind := fs.String("kind", "story", "link type: story or feature")
	project := fs.String("project", "", "project directory (default cwd, or LEANKG_PROJECT)")
	if err := fs.Parse(args[2:]); err != nil {
		os.Exit(2)
	}

	st, _, err := openVerbStore(*project, store.RW)
	if err != nil {
		verbFatal(err)
	}
	defer st.Close()

	if err := annot.Link(st, element, id, *kind); err != nil {
		verbFatal(err)
	}
	fmt.Printf("Linked '%s' to %s %s\n", element, *kind, id)
}

// cmdSearchAnnotations ports `leankg search-annotations <query>`.
func cmdSearchAnnotations(args []string) {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "usage: leankg search-annotations <query>")
		os.Exit(2)
	}
	query := args[0]
	fs := flag.NewFlagSet("search-annotations", flag.ExitOnError)
	project := fs.String("project", "", "project directory (default cwd, or LEANKG_PROJECT)")
	if err := fs.Parse(args[1:]); err != nil {
		os.Exit(2)
	}

	st, _, err := openVerbStore(*project, store.RO)
	if err != nil {
		verbFatal(err)
	}
	defer st.Close()

	results, err := annot.Search(st, query)
	if err != nil {
		verbFatal(err)
	}
	if len(results) == 0 {
		fmt.Printf("No annotations found matching '%s'\n", query)
		return
	}
	fmt.Printf("Found %d annotation(s):\n", len(results))
	for _, r := range results {
		fmt.Printf("\n  Element: %s\n", r.Element)
		fmt.Printf("  Description: %s\n", r.Description)
		if r.UserStoryID != "" {
			fmt.Printf("  User Story: %s\n", r.UserStoryID)
		}
		if r.FeatureID != "" {
			fmt.Printf("  Feature: %s\n", r.FeatureID)
		}
	}
}

// cmdShowAnnotations ports `leankg show-annotations <element>`.
func cmdShowAnnotations(args []string) {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "usage: leankg show-annotations <element>")
		os.Exit(2)
	}
	element := args[0]
	fs := flag.NewFlagSet("show-annotations", flag.ExitOnError)
	project := fs.String("project", "", "project directory (default cwd, or LEANKG_PROJECT)")
	if err := fs.Parse(args[1:]); err != nil {
		os.Exit(2)
	}

	st, _, err := openVerbStore(*project, store.RO)
	if err != nil {
		verbFatal(err)
	}
	defer st.Close()

	rec, err := annot.Get(st, element)
	if err != nil {
		verbFatal(err)
	}
	if rec == nil {
		fmt.Printf("No annotations found for '%s'\n", element)
		return
	}
	fmt.Printf("Annotations for '%s':\n", element)
	fmt.Printf("  Description: %s\n", rec.Description)
	if rec.UserStoryID != "" {
		fmt.Printf("  User Story: %s\n", rec.UserStoryID)
	}
	if rec.FeatureID != "" {
		fmt.Printf("  Feature: %s\n", rec.FeatureID)
	}
}

// optString maps an empty flag to an unset annotation link (Rust Option).
func optString(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
