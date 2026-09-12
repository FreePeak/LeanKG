package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"

	"github.com/FreePeak/LeanKG/go/internal/prdindex"
	"github.com/FreePeak/LeanKG/go/internal/store"
)

// cmdPRD ports the Rust index_prd MCP tool: parse a PRD markdown document and
// land its requirements as entities plus the FR -> ontology workflow edges.
func cmdPRD(args []string) {
	fs := flag.NewFlagSet("prd", flag.ExitOnError)
	source := fs.String("source", "docs/prd.md", "PRD markdown path, relative to the project root")
	environment := fs.String("environment", "local", "knowledge-entry environment")
	project := fs.String("project", "", "project directory (default cwd, or LEANKG_PROJECT)")
	parseInterspersed("prd", fs, args, 0)
	st, dir, err := openVerbStore(*project, store.RW)
	if err != nil {
		verbFatal(err)
	}
	defer st.Close()

	res, err := prdindex.IndexDocument(context.Background(), st, dir, *source, *environment)
	if err != nil {
		verbFatal(err)
	}
	fmt.Printf("PRD %s: %d requirements, %d user stories (%d created, %d updated, %d removed)\n",
		res.Source, res.Requirements, res.UserStories, res.Created, res.Updated, res.Removed)
	fmt.Printf("  entities %d, knowledge rows %d, workflow links %d\n",
		res.Elements, res.KnowledgeEntries, res.WorkflowLinks)
	for _, e := range res.Errors {
		fmt.Printf("  warning: %s\n", e)
	}
}

// cmdPRDTrace prints FR -> workflow traceability rows as JSON.
func cmdPRDTrace(args []string) {
	fs := flag.NewFlagSet("prd-trace", flag.ExitOnError)
	project := fs.String("project", "", "project directory (default cwd, or LEANKG_PROJECT)")
	positional := parseInterspersed("prd-trace", fs, args, 1)
	featureID := ""
	if len(positional) == 1 {
		featureID = positional[0]
	}
	st, _, err := openVerbStore(*project, store.RO)
	if err != nil {
		verbFatal(err)
	}
	defer st.Close()

	rows, err := prdindex.Trace(st, featureID)
	if err != nil {
		verbFatal(err)
	}
	b, err := json.MarshalIndent(rows, "", "  ")
	if err != nil {
		verbFatal(err)
	}
	fmt.Println(string(b))
}
