package ccmemory

import (
	"context"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/FreePeak/LeanKG/internal/memory"
	"github.com/FreePeak/LeanKG/internal/sessionlink/claudecode"
)

// sharedStyle is one body written in two repositories. Same bytes, same
// rendered row content, so the two rows share a content_id.
const sharedStyle = "---\nname: Shared style\ndescription: Keep commit subjects short.\nmetadata:\n  type: feedback\n---\nKeep commit subjects under seventy two characters.\n"

// recallFixture adds memories to the shared fixture tree (repositories alpha
// and beta of buildFixture), then syncs it, so the central banks hold what
// Sync writes from real files. It returns the central memory.
func recallFixture(t *testing.T) *memory.Memory {
	t.Helper()
	projects, mem := applySetup(t)
	mtime := time.Unix(fixedMTime, 0)
	added := map[string]string{
		memFile(projects, "-work-alpha", "freeze-policy.md"): "---\nname: Freeze policy\ndescription: Release freeze during audits.\nmetadata:\n  type: project\n---\nNo deploys during audits.\n",
		memFile(projects, "-work-alpha", "deploy-tabs.md"):   "---\nname: Deploy tabs\ndescription: Tabs in deploy scripts.\nmetadata:\n  type: feedback\n---\nUse tabs in every deploy script.\n",
		memFile(projects, "-work-alpha", "shared-style.md"):  sharedStyle,
		memFile(projects, "-work-beta", "shared-style.md"):   sharedStyle,
	}
	for path, content := range added {
		writeFile(t, path, content)
		if err := os.Chtimes(path, mtime, mtime); err != nil {
			t.Fatal(err)
		}
	}
	// beta's copy is newer, so it is the row recall keeps.
	newer := mtime.Add(time.Hour)
	if err := os.Chtimes(memFile(projects, "-work-beta", "shared-style.md"), newer, newer); err != nil {
		t.Fatal(err)
	}
	runSync(t, mem, syncOpts(projects, mem))
	return mem
}

func recallOK(t *testing.T, mem *memory.Memory, req RecallRequest) RecallResult {
	t.Helper()
	res, err := Recall(context.Background(), mem, req)
	if err != nil {
		t.Fatalf("Recall(%+v): %v", req, err)
	}
	return res
}

func findRecalled(res RecallResult, name string) (RecalledMemory, bool) {
	for _, m := range res.Memories {
		if m.Name == name {
			return m, true
		}
	}
	return RecalledMemory{}, false
}

func TestRecallProjectScopeReturnsFeedbackFromAnotherRepository(t *testing.T) {
	mem := recallFixture(t)

	res := recallOK(t, mem, RecallRequest{Query: "tabs", Caller: "-work-beta"})

	got, ok := findRecalled(res, "Prefer tabs")
	if !ok {
		t.Fatalf("project scope from -work-beta: feedback from -work-alpha not returned; memories=%+v", res.Memories)
	}
	if got.Kind != "feedback" || got.Project != "-work-alpha" {
		t.Errorf("row kind/project: got %q/%q, want feedback/-work-alpha", got.Kind, got.Project)
	}
	if res.Command != "agent_recall" || res.Scope != "project" || res.Project != "-work-beta" {
		t.Errorf("header: got command=%q scope=%q project=%q", res.Command, res.Scope, res.Project)
	}
	if want := []string{ProjectBank("-work-beta"), GlobalBank}; !slices.Equal(res.Banks, want) {
		t.Errorf("banks: got %v, want %v (project bank first)", res.Banks, want)
	}
	if res.Count != len(res.Memories) {
		t.Errorf("count: got %d, want %d (len memories)", res.Count, len(res.Memories))
	}
}

func TestRecallGlobalScopeReturnsFeedbackFromAnotherRepository(t *testing.T) {
	mem := recallFixture(t)

	res := recallOK(t, mem, RecallRequest{Query: "tabs", Scope: "global", Caller: "-work-beta"})

	if _, ok := findRecalled(res, "Prefer tabs"); !ok {
		t.Fatalf("global scope: feedback from -work-alpha not returned; memories=%+v", res.Memories)
	}
	if want := []string{GlobalBank}; !slices.Equal(res.Banks, want) {
		t.Errorf("banks: got %v, want %v", res.Banks, want)
	}
}

