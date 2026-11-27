package ccmemory

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/FreePeak/LeanKG/internal/flock"
	"github.com/FreePeak/LeanKG/internal/memory"
	"github.com/FreePeak/LeanKG/internal/telemetry"
)

// sameKeys reports whether got and want hold the same keys in the same order.
// A nil slice and an empty slice compare equal.
func sameKeys(got, want []Key) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

func TestPlanUpsertsMissingAndChangedDeletesExtra(t *testing.T) {
	desired := map[Key]string{
		{Bank: "cc-global", DocID: "doc-a"}:   "hash-a",
		{Bank: "cc-proj-one", DocID: "doc-b"}: "hash-b-new",
		{Bank: "cc-proj-one", DocID: "doc-c"}: "hash-c",
	}
	actual := map[Key]string{
		{Bank: "cc-global", DocID: "doc-a"}:   "hash-a",
		{Bank: "cc-proj-one", DocID: "doc-b"}: "hash-b-old",
		{Bank: "cc-proj-one", DocID: "doc-d"}: "hash-d",
	}

	plan := BuildPlan(desired, actual, false)

	wantUpserts := []Key{
		{Bank: "cc-proj-one", DocID: "doc-b"}, // changed hash
		{Bank: "cc-proj-one", DocID: "doc-c"}, // missing
	}
	wantDeletes := []Key{
		{Bank: "cc-proj-one", DocID: "doc-d"}, // extra
	}
	if !sameKeys(plan.Upserts, wantUpserts) {
		t.Errorf("Upserts: got %v, want %v", plan.Upserts, wantUpserts)
	}
	if !sameKeys(plan.Deletes, wantDeletes) {
		t.Errorf("Deletes: got %v, want %v", plan.Deletes, wantDeletes)
	}
	if plan.DeletesWithheld {
		t.Errorf("DeletesWithheld: got true, want false")
	}
}

func TestPlanKindChangeIsDeleteInOneBankAndUpsertInAnother(t *testing.T) {
	// The memory was a feedback note in the global bank; it is now a
	// project-scoped note. The bank is part of the key, so the same document
	// id is a delete in the old bank and an upsert in the new one.
	actual := map[Key]string{
		{Bank: "cc-global", DocID: "doc-kind"}: "hash-1",
	}
	desired := map[Key]string{
		{Bank: "cc-proj-one", DocID: "doc-kind"}: "hash-1",
	}

	plan := BuildPlan(desired, actual, false)

	wantUpserts := []Key{{Bank: "cc-proj-one", DocID: "doc-kind"}}
	wantDeletes := []Key{{Bank: "cc-global", DocID: "doc-kind"}}
	if !sameKeys(plan.Upserts, wantUpserts) {
		t.Errorf("Upserts: got %v, want %v", plan.Upserts, wantUpserts)
	}
	if !sameKeys(plan.Deletes, wantDeletes) {
		t.Errorf("Deletes: got %v, want %v", plan.Deletes, wantDeletes)
	}
}

func TestPlanEqualSetsIsEmpty(t *testing.T) {
	set := map[Key]string{
		{Bank: "cc-global", DocID: "doc-a"}:   "hash-a",
		{Bank: "cc-proj-one", DocID: "doc-b"}: "hash-b",
	}

	plan := BuildPlan(set, set, false)

	if len(plan.Upserts) != 0 || len(plan.Deletes) != 0 || plan.DeletesWithheld {
		t.Errorf("equal sets: got %+v, want empty plan", plan)
	}
}

func TestPlanEmptyDesiredWithholdsDeletesWithoutRebuild(t *testing.T) {
	actual := map[Key]string{
		{Bank: "cc-global", DocID: "doc-a"}: "hash-a",
	}

	plan := BuildPlan(map[Key]string{}, actual, false)

	if len(plan.Deletes) != 0 {
		t.Errorf("Deletes: got %v, want none", plan.Deletes)
	}
	if len(plan.Upserts) != 0 {
		t.Errorf("Upserts: got %v, want none", plan.Upserts)
	}
	if !plan.DeletesWithheld {
		t.Errorf("DeletesWithheld: got false, want true")
	}
}

func TestPlanRebuildDeletesEveryActualAndUpsertsEveryDesired(t *testing.T) {
	desired := map[Key]string{
		{Bank: "cc-global", DocID: "doc-a"}: "hash-a",
	}
	actual := map[Key]string{
		{Bank: "cc-global", DocID: "doc-a"}:   "hash-a",
		{Bank: "cc-global", DocID: "doc-z"}:   "hash-z",
		{Bank: "cc-proj-one", DocID: "doc-b"}: "hash-b",
	}

	plan := BuildPlan(desired, actual, true)

	wantUpserts := []Key{{Bank: "cc-global", DocID: "doc-a"}}
	wantDeletes := []Key{
		{Bank: "cc-global", DocID: "doc-a"},
		{Bank: "cc-global", DocID: "doc-z"},
		{Bank: "cc-proj-one", DocID: "doc-b"},
	}
	if !sameKeys(plan.Upserts, wantUpserts) {
		t.Errorf("Upserts: got %v, want %v", plan.Upserts, wantUpserts)
	}
	if !sameKeys(plan.Deletes, wantDeletes) {
		t.Errorf("Deletes: got %v, want %v", plan.Deletes, wantDeletes)
	}
	if plan.DeletesWithheld {
		t.Errorf("DeletesWithheld: got true, want false under rebuild")
	}
}

