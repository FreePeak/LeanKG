// Fleet check tests (issue #376, the #278 fleet remainder): migration drift,
// freshness and totals across registered projects, over the injectable
// FleetSource — the same seam the probe stubs use for the per-project checks.
package doctor

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/FreePeak/LeanKG/go/internal/portfolioreg"
	"github.com/FreePeak/LeanKG/go/internal/store"
)

// stubFleet is the canned registry: statuses exactly as a real run would read
// them, without a backend.
type stubFleet struct {
	rows []FleetStatus
	err  error
}

func (s stubFleet) Fleet() ([]FleetStatus, error) { return s.rows, s.err }

func fleetEnv(src FleetSource) Env { return Env{Fleet: src} }

// appliedAll is every version a fully-migrated project carries.
func appliedAll() []int {
	var out []int
	for _, m := range store.Migrations() {
		out = append(out, m.Version)
	}
	return out
}

func TestFleetCheckPassesWhenCurrent(t *testing.T) {
	f := checkFleet(nil, fleetEnv(stubFleet{rows: []FleetStatus{
		{Project: "api", Applied: appliedAll(), Elements: 100, Files: 10, Fresh: "fresh"},
		{Project: "web", Applied: appliedAll(), Elements: 50, Files: 5, Fresh: "fresh"},
	}}))
	if f.Status != StatusPass {
		t.Fatalf("fleet = %+v; want PASS for an all-current fleet", f)
	}
	// Totals ride the detail line: the operator sees the fleet size without a
	// second query.
	for _, want := range []string{"2 projects", "150 elements", "15 files"} {
		if !strings.Contains(f.Detail, want) {
			t.Fatalf("detail %q missing %q", f.Detail, want)
		}
	}
}

func TestFleetCheckDetectsMigrationDrift(t *testing.T) {
	behind := appliedAll()
	// A project one step behind: drop the last embedded version.
	behind = behind[:len(behind)-1]
	f := checkFleet(nil, fleetEnv(stubFleet{rows: []FleetStatus{
		{Project: "api", Applied: appliedAll(), Elements: 1, Files: 1, Fresh: "fresh"},
		{Project: "legacy", Applied: behind, Elements: 1, Files: 1, Fresh: "fresh"},
	}}))
	if f.Status != StatusFail {
		t.Fatalf("fleet = %+v; want FAIL when one project is behind", f)
	}
	// The finding names the project and the pending step.
	if !strings.Contains(f.Detail, "legacy behind by 1") {
		t.Fatalf("detail %q must name the drifted project and its gap", f.Detail)
	}
	if !strings.Contains(f.Detail, store.Migrations()[len(store.Migrations())-1].Name) {
		t.Fatalf("detail %q must name the pending migration", f.Detail)
	}
	// Exit-code contract: a fleet FAIL must drive doctor --deep to 2.
	if code := (Report{Findings: []Finding{f}}).ExitCode(); code != 2 {
		t.Fatalf("exit code = %d; want 2 on fleet FAIL", code)
	}
}

func TestFleetCheckFlagsUnreadableProjectsAsWarn(t *testing.T) {
	f := checkFleet(nil, fleetEnv(stubFleet{rows: []FleetStatus{
		{Project: "api", Applied: appliedAll(), Elements: 1, Files: 1, Fresh: "fresh"},
		{Project: "ghost", Unreadable: "read-only open of missing store", Elements: 0, Files: 0},
	}}))
	// An unreadable project is a WARN, not a FAIL: it is a fleet-composition
	// problem (a registration whose store left), not a data-integrity one,
	// and the hint tells the operator which of the two fixes applies.
	if f.Status != StatusWarn {
		t.Fatalf("fleet = %+v; want WARN for an unreadable project", f)
	}
	if !strings.Contains(f.Detail, "UNREADABLE") {
		t.Fatalf("detail %q must mark the unreadable project", f.Detail)
	}
	if code := (Report{Findings: []Finding{f}}).ExitCode(); code != 1 {
		t.Fatalf("exit code = %d; want 1 on fleet WARN", code)
	}
}

