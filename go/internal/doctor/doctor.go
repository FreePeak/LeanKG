// Package doctor ports `leankg doctor --deep` (Rust H9): a deployment
// self-diagnosis suite. Eight checks interrogate the deployment end to
// end — store round-trip latency, migration drift, index freshness,
// embedding coverage, pool env sanity, orphaned relationships, duplicate
// qualified_names, and .leankg directory health. Each check yields a
// severity-tagged finding (PASS/WARN/FAIL) with an actionable remediation
// hint; exit codes are CI-friendly: 0 all-pass, 1 any warn, 2 any fail.
//
// Every DB touch goes through the injectable Probes interface so tests
// stub probe results without a live backend (the Rust DeepProbes seam);
// production wires sqliteProbes or pgProbes over a read-only handle
// opened for the project's engine.
package doctor

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/FreePeak/LeanKG/go/internal/index"
	"github.com/FreePeak/LeanKG/go/internal/langs"
	"github.com/FreePeak/LeanKG/go/internal/store"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

// Thresholds and caps, mirroring the Rust deep.rs constants.
const (
	latencyWarnMS  = 500  // round-trip above this (ms) is a WARN
	latencyFailMS  = 5000 // round-trip above this (ms) is a FAIL
	orphanLimit    = 1000 // edge sample cap for the orphan scan
	duplicateLimit = 10   // duplicate offenders listed in findings
	poolSizeMax    = 1024 // valid LEANKG_PG_POOL_SIZE upper bound
	poolWaitMaxMS  = 600000
)

// CheckStatus is the severity of one deep-check finding.
type CheckStatus string

const (
	StatusPass CheckStatus = "PASS"
	StatusWarn CheckStatus = "WARN"
	StatusFail CheckStatus = "FAIL"
)

// worst folds two statuses, keeping the more severe.
func (s CheckStatus) worst(other CheckStatus) CheckStatus {
	if s == StatusFail || other == StatusFail {
		return StatusFail
	}
	if s == StatusWarn || other == StatusWarn {
		return StatusWarn
	}
	return StatusPass
}

// Finding is one check's verdict: stable machine name + human detail +
// remediation hint.
type Finding struct {
	Check  string      `json:"check"`
	Status CheckStatus `json:"status"`
	Detail string      `json:"detail"`
	Hint   string      `json:"hint"`
}

// Counts is the PASS/WARN/FAIL tally across a report.
type Counts struct {
	Pass int `json:"pass"`
	Warn int `json:"warn"`
	Fail int `json:"fail"`
}

// Report is the full deep-doctor result: one finding per check, in
// registry order.
type Report struct {
	Findings []Finding `json:"findings"`
}

// Counts tallies the findings.
func (r Report) Counts() Counts {
	var c Counts
	for _, f := range r.Findings {
		switch f.Status {
		case StatusPass:
			c.Pass++
		case StatusWarn:
			c.Warn++
		case StatusFail:
			c.Fail++
		}
	}
	return c
}

// ExitCode is the CI contract: 0 all-pass, 1 any warn (no fails), 2 any fail.
func (r Report) ExitCode() int {
	c := r.Counts()
	if c.Fail > 0 {
		return 2
	}
	if c.Warn > 0 {
		return 1
	}
	return 0
}