func TestPlanRebuildWithEmptyDesiredDeletesAll(t *testing.T) {
	actual := map[Key]string{
		{Bank: "cc-global", DocID: "doc-a"}: "hash-a",
	}

	plan := BuildPlan(map[Key]string{}, actual, true)

	wantDeletes := []Key{{Bank: "cc-global", DocID: "doc-a"}}
	if !sameKeys(plan.Deletes, wantDeletes) {
		t.Errorf("Deletes: got %v, want %v", plan.Deletes, wantDeletes)
	}
	if plan.DeletesWithheld {
		t.Errorf("DeletesWithheld: got true, want false under rebuild")
	}
}

func TestPlanNeverDeletesOutsideCCBanks(t *testing.T) {
	foreign := Key{Bank: "mem-personal", DocID: "doc-x"}
	actual := map[Key]string{
		foreign:                             "hash-x",
		{Bank: "cc-global", DocID: "doc-a"}: "hash-a",
	}
	desired := map[Key]string{
		{Bank: "cc-global", DocID: "doc-a"}: "hash-a",
	}

	// Foreign rows are not part of the actual set, so an equal cc- set
	// plans nothing and the foreign row is never touched.
	if plan := BuildPlan(desired, actual, false); len(plan.Deletes) != 0 || len(plan.Upserts) != 0 {
		t.Errorf("non-cc actual row leaked into plan: %+v", plan)
	}

	// Rebuild deletes every cc- row but must not name the foreign row.
	plan := BuildPlan(map[Key]string{}, actual, true)
	wantDeletes := []Key{{Bank: "cc-global", DocID: "doc-a"}}
	if !sameKeys(plan.Deletes, wantDeletes) {
		t.Errorf("rebuild Deletes: got %v, want %v", plan.Deletes, wantDeletes)
	}

	// A foreign-only actual set is not a deletion candidate, so an empty
	// desired set has nothing to withhold.
	onlyForeign := map[Key]string{foreign: "hash-x"}
	plan = BuildPlan(map[Key]string{}, onlyForeign, false)
	if len(plan.Deletes) != 0 || plan.DeletesWithheld {
		t.Errorf("foreign-only actual: got %+v, want no deletes and no withheld flag", plan)
	}
}

func TestPlanIsSortedByBankThenDocID(t *testing.T) {
	desired := map[Key]string{
		{Bank: "cc-proj-two", DocID: "doc-a"}: "h",
		{Bank: "cc-global", DocID: "doc-c"}:   "h",
		{Bank: "cc-global", DocID: "doc-b"}:   "h",
	}

	plan := BuildPlan(desired, map[Key]string{}, false)

	wantUpserts := []Key{
		{Bank: "cc-global", DocID: "doc-b"},
		{Bank: "cc-global", DocID: "doc-c"},
		{Bank: "cc-proj-two", DocID: "doc-a"},
	}
	if !sameKeys(plan.Upserts, wantUpserts) {
		t.Errorf("Upserts order: got %v, want %v", plan.Upserts, wantUpserts)
	}
}

// applySetup isolates one sync test. HOME and CLAUDE_CONFIG_DIR point at temp
// dirs, the process-wide deny-list and recorder are reset now and again when
// the test ends, and the fixture projects root and a real memory root (opened
// with memory.Open in a temp dir) are returned.
func applySetup(t *testing.T) (projects string, mem *memory.Memory) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	absent := filepath.Join(t.TempDir(), "no-scrub.txt")
	resetProcessState := func() {
		memory.SetRecorder(nil)
		_, digest, err := memory.LoadScrubFile(absent)
		if err != nil {
			t.Fatalf("LoadScrubFile: %v", err)
		}
		memory.SetScrubPatterns(nil, digest)
	}
	resetProcessState()
	t.Cleanup(resetProcessState)

	mem, err := memory.Open(context.Background(), t.TempDir(), false)
	if err != nil {
		t.Fatalf("memory.Open: %v", err)
	}
	t.Cleanup(func() { _ = mem.Close() })
	return buildFixture(t), mem
}

func syncOpts(projects string, mem *memory.Memory) SyncOptions {
	return SyncOptions{Projects: projects, CentralRoot: mem.Root()}
}

