package main

import (
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/FreePeak/LeanKG/go/internal/store"
)

// TestParseTimeFilter ports the Rust cli::audit tests: the three accepted
// forms and the rejects.
func TestParseTimeFilter(t *testing.T) {
	for _, bad := range []string{"", "   ", "1x", "soon", "-5m"} {
		if _, err := parseTimeFilter(bad); err == nil {
			t.Errorf("parseTimeFilter(%q) must fail", bad)
		}
	}

	if ts, err := parseTimeFilter("1770000000"); err != nil || ts.Unix() != 1770000000 {
		t.Fatalf("epoch filter = %v (%v)", ts, err)
	}
	ts, err := parseTimeFilter("2026-08-22T10:00:00Z")
	if err != nil || ts.Unix() != time.Date(2026, 8, 22, 10, 0, 0, 0, time.UTC).Unix() {
		t.Fatalf("RFC3339 filter = %v (%v)", ts, err)
	}

	before := time.Now()
	rel, err := parseTimeFilter("90s")
	if err != nil {
		t.Fatalf("90s: %v", err)
	}
	wantLo := before.Add(-91 * time.Second)
	wantHi := time.Now().Add(-89 * time.Second)
	if rel.Before(wantLo) || rel.After(wantHi) {
		t.Fatalf("90s resolved to %v, want ~%v", rel, before.Add(-90*time.Second))
	}
	if _, err := parseTimeFilter("7d"); err != nil {
		t.Fatalf("7d: %v", err)
	}
}

