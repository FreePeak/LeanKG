package doctor

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/FreePeak/LeanKG/go/internal/store"
)

// shortHash replicates the store's schema key derivation for the
// schema-parity test.
func shortHash(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])[:16]
}

// stubProbes is the canned probe set (Rust deep.rs StubProbes port):
// defaults describe a tiny healthy deployment — fast ping, fully-applied
// migrations, two indexed/fresh files, full embedding coverage, clean
// edges. Every failure mode flips one field.
type stubProbes struct {
	ping       int64
	pingErr    error
	applied    []int
	appliedErr error
	indexed    []string
	indexedErr error
	names      []string
	namesErr   error
	edges      []Edge
	edgesErr   error
	embedded   []string
	tablesAbs  bool
}

func (s *stubProbes) PingMS() (int64, error) { return s.ping, s.pingErr }
func (s *stubProbes) AppliedMigrations() ([]int, error) {
	return s.applied, s.appliedErr
}
func (s *stubProbes) IndexedFiles() ([]string, error)    { return s.indexed, s.indexedErr }
func (s *stubProbes) QualifiedNames() ([]string, error)  { return s.names, s.namesErr }
func (s *stubProbes) RelationshipEdges() ([]Edge, error) { return s.edges, s.edgesErr }
func (s *stubProbes) EmbeddedNames() ([]string, bool, error) {
	return s.embedded, !s.tablesAbs, s.namesErr
}
func (s *stubProbes) EngineName() string { return "stub" }

// embeddedVersions lists every embedded migration version: the healthy stub
// applies all of them, so this fixture cannot drift behind a new migration.
func embeddedVersions() []int {
	steps := store.Migrations()
	out := make([]int, 0, len(steps))
	for _, m := range steps {
		out = append(out, m.Version)
	}
	return out
}

func healthyStub() *stubProbes {
	return &stubProbes{
		ping:    3,
		applied: embeddedVersions(),
		indexed: []string{"src/a.go", "src/b.go"},
		names:   []string{"pkg.A", "pkg.B"},
		edges:   []Edge{{"pkg.A", "pkg.B", "calls"}},
	}
}

func healthyEnv() Env {
	return Env{ProjectRoot: "/proj", LeankgDir: "/proj/.leankg",
		DiskFiles: []string{"src/a.go", "src/b.go"}}
}

func finding(t *testing.T, r Report, check string) Finding {
	t.Helper()
	for _, f := range r.Findings {
		if f.Check == check {
			return f
		}
	}
	t.Fatalf("no finding for check %q in %+v", check, r.Findings)
	return Finding{}
}

func TestReportExitCodes(t *testing.T) {
	allPass := Report{Findings: []Finding{
		{Check: "a", Status: StatusPass}, {Check: "b", Status: StatusPass},
	}}
	if got := allPass.ExitCode(); got != 0 {
		t.Fatalf("all-pass exit = %d, want 0", got)
	}
	withWarn := Report{Findings: []Finding{{Check: "a", Status: StatusWarn}}}
	if got := withWarn.ExitCode(); got != 1 {
		t.Fatalf("warn exit = %d, want 1", got)
	}
	withFail := Report{Findings: []Finding{{Check: "a", Status: StatusWarn}, {Check: "b", Status: StatusFail}}}
	if got := withFail.ExitCode(); got != 2 {
		t.Fatalf("fail exit = %d, want 2", got)
	}
}