func runSync(t *testing.T, mem *memory.Memory, opts SyncOptions) SyncResult {
	t.Helper()
	res, err := Sync(context.Background(), mem, opts)
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	return res
}

// countingRecorder counts memory events. Level is Metadata so the memory layer
// builds and sends events.
type countingRecorder struct{ memEvents atomic.Int64 }

func (r *countingRecorder) Level() telemetry.Level             { return telemetry.Metadata }
func (r *countingRecorder) SetLevel(telemetry.Level) error     { return nil }
func (r *countingRecorder) RecordCall(telemetry.CallEvent)     {}
func (r *countingRecorder) RecordMemory(telemetry.MemoryEvent) { r.memEvents.Add(1) }
func (r *countingRecorder) Close() error                       { return nil }

func installRecorder(t *testing.T) *countingRecorder {
	t.Helper()
	rec := &countingRecorder{}
	memory.SetRecorder(rec)
	return rec
}

// bankRows returns the rows of one bank keyed by their metadata document_id.
func bankRows(t *testing.T, mem *memory.Memory, bank string) map[string]memory.Entry {
	t.Helper()
	rows, _, err := mem.List(bank, 0, 0)
	if err != nil {
		t.Fatalf("List(%s): %v", bank, err)
	}
	out := make(map[string]memory.Entry, len(rows))
	for _, r := range rows {
		id, _ := r.Metadata["document_id"].(string)
		out[id] = r
	}
	return out
}

// strList reads a metadata list. Rows come back from JSON, so lists are []any.
func strList(v any) []string {
	items, _ := v.([]any)
	out := make([]string, 0, len(items))
	for _, it := range items {
		s, _ := it.(string)
		out = append(out, s)
	}
	return out
}

// wantRow is the mapped content one row must carry (plan 6.4 row mapping).
type wantRow struct {
	file, kind, name, description, project string
	links                                  []string
	timestamp                              int64
}

func checkRow(t *testing.T, got memory.Entry, want wantRow) {
	t.Helper()
	id := DocID(want.project, want.file)
	if got.ID != id {
		t.Errorf("%s: ID = %q, want %q", want.file, got.ID, id)
	}
	if got.Source != "claude-code-memory" {
		t.Errorf("%s: Source = %q, want claude-code-memory", want.file, got.Source)
	}
	if got.Timestamp != want.timestamp {
		t.Errorf("%s: Timestamp = %d, want %d", want.file, got.Timestamp, want.timestamp)
	}
	header := fmt.Sprintf("[%s] %s - %s\n", want.kind, want.name, want.description)
	if !strings.HasPrefix(got.Content, header) {
		t.Errorf("%s: Content starts %q, want prefix %q", want.file, firstLineOf(got.Content), header)
	}
	meta := map[string]string{"document_id": id, "kind": want.kind, "name": want.name,
		"description": want.description, "file": want.file, "project": want.project}
	for k, v := range meta {
		if got.Metadata[k] != v {
			t.Errorf("%s: Metadata[%s] = %v, want %q", want.file, k, got.Metadata[k], v)
		}
	}
	if tags := strList(got.Metadata["tags"]); !slices.Equal(tags, []string{"cc", "type:" + want.kind}) {
		t.Errorf("%s: tags = %v, want [cc type:%s]", want.file, tags, want.kind)
	}
	if links := strList(got.Metadata["links"]); !slices.Equal(links, want.links) {
		t.Errorf("%s: links = %v, want %v", want.file, links, want.links)
	}
	if h, _ := got.Metadata["source_hash"].(string); len(h) != 64 {
		t.Errorf("%s: source_hash = %q, want 64 hex characters", want.file, h)
	}
}

func firstLineOf(s string) string {
	line, _, _ := strings.Cut(s, "\n")
	return line
}

// fileState is one file's bytes and modification time.
type fileState struct {
	data []byte
	mod  time.Time
}

func snapshotDir(t *testing.T, dir string) map[string]fileState {
	t.Helper()
	out := map[string]fileState{}
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		fi, err := d.Info()
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, path)
		out[rel] = fileState{data: data, mod: fi.ModTime()}
		return nil
	})
	if err != nil {
		t.Fatalf("snapshot %s: %v", dir, err)
	}
	return out
}

func memFile(projects, slug, file string) string {
	return filepath.Join(projects, slug, "memory", file)
}