// RenderJSON renders the machine-readable form including the summary block.
func (r Report) RenderJSON() (string, error) {
	env := struct {
		Findings []Finding `json:"findings"`
		Summary  Counts    `json:"summary"`
	}{r.Findings, r.Counts()}
	b, err := json.MarshalIndent(env, "", "  ")
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// RenderTable renders the aligned human table: check | status | detail |
// hint, then a summary row.
func (r Report) RenderTable() string {
	rows := make([][4]string, 0, len(r.Findings)+1)
	for _, f := range r.Findings {
		rows = append(rows, [4]string{f.Check, string(f.Status), f.Detail, f.Hint})
	}
	c := r.Counts()
	rows = append(rows, [4]string{"", "",
		fmt.Sprintf("%d pass, %d warn, %d fail — exit %d", c.Pass, c.Warn, c.Fail, r.ExitCode()), ""})

	widths := [4]int{5, 6, 6, 4} // "check", "status", "detail", "hint"
	for _, row := range rows {
		for i, cell := range row {
			if n := len([]rune(cell)); n > widths[i] {
				widths[i] = n
			}
		}
	}
	var b strings.Builder
	emit := func(cells [4]string) {
		for i, cell := range cells {
			if i > 0 {
				b.WriteString(" | ")
			}
			b.WriteString(cell)
			b.WriteString(strings.Repeat(" ", widths[i]-len([]rune(cell))))
		}
		b.WriteString("\n")
	}
	emit([4]string{"check", "status", "detail", "hint"})
	for i, w := range widths {
		if i > 0 {
			b.WriteString("-+-")
		}
		b.WriteString(strings.Repeat("-", w))
	}
	b.WriteString("\n")
	for _, row := range rows {
		emit(row)
	}
	return b.String()
}

// ---------------------------------------------------------------------------
// Injectable probes
// ---------------------------------------------------------------------------

// Edge is one sampled relationship (source, target, rel_type).
type Edge struct {
	Source  string
	Target  string
	RelType string
}

// Probes is everything the deep checks need from the deployment, as pure
// results. Tests inject canned values (stubProbes) so every failure mode
// is provable without a live backend.
type Probes interface {
	// PingMS performs one trivial timed round trip in milliseconds.
	PingMS() (int64, error)
	// AppliedMigrations lists the versions recorded in the ledger.
	AppliedMigrations() ([]int, error)
	// IndexedFiles lists the distinct file paths currently indexed.
	IndexedFiles() ([]string, error)
	// QualifiedNames lists every qualified_name, duplicates preserved
	// (duplicate detection depends on them).
	QualifiedNames() ([]string, error)
	// RelationshipEdges samples up to 1000 edges.
	RelationshipEdges() ([]Edge, error)
	// EmbeddedNames returns the qualified_names with embedding state and
	// whether the embedding tables exist (false = never built).
	EmbeddedNames() (names []string, present bool, err error)
	// EngineName reports the backend kind ("sqlite" | "postgres").
	EngineName() string
}

// dbProbes runs the probe queries over a read-only database handle.
// SQLite and PG probes differ only in how the handle is opened; the
// schema is the shared contract (both stores own the same table names).
type dbProbes struct {
	db     *sql.DB
	engine string
}

// openProbes opens the production probe handle for a project directory:
// engine "" or "sqlite" opens the project's leankg.db read-only
// (query_only ON; WAL readers never block a live writer); "postgres"
// connects to LEANKG_PG_URL's schema-per-project schema.
func openProbes(ctx context.Context, projectDir, engine, pgURL string) (Probes, error) {
	switch engine {
	case "", store.EngineSQLite:
		return sqliteProbes(projectDir)
	case store.EnginePostgres:
		return pgProbes(ctx, projectDir, pgURL)
	default:
		return nil, fmt.Errorf("doctor: unknown engine %q (want sqlite|postgres)", engine)
	}
}

func sqliteProbes(projectDir string) (*dbProbes, error) {
	dbPath := filepath.Join(projectDir, ".leankg", "leankg.db")
	if _, err := os.Stat(dbPath); err != nil {
		return nil, fmt.Errorf("doctor: no store at %s (run `leankg index` first): %w", dbPath, err)
	}
	dsn := "file:" + dbPath + "?mode=ro&_pragma=busy_timeout(10000)&_pragma=query_only(ON)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("doctor: open %s: %w", dbPath, err)
	}
	return &dbProbes{db: db, engine: store.EngineSQLite}, nil
}

// pgProbes connects to the Postgres from pgURL, pins search_path to the
// project's schema-per-project schema (leankg_<sha256(dir)[:16]> — the
// same derivation store.OpenPG uses), and sets the read-only transaction
// default so probes can never write.
func pgProbes(ctx context.Context, projectDir, pgURL string) (*dbProbes, error) {
	schema, err := pgSchemaForDir(projectDir)
	if err != nil {
		return nil, err
	}
	cfg, err := pgx.ParseConfig(pgURL)
	if err != nil {
		return nil, fmt.Errorf("doctor: parse pg dsn: %w", err)
	}
	cfg.RuntimeParams["default_transaction_read_only"] = "on"
	cfg.RuntimeParams["search_path"] = schema + ", public"
	db := stdlib.OpenDB(*cfg)
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("doctor: connect postgres: %w", err)
	}
	return &dbProbes{db: db, engine: store.EnginePostgres}, nil
}

// Close releases the probe handle.
func (p *dbProbes) Close() error { return p.db.Close() }

// EngineName reports the backend kind.
func (p *dbProbes) EngineName() string { return p.engine }

