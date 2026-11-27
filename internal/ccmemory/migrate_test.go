package ccmemory

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/FreePeak/LeanKG/internal/memory"
)

// wantFixtureProjects is the inventory of buildFixture: alpha holds two global
// memories; beta holds three documents and two skipped files; gamma holds one
// document and one skipped file.
func wantFixtureProjects() []ProjectInventory {
	return []ProjectInventory{
		{Slug: "-work-alpha", Bank: ProjectBank("-work-alpha"), Files: 2, Global: 2},
		{Slug: "-work-beta", Bank: ProjectBank("-work-beta"), Files: 3, Skipped: 2},
		{Slug: "-work-gamma", Bank: ProjectBank("-work-gamma"), Files: 1, Skipped: 1},
	}
}

func migrateOpts(projects string, mem *memory.Memory) SyncOptions {
	return SyncOptions{Projects: projects, CentralRoot: mem.Root()}
}

func runMigrate(t *testing.T, mem *memory.Memory, opts SyncOptions) MigrateResult {
	t.Helper()
	res, err := Migrate(context.Background(), mem, opts)
	if err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	return res
}

func TestMigrateDryRunPrintsInventoryAndWritesNothing(t *testing.T) {
	projects, mem := applySetup(t)
	before := snapshotDir(t, mem.Root())
	opts := migrateOpts(projects, mem)
	opts.DryRun = true

	res := runMigrate(t, mem, opts)

	if !reflect.DeepEqual(res.Projects, wantFixtureProjects()) {
		t.Errorf("inventory = %+v, want %+v", res.Projects, wantFixtureProjects())
	}
	if res.Documents != 6 || res.Skipped != 3 || res.Upserted != 6 || res.Deleted != 0 {
		t.Errorf("plan = docs %d skipped %d upserted %d deleted %d, want 6 3 6 0",
			res.Documents, res.Skipped, res.Upserted, res.Deleted)
	}
	if res.Verified {
		t.Error("dry run reported verified; nothing was applied")
	}
	if after := snapshotDir(t, mem.Root()); !reflect.DeepEqual(before, after) {
		t.Fatalf("dry run changed the central root: %d files before, %d after", len(before), len(after))
	}
}

func TestMigrateRealRunRowsEqualFilesPerBank(t *testing.T) {
	projects, mem := applySetup(t)

	res := runMigrate(t, mem, migrateOpts(projects, mem))

	if !res.Verified || len(res.Mismatches) != 0 || res.Upserted != 6 {
		t.Fatalf("result = verified %v, mismatches %v, upserted %d; want verified, none, 6",
			res.Verified, res.Mismatches, res.Upserted)
	}
	want := map[string]int{
		GlobalBank:                 2,
		ProjectBank("-work-alpha"): 0,
		ProjectBank("-work-beta"):  3,
		ProjectBank("-work-gamma"): 1,
	}
	for bank, n := range want {
		if got := len(bankRows(t, mem, bank)); got != n {
			t.Errorf("bank %s rows = %d, want %d", bank, got, n)
		}
	}
}

// snapshotDurable is snapshotDir without the SQLite write-ahead log and shared
// memory files. The checkpoint that compaction runs rewrites those, but they
// hold no state of their own: index.db and the bank files carry it.
func snapshotDurable(t *testing.T, dir string) map[string]fileState {
	t.Helper()
	out := snapshotDir(t, dir)
	for name := range out {
		if strings.HasSuffix(name, "-wal") || strings.HasSuffix(name, "-shm") {
			delete(out, name)
		}
	}
	return out
}

func TestMigrateSecondRunChangesNothing(t *testing.T) {
	projects, mem := applySetup(t)
	runMigrate(t, mem, migrateOpts(projects, mem))
	before := snapshotDurable(t, mem.Root())

	res := runMigrate(t, mem, migrateOpts(projects, mem))

	if res.Upserted != 0 || res.Deleted != 0 || !res.Verified {
		t.Errorf("second run = upserted %d deleted %d verified %v, want 0 0 true", res.Upserted, res.Deleted, res.Verified)
	}
	if after := snapshotDurable(t, mem.Root()); !reflect.DeepEqual(before, after) {
		t.Fatalf("second run changed the central root")
	}
}

func TestMigrateLeavesSourceTreeUnchanged(t *testing.T) {
	projects, mem := applySetup(t)
	before := snapshotDir(t, projects)

	runMigrate(t, mem, migrateOpts(projects, mem))

	if after := snapshotDir(t, projects); !reflect.DeepEqual(before, after) {
		t.Fatal("migrate changed the Claude Code projects tree")
	}
}

func TestMigrateDenyListMasksCentralRoot(t *testing.T) {
	projects, mem := applySetup(t)
	writeFile(t, filepath.Join(mem.Root(), scrubFile), "Fr[e]eze\n")
	writeFile(t, memFile(projects, "-work-beta", "deploy-window.md"),
		"---\nname: Deploy window\ndescription: Freeze on Fridays.\nmetadata:\n  type: project\n---\nFreeze all deploys.\n")

	res := runMigrate(t, mem, migrateOpts(projects, mem))

	if !res.Verified {
		t.Fatalf("verified = false with mismatches %v", res.Mismatches)
	}
	for path, state := range snapshotDir(t, mem.Root()) {
		if bytes.Contains(state.data, []byte("Freeze")) {
			t.Errorf("deny-listed word found in %s", path)
		}
	}
}