func TestApplyFirstRunWritesMappedRows(t *testing.T) {
	projects, mem := applySetup(t)

	res := runSync(t, mem, syncOpts(projects, mem))

	if res.Documents != 6 || res.Upserted != 6 || res.Deleted != 0 || res.Skipped != 3 || res.DeletesWithheld {
		t.Fatalf("counts = %+v, want documents 6, upserted 6, deleted 0, skipped 3, not withheld", res)
	}
	if res.Root != mem.Root() || res.Projects != projects {
		t.Errorf("Root/Projects = %q/%q, want %q/%q", res.Root, res.Projects, mem.Root(), projects)
	}

	global := bankRows(t, mem, GlobalBank)
	if len(global) != 2 {
		t.Fatalf("cc-global rows = %d, want 2", len(global))
	}
	checkRow(t, global[DocID("-work-alpha", "prefer-tabs.md")], wantRow{
		file: "prefer-tabs.md", kind: "feedback", name: "Prefer tabs", description: "Use tabs for indentation.",
		project: "-work-alpha", links: []string{"style-guide", "prefer-tabs"}, timestamp: fixedMTime})
	checkRow(t, global[DocID("-work-alpha", "user-role.md")], wantRow{
		file: "user-role.md", kind: "user", name: "user-role", description: "First line here.",
		project: "-work-alpha", timestamp: fixedMTime})

	beta := bankRows(t, mem, ProjectBank("-work-beta"))
	if len(beta) != 3 {
		t.Fatalf("beta project bank rows = %d, want 3", len(beta))
	}
	checkRow(t, beta[DocID("-work-beta", "api-ref.md")], wantRow{
		file: "api-ref.md", kind: "reference", name: "api-ref", description: "Reference notes for the API.",
		project: "-work-beta", links: []string{"service-map"}, timestamp: fixedMTime})
	checkRow(t, beta[DocID("-work-beta", "deploy-window.md")], wantRow{
		file: "deploy-window.md", kind: "project", name: "Deploy window", description: "Freeze on Fridays.",
		project: "-work-beta", links: []string{"release-process"}, timestamp: fixedMTime})
	checkRow(t, beta[DocID("-work-beta", "notes-plain.md")], wantRow{
		file: "notes-plain.md", kind: "unknown", name: "notes-plain", description: "Plain note first line.",
		project: "-work-beta", timestamp: fixedMTime})

	gamma := bankRows(t, mem, ProjectBank("-work-gamma"))
	if len(gamma) != 1 {
		t.Fatalf("gamma project bank rows = %d, want 1", len(gamma))
	}
	checkRow(t, gamma[DocID("-work-gamma", "ci-quirk.md")], wantRow{
		file: "ci-quirk.md", kind: "project", name: "CI quirk", description: "Cache resets nightly.",
		project: "-work-gamma", timestamp: fixedMTime})

	// alpha has only global memories, so it gets no project bank.
	if n := len(bankRows(t, mem, ProjectBank("-work-alpha"))); n != 0 {
		t.Errorf("alpha project bank rows = %d, want 0", n)
	}
	if len(res.Banks) != 3 {
		t.Errorf("Banks = %+v, want 3 entries", res.Banks)
	}
}

func TestApplySecondRunIsNoOp(t *testing.T) {
	projects, mem := applySetup(t)
	rec := installRecorder(t)
	runSync(t, mem, syncOpts(projects, mem))
	if rec.memEvents.Load() == 0 {
		t.Fatal("first run emitted no memory events; the counting recorder is not wired")
	}
	banks := filepath.Join(mem.Root(), "banks")
	before := snapshotDir(t, banks)

	rec.memEvents.Store(0)
	res := runSync(t, mem, syncOpts(projects, mem))

	if res.Upserted != 0 || res.Deleted != 0 || res.DeletesWithheld {
		t.Errorf("second run = %+v, want nothing planned", res)
	}
	after := snapshotDir(t, banks)
	if len(after) != len(before) {
		t.Errorf("bank file count changed: %d -> %d", len(before), len(after))
	}
	for rel, b := range before {
		a, ok := after[rel]
		if !ok || !bytes.Equal(a.data, b.data) || !a.mod.Equal(b.mod) {
			t.Errorf("bank file %s changed on a no-op run", rel)
		}
	}
	if n := rec.memEvents.Load(); n != 0 {
		t.Errorf("second run emitted %d memory events, want 0", n)
	}
}

func TestApplyEditingOneFileRewritesOneRow(t *testing.T) {
	projects, mem := applySetup(t)
	runSync(t, mem, syncOpts(projects, mem))
	before := bankRows(t, mem, GlobalBank)
	tabsHash := before[DocID("-work-alpha", "prefer-tabs.md")].Metadata["source_hash"]

	writeFile(t, memFile(projects, "-work-alpha", "user-role.md"), "---\ntype: user\n---\nEdited line here.\n")
	res := runSync(t, mem, syncOpts(projects, mem))

	if res.Upserted != 1 || res.Deleted != 0 {
		t.Fatalf("counts = %+v, want upserted 1, deleted 0", res)
	}
	after := bankRows(t, mem, GlobalBank)
	if len(after) != 2 {
		t.Fatalf("cc-global rows = %d, want 2", len(after))
	}
	if got := after[DocID("-work-alpha", "user-role.md")].Content; !strings.Contains(got, "Edited line here.") {
		t.Errorf("edited row Content = %q, want the new body", got)
	}
	if got := after[DocID("-work-alpha", "prefer-tabs.md")].Metadata["source_hash"]; got != tabsHash {
		t.Errorf("unchanged row source_hash moved: %v -> %v", tabsHash, got)
	}
}

