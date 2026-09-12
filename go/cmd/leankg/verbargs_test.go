package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/FreePeak/LeanKG/go/internal/setupcfg"
	"github.com/FreePeak/LeanKG/go/internal/store"
)

// cliCase is one CLI-level assertion: the argv, the working directory, the exit
// code, and the substrings the captured streams must carry.
type cliCase struct {
	name     string
	dir      string // "" inherits the test process's cwd
	args     []string
	wantCode int
	wantOut  []string
	wantErr  []string
	check    func(t *testing.T)
}

// TestVerbFlagsAfterPositionals is the regression guard for the silent-drop
// defect class. Go's flag package stops at the first positional, so
// `verb <positional> --flag` (the documented, clap-accepted order) used to drop
// the flag and every token after it — `refresh . --full` ran incrementally and
// `env-conflicts w2 --env production` reported a confident empty-service
// result. A positional the verb does not accept must now fail loudly.
func TestVerbFlagsAfterPositionals(t *testing.T) {
	t.Setenv("LEANKG_DB_ENGINE", "")
	t.Setenv("LEANKG_PG_URL", "")
	t.Setenv("LEANKG_PROJECT", "")
	// `refresh --full` only reaches embed.Run — where the mode is recorded —
	// when a provider is available; the deterministic one is offline.
	t.Setenv("LEANKG_EMBED_PROVIDER", "deterministic")

	els, rels := graphFixture()
	seed := seedCLIProject(t, els, rels)
	refreshDir := t.TempDir()
	writeGoFile(t, refreshDir)
	runDir := t.TempDir()
	outDir := t.TempDir()

	cases := []cliCase{
		// refresh: the flag governs the embed mode, so it is observable in the
		// store the binary itself wrote.
		{
			name:     "refresh applies trailing --full",
			dir:      refreshDir,
			args:     []string{"refresh", ".", "--full"},
			wantCode: 0,
			wantOut:  []string{"Refresh complete."},
			check:    func(t *testing.T) { requireEmbedMode(t, refreshDir, "full") },
		},
		{
			name:     "refresh without the flag stays incremental",
			dir:      refreshDir,
			args:     []string{"refresh", "."},
			wantCode: 0,
			check:    func(t *testing.T) { requireEmbedMode(t, refreshDir, "incremental") },
		},
		{
			name:     "refresh rejects a leftover positional",
			dir:      refreshDir,
			args:     []string{"refresh", ".", "extra"},
			wantCode: 2,
			wantErr:  []string{`refresh: unexpected argument "extra"`},
		},

		// prd-trace [FEATURE_ID] [--project DIR]
		{
			name:     "prd-trace parses its trailing --project",
			args:     []string{"prd-trace", "FR-ZCP-13", "--project", seed},
			wantCode: 0,
			wantOut:  []string{"[]"},
		},
		{
			// A dropped --project would silently answer from the cwd's store and
			// exit 0; resolving the named (missing) store instead proves the flag
			// was parsed.
			name:     "prd-trace trailing --project selects the store",
			args:     []string{"prd-trace", "FR-ZCP-13", "--project", filepath.Join(outDir, "no-such-store")},
			wantCode: 1,
			wantErr:  []string{"no-such-store"},
		},
		{
			name:     "prd-trace rejects a leftover positional",
			args:     []string{"prd-trace", "FR-ZCP-13", "extra", "--project", seed},
			wantCode: 2,
			wantErr:  []string{`prd-trace: unexpected argument "extra"`},
		},

		// env-conflicts --service NAME [--project DIR]
		{
			name:     "env-conflicts reads the --service store",
			args:     []string{"env-conflicts", "--service", "pkg.Alpha", "--project", seed},
			wantCode: 0,
			wantOut:  []string{"No environment conflicts"},
		},
		{
			name:     "env-conflicts rejects the repro's stray positional",
			args:     []string{"env-conflicts", "w2", "--env", "production", "--project", seed},
			wantCode: 2,
			wantErr:  []string{`env-conflicts: unexpected argument "w2"`},
		},

		// service-context --service NAME [--env E] [--project DIR]
		{
			name:     "service-context applies its trailing flags",
			args:     []string{"service-context", "--service", "pkg.Alpha", "--env", "staging", "--project", seed},
			wantCode: 0,
			wantOut:  []string{`"service": "pkg.Alpha"`, `"env": "staging"`},
		},
		{
			name:     "service-context rejects a leftover positional",
			args:     []string{"service-context", "stray", "--project", seed},
			wantCode: 2,
			wantErr:  []string{`service-context: unexpected argument "stray"`},
		},

		// note --target T --content C [--project DIR]
		{
			name:     "note applies its trailing flags",
			args:     []string{"note", "--target", "pkg.Alpha", "--content", "hello team", "--project", seed},
			wantCode: 0,
			wantOut:  []string{"Added note to 'pkg.Alpha'", "hello team"},
		},
		{
			name:     "note rejects a leftover positional",
			args:     []string{"note", "--target", "pkg.Alpha", "--content", "hello team", "stray", "--project", seed},
			wantCode: 2,
			wantErr:  []string{`note: unexpected argument "stray"`},
		},

		// incident show <id> [--project DIR]
		{
			name:     "incident show parses its trailing --project",
			args:     []string{"incident", "show", "INC-404", "--project", seed},
			wantCode: 0,
			wantOut:  []string{"Incident 'INC-404' not found"},
		},
		{
			name:     "incident show rejects a leftover positional",
			args:     []string{"incident", "show", "INC-404", "extra", "--project", seed},
			wantCode: 2,
			wantErr:  []string{`incident show: unexpected argument "extra"`},
		},

		// metrics --project DIR --json
		{
			name:     "metrics keeps its documented flag form",
			args:     []string{"metrics", "--project", seed, "--json"},
			wantCode: 0,
			wantOut:  []string{`"retention_days"`},
		},
		{
			name:     "metrics rejects a leftover positional",
			args:     []string{"metrics", "--project", seed, "stray"},
			wantCode: 2,
			wantErr:  []string{`metrics: unexpected argument "stray"`},
		},

		// export [--format F] [--output FILE] [--project DIR]
		{
			name:     "export applies its trailing flags",
			dir:      outDir,
			args:     []string{"export", "--project", seed, "--format", "mermaid", "--output", "graph.mmd"},
			wantCode: 0,
			wantOut:  []string{"Exported 4 nodes and 4 edges to graph.mmd"},
			check:    func(t *testing.T) { requireFile(t, filepath.Join(outDir, "graph.mmd")) },
		},
		{
			name:     "export rejects a leftover positional",
			args:     []string{"export", "--project", seed, "--format", "json", "stray", "--output", filepath.Join(outDir, "g.json")},
			wantCode: 2,
			wantErr:  []string{`export: unexpected argument "stray"`},
		},

		// annotate <element> --description TEXT [--project DIR]
		{
			name:     "annotate applies its trailing --description",
			args:     []string{"annotate", "pkg.Alpha", "--description", "business rule", "--project", seed},
			wantCode: 0,
			wantOut:  []string{"Created annotation for 'pkg.Alpha'"},
			check: func(t *testing.T) {
				stdout, stderr, code := runCLI(t, "show-annotations", "pkg.Alpha", "--project", seed)
				if code != 0 || !strings.Contains(stdout, "business rule") {
					t.Fatalf("annotation did not persist (exit %d):\nstdout: %s\nstderr: %s", code, stdout, stderr)
				}
			},
		},
		{
			name:     "annotate rejects a leftover positional",
			args:     []string{"annotate", "pkg.Alpha", "extra", "--description", "x", "--project", seed},
			wantCode: 2,
			wantErr:  []string{`annotate: unexpected argument "extra"`},
		},

		// detect-clusters [--path DIR]
		{
			name:     "detect-clusters keeps its documented flag form",
			args:     []string{"detect-clusters", "--path", seed},
			wantCode: 0,
			wantOut:  []string{`"clusters"`},
		},
		{
			name:     "detect-clusters rejects a leftover positional",
			args:     []string{"detect-clusters", "stray", "--path", seed},
			wantCode: 2,
			wantErr:  []string{`detect-clusters: unexpected argument "stray"`},
		},

		// setup [--reset] [--clone] [--index] [--embed] [--status]
		{
			name:     "setup rejects a leftover positional",
			dir:      runDir,
			args:     []string{"setup", "stray"},
			wantCode: 2,
			wantErr:  []string{`setup: unexpected argument "stray"`},
		},

		// run is trailing-var-arg: the child's argv — flags included — must reach
		// the child untouched, which is why it deliberately does not use the
		// interspersed walk.
		{
			name:     "run passes child flags through",
			dir:      runDir,
			args:     []string{"run", "--", "echo", "-n", "hi"},
			wantCode: 0,
			wantOut:  []string{"hi"},
		},
		{
			name:     "run rejects an empty command",
			dir:      runDir,
			args:     []string{"run", "--compress"},
			wantCode: 2,
			wantErr:  []string{"no command provided"},
		},

		// install/connect/writer: the leftover guard fires before these verbs
		// touch $HOME, so the assertion stays side-effect free.
		{
			name:     "install rejects a leftover positional",
			dir:      runDir,
			args:     []string{"install", "stray", "--target", "claude-code"},
			wantCode: 2,
			wantErr:  []string{`install: unexpected argument "stray"`},
		},
		{
			name:     "writer rejects a leftover positional",
			dir:      runDir,
			args:     []string{"writer", "stray"},
			wantCode: 2,
			wantErr:  []string{`writer: unexpected argument "stray"`},
		},
		{
			name:     "connect rejects a second positional",
			dir:      runDir,
			args:     []string{"connect", "claude-code", "extra"},
			wantCode: 2,
			wantErr:  []string{`connect: unexpected argument "extra"`},
		},
		{
			name:     "connect requires exactly one client",
			dir:      runDir,
			args:     []string{"connect"},
			wantCode: 1,
			wantErr:  []string{"requires exactly one client"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stdout, stderr, code := runCLIIn(t, tc.dir, tc.args...)
			if code != tc.wantCode {
				t.Fatalf("exit = %d, want %d\nstdout: %s\nstderr: %s", code, tc.wantCode, stdout, stderr)
			}
			for _, want := range tc.wantOut {
				if !strings.Contains(stdout, want) {
					t.Fatalf("stdout missing %q:\n%s", want, stdout)
				}
			}
			for _, want := range tc.wantErr {
				if !strings.Contains(stderr, want) {
					t.Fatalf("stderr missing %q:\n%s", want, stderr)
				}
			}
			if tc.check != nil {
				tc.check(t)
			}
		})
	}
}