func TestMigrateOnlySlugScopesInventoryAndDeletesNothingElse(t *testing.T) {
	projects, mem := applySetup(t)
	ctx := context.Background()
	runMigrate(t, mem, migrateOpts(projects, mem))

	// A slug whose source files are gone, with rows in its project bank and in
	// the global bank.
	plant := func(bank string, d Doc) {
		t.Helper()
		if _, err := mem.RetainReplacing(ctx, bank, []memory.Entry{rowFor(d, "ghost-hash")}, nil); err != nil {
			t.Fatal(err)
		}
	}
	plant(ProjectBank("-work-ghost"), Doc{Slug: "-work-ghost", File: "old.md", Name: "old", Description: "gone", Kind: KindProject, Body: "gone"})
	plant(GlobalBank, Doc{Slug: "-work-ghost", File: "rule.md", Name: "rule", Description: "gone", Kind: KindFeedback, Body: "gone"})
	// Inside the scope: beta's reference memory is gone, so its row is stale.
	if err := os.Remove(memFile(projects, "-work-beta", "api-ref.md")); err != nil {
		t.Fatal(err)
	}
	// Outside the scope: gamma's only memory is gone too.
	if err := os.Remove(memFile(projects, "-work-gamma", "ci-quirk.md")); err != nil {
		t.Fatal(err)
	}

	opts := migrateOpts(projects, mem)
	opts.OnlySlug = "-work-beta"
	res := runMigrate(t, mem, opts)

	if !res.Verified || res.Deleted != 1 || res.Documents != 2 || res.Skipped != 2 {
		t.Fatalf("beta-scoped run = verified %v deleted %d documents %d skipped %d; want true 1 2 2",
			res.Verified, res.Deleted, res.Documents, res.Skipped)
	}
	// beta's api-ref memory is gone, so its inventory is two files, two skipped.
	if want := []ProjectInventory{{Slug: "-work-beta", Bank: ProjectBank("-work-beta"), Files: 2, Skipped: 2}}; !reflect.DeepEqual(res.Projects, want) {
		t.Errorf("scoped inventory = %+v, want %+v", res.Projects, want)
	}
	if _, ok := bankRows(t, mem, ProjectBank("-work-beta"))[DocID("-work-beta", "api-ref.md")]; ok {
		t.Error("beta's stale row survived a beta-scoped run")
	}
	untouched := map[string]string{
		GlobalBank:                 DocID("-work-alpha", "prefer-tabs.md"),
		ProjectBank("-work-gamma"): DocID("-work-gamma", "ci-quirk.md"),
		ProjectBank("-work-ghost"): DocID("-work-ghost", "old.md"),
	}
	for bank, id := range untouched {
		if _, ok := bankRows(t, mem, bank)[id]; !ok {
			t.Errorf("row %s in %s was deleted by a beta-scoped run", id, bank)
		}
	}
	if _, ok := bankRows(t, mem, GlobalBank)[DocID("-work-ghost", "rule.md")]; !ok {
		t.Error("ghost's global row was deleted by a beta-scoped run")
	}
}

func TestMigrateUnknownSlugIsAnErrorAndWritesNothing(t *testing.T) {
	projects, mem := applySetup(t)
	before := snapshotDir(t, mem.Root())
	opts := migrateOpts(projects, mem)
	opts.OnlySlug = "-work-typo"

	_, err := Migrate(context.Background(), mem, opts)

	if err == nil || !strings.Contains(err.Error(), "no memory found for project -work-typo") {
		t.Fatalf("Migrate(unknown slug) error = %v, want no memory found for project -work-typo", err)
	}
	if after := snapshotDir(t, mem.Root()); !reflect.DeepEqual(before, after) {
		t.Fatal("unknown-slug migrate changed the central root")
	}
}

func TestMigrateVerifyRowsReportsMissingExtraAndHashMismatch(t *testing.T) {
	a := Key{Bank: "cc-global", DocID: "doc-a"}
	b := Key{Bank: "cc-p-x", DocID: "doc-b"}
	c := Key{Bank: "cc-p-x", DocID: "doc-c"}
	d := Key{Bank: "cc-p-y", DocID: "doc-d"}
	cases := []struct {
		name            string
		desired, actual map[Key]string
		want            []Mismatch
	}{
		{"equal sets verify", map[Key]string{a: "h1", b: "h2"}, map[Key]string{a: "h1", b: "h2"}, []Mismatch{}},
		{"empty sets verify", map[Key]string{}, map[Key]string{}, []Mismatch{}},
		{"missing row", map[Key]string{a: "h1", b: "h2"}, map[Key]string{a: "h1"},
			[]Mismatch{{Bank: b.Bank, DocID: b.DocID, Reason: "missing"}}},
		{"extra row", map[Key]string{a: "h1"}, map[Key]string{a: "h1", d: "h4"},
			[]Mismatch{{Bank: d.Bank, DocID: d.DocID, Reason: "extra"}}},
		{"hash differs", map[Key]string{b: "h2"}, map[Key]string{b: "old"},
			[]Mismatch{{Bank: b.Bank, DocID: b.DocID, Reason: "hash"}}},
		{"all kinds, sorted by bank then document",
			map[Key]string{a: "h1", b: "h2", c: "h3"}, map[Key]string{a: "x", b: "h2", d: "h4"},
			[]Mismatch{
				{Bank: a.Bank, DocID: a.DocID, Reason: "hash"},
				{Bank: c.Bank, DocID: c.DocID, Reason: "missing"},
				{Bank: d.Bank, DocID: d.DocID, Reason: "extra"},
			}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := VerifyRows(tc.desired, tc.actual); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("VerifyRows = %#v, want %#v", got, tc.want)
			}
		})
	}
}