func TestApplyRemovingOneFileRemovesOneRow(t *testing.T) {
	projects, mem := applySetup(t)
	runSync(t, mem, syncOpts(projects, mem))

	if err := os.Remove(memFile(projects, "-work-beta", "deploy-window.md")); err != nil {
		t.Fatal(err)
	}
	res := runSync(t, mem, syncOpts(projects, mem))

	if res.Upserted != 0 || res.Deleted != 1 || res.DeletesWithheld {
		t.Fatalf("counts = %+v, want upserted 0, deleted 1, not withheld", res)
	}
	beta := bankRows(t, mem, ProjectBank("-work-beta"))
	if len(beta) != 2 {
		t.Fatalf("beta rows = %d, want 2", len(beta))
	}
	if _, ok := beta[DocID("-work-beta", "deploy-window.md")]; ok {
		t.Error("removed file still has a row")
	}
}

func TestApplyDenyListChangeRewritesEveryRow(t *testing.T) {
	projects, mem := applySetup(t)
	runSync(t, mem, syncOpts(projects, mem))

	// The pattern is written as Fr[e]eze so the file itself does not contain
	// the word it masks.
	writeFile(t, filepath.Join(mem.Root(), "scrub.txt"), "# masked terms\nFr[e]eze\n")
	res := runSync(t, mem, syncOpts(projects, mem))

	if res.Upserted != 6 || res.Deleted != 0 {
		t.Fatalf("counts = %+v, want every one of 6 rows rewritten", res)
	}
	for _, bank := range []string{GlobalBank, ProjectBank("-work-beta"), ProjectBank("-work-gamma")} {
		for id, row := range bankRows(t, mem, bank) {
			if strings.Contains(row.Content, "Freeze") {
				t.Errorf("row %s in %s still holds the masked term: %q", id, bank, row.Content)
			}
		}
	}
	if again := runSync(t, mem, syncOpts(projects, mem)); again.Upserted != 0 {
		t.Errorf("run after the deny-list rewrite planned %d upserts, want 0", again.Upserted)
	}
}

func TestApplyPlantedSecretAbsentFromCentralRoot(t *testing.T) {
	projects, mem := applySetup(t)
	// A fake key built at runtime, shaped like a provider key so the fixed
	// redaction rule masks it. It is not a credential.
	secret := "sk" + "-" + "PLACEHOLDERKEY" + "0123456789abcdef"
	source := memFile(projects, "-work-alpha", "user-role.md")
	writeFile(t, source, "---\ntype: user\n---\nFirst line here.\nkey "+secret+"\n")
	writeFile(t, filepath.Join(mem.Root(), "scrub.txt"), "Fr[e]eze\n")
	writeFile(t, memFile(projects, "-work-beta", "deploy-window.md"),
		"---\nname: Deploy window\ndescription: Freeze on Fridays.\nmetadata:\n  type: project\n---\nFreeze all deploys.\n")

	runSync(t, mem, syncOpts(projects, mem))
	if err := mem.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	if raw, _ := os.ReadFile(source); !bytes.Contains(raw, []byte(secret)) {
		t.Fatal("test setup: the planted secret is not in the source file")
	}
	row := bankRows(t, mem, GlobalBank)[DocID("-work-alpha", "user-role.md")]
	if !strings.Contains(row.Content, "[REDACTED:key]") {
		t.Errorf("stored row was not redacted: %q", row.Content)
	}
	for path, state := range snapshotDir(t, mem.Root()) {
		if bytes.Contains(state.data, []byte(secret)) {
			t.Errorf("planted secret found in %s", path)
		}
		if bytes.Contains(state.data, []byte("Freeze")) {
			t.Errorf("deny-listed word found in %s", path)
		}
	}
}

func TestApplyDenyListedTextAbsentFromMetadataFields(t *testing.T) {
	projects, mem := applySetup(t)
	// The pattern is written with a bracket so this file does not contain the
	// word it masks.
	const term = "SERVICE_DNS_PLACEHOLDER"
	writeFile(t, filepath.Join(mem.Root(), "scrub.txt"), "SERVICE_DNS_PLACE[H]OLDER\n")
	source := memFile(projects, "-work-alpha", term+"-notes.md")
	writeFile(t, source, "---\nname: "+term+" name\ndescription: "+term+" description\n---\n"+
		"Body text. See [["+term+"-runbook]].\n")

	runSync(t, mem, syncOpts(projects, mem))
	if again := runSync(t, mem, syncOpts(projects, mem)); again.Upserted != 0 {
		t.Errorf("second run planned %d upserts, want 0", again.Upserted)
	}
	if err := mem.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	for path, state := range snapshotDir(t, mem.Root()) {
		if bytes.Contains(state.data, []byte(term)) {
			t.Errorf("deny-listed term found in %s", path)
		}
	}
}