// TestFRZCP13SetupModePersistence is the acceptance test the FR-ZCP-13 row
// claimed done without one: exactly one auto/manual question per user, stored
// at <project>/.leankg/config.json, reused silently, overridable with --auto
// (reported as source=flag), and clearable with `setup --reset` so the next
// registration asks again.
func TestFRZCP13SetupModePersistence(t *testing.T) {
	t.Setenv("LEANKG_DB_ENGINE", "")
	t.Setenv("LEANKG_PG_URL", "")
	t.Setenv("LEANKG_SETUP_MODE", "")
	proj := t.TempDir()
	writeGoFile(t, proj)
	cfgPath := filepath.Join(proj, ".leankg", "config.json")

	// (a) Non-interactive first registration resolves to the manual default,
	// reports where the choice came from, and persists it.
	_, stderr, code := runCLIIn(t, proj, "index", proj)
	if code != 0 {
		t.Fatalf("first index exit %d: %s", code, stderr)
	}
	if !strings.Contains(stderr, "manual_default") {
		t.Fatalf("first index must report the manual-default provenance:\n%s", stderr)
	}
	if got := readSetupMode(t, cfgPath); got != setupcfg.ModeManual {
		t.Fatalf("stored setup mode = %q, want %q", got, setupcfg.ModeManual)
	}

	// (b) The stored choice is reused: no re-ask, no prompt.
	stdout, stderr, code := runCLIIn(t, proj, "index", proj)
	if code != 0 {
		t.Fatalf("second index exit %d: %s", code, stderr)
	}
	if strings.Contains(stderr, "setup mode:") || strings.Contains(stdout, "First-run setup") {
		t.Fatalf("stored choice must not re-ask:\nstdout: %s\nstderr: %s", stdout, stderr)
	}
	if got := readSetupMode(t, cfgPath); got != setupcfg.ModeManual {
		t.Fatalf("stored setup mode = %q after reuse, want %q", got, setupcfg.ModeManual)
	}

	// (c) The flag wins over the stored value and is reported as its source.
	_, stderr, code = runCLIIn(t, proj, "index", "--auto", proj)
	if code != 0 {
		t.Fatalf("index --auto exit %d: %s", code, stderr)
	}
	if !strings.Contains(stderr, "setup mode: auto (flag)") {
		t.Fatalf("index --auto must report source=flag:\n%s", stderr)
	}
	if got := readSetupMode(t, cfgPath); got != setupcfg.ModeAuto {
		t.Fatalf("stored setup mode = %q after --auto, want %q", got, setupcfg.ModeAuto)
	}

	// (d) --reset clears the choice, keeping the rest of the config.
	stdout, stderr, code = runCLIIn(t, proj, "setup", "--reset")
	if code != 0 || !strings.Contains(stdout, "Cleared stored setup choice") {
		t.Fatalf("setup --reset exit %d:\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}
	cfg, outcome := setupcfg.Load(proj)
	if outcome != setupcfg.OutcomeFound {
		t.Fatalf("after --reset: outcome = %v, want the config still present", outcome)
	}
	if cfg.Setup != nil {
		t.Fatalf("after --reset: setup = %q, want the choice cleared", *cfg.Setup)
	}
}

// TestSetupStatusNamesEmptyResolution pins the report contract: a status run
// that resolves nothing must say so and name the knobs, exit 0 (it is a report,
// not an error), and leave no .leankg of its own behind.
func TestSetupStatusNamesEmptyResolution(t *testing.T) {
	t.Setenv("LEANKG_WORKSPACE_DIR", "")
	t.Setenv("LEANKG_PROJECT_DIRS", "")
	t.Setenv("LEANKG_REPOS", "")
	dir := t.TempDir()

	stdout, stderr, code := runCLIIn(t, dir, "setup", "--status")
	if code != 0 {
		t.Fatalf("setup --status exit %d:\nstderr: %s", code, stderr)
	}
	for _, want := range []string{"No repositories resolved.", "LEANKG_WORKSPACE_DIR", "LEANKG_PROJECT_DIRS", "LEANKG_REPOS"} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("status report missing %q:\n%s", want, stdout)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, ".leankg")); !os.IsNotExist(err) {
		t.Fatalf("setup --status must not create %s (stat err = %v)", filepath.Join(dir, ".leankg"), err)
	}
}

