package main

import (
	"context"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/FreePeak/LeanKG/go/internal/indexgate"
	"github.com/FreePeak/LeanKG/go/internal/projectcfg"
	"github.com/FreePeak/LeanKG/go/internal/registry"
	"github.com/FreePeak/LeanKG/go/internal/setup"
	"github.com/FreePeak/LeanKG/go/internal/store"
)

// maybeAutoIndexOnStart ports the Rust MCP auto-index-on-start gate
// (server.rs auto_index_if_needed): for an already-initialized project,
// consult mcp.auto_index_on_start / require_git_for_auto_index /
// auto_index_threshold_minutes (+ LEANKG_SKIP_FRESHNESS_CHECK) and, on the
// positive branch, run the incremental index in the BACKGROUND so the
// listener binds immediately (Rust spawned it for exactly that reason — a
// freshness reindex over a polyrepo used to block /health for tens of
// minutes).
func maybeAutoIndexOnStart(ctx context.Context, dir string, readOnly bool) {
	if info, err := os.Stat(filepath.Join(dir, ".leankg")); err != nil || !info.IsDir() {
		return
	}
	cfg := projectcfg.LoadOrDefault(dir).MCP

	// The store may live under the config's project_path anchor, not the
	// serve dir itself (cmdServe applies the same anchor above).
	dbDir := projectcfg.ResolveProjectRoot(filepath.Join(dir, ".leankg"))
	st, err := store.Open(filepath.Join(dbDir, "leankg.db"), store.RO)
	if err != nil {
		log.Printf("auto-index: cannot read store state: %v", err)
		return
	}
	elements, eerr := st.ElementCount()
	seq, at, werr := st.Watermark()
	st.Close()
	state := indexgate.StoreState{
		Elements:    elements,
		LastWrite:   at,
		LastWriteOK: werr == nil && eerr == nil && seq > 0,
	}

	decision := indexgate.Decide(cfg, setup.Workspace{Root: dir}, state, readOnly,
		indexgate.SkipFreshnessFromEnv(os.Getenv))
	log.Printf("auto-index on start: %s", decision)
	if !decision.Go() {
		return
	}
	if err := runIndex("", dir, "", "", ""); err != nil {
		log.Printf("auto-index failed: %v", err)
	}
}

// maybeRunServeSetup ports the Rust LEANKG_SETUP=1 trigger (main.rs:462-494):
// once the server is up, run the setup pipeline (clone -> index -> embed) once
// per clone root, gated on the setup.done marker.
func maybeRunServeSetup(ctx context.Context, dir string) {
	v := os.Getenv("LEANKG_SETUP")
	if v == "" || v == "0" || strings.EqualFold(v, "false") {
		return
	}
	if setup.SetupDone() {
		log.Printf("LEANKG_SETUP=1 but setup already done (marker exists), skipping.")
		return
	}
	log.Printf("LEANKG_SETUP=1 — scheduling setup after the server binds...")
	// Rust polled GET /health for up to 120 s. The Go engine binds its
	// listeners in goroutines without an exported readiness channel, so gate
	// on a short settle; the pipeline only runs index/embed flows, which do
	// not depend on the listener being up.
	select {
	case <-ctx.Done():
		return
	case <-time.After(2 * time.Second):
	}
	res, err := setup.RunSetup(ctx, setup.Options{
		Clone: true, Index: true, Embed: true, Status: false,
		Stages: cliStages{}, Logf: log.Printf,
	})
	if err != nil {
		log.Printf("LEANKG_SETUP: pipeline failed: %v", err)
		return
	}
	for _, repo := range res.IndexedRepos {
		if rerr := registry.Register(repo.Name, repo.Dir); rerr != nil {
			log.Printf("LEANKG_SETUP: register %s: %v", repo.Name, rerr)
		}
	}
	log.Printf("LEANKG_SETUP: pipeline complete.")
}