func TestApplyScopedRunOnDenyListedSlugIsNoOp(t *testing.T) {
	projects, mem := applySetup(t)
	const term = "SERVICE_DNS_PLACEHOLDER"
	writeFile(t, filepath.Join(mem.Root(), "scrub.txt"), "SERVICE_DNS_PLACE[H]OLDER\n")
	writeFile(t, memFile(projects, "-work-"+term, "a.md"), "---\nname: a\ndescription: b\n---\nBody.\n")

	runSync(t, mem, syncOpts(projects, mem))
	opts := syncOpts(projects, mem)
	opts.OnlySlug = "-work-" + term
	if res := runSync(t, mem, opts); res.Upserted != 0 || res.Deleted != 0 {
		t.Errorf("scoped run on a deny-listed slug: upserted %d deleted %d, want 0 and 0", res.Upserted, res.Deleted)
	}
}

func TestApplyEmptyProjectsRootWithholdsDeletes(t *testing.T) {
	projects, mem := applySetup(t)
	runSync(t, mem, syncOpts(projects, mem))

	for name, dir := range map[string]string{
		"missing root": filepath.Join(t.TempDir(), "gone"),
		"empty root":   t.TempDir(),
	} {
		res := runSync(t, mem, SyncOptions{Projects: dir, CentralRoot: mem.Root()})
		if !res.DeletesWithheld || res.Deleted != 0 || res.Upserted != 0 {
			t.Errorf("%s: %+v, want deletes withheld and nothing changed", name, res)
		}
		if n := len(bankRows(t, mem, GlobalBank)); n != 2 {
			t.Errorf("%s: cc-global rows = %d, want 2 kept", name, n)
		}
	}

	res := runSync(t, mem, SyncOptions{Projects: t.TempDir(), CentralRoot: mem.Root(), Rebuild: true})
	if res.DeletesWithheld || res.Deleted != 6 {
		t.Fatalf("rebuild over empty root = %+v, want deleted 6 and not withheld", res)
	}
	if n := len(bankRows(t, mem, GlobalBank)); n != 0 {
		t.Errorf("rebuild left %d cc-global rows, want 0", n)
	}
}

func TestApplyRebuildRemovesAndRebuildsEveryRow(t *testing.T) {
	projects, mem := applySetup(t)
	runSync(t, mem, syncOpts(projects, mem))

	res := runSync(t, mem, SyncOptions{Projects: projects, CentralRoot: mem.Root(), Rebuild: true})

	if res.Deleted != 6 || res.Upserted != 6 {
		t.Fatalf("rebuild counts = %+v, want deleted 6 and upserted 6", res)
	}
	if n := len(bankRows(t, mem, GlobalBank)); n != 2 {
		t.Errorf("cc-global rows after rebuild = %d, want 2", n)
	}
	if n := len(bankRows(t, mem, ProjectBank("-work-beta"))); n != 3 {
		t.Errorf("beta rows after rebuild = %d, want 3", n)
	}
}

func TestApplyKindChangeMovesRowBetweenBanks(t *testing.T) {
	projects, mem := applySetup(t)
	runSync(t, mem, syncOpts(projects, mem))

	writeFile(t, memFile(projects, "-work-beta", "api-ref.md"),
		"---\ntype: feedback\n---\nReference notes for the API.\n")
	res := runSync(t, mem, syncOpts(projects, mem))

	if res.Upserted != 1 || res.Deleted != 1 {
		t.Fatalf("counts = %+v, want upserted 1 and deleted 1", res)
	}
	id := DocID("-work-beta", "api-ref.md")
	if row, ok := bankRows(t, mem, GlobalBank)[id]; !ok || row.Metadata["kind"] != "feedback" {
		t.Errorf("api-ref not upserted into cc-global as feedback: %+v", row.Metadata)
	}
	if _, ok := bankRows(t, mem, ProjectBank("-work-beta"))[id]; ok {
		t.Error("api-ref still in the beta project bank after its kind changed")
	}
}