// writeGoFile gives a fixture project one indexable source file.
func writeGoFile(t *testing.T, dir string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "p.go"), []byte("package p\n\nfunc A() int { return 1 }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// readSetupMode reads the persisted first-run choice directly from the config
// document (the on-disk artifact the contract names, not the loader's view).
func readSetupMode(t *testing.T, cfgPath string) setupcfg.SetupMode {
	t.Helper()
	raw, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("read %s: %v", cfgPath, err)
	}
	var doc struct {
		Setup *string `json:"setup"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("parse %s: %v", cfgPath, err)
	}
	if doc.Setup == nil {
		return ""
	}
	return setupcfg.SetupMode(*doc.Setup)
}

// requireEmbedMode asserts the mode of the newest embed run recorded in the
// project store — the observable effect of `refresh --full`.
func requireEmbedMode(t *testing.T, dir, want string) {
	t.Helper()
	st, err := store.Open(filepath.Join(dir, ".leankg", "leankg.db"), store.RO)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()
	run, err := st.LastEmbedRunAny()
	if err != nil {
		t.Fatalf("last embed run: %v", err)
	}
	if run == nil {
		t.Fatal("no embed run recorded")
	}
	if run.Mode != want {
		t.Fatalf("embed run mode = %q, want %q", run.Mode, want)
	}
}

func requireFile(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("expected %s on disk: %v", path, err)
	}
}
