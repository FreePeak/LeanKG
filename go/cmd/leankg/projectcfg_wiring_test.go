package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// anchorFixture builds the anchored-project layout: `holder` runs the verbs
// while its .leankg/leankg.yaml project_path names `store`, the dir that owns
// the real indexed .leankg store. This is the reachability contract the
// projectcfg wiring exists for: reader verbs must open the ANCHORED store,
// not the holder's own (empty) one.
func anchorFixture(t *testing.T) (holder, storeDir string) {
	t.Helper()
	t.Setenv("LEANKG_DB_ENGINE", "")
	t.Setenv("LEANKG_PG_URL", "")
	base := t.TempDir()
	holder = filepath.Join(base, "holder")
	storeDir = filepath.Join(base, "realstore")
	if err := os.MkdirAll(filepath.Join(holder, ".leankg"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(storeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(storeDir, "p.go"),
		[]byte("package p\n\nfunc A() int { return 1 }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := buildLeanKG(t)
	if out, err := exec.Command(bin, "index", storeDir).CombinedOutput(); err != nil {
		t.Fatalf("index realstore: %v\n%s", err, out)
	}
	cfg := "project:\n  name: holder\n  root: .\n  project_path: " + storeDir + "\n" +
		"db:\n  url: postgresql://u:pw@localhost:5433/leankg\n  pool_size: 7\n" +
		"mcp:\n  auth_token: super-secret-token\n"
	if err := os.WriteFile(filepath.Join(holder, ".leankg", "leankg.yaml"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	return holder, storeDir
}

// TestStatusResolvesConfigAnchor pins the audited gap this wiring closes:
// `leankg status` must open the store the config's project_path names — the
// schema the server actually serves — and surface the effective config
// (path, name, anchor) with the config's credentials redacted.
func TestStatusResolvesConfigAnchor(t *testing.T) {
	holder, storeDir := anchorFixture(t)
	stdout, _, code := runCLI(t, "status", "--project", holder)
	if code != 0 {
		t.Fatalf("status exit %d:\n%s", code, stdout)
	}
	var out struct {
		Store         string         `json:"store"`
		ProjectConfig map[string]any `json:"project_config"`
	}
	if err := json.Unmarshal([]byte(stdout), &out); err != nil {
		t.Fatalf("parse status json: %v\n%s", err, stdout)
	}
	if want := filepath.Join(realPath(t, storeDir), ".leankg", "leankg.db"); out.Store != want {
		t.Fatalf("status store = %q, want the anchored %q", out.Store, want)
	}
	if out.ProjectConfig["project_path"] != storeDir {
		t.Fatalf("project_config.project_path = %v, want %s", out.ProjectConfig["project_path"], storeDir)
	}
	if out.ProjectConfig["name"] != "holder" {
		t.Fatalf("project_config.name = %v, want holder", out.ProjectConfig["name"])
	}
	db, _ := out.ProjectConfig["db"].(map[string]any)
	if db == nil {
		t.Fatal("project_config.db missing")
	}
	// The connection string is a credential: it must never reach stdout raw.
	if db["url"] != "postgresql://***@localhost:5433/leankg" {
		t.Fatalf("db.url = %v, want redacted", db["url"])
	}
	// The tier is read walking up from the STORE dir (the dir the connection
	// is keyed on). This fixture's db block sits in the store-side config,
	// which the `db:` reader does not consult (Rust db_config_from_cwd only
	// read <dir>/leankg.yaml levels), so nothing supplies a URL here; a
	// repo-root db block reports "yaml" instead (self-anchored test below).
	if db["url_source"] != "default" {
		t.Fatalf("db.url_source = %v, want default", db["url_source"])
	}
	if strings.Contains(stdout, "super-secret-token") || !strings.Contains(stdout, `"AuthToken": "***"`) {
		t.Fatalf("status must not leak the config's mcp token:\n%s", stdout)
	}
}

// TestDoctorResolvesConfigAnchor pins the doctor side: the shallow doctor
// inspects the anchored store, and the deep doctor runs its config check
// against the live anchor.
func TestDoctorResolvesConfigAnchor(t *testing.T) {
	holder, storeDir := anchorFixture(t)
	stdout, _, code := runCLI(t, "doctor", "--project", holder)
	if code != 0 {
		t.Fatalf("doctor exit %d:\n%s", code, stdout)
	}
	if !strings.Contains(stdout, filepath.Join(realPath(t, storeDir), ".leankg", "leankg.db")) {
		t.Fatalf("doctor must report the ANCHORED store, got:\n%s", stdout)
	}

	// The anchored store is what the deep checks must interrogate: the
	// config check passing with the anchor reported is the candidate-loop
	// proof (before this wiring the deep doctor probed the holder's own
	// absent store). The aggregate exit code is not pinned — index-freshness
	// legitimately compares the index against the dir doctor ran from.
	stdout, _, _ = runCLI(t, "doctor", "--deep", "--project", holder)
	if !strings.Contains(stdout, "config") || !strings.Contains(stdout, storeDir) {
		t.Fatalf("deep doctor must report the resolved anchor:\n%s", stdout)
	}
	for _, line := range strings.Split(stdout, "\n") {
		if strings.HasPrefix(line, "config") && !strings.Contains(line, "PASS") {
			t.Fatalf("config check must PASS on a live anchor:\n%s", stdout)
		}
	}
}

// TestDoctorFlagsDanglingAnchor pins the failure mode: a project_path that
// owns no .leankg must surface as a FAIL config finding (exit 2), not as a
// silent fallback to the holder's own store.
func TestDoctorFlagsDanglingAnchor(t *testing.T) {
	t.Setenv("LEANKG_DB_ENGINE", "")
	t.Setenv("LEANKG_PG_URL", "")
	base := t.TempDir()
	holder := filepath.Join(base, "holder")
	if err := os.MkdirAll(filepath.Join(holder, ".leankg"), 0o755); err != nil {
		t.Fatal(err)
	}
	ghost := filepath.Join(base, "does-not-exist")
	cfg := "project:\n  name: holder\n  project_path: " + ghost + "\n"
	if err := os.WriteFile(filepath.Join(holder, ".leankg", "leankg.yaml"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	stdout, _, code := runCLI(t, "doctor", "--deep", "--project", holder)
	if code != 2 {
		t.Fatalf("dangling anchor must exit 2, got %d:\n%s", code, stdout)
	}
	if !strings.Contains(stdout, "config") || !strings.Contains(stdout, "FAIL") ||
		!strings.Contains(stdout, "owns no .leankg") {
		t.Fatalf("deep doctor must FAIL the config check on a dangling anchor:\n%s", stdout)
	}
}

// TestIndexSelfHealsProjectPath pins the N1 behavior plus the healthy
// deployment shape: an index run into a project whose leankg.yaml lacks
// project.project_path backfills the anchor with this run's canonical
// identity (preserving the user's db block), and the deep doctor then resolves
// that self-anchor and passes every check.
func TestIndexSelfHealsProjectPath(t *testing.T) {
	t.Setenv("LEANKG_DB_ENGINE", "")
	t.Setenv("LEANKG_PG_URL", "")
	proj := t.TempDir()
	if err := os.WriteFile(filepath.Join(proj, "p.go"),
		[]byte("package p\n\nfunc A() int { return 1 }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfgYaml := "project:\n  name: selfheal\n  root: .\ndb:\n  url: postgresql://u:pw@localhost:5433/leankg\n"
	if err := os.WriteFile(filepath.Join(proj, "leankg.yaml"), []byte(cfgYaml), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := buildLeanKG(t)
	if out, err := exec.Command(bin, "index", proj).CombinedOutput(); err != nil {
		t.Fatalf("index: %v\n%s", err, out)
	}
	after, err := os.ReadFile(filepath.Join(proj, "leankg.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	s := string(after)
	if !strings.Contains(s, "project_path: "+realPath(t, proj)) {
		t.Fatalf("index must backfill project_path with the canonical root:\n%s", s)
	}
	if !strings.Contains(s, "url: postgresql://u:pw@localhost:5433/leankg") {
		t.Fatalf("index must preserve the user's db block:\n%s", s)
	}

	stdout, _, code := runCLI(t, "doctor", "--deep", "--project", proj)
	if code != 0 || !strings.Contains(stdout, "0 fail") {
		t.Fatalf("deep doctor on a self-anchored project must pass, exit %d:\n%s", code, stdout)
	}

	// The repo-root db block is the tier the status report must name.
	stdout, _, code = runCLI(t, "status", "--project", proj)
	if code != 0 {
		t.Fatalf("status exit %d:\n%s", code, stdout)
	}
	var out struct {
		ProjectConfig struct {
			DB map[string]any `json:"db"`
		} `json:"project_config"`
	}
	if err := json.Unmarshal([]byte(stdout), &out); err != nil {
		t.Fatalf("parse status json: %v\n%s", err, stdout)
	}
	if out.ProjectConfig.DB["url_source"] != "yaml" {
		t.Fatalf("db.url_source = %v, want yaml (repo-root db block, no env URL)", out.ProjectConfig.DB["url_source"])
	}
}

// realPath resolves a dir the way the binary reports it (abs + EvalSymlinks —
// macOS /tmp is a symlink to /private/tmp).
func realPath(t *testing.T, dir string) string {
	t.Helper()
	abs, err := filepath.Abs(dir)
	if err != nil {
		t.Fatal(err)
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		return resolved
	}
	return abs
}