func TestApplyLeavesForeignBanksAlone(t *testing.T) {
	projects, mem := applySetup(t)
	foreign := memory.Entry{ID: "keep", Content: "personal note", Source: "test",
		Metadata: map[string]any{"document_id": "keep"}}
	if err := mem.RetainRaw(context.Background(), "mem-personal", []memory.Entry{foreign}); err != nil {
		t.Fatalf("RetainRaw: %v", err)
	}

	runSync(t, mem, SyncOptions{Projects: projects, CentralRoot: mem.Root(), Rebuild: true})
	runSync(t, mem, SyncOptions{Projects: t.TempDir(), CentralRoot: mem.Root(), Rebuild: true})

	if rows := bankRows(t, mem, "mem-personal"); len(rows) != 1 || rows["keep"].Content != "personal note" {
		t.Errorf("foreign bank changed: %+v", rows)
	}
}

func TestApplyDryRunWritesNothing(t *testing.T) {
	projects, mem := applySetup(t)
	opts := syncOpts(projects, mem)
	opts.DryRun = true

	res := runSync(t, mem, opts)

	if res.Upserted != 6 || res.Documents != 6 {
		t.Fatalf("dry-run plan = %+v, want 6 documents and 6 upserts", res)
	}
	banks, err := mem.Banks()
	if err != nil {
		t.Fatal(err)
	}
	if len(banks) != 0 {
		t.Errorf("dry run wrote banks: %v", banks)
	}
}

func TestApplyDryRunLeavesCentralTreeUnchanged(t *testing.T) {
	projects, mem := applySetup(t)
	before := snapshotDir(t, mem.Root())
	opts := syncOpts(projects, mem)
	opts.DryRun = true

	runSync(t, mem, opts)

	if after := snapshotDir(t, mem.Root()); !reflect.DeepEqual(before, after) {
		t.Fatalf("dry run changed the central root: %d files before, %d after", len(before), len(after))
	}
	if _, err := os.Stat(filepath.Join(mem.Root(), syncLockFile)); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("dry run left %s behind (stat err = %v)", syncLockFile, err)
	}
}

func TestApplyLockHeldWithoutWaitReturnsErrLockHeld(t *testing.T) {
	projects, mem := applySetup(t)
	release, err := flock.TryLock(filepath.Join(mem.Root(), syncLockFile))
	if err != nil {
		t.Fatalf("TryLock: %v", err)
	}

	_, err = Sync(context.Background(), mem, syncOpts(projects, mem))
	if !errors.Is(err, ErrLockHeld) {
		t.Fatalf("Sync error = %v, want ErrLockHeld", err)
	}
	if banks, _ := mem.Banks(); len(banks) != 0 {
		t.Errorf("a locked run wrote banks: %v", banks)
	}

	release()
	runSync(t, mem, syncOpts(projects, mem))
}

func TestApplyBadDenyListLineWritesNothing(t *testing.T) {
	projects, mem := applySetup(t)
	rec := installRecorder(t)
	writeFile(t, filepath.Join(mem.Root(), "scrub.txt"), "Fr[e]eze\n(unclosed\n")

	_, err := Sync(context.Background(), mem, syncOpts(projects, mem))

	if err == nil || errors.Is(err, ErrLockHeld) {
		t.Fatalf("Sync error = %v, want a deny-list parse error", err)
	}
	if !strings.Contains(err.Error(), "line 2") {
		t.Errorf("error %q does not name line 2", err)
	}
	if banks, _ := mem.Banks(); len(banks) != 0 {
		t.Errorf("a bad deny-list wrote banks: %v", banks)
	}
	if n := rec.memEvents.Load(); n != 0 {
		t.Errorf("a bad deny-list emitted %d memory events", n)
	}
}

func TestApplyCancelledContextWritesNothing(t *testing.T) {
	projects, mem := applySetup(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := Sync(ctx, mem, syncOpts(projects, mem))

	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Sync error = %v, want context.Canceled", err)
	}
	if banks, _ := mem.Banks(); len(banks) != 0 {
		t.Errorf("a cancelled run wrote banks: %v", banks)
	}
}

// TestApplyPostRunCompactionLeavesForeignBankAlone runs the steps a real memory
// verb runs: sync, migrate, then the post-run compaction limited to the central
// prefix. A bank outside that prefix must stay byte-identical.
func TestApplyPostRunCompactionLeavesForeignBankAlone(t *testing.T) {
	projects, mem := applySetup(t)
	foreign := filepath.Join(mem.Root(), "banks", "other-bank.jsonl")
	if err := os.MkdirAll(filepath.Dir(foreign), 0o755); err != nil {
		t.Fatal(err)
	}
	home := os.Getenv("HOME")
	before := `{"id":"own-1","content":"team wiki index","timestamp":1}` + "\n" +
		`{"id":"own-2","content":"team wiki index","timestamp":2}` + "\n" +
		`{"id":"own-3","content":"notes live in ` + home + `/notes","timestamp":3}` + "\n"
	if err := os.WriteFile(foreign, []byte(before), 0o644); err != nil {
		t.Fatal(err)
	}

	runSync(t, mem, syncOpts(projects, mem))
	if res := runMigrate(t, mem, migrateOpts(projects, mem)); !res.Verified {
		t.Fatalf("Migrate did not verify: %+v", res.Mismatches)
	}
	if err := mem.CompactPrefix(context.Background(), CentralBankPrefix); err != nil {
		t.Fatalf("CompactPrefix: %v", err)
	}

	after, err := os.ReadFile(foreign)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != before {
		t.Fatalf("post-run compaction rewrote a foreign bank:\nbefore: %q\nafter:  %q", before, after)
	}
}

