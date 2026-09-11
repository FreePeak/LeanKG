package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"

	"github.com/FreePeak/LeanKG/go/internal/core"
	"github.com/FreePeak/LeanKG/go/internal/store"
)

// cmdStatus prints the status tool payload as JSON (Rust `leankg status --json`
// parity): health, inventory, freshness, embed state, lazy language tiers.
func cmdStatus(args []string) {
	fs := flag.NewFlagSet("status", flag.ExitOnError)
	project := fs.String("project", "", "project directory (default cwd)")
	if err := fs.Parse(args); err != nil {
		log.Fatal(err)
	}
	dir := *project
	if dir == "" {
		var err error
		dir, err = os.Getwd()
		if err != nil {
			log.Fatalf("resolve cwd: %v", err)
		}
	}
	st, err := store.OpenBackend(context.Background(), dir,
		envOr("LEANKG_DB_ENGINE", "sqlite"), os.Getenv("LEANKG_PG_URL"), store.RO)
	if err != nil {
		// Missing store: report cold, not an error — same posture as the
		// L0 rung guiding first-run indexing.
		fmt.Printf("{\"elements\":0,\"freshness\":\"cold\",\"note\":\"%s\"}\n", err.Error())
		os.Exit(0)
	}
	defer st.Close()
	engine := core.New(st, nil, nil)
	engine.SetProjectDir(dir)
	out, err := engine.Status(context.Background())
	if err != nil {
		log.Fatal(err)
	}
	printJSON(out)
}