func TestRecallProjectKindOfAnotherRepositoryOnlyUnderAll(t *testing.T) {
	mem := recallFixture(t)
	const query = "audits"

	for _, scope := range []string{"", "global"} {
		res := recallOK(t, mem, RecallRequest{Query: query, Scope: scope, Caller: "-work-beta"})
		if res.Count != 0 {
			t.Errorf("scope %q: got %d rows for %q, want 0 (other repository's project memory must not leak): %+v", scope, res.Count, query, res.Memories)
		}
	}

	res := recallOK(t, mem, RecallRequest{Query: query, Scope: "all", Caller: "-work-beta"})
	got, ok := findRecalled(res, "Freeze policy")
	if !ok {
		t.Fatalf("scope all: project memory of -work-alpha not returned; memories=%+v", res.Memories)
	}
	if got.Kind != "project" || got.Project != "-work-alpha" {
		t.Errorf("row kind/project: got %q/%q, want project/-work-alpha", got.Kind, got.Project)
	}
	if want := "all"; res.Scope != want {
		t.Errorf("scope: got %q, want %q", res.Scope, want)
	}
}

func TestRecallTypesNarrowBeforeTheLimit(t *testing.T) {
	mem := recallFixture(t)
	const query = "deploy tabs"

	unfiltered := recallOK(t, mem, RecallRequest{Query: query, Scope: "all", Limit: 1, Caller: "-work-beta"})
	if len(unfiltered.Memories) != 1 || unfiltered.Memories[0].Kind != "feedback" {
		t.Fatalf("premise: unfiltered top-1 should be the feedback row that matches both terms; got %+v", unfiltered.Memories)
	}

	res := recallOK(t, mem, RecallRequest{Query: query, Scope: "all", Limit: 1, Types: []string{"project"}, Caller: "-work-beta"})
	if res.Count != 1 || res.Memories[0].Kind != "project" {
		t.Errorf("types=project limit=1: got %+v, want one project row (type filter must run before the limit)", res.Memories)
	}
}

func TestRecallIdenticalGlobalMemoryFromTwoRepositoriesReturnedOnce(t *testing.T) {
	mem := recallFixture(t)
	const query = "commit subjects"

	raw, err := mem.RecallFiltered(context.Background(), []string{GlobalBank}, query, 8, nil)
	if err != nil {
		t.Fatalf("RecallFiltered: %v", err)
	}
	if len(raw) != 2 {
		t.Fatalf("premise: the two identical rows should both rank without collapse; got %d", len(raw))
	}

	res := recallOK(t, mem, RecallRequest{Query: query, Scope: "global", Caller: "-work-beta"})
	if res.Count != 1 {
		t.Fatalf("count: got %d, want 1 (same content_id collapsed): %+v", res.Count, res.Memories)
	}
	// Both rows are equally relevant; the newer one (beta's) is the one kept.
	if got := res.Memories[0].Project; got != "-work-beta" {
		t.Errorf("kept row project: got %q, want -work-beta (newer on ties)", got)
	}
}

func TestRecallCallerWithoutProjectBankGetsGlobalRowsAndEmptyProject(t *testing.T) {
	mem := recallFixture(t)

	for _, caller := range []string{"", "-work-delta"} {
		res := recallOK(t, mem, RecallRequest{Query: "tabs", Caller: caller})
		if _, ok := findRecalled(res, "Prefer tabs"); !ok {
			t.Errorf("caller %q: global feedback not returned; memories=%+v", caller, res.Memories)
		}
		if res.Project != "" {
			t.Errorf("caller %q: project got %q, want empty", caller, res.Project)
		}
		if want := []string{GlobalBank}; !slices.Equal(res.Banks, want) {
			t.Errorf("caller %q: banks got %v, want %v", caller, res.Banks, want)
		}
	}
}

func TestRecallDefaultsToProjectScopeAndRecallLimit(t *testing.T) {
	mem := recallFixture(t)

	res := recallOK(t, mem, RecallRequest{Query: "tabs", Caller: "-work-beta"})

	if res.Scope != "project" {
		t.Errorf("scope: got %q, want project", res.Scope)
	}
	if res.Count > memory.RecallLimit {
		t.Errorf("count: got %d, want at most %d", res.Count, memory.RecallLimit)
	}
}