// ccRowCount counts the rows of every cc- bank.
func ccRowCount(t *testing.T, mem *memory.Memory) int {
	t.Helper()
	banks, err := mem.Banks()
	if err != nil {
		t.Fatalf("Banks: %v", err)
	}
	n := 0
	for _, bank := range banks {
		if strings.HasPrefix(bank, CentralBankPrefix) {
			n += len(bankRows(t, mem, bank))
		}
	}
	return n
}

// TestApplyUnreadableProjectKeepsItsRows makes one project's memory directory
// unreadable after a sync. Its rows are not in the desired set, but the scan
// failed, so they must survive the next sync and migrate must not verify.
func TestApplyUnreadableProjectKeepsItsRows(t *testing.T) {
	projects, mem := applySetup(t)
	runSync(t, mem, syncOpts(projects, mem))
	before := ccRowCount(t, mem)
	restore := makeUnreadable(t, memoryDirOf(projects, "-work-beta"))

	res := runSync(t, mem, syncOpts(projects, mem))
	if res.Deleted != 0 || !res.DeletesWithheld {
		t.Fatalf("sync with an unreadable project: deleted %d withheld %v, want 0 and true", res.Deleted, res.DeletesWithheld)
	}
	if !slices.Contains(res.FailedProjects, "-work-beta") {
		t.Errorf("FailedProjects = %v, want -work-beta", res.FailedProjects)
	}
	if after := ccRowCount(t, mem); after != before {
		t.Errorf("rows after sync = %d, want %d kept", after, before)
	}

	mig := runMigrate(t, mem, migrateOpts(projects, mem))
	if mig.Verified {
		t.Fatal("migrate verified with a project whose scan failed")
	}
	if !slices.ContainsFunc(mig.Mismatches, func(m Mismatch) bool {
		return m.Bank == ProjectBank("-work-beta") && m.Reason == mismatchScanFailed
	}) {
		t.Errorf("mismatches = %v, want a scan_failed entry for -work-beta", mig.Mismatches)
	}
	if after := ccRowCount(t, mem); after != before {
		t.Errorf("migrate rows = %d, want %d kept", after, before)
	}

	restore()
	if res := runSync(t, mem, syncOpts(projects, mem)); res.DeletesWithheld || res.Deleted != 0 || len(res.FailedProjects) != 0 {
		t.Errorf("sync after restoring permissions: %+v, want a clean run", res)
	}
	if mig := runMigrate(t, mem, migrateOpts(projects, mem)); !mig.Verified {
		t.Errorf("migrate after restoring permissions: verified=false mismatches %v", mig.Mismatches)
	}
}

// TestApplyCompactsCentralUnderSyncLock proves the post-apply compaction runs
// while the sync lock is held: compaction rewrites the bank files, so a locked
// sync that appended in between would lose rows. The seam records whether the
// lock is held when compaction runs. No sleeps are involved.
func TestApplyCompactsCentralUnderSyncLock(t *testing.T) {
	projects, mem := applySetup(t)
	lockPath := filepath.Join(mem.Root(), syncLockFile)
	prev := compactCentral
	calls, lockHeld := 0, false
	compactCentral = func(ctx context.Context, mem *memory.Memory) error {
		calls++
		release, err := flock.TryLock(lockPath)
		switch {
		case errors.Is(err, flock.ErrHeld):
			lockHeld = true
		case err == nil:
			release()
		default:
			return err
		}
		return prev(ctx, mem)
	}
	t.Cleanup(func() { compactCentral = prev })

	runSync(t, mem, syncOpts(projects, mem))
	if calls != 1 || !lockHeld {
		t.Fatalf("sync: compaction calls = %d, lock held during compaction = %v; want 1 and true", calls, lockHeld)
	}

	dry := syncOpts(projects, mem)
	dry.DryRun = true
	runSync(t, mem, dry)
	if calls != 1 {
		t.Errorf("dry run compacted: calls = %d, want 1", calls)
	}

	runMigrate(t, mem, migrateOpts(projects, mem))
	if calls != 2 || !lockHeld {
		t.Errorf("migrate: compaction calls = %d, lock held = %v; want 2 and true", calls, lockHeld)
	}

	release, err := flock.TryLock(lockPath)
	if err != nil {
		t.Fatalf("sync lock still held after Migrate: %v", err)
	}
	release()
}