// seedAuditLedger appends two entries (one backdated, one now) and closes the
// handle, so the CLI under test owns the file afterward.
func seedAuditLedger(t *testing.T, dir string) {
	t.Helper()
	st, err := store.Open(filepath.Join(dir, ".leankg", "leankg.db"), store.RW)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.Migrate(); err != nil {
		t.Fatal(err)
	}
	if err := st.AppendAudit(&store.AuditEntry{
		At:      1_600_000_000,
		Actor:   "tester",
		Action:  "tool_call",
		Target:  "query",
		Details: map[string]any{"k": "v"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.AppendAudit(&store.AuditEntry{Actor: "tester", Action: "index", Target: "repo"}); err != nil {
		t.Fatal(err)
	}
}

// TestAuditExportCLI covers the JSONL export and its time window + --out.
func TestAuditExportCLI(t *testing.T) {
	dir := seedCLIProject(t, nil, nil)
	seedAuditLedger(t, dir)

	stdout, stderr, code := runCLI(t, "audit", "export", "--project", dir)
	if code != 0 {
		t.Fatalf("audit export exit = %d, stderr: %s", code, stderr)
	}
	lines := strings.Split(strings.TrimSuffix(stdout, "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("export lines = %d, want 2:\n%s", len(lines), stdout)
	}
	var first struct {
		Seq      int64          `json:"seq"`
		Actor    string         `json:"actor"`
		Action   string         `json:"action"`
		Target   string         `json:"target"`
		PrevHash string         `json:"prev_hash"`
		Details  map[string]any `json:"details"`
	}
	if err := json.Unmarshal([]byte(lines[0]), &first); err != nil {
		t.Fatalf("first JSONL line invalid: %v\n%s", err, lines[0])
	}
	if first.Seq != 1 || first.Actor != "tester" || first.Action != "tool_call" ||
		first.Target != "query" || first.PrevHash != "" || first.Details["k"] != "v" {
		t.Fatalf("first entry = %+v", first)
	}
	var second struct {
		Seq      int64  `json:"seq"`
		PrevHash string `json:"prev_hash"`
	}
	if err := json.Unmarshal([]byte(lines[1]), &second); err != nil {
		t.Fatalf("second JSONL line invalid: %v", err)
	}
	if second.PrevHash == "" {
		t.Fatal("second entry must chain onto the first")
	}

	// The window filters are inclusive on epoch seconds.
	stdout, _, code = runCLI(t, "audit", "export", "--project", dir, "--since", "1700000000")
	if code != 0 || strings.Count(stdout, "\n") != 1 || !strings.Contains(stdout, `"action":"index"`) {
		t.Fatalf("since-window export: code=%d\n%s", code, stdout)
	}
	stdout, _, code = runCLI(t, "audit", "export", "--project", dir, "--until", "1600000000")
	if code != 0 || strings.Count(stdout, "\n") != 1 || !strings.Contains(stdout, `"action":"tool_call"`) {
		t.Fatalf("until-window export: code=%d\n%s", code, stdout)
	}
	// Relative durations parse ("24h" keeps both fresh entries' window open
	// but excludes the 2020 backdate).
	stdout, _, code = runCLI(t, "audit", "export", "--project", dir, "--since", "24h")
	if code != 0 || strings.Count(stdout, "\n") != 1 {
		t.Fatalf("relative-since export: code=%d\n%s", code, stdout)
	}

	// --out writes the file instead of stdout.
	out := filepath.Join(dir, "ledger.jsonl")
	stdout, stderr, code = runCLI(t, "audit", "export", "--project", dir, "--out", out)
	if code != 0 || stdout != "" {
		t.Fatalf("export --out stdout = %q (code %d, stderr %s)", stdout, code, stderr)
	}
	if !strings.Contains(stderr, "wrote 2 audit entries to") {
		t.Fatalf("export --out stderr = %q", stderr)
	}
	if body, err := os.ReadFile(out); err != nil || strings.Count(string(body), "\n") != 2 {
		t.Fatalf("export --out file: %v\n%s", err, body)
	}

	// Only jsonl is wired, like the Rust AuditFormat enum.
	if _, _, code = runCLI(t, "audit", "export", "--project", dir, "--format", "csv"); code != 2 {
		t.Fatalf("unknown format exit = %d, want 2", code)
	}
	// Bad filters are usage errors, not exit-1 store errors.
	if _, _, code = runCLI(t, "audit", "export", "--project", dir, "--since", "bogus"); code != 2 {
		t.Fatalf("bad filter exit = %d, want 2", code)
	}
}

// TestAuditVerifyCLI pins the ok/broken contract and the non-zero exit on a
// tampered ledger.
func TestAuditVerifyCLI(t *testing.T) {
	dir := seedCLIProject(t, nil, nil)
	seedAuditLedger(t, dir)

	stdout, stderr, code := runCLI(t, "audit", "verify", "--project", dir)
	if code != 0 {
		t.Fatalf("verify exit = %d, stderr: %s", code, stderr)
	}
	if !strings.Contains(stdout, "OK: audit chain intact (2 entries verified)") {
		t.Fatalf("verify stdout = %q", stdout)
	}

	// Tamper with one row: the chain must break exactly there and the CLI
	// must exit non-zero (the operator-facing contract).
	db, err := sql.Open("sqlite", "file:"+filepath.Join(dir, ".leankg", "leankg.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE audit_ledger SET target='evil' WHERE seq=1`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	_, stderr, code = runCLI(t, "audit", "verify", "--project", dir)
	if code == 0 {
		t.Fatal("tampered ledger must verify non-zero")
	}
	if !strings.Contains(stderr, "broken at seq 1") {
		t.Fatalf("tampered verify stderr = %q", stderr)
	}
}

// TestAuditSubcommandValidation covers the dispatch guard.
func TestAuditSubcommandValidation(t *testing.T) {
	dir := seedCLIProject(t, nil, nil)
	if _, stderr, code := runCLI(t, "audit"); code != 2 || !strings.Contains(stderr, "expected a subcommand") {
		t.Fatalf("bare audit: code=%d stderr=%s", code, stderr)
	}
	if _, stderr, code := runCLI(t, "audit", "frobnicate", "--project", dir); code != 2 || !strings.Contains(stderr, "unknown subcommand") {
		t.Fatalf("bad subcommand: code=%d stderr=%s", code, stderr)
	}
}
