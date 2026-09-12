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
	"github.com/FreePeak/LeanKG/go/internal/projectcfg"
	"github.com/FreePeak/LeanKG/go/internal/store"
)

// cmdStatus prints the status tool payload as JSON (Rust `leankg status --json`
// parity): health, inventory, freshness, embed state, lazy language tiers, and
// the effective leankg.yaml project config.
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
	// Rust find_project_root + MCPServer::resolve_project_root: walk up to the
	// owning project, then honor the config's project_path anchor. The store
	// lives under the ANCHORED dir — the directory the serving transports key
	// the schema on — so status must inspect that same store.
	dir = projectcfg.FindProjectRoot(dir)
	dbDir := projectcfg.ResolveProjectRoot(filepath.Join(dir, ".leankg"))
	storeDir := filepath.Dir(dbDir)
	st, err := store.OpenBackend(context.Background(), storeDir,
		envOr("LEANKG_DB_ENGINE", "sqlite"), pgURLFor(storeDir), store.RO)
	if err != nil {
		// An uninitialized project is legitimately "cold" (exit 0); a store
		// that EXISTS but cannot be opened is an operational failure and
		// must exit nonzero (the silent-failure class that manufactured the
		// old fake numbers).
		if _, statErr := os.Lstat(dbDir); os.IsNotExist(statErr) {
			fmt.Printf("{\"elements\":0,\"freshness\":\"cold\",\"note\":\"%s\"}\n", err.Error())
			os.Exit(0)
		}
		fmt.Fprintf(os.Stderr, "status: %v\n", err)
		os.Exit(1)
	}
	defer st.Close()
	engine := core.New(st, nil, nil)
	engine.SetProjectDir(storeDir)
	// Attach the language registry (activated for this project) so status
	// reports the lazy activation state, not an empty list.
	reg := langs.DefaultRegistry()
	if _, aerr := reg.Activate(storeDir); aerr == nil {
		engine.SetLangsRegistry(reg)
	}
	out, err := engine.Status(context.Background())
	if err != nil {
		log.Fatal(err)
	}
	// Surface the effective project config (and its parse error) alongside the
	// numbers: which config produced this store is part of the payload.
	out["project_config"] = effectiveConfig(dir, storeDir)
	printJSON(out)
}

// effectiveConfig reports the project config that governs a store: the config
// at the requested dir (preferring the store-side <dir>/.leankg/leankg.yaml,
// which is what the setup pipeline writes and the resolver reads, over the
// repo-root leankg.yaml), its parse error when unreadable, and which tier of
// the Postgres precedence supplied the connection URL for storeDir.
func effectiveConfig(dir, storeDir string) map[string]any {
	cfgDir := dir
	if _, err := os.Stat(projectcfg.ConfigPath(filepath.Join(dir, ".leankg"))); err == nil {
		cfgDir = filepath.Join(dir, ".leankg")
	}
	cfgPath := projectcfg.ConfigPath(cfgDir)
	cfg, err := projectcfg.Load(cfgDir)
	if err != nil {
		return map[string]any{"path": cfgPath, "error": err.Error()}
	}
	m := map[string]any{
		"path":         cfgPath,
		"name":         cfg.Project.Name,
		"project_path": cfg.Project.ProjectPath,
		"mcp":          redactedMCP(cfg.MCP),
	}
	if cfg.DB != nil {
		m["db"] = map[string]any{
			"url":        redactDSN(cfg.DB.URL),
			"pool_size":  cfg.DB.PoolSize,
			"lock":       cfg.DB.Lock,
			"url_source": pgURLSource(storeDir),
		}
	}
	return m
}

// redactedMCP is the mcp block with its bearer token masked: status output
// travels through logs, dashboards, and issue trackers.
func redactedMCP(cfg projectcfg.MCPConfig) projectcfg.MCPConfig {
	if cfg.AuthToken != "" {
		cfg.AuthToken = "***"
	}
	return cfg
}

// pgURLSource reports which tier of the Postgres precedence supplied the
// connection URL: "env" (LEANKG_PG_URL), "yaml" (the nearest leankg.yaml
// `db:` block), or "default" (neither: the built-in sqlite engine).
func pgURLSource(dir string) string {
	if os.Getenv("LEANKG_PG_URL") != "" {
		return "env"
	}
	if db := projectcfg.DBConfigFromDir(dir); db != nil && db.URL != "" {
		return "yaml"
	}
	return "default"
}

// redactDSN strips credentials from a DSN for display. It mirrors
// store.redactDSN (unexported): keep the scheme/prefix, mask everything
// before the last '@'.
func redactDSN(dsn string) string {
	for i := 0; i < len(dsn); i++ {
		if dsn[i] == '@' {
			schemeEnd := 0
			for j := 0; j < i; j++ {
				if dsn[j] == '/' {
					schemeEnd = j + 1
				}
			}
			return dsn[:schemeEnd] + "***@" + dsn[i+1:]
		}
	}
	return dsn
}