func TestFleetCheckFlagsStaleFreshness(t *testing.T) {
	f := checkFleet(nil, fleetEnv(stubFleet{rows: []FleetStatus{
		{Project: "api", Applied: appliedAll(), Elements: 1, Files: 1, Fresh: "possibly_stale"},
	}}))
	if f.Status != StatusWarn || !strings.Contains(f.Detail, "possibly_stale") {
		t.Fatalf("fleet = %+v; want WARN carrying the freshness label", f)
	}
}

func TestFleetCheckFlagsSchemaAhead(t *testing.T) {
	ahead := append(appliedAll(), 99)
	f := checkFleet(nil, fleetEnv(stubFleet{rows: []FleetStatus{
		{Project: "future", Applied: ahead, Elements: 1, Files: 1, Fresh: "fresh"},
	}}))
	if f.Status != StatusWarn || !strings.Contains(f.Detail, "ahead by 1") {
		t.Fatalf("fleet = %+v; want WARN naming the binary-older-than-schema case", f)
	}
}

func TestFleetCheckWithoutRegistry(t *testing.T) {
	// No registry at all: PASS, phrased as a single-project deployment — a
	// fleet feature must not make every single-repo doctor run report WARN.
	f := checkFleet(nil, fleetEnv(stubFleet{err: fmt.Errorf("%w: /missing.db", portfolioreg.ErrNoRegistry)}))
	if f.Status != StatusPass || !strings.Contains(f.Detail, "no portfolio registry") {
		t.Fatalf("fleet = %+v; want PASS for a missing registry", f)
	}
	// Not wired (Env.Fleet nil) is the same answer for a different reason.
	if f := checkFleet(nil, Env{}); f.Status != StatusPass {
		t.Fatalf("fleet = %+v; want PASS when no source is wired", f)
	}
	// Empty registry: PASS, zero projects.
	f = checkFleet(nil, fleetEnv(stubFleet{rows: nil}))
	if f.Status != StatusPass || !strings.Contains(f.Detail, "0 projects") {
		t.Fatalf("fleet = %+v; want PASS for an empty registry", f)
	}
}

func TestFleetCheckRegistryFailureFails(t *testing.T) {
	// A registry that EXISTS but cannot be read is a deployment fault: FAIL.
	f := checkFleet(nil, fleetEnv(stubFleet{err: errors.New("connection refused")}))
	if f.Status != StatusFail || !strings.Contains(f.Detail, "cannot read portfolio registry") {
		t.Fatalf("fleet = %+v; want FAIL naming the registry read failure", f)
	}
}

func TestFleetCheckRunsInDefaultRegistry(t *testing.T) {
	names := map[string]bool{}
	for _, c := range Defaults() {
		names[c.Name()] = true
		if c.Name() == "fleet" {
			// A defaults-listed check must survive a nil source: RunDeep
			// always wires one, but RunAll(nil env) must never panic.
			if f := c.Run(&stubProbes{}, Env{}); f.Status != StatusPass {
				t.Fatalf("fleet with nil source = %+v; want PASS", f)
			}
		}
	}
	if !names["fleet"] {
		t.Fatal("Defaults() must include the fleet check")
	}
}

func TestMigrationDriftVocabulary(t *testing.T) {
	pending, unknown := migrationDrift(
		[]store.MigrationStep{{Version: 1, Name: "index-layer"}, {Version: 2, Name: "fts"}},
		[]int{1, 7},
	)
	if len(pending) != 1 || pending[0] != "002 fts" {
		t.Fatalf("pending = %v; want [002 fts]", pending)
	}
	if len(unknown) != 1 || unknown[0] != "007" {
		t.Fatalf("unknown = %v; want [007]", unknown)
	}
}