// TestRecallLimitIsCappedAndZeroDefaults: a request for more rows than the
// documented cap (50) is answered with 50 rows, and a zero or negative limit gets
// the default. The rows are distinct, so none collapses by content_id.
func TestRecallLimitIsCappedAndZeroDefaults(t *testing.T) {
	const wantCap = 50
	projects, mem := applySetup(t)
	mtime := time.Unix(fixedMTime, 0)
	for i := 0; i < wantCap+10; i++ {
		body := fmt.Sprintf("---\nname: Deploy note %02d\ndescription: Deploy runbook note %02d.\nmetadata:\n  type: project\n---\nDeploy step %02d runs after the freeze lifts.\n", i, i, i)
		path := memFile(projects, "-work-alpha", fmt.Sprintf("deploy-note-%02d.md", i))
		writeFile(t, path, body)
		if err := os.Chtimes(path, mtime, mtime); err != nil {
			t.Fatal(err)
		}
	}
	runSync(t, mem, syncOpts(projects, mem))

	for _, limit := range []int{wantCap + 10, 1000} {
		res := recallOK(t, mem, RecallRequest{Query: "deploy", Scope: "all", Limit: limit})
		if res.Count != wantCap {
			t.Errorf("limit %d: count %d, want the cap %d", limit, res.Count, wantCap)
		}
	}
	for _, limit := range []int{0, -3} {
		res := recallOK(t, mem, RecallRequest{Query: "deploy", Scope: "all", Limit: limit})
		if res.Count != memory.RecallLimit {
			t.Errorf("limit %d: count %d, want the default %d", limit, res.Count, memory.RecallLimit)
		}
	}
}

func TestRecallUnknownScopeNamesValidValues(t *testing.T) {
	mem := recallFixture(t)

	_, err := Recall(context.Background(), mem, RecallRequest{Query: "tabs", Scope: "team"})
	if err == nil {
		t.Fatal("unknown scope: got nil error, want an error")
	}
	for _, want := range []string{"project", "global", "all"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not name valid scope %q", err, want)
		}
	}
}

func TestRecallUnknownTypeNamesValidValues(t *testing.T) {
	mem := recallFixture(t)

	_, err := Recall(context.Background(), mem, RecallRequest{Query: "tabs", Types: []string{"memo"}})
	if err == nil {
		t.Fatal("unknown type: got nil error, want an error")
	}
	for _, want := range []string{"user", "feedback", "project", "reference", "unknown"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not name valid type %q", err, want)
		}
	}
}

func TestRecallEmptyQueryNamesTheArgument(t *testing.T) {
	mem := recallFixture(t)

	_, err := Recall(context.Background(), mem, RecallRequest{Query: "   "})
	if err == nil || !strings.Contains(err.Error(), "query") {
		t.Fatalf("empty query: got %v, want an error naming query", err)
	}
}

func TestRecallWithoutCentralBanksAsksForSync(t *testing.T) {
	_, mem := applySetup(t) // no sync run: no cc- bank exists

	_, err := Recall(context.Background(), mem, RecallRequest{Query: "tabs"})
	if err == nil || !strings.Contains(err.Error(), "leankg memory sync") {
		t.Fatalf("no central banks: got %v, want an error telling the caller to run leankg memory sync", err)
	}
}

// TestRecallOmitsExcludedProjectRows syncs a project's memory, then excludes the
// project. The row is still stored until the next sync, so recall must filter it.
func TestRecallOmitsExcludedProjectRows(t *testing.T) {
	projects, mem := applySetup(t)
	excluded := claudecode.Slug(syntheticExcluded)
	writeFile(t, memFile(projects, excluded, "marker-note.md"), "---\nname: Marker note\ndescription: Synthetic marker.\nmetadata:\n  type: project\n---\nsyntheticmarkerzeta body\n")
	runSync(t, mem, syncOpts(projects, mem))
	req := RecallRequest{Query: "syntheticmarkerzeta", Scope: scopeAll}
	if got := recallOK(t, mem, req); got.Count != 1 {
		t.Fatalf("setup: recall before the exclusion returned %d rows, want 1", got.Count)
	}

	setExcludeFile(t, syntheticExcluded+"\n")
	if got := recallOK(t, mem, req); got.Count != 0 {
		t.Errorf("recall after the exclusion returned %d rows, want 0", got.Count)
	}
}

// TestRecallFailsClosedOnUnreadableExcludeList makes the exclude file unreadable.
// Recall must return an error, not rows that the list may exclude.
func TestRecallFailsClosedOnUnreadableExcludeList(t *testing.T) {
	mem := recallFixture(t)
	t.Setenv(excludeFileEnv, t.TempDir())

	if _, err := Recall(context.Background(), mem, RecallRequest{Query: "tabs", Scope: scopeAll}); err == nil {
		t.Error("Recall with an unreadable exclude file returned nil error, want an error")
	}
}
