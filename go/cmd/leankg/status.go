package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/FreePeak/LeanKG/go/internal/core"
	"github.com/FreePeak/LeanKG/go/internal/langs"
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
		// An uninitialized project is legitimately "cold" (exit 0); a store
		// that EXISTS but cannot be opened is an operational failure and
		// must exit nonzero (the silent-failure class that manufactured the
		// old fake numbers).
		if _, statErr := os.Lstat(filepath.Join(dir, ".leankg")); os.IsNotExist(statErr) {
			fmt.Printf("{\"elements\":0,\"freshness\":\"cold\",\"note\":\"%s\"}\n", err.Error())
			os.Exit(0)
		}
		fmt.Fprintf(os.Stderr, "status: %v\n", err)
		os.Exit(1)
	}
	defer st.Close()
	engine := core.New(st, nil, nil)
	engine.SetProjectDir(dir)
	// Attach the language registry (activated for this project) so status
	// reports the lazy activation state, not an empty list.
	reg := langs.DefaultRegistry()
	if _, aerr := reg.Activate(dir); aerr == nil {
		engine.SetLangsRegistry(reg)
	}
	out, err := engine.Status(context.Background())
	if err != nil {
		log.Fatal(err)
	}
	printJSON(out)
}