// PingMS times one trivial round trip after a warm-up, so lazy connect
// and TLS handshake never pollute the steady-state measurement (the Rust
// parity behavior: one untimed warm-up, one timed probe).
func (p *dbProbes) PingMS() (int64, error) {
	if _, err := p.db.Exec("SELECT 1"); err != nil {
		return 0, err // warm-up also failed: unreachable, timed or not
	}
	start := time.Now()
	if _, err := p.db.Exec("SELECT 1"); err != nil {
		return 0, err
	}
	return time.Since(start).Milliseconds(), nil
}

// AppliedMigrations reads the version ledger both engines share.
func (p *dbProbes) AppliedMigrations() ([]int, error) {
	rows, err := p.db.Query(`SELECT version FROM schema_migrations ORDER BY version`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []int
	for rows.Next() {
		var v int
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// IndexedFiles reads the distinct file paths of indexed elements.
func (p *dbProbes) IndexedFiles() ([]string, error) {
	return p.strings(`SELECT DISTINCT file_path FROM code_elements ORDER BY file_path`)
}

// QualifiedNames enumerates every qualified_name; duplicates preserved.
func (p *dbProbes) QualifiedNames() ([]string, error) {
	return p.strings(`SELECT qualified_name FROM code_elements ORDER BY qualified_name`)
}

// RelationshipEdges samples the orphan-scan window.
func (p *dbProbes) RelationshipEdges() ([]Edge, error) {
	rows, err := p.db.Query(
		`SELECT source_qualified, target_qualified, rel_type FROM relationships LIMIT ?`, orphanLimit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Edge
	for rows.Next() {
		var e Edge
		if err := rows.Scan(&e.Source, &e.Target, &e.RelType); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// EmbeddedNames reads embedding_state across models; present=false when
// the tables were never created.
func (p *dbProbes) EmbeddedNames() ([]string, bool, error) {
	names, err := p.strings(`SELECT DISTINCT qualified_name FROM embedding_state ORDER BY qualified_name`)
	if err != nil {
		if tableAbsent(err) {
			return nil, false, nil
		}
		return nil, true, err
	}
	return names, true, nil
}

// unreachableProbes substitutes for a dead backend handle: every probe
// fails with the same cause, so the DB-backed checks degrade to
// structured FAIL findings instead of aborting the report (a doctor run
// against an absent PG still tells you which checks could not run).
type unreachableProbes struct{ cause error }

func (u unreachableProbes) PingMS() (int64, error)                 { return 0, u.cause }
func (u unreachableProbes) AppliedMigrations() ([]int, error)      { return nil, u.cause }
func (u unreachableProbes) IndexedFiles() ([]string, error)        { return nil, u.cause }
func (u unreachableProbes) QualifiedNames() ([]string, error)      { return nil, u.cause }
func (u unreachableProbes) RelationshipEdges() ([]Edge, error)     { return nil, u.cause }
func (u unreachableProbes) EmbeddedNames() ([]string, bool, error) { return nil, false, u.cause }
func (u unreachableProbes) EngineName() string                     { return "unreachable" }

func (p *dbProbes) strings(query string) ([]string, error) {
	rows, err := p.db.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// tableAbsent distinguishes "the embedding tables were never created"
// from a genuine probe failure (Rust deep.rs table_absent heuristic).
func tableAbsent(err error) bool {
	lower := strings.ToLower(err.Error())
	return strings.Contains(lower, "does not exist") ||
		strings.Contains(lower, "no such table") ||
		strings.Contains(lower, "unknown relation") ||
		strings.Contains(lower, "not found in schema")
}

// pgSchemaForDir derives the per-project Postgres schema:
// leankg_ + first 16 hex chars of sha256(canonical absolute dir). This is
// the same derivation store.OpenPG applies (pgSchemaForDir there is
// unexported); identical algorithm + identical input keep the two names
// in sync.
// ponytail: duplicated from store_pg.go rather than exporting it there —
// a 10-line pure function; if the scheme ever changes, change both.
func pgSchemaForDir(projectDir string) (string, error) {
	abs, err := filepath.Abs(projectDir)
	if err != nil {
		return "", fmt.Errorf("doctor: resolve project dir: %w", err)
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		abs = resolved
	}
	h := sha256.Sum256([]byte(abs))
	return "leankg_" + hex.EncodeToString(h[:])[:16], nil
}

// PoolEnv is the parsed LEANKG_PG_POOL_SIZE / LEANKG_PG_POOL_WAIT_MS
// snapshot (nil = unset).
type PoolEnv struct {
	PoolSize   *string
	PoolWaitMS *string
}

// PoolEnvFromEnv reads the pool env vars.
func PoolEnvFromEnv() PoolEnv {
	pe := PoolEnv{}
	if v := os.Getenv("LEANKG_PG_POOL_SIZE"); v != "" {
		s := v
		pe.PoolSize = &s
	}
	if v := os.Getenv("LEANKG_PG_POOL_WAIT_MS"); v != "" {
		s := v
		pe.PoolWaitMS = &s
	}
	return pe
}

// Env is everything a check run needs beyond the probes.
type Env struct {
	ProjectRoot string // canonical absolute project dir
	LeankgDir   string // <root>/.leankg
	Pool        PoolEnv
	// DiskFiles lists the indexable files on disk, project-root-relative
	// slash paths (nil triggers the real walk; tests inject).
	DiskFiles []string
	// Registry is the language registry for the disk walk (nil = default).
	Registry *langs.Registry
}

// Check is one named diagnosis over the deployment.
type Check interface {
	Name() string
	Run(probes Probes, env Env) Finding
}

type checkFunc struct {
	name string
	run  func(Probes, Env) Finding
}

func (c checkFunc) Name() string                { return c.name }
func (c checkFunc) Run(p Probes, e Env) Finding { return c.run(p, e) }

// Defaults is the standard check registry, in Rust deep.rs order.
func Defaults() []Check {
	return []Check{
		checkFunc{"pg-latency", checkStoreLatency},
		checkFunc{"migrations", checkMigrations},
		checkFunc{"index-freshness", checkIndexFreshness},
		checkFunc{"embedding-coverage", checkEmbeddingCoverage},
		checkFunc{"pool-env", checkPoolEnv},
		checkFunc{"orphaned-relationships", checkOrphanedRelationships},
		checkFunc{"duplicate-names", checkDuplicateNames},
		checkFunc{"leankg-dir", checkLeankgDir},
	}
}

// RunAll executes the checks in registry order.
func RunAll(checks []Check, probes Probes, env Env) Report {
	r := Report{Findings: make([]Finding, 0, len(checks))}
	for _, c := range checks {
		r.Findings = append(r.Findings, c.Run(probes, env))
	}
	return r
}

// ---------------------------------------------------------------------------
// The eight checks
// ---------------------------------------------------------------------------

// checkStoreLatency: connectivity + round-trip latency against the
// store (PG when the engine is postgres; the SQLite file otherwise — the
// Rust check targeted the CozoDb handle the same way).
func checkStoreLatency(p Probes, _ Env) Finding {
	const check = "pg-latency"
	warn := int64(latencyWarnMS)
	if v := latencyWarnFromEnv(); v > warn {
		warn = v
	}
	ms, err := p.PingMS()
	switch {
	case err != nil:
		return Finding{check, StatusFail, fmt.Sprintf("unreachable: %v", err),
			"Verify LEANKG_PG_URL points at a running Postgres (or the sqlite store file " +
				"exists and is readable) and that network/firewall allows the connection " +
				"(`psql \"$LEANKG_PG_URL\" -c 'select 1'`)."}
	case ms <= warn:
		return Finding{check, StatusPass, fmt.Sprintf("%d ms round-trip", ms), ""}
	case ms <= latencyFailMS:
		return Finding{check, StatusWarn, fmt.Sprintf("round-trip %d ms (>%d ms)", ms, warn),
			"The store answers but slowly — check network distance to the DB host " +
				"(cross-region links routinely exceed this); prefer a co-located replica."}
	default:
		return Finding{check, StatusFail, fmt.Sprintf("round-trip %d ms (>%d ms)", ms, latencyFailMS),
			"Unusable latency — every query pays it. Move the workload next to the " +
				"database or fix the degraded network path."}
	}
}

// latencyWarnFromEnv honors LEANKG_DOCTOR_LATENCY_WARN_MS: only raises,
// clamped just under the fail tier so the Fail tier stays reachable.
func latencyWarnFromEnv() int64 {
	raw := strings.TrimSpace(os.Getenv("LEANKG_DOCTOR_LATENCY_WARN_MS"))
	if raw == "" {
		return latencyWarnMS
	}
	var v int64
	if _, err := fmt.Sscanf(raw, "%d", &v); err != nil || v < latencyWarnMS {
		return latencyWarnMS
	}
	if v > latencyFailMS-1 {
		v = latencyFailMS - 1
	}
	return v
}

// checkMigrations: applied ledger vs the embedded migration list.
func checkMigrations(p Probes, _ Env) Finding {
	const check = "migrations"
	applied, err := p.AppliedMigrations()
	if err != nil {
		return Finding{check, StatusFail, fmt.Sprintf("cannot read migration ledger: %v", err),
			"The schema_migrations table must exist after Migrate; verify connectivity " +
				"and that the project schema was initialized."}
	}
	appliedSet := make(map[int]bool, len(applied))
	for _, v := range applied {
		appliedSet[v] = true
	}
	var pending, unknown []string
	for _, m := range store.Migrations() {
		if !appliedSet[m.Version] {
			pending = append(pending, fmt.Sprintf("%03d %s", m.Version, m.Name))
		}
		delete(appliedSet, m.Version)
	}
	for v := range appliedSet {
		unknown = append(unknown, fmt.Sprintf("%03d", v))
	}
	sort.Strings(unknown)
	switch {
	case len(pending) > 0:
		return Finding{check, StatusFail,
			fmt.Sprintf("%d pending: %s", len(pending), strings.Join(pending, ", ")),
			"Run any writer command (e.g. `leankg index`) — migrations apply " +
				"automatically when a writer opens the schema."}
	case len(unknown) > 0:
		return Finding{check, StatusWarn,
			fmt.Sprintf("schema ahead of binary: %s", strings.Join(unknown, ", ")),
			"These migrations were applied by a newer leankg; upgrade this binary " +
				"to match the database schema."}
	default:
		return Finding{check, StatusPass,
			fmt.Sprintf("all %d embedded migrations applied", len(applied)), ""}
	}
}

// checkIndexFreshness: indexed files vs on-disk indexable files.
func checkIndexFreshness(p Probes, env Env) Finding {
	const check = "index-freshness"
	indexed, err := p.IndexedFiles()
	if err != nil {
		return Finding{check, StatusFail, fmt.Sprintf("cannot read indexed file list: %v", err),
			"code_elements should be readable after an index; verify connectivity and schema."}
	}
	disk := env.DiskFiles
	if disk == nil {
		disk, err = index.SupportedFiles(env.ProjectRoot, env.Registry)
		if err != nil {
			return Finding{check, StatusFail, fmt.Sprintf("cannot walk %s: %v", env.ProjectRoot, err),
				"Fix filesystem permissions on the project root, or pass --project with the correct path."}
		}
	}

	// Both sides in project-root-relative slash form. SQLite stores
	// relative paths; PG-era absolute spellings resolve against the root.
	indexedRel := make(map[string]bool, len(indexed))
	for _, raw := range indexed {
		indexedRel[relPath(env.ProjectRoot, raw)] = true
	}
	diskSet := make(map[string]bool, len(disk))
	for _, d := range disk {
		diskSet[d] = true
	}

	// Synthetic elements (ontology://, session://) have no filesystem
	// backing — never stale. A stale path is one missing from disk AND
	// missing from the filesystem under the root.
	var fsIndexed []string
	var stale []string
	for _, raw := range indexed {
		if strings.Contains(raw, "://") {
			continue
		}
		rel := relPath(env.ProjectRoot, raw)
		fsIndexed = append(fsIndexed, rel)
		if !diskSet[rel] && !fileExists(filepath.Join(env.ProjectRoot, rel)) {
			stale = append(stale, rel)
		}
	}
	missingCount := 0
	for _, d := range disk {
		if !indexedRel[d] {
			missingCount++
		}
	}

	if len(indexed) == 0 && len(disk) > 0 {
		return Finding{check, StatusFail,
			fmt.Sprintf("index is empty; %d supported file(s) on disk are unindexed", len(disk)),
			"Run `leankg index <path>` to build the index."}
	}
	stalePct := 0
	if len(fsIndexed) > 0 {
		stalePct = len(stale) * 100 / len(fsIndexed)
	}
	switch {
	case stalePct > 50:
		samples := stale
		if len(samples) > 3 {
			samples = samples[:3]
		}
		return Finding{check, StatusFail,
			fmt.Sprintf("%d/%d indexed paths (%d%%) no longer exist on disk; e.g. %s",
				len(stale), len(fsIndexed), stalePct, strings.Join(samples, ", ")),
			"The index points at a moved/rotated tree — re-index the current project root (`leankg index <path>`)."}
	case len(stale) == 0 && missingCount == 0:
		return Finding{check, StatusPass,
			fmt.Sprintf("%d indexed file(s), all present on disk (%d synthetic URI entries skipped)",
				len(fsIndexed), len(indexed)-len(fsIndexed)), ""}
	default:
		return Finding{check, StatusWarn,
			fmt.Sprintf("%d missing file(s) not indexed, %d stale (%d%%)", missingCount, len(stale), stalePct),
			"Run `leankg index` (or watch mode) to refresh; a large delta usually means " +
				"the index was built from a different root or env."}
	}
}

// relPath normalizes a stored file_path spelling to a project-root-
// relative slash path (the Rust deep.rs to_rel parity: strip the
// canonical root, then any absolute prefix, else keep the raw spelling).
func relPath(root, p string) string {
	p = filepath.ToSlash(p)
	if rel, err := filepath.Rel(root, p); err == nil && !strings.HasPrefix(rel, "..") {
		return filepath.ToSlash(rel)
	}
	return p
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// checkEmbeddingCoverage: embedding_state coverage over code_elements.
func checkEmbeddingCoverage(p Probes, _ Env) Finding {
	const check = "embedding-coverage"
	qns, err := p.QualifiedNames()
	if err != nil {
		return Finding{check, StatusFail, fmt.Sprintf("cannot read code_elements: %v", err),
			"Coverage is measured against indexed elements; verify connectivity and that an index exists."}
	}
	names, present, err := p.EmbeddedNames()
	switch {
	case err != nil:
		return Finding{check, StatusFail, fmt.Sprintf("cannot read embedding_state: %v", err),
			"Embedding tables are created by migrations; check permissions and schema state " +
				"(`leankg doctor --deep` migrations row)."}
	case !present:
		return Finding{check, StatusPass, "embedding tables absent — embeddings never built", ""}
	}
	unique := make(map[string]bool, len(qns))
	for _, q := range qns {
		unique[q] = true
	}
	switch {
	case len(unique) == 0:
		return Finding{check, StatusPass, "no elements indexed yet; nothing to embed", ""}
	case len(names) == 0:
		return Finding{check, StatusPass, fmt.Sprintf("0/%d embedded (embeddings never built)", len(unique)), ""}
	}
	embedded := make(map[string]bool, len(names))
	for _, n := range names {
		embedded[n] = true
	}
	covered := 0
	for q := range unique {
		if embedded[q] {
			covered++
		}
	}
	uncoveredPct := 100 - covered*100/len(unique)
	if uncoveredPct == 0 {
		return Finding{check, StatusPass, fmt.Sprintf("%d/%d elements embedded", covered, len(unique)), ""}
	}
	return Finding{check, StatusWarn, fmt.Sprintf("%d%% uncovered (%d/%d embedded)", uncoveredPct, covered, len(unique)),
		"Run `leankg embed` to build vectors for new/changed elements so semantic search stays complete."}
}

// checkPoolEnv: LEANKG_PG_POOL_SIZE / LEANKG_PG_POOL_WAIT_MS sanity.
func checkPoolEnv(_ Probes, env Env) Finding {
	const check = "pool-env"
	status := StatusPass
	var details, hints []string

	if env.Pool.PoolSize == nil {
		details = append(details, "pool size: default 5")
	} else {
		raw := *env.Pool.PoolSize
		trimmed := strings.TrimSpace(raw)
		var v int64
		if trimmed == "" || !isAllDigits(trimmed) {
			status = status.worst(StatusFail)
			details = append(details, fmt.Sprintf("invalid LEANKG_PG_POOL_SIZE=%q", raw))
			hints = append(hints, fmt.Sprintf("Set LEANKG_PG_POOL_SIZE to an integer between 1 and %d.", poolSizeMax))
		} else {
			fmt.Sscanf(trimmed, "%d", &v)
			switch {
			case v == 0:
				status = status.worst(StatusFail)
				details = append(details, fmt.Sprintf("invalid LEANKG_PG_POOL_SIZE=%q", raw))
				hints = append(hints, fmt.Sprintf("Set LEANKG_PG_POOL_SIZE to an integer between 1 and %d.", poolSizeMax))
			case v > poolSizeMax:
				status = status.worst(StatusWarn)
				details = append(details, fmt.Sprintf("pool size %d exceeds %d", v, poolSizeMax))
				hints = append(hints, fmt.Sprintf("Keep LEANKG_PG_POOL_SIZE within 1..=%d; oversubscribing "+
					"connections can exhaust Postgres max_connections.", poolSizeMax))
			default:
				details = append(details, fmt.Sprintf("pool size %d", v))
			}
		}
	}

	if env.Pool.PoolWaitMS == nil {
		details = append(details, "pool wait: default 10000 ms")
	} else {
		raw := *env.Pool.PoolWaitMS
		trimmed := strings.TrimSpace(raw)
		var v int64
		if trimmed == "" || !isAllDigits(trimmed) {
			status = status.worst(StatusFail)
			details = append(details, fmt.Sprintf("invalid LEANKG_PG_POOL_WAIT_MS=%q", raw))
			hints = append(hints, fmt.Sprintf("Set LEANKG_PG_POOL_WAIT_MS to an integer between 1 and %d (milliseconds).", poolWaitMaxMS))
		} else {
			fmt.Sscanf(trimmed, "%d", &v)
			switch {
			case v <= 0:
				status = status.worst(StatusFail)
				details = append(details, fmt.Sprintf("invalid LEANKG_PG_POOL_WAIT_MS=%q", raw))
				hints = append(hints, fmt.Sprintf("Set LEANKG_PG_POOL_WAIT_MS to an integer between 1 and %d (milliseconds).", poolWaitMaxMS))
			case v > poolWaitMaxMS:
				status = status.worst(StatusWarn)
				details = append(details, fmt.Sprintf("pool wait %d ms exceeds %d ms", v, poolWaitMaxMS))
				hints = append(hints, fmt.Sprintf("Waits beyond %d ms hide pool starvation instead of failing fast; "+
					"lower it or raise the pool size.", poolWaitMaxMS))
			default:
				details = append(details, fmt.Sprintf("pool wait %d ms", v))
			}
		}
	}

	return Finding{check, status, strings.Join(details, "; "), strings.Join(hints, " ")}
}

func isAllDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// checkOrphanedRelationships: edges whose endpoints no longer exist.
func checkOrphanedRelationships(p Probes, _ Env) Finding {
	const check = "orphaned-relationships"
	edges, err := p.RelationshipEdges()
	if err != nil {
		return Finding{check, StatusFail, fmt.Sprintf("cannot sample relationships: %v", err),
			"Verify connectivity; the relationships table is created by migrations."}
	}
	qns, err := p.QualifiedNames()
	if err != nil {
		return Finding{check, StatusFail, fmt.Sprintf("cannot read code_elements: %v", err),
			"Orphan detection needs the element set to compare against."}
	}
	live := make(map[string]bool, len(qns))
	for _, q := range qns {
		live[q] = true
	}
	var orphans []Edge
	for _, e := range edges {
		if !live[e.Source] || !live[e.Target] {
			orphans = append(orphans, e)
		}
	}
	if len(orphans) == 0 {
		return Finding{check, StatusPass, fmt.Sprintf("%d sampled edge(s) all resolve", len(edges)), ""}
	}
	first := orphans[0]
	return Finding{check, StatusFail,
		fmt.Sprintf("%d/%d sampled edges reference missing elements; e.g. %s: %s -> %s",
			len(orphans), len(edges), first.RelType, first.Source, first.Target),
		"Dangling edges break graph traversals — re-run `leankg index` to rebuild " +
			"(delete-then-insert), or purge leftovers with `leankg gc`."}
}

// checkDuplicateNames: duplicated qualified_names in code_elements.
func checkDuplicateNames(p Probes, _ Env) Finding {
	const check = "duplicate-names"
	qns, err := p.QualifiedNames()
	if err != nil {
		return Finding{check, StatusFail, fmt.Sprintf("cannot read code_elements: %v", err),
			"Duplicate detection needs the full qualified_name column; verify connectivity."}
	}
	counts := make(map[string]int, len(qns))
	for _, q := range qns {
		counts[q]++
	}
	type offender struct {
		name  string
		count int
	}
	var dupes []offender
	for name, c := range counts {
		if c > 1 {
			dupes = append(dupes, offender{name, c})
		}
	}
	if len(dupes) == 0 {
		return Finding{check, StatusPass, fmt.Sprintf("%d qualified name(s), no duplicates", len(qns)), ""}
	}
	// Worst offenders first, then alphabetical for stable output.
	sort.Slice(dupes, func(i, j int) bool {
		if dupes[i].count != dupes[j].count {
			return dupes[i].count > dupes[j].count
		}
		return dupes[i].name < dupes[j].name
	})
	if len(dupes) > duplicateLimit {
		dupes = dupes[:duplicateLimit]
	}
	parts := make([]string, 0, len(dupes))
	for _, d := range dupes {
		parts = append(parts, fmt.Sprintf("%s×%d", d.name, d.count))
	}
	return Finding{check, StatusFail,
		fmt.Sprintf("%d duplicated qualified_name(s); top: %s", len(dupes), strings.Join(parts, ", ")),
		"Duplicate rows double-count symbols in queries — a full `leankg index` wipes " +
			"and reinserts per env, clearing them."}
}

// checkLeankgDir: .leankg directory writability + stray lock files.
func checkLeankgDir(_ Probes, env Env) Finding {
	const check = "leankg-dir"
	dir := env.LeankgDir
	info, err := os.Stat(dir)
	switch {
	case os.IsNotExist(err):
		return Finding{check, StatusFail, fmt.Sprintf(".leankg not found at %s", dir),
			"Run `leankg index` in the project root to create it."}
	case err != nil:
		return Finding{check, StatusFail, fmt.Sprintf("cannot stat %s: %v", dir, err),
			"Fix filesystem permissions on the project root."}
	case !info.IsDir():
		return Finding{check, StatusFail, fmt.Sprintf(".leankg exists but is not a directory: %s", dir),
			"Remove the stray file and run `leankg index`."}
	}
	probePath := filepath.Join(dir, fmt.Sprintf(".doctor_probe_%d", os.Getpid()))
	if err := os.WriteFile(probePath, []byte("ok"), 0o644); err != nil {
		return Finding{check, StatusFail, fmt.Sprintf("not writable: %v", err),
			fmt.Sprintf("Fix ownership/permissions on %s so leankg can write state.", dir)}
	}
	_ = os.Remove(probePath)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return Finding{check, StatusFail, fmt.Sprintf("cannot list %s: %v", dir, err),
			"Fix filesystem permissions on the .leankg directory."}
	}
	var locks []string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".lock") {
			locks = append(locks, e.Name())
		}
	}
	if len(locks) == 0 {
		return Finding{check, StatusPass, "writable, no stray lock files", ""}
	}
	return Finding{check, StatusWarn, fmt.Sprintf("stray lock file(s): %s", strings.Join(locks, ", ")),
		"Locks are written by embed/watch runs; delete them if no such process is alive " +
			"(`cat <lock>` shows the owning PID)."}
}

// ---------------------------------------------------------------------------
// Run entry point
// ---------------------------------------------------------------------------

// RunDeep runs the full deep diagnosis for projectRoot against the
// configured engine (sqlite default; postgres needs pgURL). Every probe
// failure becomes a structured FAIL finding, never a panic. checks may be
// nil for the default registry (tests inject custom lists).
func RunDeep(ctx context.Context, projectRoot, engineName, pgURL string, checks []Check) (Report, error) {
	root, err := filepath.Abs(projectRoot)
	if err != nil {
		return Report{}, fmt.Errorf("doctor: resolve project root: %w", err)
	}
	if resolved, err := filepath.EvalSymlinks(root); err == nil {
		root = resolved
	}
	leankgDir := filepath.Join(root, ".leankg")
	if info, err := os.Stat(leankgDir); err != nil || !info.IsDir() {
		return Report{}, fmt.Errorf(".leankg not found under %s; run `leankg index` first", projectRoot)
	}
	if engineName == "" {
		engineName = os.Getenv("LEANKG_DB_ENGINE")
	}
	probes, perr := openProbes(ctx, root, engineName, pgURL)
	if perr != nil {
		// Graceful degradation: a doctor run against an absent/unreachable
		// backend still produces the structured report — the DB-backed
		// checks FAIL with the cause, the file checks still run.
		probes = unreachableProbes{perr}
	}
	// The probe handle closes when it has one (the unreachable stub does
	// not); a close failure never invalidates the report.
	defer func() {
		if c, ok := probes.(interface{ Close() error }); ok {
			_ = c.Close()
		}
	}()

	reg := langs.DefaultRegistry()
	if _, aerr := reg.Activate(root); aerr != nil {
		reg = nil // a language-detection failure must not kill the report
	}
	if checks == nil {
		checks = Defaults()
	}
	return RunAll(checks, probes, Env{
		ProjectRoot: root,
		LeankgDir:   leankgDir,
		Pool:        PoolEnvFromEnv(),
		Registry:    reg,
	}), nil
}
