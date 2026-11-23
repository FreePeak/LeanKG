package main

import (
	"bytes"
	"context"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// pgURLOnlyInYAML writes a serve project whose Postgres DSN lives only in
// leankg.yaml db.url. The port is closed on purpose, so a PG open fails at
// connect, never at the "no dsn" check.
func pgURLOnlyInYAML(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "leankg.yaml"),
		[]byte("db:\n  url: postgres://leankg:leankg@127.0.0.1:1/leankg?connect_timeout=1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LEANKG_PG_URL", "")
	return dir
}

// TestSweepRegistryHonoursLeankgYAMLPGURL: the registry sweep must read the
// serve project's leankg.yaml db.url, as the single-project store does. An
// env-only read reports "no dsn" instead of trying the configured server.
func TestSweepRegistryHonoursLeankgYAMLPGURL(t *testing.T) {
	dir := pgURLOnlyInYAML(t)
	t.Chdir(t.TempDir()) // keep the cwd's config out of the answer
	var logs bytes.Buffer
	prev := log.Writer()
	log.SetOutput(&logs)
	t.Cleanup(func() { log.SetOutput(prev) })

	sweepRegistry(context.Background(), "postgres", dir)

	if strings.Contains(logs.String(), "no dsn") {
		t.Fatalf("registry sweep ignored leankg.yaml db.url: %s", logs.String())
	}
}

// TestServeRouterReadsLeankgYAMLPGURL: the multi-project router opens every
// registered project with one DSN. It must come from projectcfg.PGURL(dir), as
// the single-project store does, not from the env alone. serve binds listeners
// and blocks, so this reads the source, as TestServePathSweepsTheRegistry does.
func TestServeRouterReadsLeankgYAMLPGURL(t *testing.T) {
	src, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatalf("read main.go: %v", err)
	}
	body := string(src)
	start := strings.Index(body, "projects.NewRouter(dir, projects.Config{")
	if start < 0 {
		t.Fatal("no projects.NewRouter(dir, projects.Config{...}) call in main.go")
	}
	block := body[start:]
	if end := strings.Index(block, "})"); end > 0 {
		block = block[:end]
	}
	if !strings.Contains(block, "projectcfg.PGURL(dir)") {
		t.Errorf("the multi-project router takes its PG URL from the env only; want projectcfg.PGURL(dir) in:\n%s", block)
	}
}