func TestRenderTableAndJSON(t *testing.T) {
	r := Report{Findings: []Finding{
		{Check: "pg-latency", Status: StatusPass, Detail: "3 ms round-trip"},
		{Check: "migrations", Status: StatusWarn, Detail: "1 pending", Hint: "run index"},
	}}
	table := r.RenderTable()
	for _, want := range []string{"check", "status", "pg-latency", "PASS", "migrations", "WARN",
		"1 pass, 1 warn, 0 fail — exit 1"} {
		if !strings.Contains(table, want) {
			t.Errorf("table missing %q:\n%s", want, table)
		}
	}
	js, err := r.RenderJSON()
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"findings"`, `"summary"`, `"warn": 1`} {
		if !strings.Contains(js, want) {
			t.Errorf("json missing %q:\n%s", want, js)
		}
	}
}

func TestStoreLatencyThresholds(t *testing.T) {
	tests := []struct {
		name string
		ping int64
		perr error
		want CheckStatus
	}{
		{"fast", 3, nil, StatusPass},
		{"warn-tier", 900, nil, StatusWarn},
		{"fail-tier", 6000, nil, StatusFail},
		{"unreachable", 0, fmt.Errorf("connection refused"), StatusFail},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p := healthyStub()
			p.ping, p.pingErr = tc.ping, tc.perr
			f := checkStoreLatency(p, Env{})
			if f.Status != tc.want {
				t.Fatalf("status = %s, want %s (detail %q)", f.Status, tc.want, f.Detail)
			}
			if tc.perr != nil && f.Hint == "" {
				t.Error("unreachable finding must carry a remediation hint")
			}
		})
	}
}

func TestMigrationsDrift(t *testing.T) {
	all := healthyStub() // every embedded migration applied
	if f := checkMigrations(all, Env{}); f.Status != StatusPass {
		t.Fatalf("fully-applied = %s (%s)", f.Status, f.Detail)
	}
	behind := healthyStub()
	behind.applied = []int{1, 2}
	f := checkMigrations(behind, Env{})
	if f.Status != StatusFail || !strings.Contains(f.Detail, "pending") {
		t.Fatalf("behind = %s (%s), want FAIL pending", f.Status, f.Detail)
	}
	ahead := healthyStub()
	vs := embeddedVersions()
	ahead.applied = append(vs, vs[len(vs)-1]+1) // one past the newest embedded version
	f = checkMigrations(ahead, Env{})
	if f.Status != StatusWarn || !strings.Contains(f.Detail, "ahead of binary") {
		t.Fatalf("ahead = %s (%s), want WARN ahead-of-binary", f.Status, f.Detail)
	}
	ledgerErr := healthyStub()
	ledgerErr.appliedErr = fmt.Errorf("no such table: schema_migrations")
	f = checkMigrations(ledgerErr, Env{})
	if f.Status != StatusFail || !strings.Contains(f.Detail, "cannot read migration ledger") {
		t.Fatalf("ledger error = %s (%s)", f.Status, f.Detail)
	}
}

func TestIndexFreshness(t *testing.T) {
	allGood := healthyStub()
	if f := checkIndexFreshness(allGood, healthyEnv()); f.Status != StatusPass {
		t.Fatalf("fresh = %s (%s)", f.Status, f.Detail)
	}
	// Synthetic URI entries never count as stale.
	synth := healthyStub()
	synth.indexed = []string{"src/a.go", "ontology://concept-1"}
	env := healthyEnv()
	env.DiskFiles = []string{"src/a.go"}
	if f := checkIndexFreshness(synth, env); f.Status != StatusPass {
		t.Fatalf("synthetic = %s (%s)", f.Status, f.Detail)
	}
	// Empty index over a non-empty tree FAILs.
	empty := healthyStub()
	empty.indexed = nil
	f := checkIndexFreshness(empty, env)
	if f.Status != StatusFail || !strings.Contains(f.Detail, "index is empty") {
		t.Fatalf("empty = %s (%s)", f.Status, f.Detail)
	}
	// >50% stale paths FAIL.
	moved := healthyStub()
	moved.indexed = []string{"src/a.go", "src/old1.go", "src/old2.go"}
	f = checkIndexFreshness(moved, env)
	if f.Status != StatusFail || !strings.Contains(f.Detail, "no longer exist") {
		t.Fatalf("moved = %s (%s)", f.Status, f.Detail)
	}
	// One new file on disk WARNs with the missing count.
	newFile := healthyStub()
	env = healthyEnv()
	env.DiskFiles = []string{"src/a.go", "src/b.go", "src/new.go"}
	f = checkIndexFreshness(newFile, env)
	if f.Status != StatusWarn || !strings.Contains(f.Detail, "1 missing file(s)") {
		t.Fatalf("new-file = %s (%s)", f.Status, f.Detail)
	}
}

func TestEmbeddingCoverage(t *testing.T) {
	full := healthyStub()
	full.embedded = full.names
	if f := checkEmbeddingCoverage(full, Env{}); f.Status != StatusPass ||
		!strings.Contains(f.Detail, "2/2 elements embedded") {
		t.Fatalf("full = %s (%s)", f.Status, f.Detail)
	}
	none := healthyStub()
	none.embedded = nil
	if f := checkEmbeddingCoverage(none, Env{}); f.Status != StatusPass ||
		!strings.Contains(f.Detail, "never built") {
		t.Fatalf("none = %s (%s)", f.Status, f.Detail)
	}
	absent := healthyStub()
	absent.tablesAbs = true
	if f := checkEmbeddingCoverage(absent, Env{}); f.Status != StatusPass ||
		!strings.Contains(f.Detail, "tables absent") {
		t.Fatalf("absent = %s (%s)", f.Status, f.Detail)
	}
	partial := healthyStub()
	partial.embedded = []string{"pkg.A"}
	f := checkEmbeddingCoverage(partial, Env{})
	if f.Status != StatusWarn || !strings.Contains(f.Detail, "50% uncovered") {
		t.Fatalf("partial = %s (%s)", f.Status, f.Detail)
	}
	noElements := healthyStub()
	noElements.names = nil
	if f := checkEmbeddingCoverage(noElements, Env{}); f.Status != StatusPass {
		t.Fatalf("no-elements = %s (%s)", f.Status, f.Detail)
	}
}

func TestPoolEnv(t *testing.T) {
	// Unset: pass with defaults spelled out.
	f := checkPoolEnv(nil, Env{})
	if f.Status != StatusPass || !strings.Contains(f.Detail, "pool size: default 5") {
		t.Fatalf("unset = %s (%s)", f.Status, f.Detail)
	}
	valid := Env{Pool: PoolEnv{strp("10"), strp("5000")}}
	if f = checkPoolEnv(nil, valid); f.Status != StatusPass {
		t.Fatalf("valid = %s (%s)", f.Status, f.Detail)
	}
	oversized := Env{Pool: PoolEnv{strp("2048"), strp("5000")}}
	if f = checkPoolEnv(nil, oversized); f.Status != StatusWarn {
		t.Fatalf("oversized = %s (%s)", f.Status, f.Detail)
	}
	tooLongWait := Env{Pool: PoolEnv{strp("10"), strp("600001")}}
	if f = checkPoolEnv(nil, tooLongWait); f.Status != StatusWarn {
		t.Fatalf("long wait = %s (%s)", f.Status, f.Detail)
	}
	invalid := Env{Pool: PoolEnv{strp("zero"), strp("fast")}}
	f = checkPoolEnv(nil, invalid)
	if f.Status != StatusFail {
		t.Fatalf("invalid = %s (%s)", f.Status, f.Detail)
	}
	zero := Env{Pool: PoolEnv{strp("0"), nil}}
	if f = checkPoolEnv(nil, zero); f.Status != StatusFail {
		t.Fatalf("zero = %s (%s)", f.Status, f.Detail)
	}
}

func strp(s string) *string { return &s }

func TestOrphanedRelationships(t *testing.T) {
	clean := healthyStub()
	if f := checkOrphanedRelationships(clean, Env{}); f.Status != StatusPass ||
		!strings.Contains(f.Detail, "1 sampled edge(s) all resolve") {
		t.Fatalf("clean = %s (%s)", f.Status, f.Detail)
	}
	// The Go store's UNIQUE(source,target,type) lets callers still write
	// edges whose endpoints were deleted later — the exact production
	// failure mode this check catches.
	orph := healthyStub()
	orph.edges = append(orph.edges, Edge{"pkg.Ghost", "pkg.B", "calls"})
	f := checkOrphanedRelationships(orph, Env{})
	if f.Status != StatusFail || !strings.Contains(f.Detail, "1/2 sampled edges reference missing") {
		t.Fatalf("orphan = %s (%s)", f.Status, f.Detail)
	}
	if !strings.Contains(f.Detail, "pkg.Ghost -> pkg.B") {
		t.Errorf("detail must name the offending edge, got %q", f.Detail)
	}
}

func TestDuplicateNames(t *testing.T) {
	clean := healthyStub()
	if f := checkDuplicateNames(clean, Env{}); f.Status != StatusPass {
		t.Fatalf("clean = %s (%s)", f.Status, f.Detail)
	}
	// Duplicates cannot be produced through the sqlite store's UNIQUE
	// constraint (the Go engine upserts by qualified_name); the Rust
	// parity path is a PG fleet where a pre-upsert schema allowed them.
	// The check therefore runs against the raw column, duplicates intact.
	dup := healthyStub()
	dup.names = []string{"pkg.A", "pkg.A", "pkg.B"}
	f := checkDuplicateNames(dup, Env{})
	if f.Status != StatusFail || !strings.Contains(f.Detail, "pkg.A×2") {
		t.Fatalf("dup = %s (%s)", f.Status, f.Detail)
	}
}

func TestLeankgDir(t *testing.T) {
	dir := t.TempDir()
	env := Env{LeankgDir: filepath.Join(dir, ".leankg")}
	if f := checkLeankgDir(nil, env); f.Status != StatusFail {
		t.Fatalf("missing = %s (%s)", f.Status, f.Detail)
	}
	if err := os.Mkdir(env.LeankgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if f := checkLeankgDir(nil, env); f.Status != StatusPass {
		t.Fatalf("writable = %s (%s)", f.Status, f.Detail)
	}
	if err := os.WriteFile(filepath.Join(env.LeankgDir, "embed.lock"), []byte("1"), 0o644); err != nil {
		t.Fatal(err)
	}
	f := checkLeankgDir(nil, env)
	if f.Status != StatusWarn || !strings.Contains(f.Detail, "embed.lock") {
		t.Fatalf("lock = %s (%s)", f.Status, f.Detail)
	}
}

func TestRunDeepSqlite(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, ".leankg", "leankg.db"), store.RW)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.Migrate(); err != nil {
		t.Fatal(err)
	}
	// Seed via the public API: two elements and one dangling edge (the
	// target was never indexed — the delete-then-stale production path).
	// The indexed file must exist on disk: index-freshness compares the
	// index against the tree.
	if err := os.WriteFile(filepath.Join(dir, "a.go"), []byte("package a\n\nfunc A() {}\nfunc B() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	els := []store.Element{
		{QualifiedName: "a.A", ElementType: "func", Name: "A", FilePath: "a.go", Language: "go"},
		{QualifiedName: "a.B", ElementType: "func", Name: "B", FilePath: "a.go", Language: "go"},
	}
	if err := st.UpsertElements(els); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertRelationships([]store.Relationship{{Source: "a.A", Target: "a.Ghost", RelType: "calls"}}); err != nil {
		t.Fatal(err)
	}
	report, err := RunDeep(context.Background(), dir, "", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	orph := finding(t, report, "orphaned-relationships")
	if orph.Status != StatusFail {
		t.Fatalf("orphan check = %s (%s), want FAIL on the dangling edge", orph.Status, orph.Detail)
	}
	// Everything else on a freshly migrated sqlite store must pass.
	for _, f := range report.Findings {
		if f.Check == "orphaned-relationships" {
			continue
		}
		if f.Status != StatusPass {
			t.Errorf("%s = %s (%s), want PASS on a healthy sqlite store", f.Check, f.Status, f.Detail)
		}
	}
	if got := report.ExitCode(); got != 2 {
		t.Fatalf("exit = %d, want 2", got)
	}
}

func TestRunDeepMissingLeankgDir(t *testing.T) {
	_, err := RunDeep(context.Background(), t.TempDir(), "", "", nil)
	if err == nil || !strings.Contains(err.Error(), ".leankg not found") {
		t.Fatalf("err = %v, want .leankg-not-found", err)
	}
}

func TestRunDeepPGAbsenceDegradesGracefully(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".leankg"), 0o755); err != nil {
		t.Fatal(err)
	}
	// Engine=postgres against a dead URL: the report must still come back
	// structured — DB checks FAIL with the cause, the .leankg-dir check
	// (file-based) still runs.
	report, err := RunDeep(context.Background(), dir, "postgres", "postgres://127.0.0.1:1/none", nil)
	if err != nil {
		t.Fatalf("RunDeep must degrade, not error: %v", err)
	}
	lat := finding(t, report, "pg-latency")
	if lat.Status != StatusFail || !strings.Contains(lat.Detail, "unreachable") {
		t.Fatalf("pg-latency = %s (%s)", lat.Status, lat.Detail)
	}
	if f := finding(t, report, "leankg-dir"); f.Status != StatusPass {
		t.Errorf("leankg-dir = %s (%s), want PASS (file checks independent of PG)", f.Status, f.Detail)
	}
	if got := report.ExitCode(); got != 2 {
		t.Fatalf("exit = %d, want 2", got)
	}
}

func TestPGSchemaForDirMatchesStoreDerivation(t *testing.T) {
	// The doctor's schema derivation must byte-match store.OpenPG's.
	// store.pgSchemaForDir is unexported; verify against its documented
	// construction: leankg_ + hex(sha256(evalSymlinks(abs)))[:16].
	dir := t.TempDir()
	real, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	want := "leankg_" + shortHash(real)
	if got, err := pgSchemaForDir(dir); err != nil || got != want {
		t.Fatalf("pgSchemaForDir(%s) = %s,%v want %s", dir, got, err, want)
	}
}
