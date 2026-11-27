package core

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/FreePeak/LeanKG/internal/ccmemory"
	"github.com/FreePeak/LeanKG/internal/embed"
	"github.com/FreePeak/LeanKG/internal/memory"
)

// recallRepoA and recallRepoB are caller repositories that do not exist on
// disk. ccmemory.ProjectKey then takes their plain slug, which is stable across
// machines.
const (
	recallRepoA = "/work/repo-alpha"
	recallRepoB = "/work/repo-beta"
)

// recallMemoryFile renders one memory file in the Claude Code layout.
func recallMemoryFile(name, description, kind, body string) string {
	return "---\nname: " + name + "\ndescription: " + description + "\nmetadata:\n  type: " + kind + "\n---\n" + body + "\n"
}

// seedRecallSource writes two repositories' memory files under the Claude Code
// projects root, then syncs them into the central root under home. Fixed file
// times keep the rows deterministic.
func seedRecallSource(t *testing.T, home, projects string) {
	t.Helper()
	ctx := context.Background()
	mtime := time.Unix(1_700_000_000, 0)
	files := map[string]string{
		"-work-repo-alpha/memory/prefer-tabs.md":   recallMemoryFile("Prefer tabs", "Tabs in deploy scripts.", "feedback", "Use tabs in every deploy script."),
		"-work-repo-alpha/memory/freeze-policy.md": recallMemoryFile("Freeze policy", "Release freeze during audits.", "project", "No deploys during audits."),
		"-work-repo-beta/memory/release-notes.md":  recallMemoryFile("Release notes", "Beta release cadence.", "project", "Beta ships on Tuesdays."),
	}
	for rel, content := range files {
		p := filepath.Join(projects, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(p, mtime, mtime); err != nil {
			t.Fatal(err)
		}
	}
	central, err := memory.Open(ctx, home, false)
	if err != nil {
		t.Fatal(err)
	}
	defer central.Close()
	if _, err := ccmemory.Sync(ctx, central, ccmemory.SyncOptions{Projects: projects, CentralRoot: central.Root()}); err != nil {
		t.Fatalf("seed sync: %v", err)
	}
}

// newRecallEngine returns a fixture whose HOME and CLAUDE_CONFIG_DIR are temp
// directories, with the central memory synced when synced is true. The engine
// serves the central root through a lazy handle, as serve wires it.
func newRecallEngine(t *testing.T, synced bool, embedder embed.Provider) (*actionFixture, string) {
	t.Helper()
	home := t.TempDir()
	config := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", config)
	projects := filepath.Join(config, "projects")
	if synced {
		seedRecallSource(t, home, projects)
	}
	f := newActionFixture(t)
	f.e = New(f.st, Options{
		Memory: f.mem, Embedder: embedder, ProjectDir: f.project,
		AgentMemory: ccmemory.NewHandle(home, nil),
	})
	return f, home
}

// recallQuery calls query{action:"memory", args:{command:"agent_recall", ...}}.
func recallQuery(t *testing.T, e *Engine, query string, args map[string]any) (map[string]any, error) {
	t.Helper()
	if args == nil {
		args = map[string]any{}
	}
	args["command"] = "agent_recall"
	return e.Query(context.Background(), QueryRequest{Action: "memory", Query: query, Args: args})
}

func recallOKQuery(t *testing.T, e *Engine, query string, args map[string]any) map[string]any {
	t.Helper()
	out, err := recallQuery(t, e, query, args)
	if err != nil {
		t.Fatalf("agent_recall %q %v: %v", query, args, err)
	}
	return out
}

func recallRows(t *testing.T, out map[string]any) []ccmemory.RecalledMemory {
	t.Helper()
	rows, ok := out["memories"].([]ccmemory.RecalledMemory)
	if !ok {
		t.Fatalf("memories: got %T, want []ccmemory.RecalledMemory", out["memories"])
	}
	return rows
}

func recallFind(rows []ccmemory.RecalledMemory, name string) (ccmemory.RecalledMemory, bool) {
	for _, r := range rows {
		if r.Name == name {
			return r, true
		}
	}
	return ccmemory.RecalledMemory{}, false
}

func TestAgentRecallReturnsSection65ResponseWithProvenance(t *testing.T) {
	f, _ := newRecallEngine(t, true, nil)

	out := recallOKQuery(t, f.e, "tabs", map[string]any{"cwd": recallRepoB})

	if got := out["command"]; got != "agent_recall" {
		t.Errorf("command: got %v, want agent_recall", got)
	}
	if got := out["scope"]; got != "project" {
		t.Errorf("scope: got %v, want project", got)
	}
	if got := out["project"]; got != "-work-repo-beta" {
		t.Errorf("project: got %v, want -work-repo-beta", got)
	}
	wantBanks := []string{ccmemory.ProjectBank("-work-repo-beta"), ccmemory.GlobalBank}
	if got, _ := out["banks"].([]string); !slices.Equal(got, wantBanks) {
		t.Errorf("banks: got %v, want %v (project bank first)", out["banks"], wantBanks)
	}
	rows := recallRows(t, out)
	if out["count"] != len(rows) {
		t.Errorf("count: got %v, want %d", out["count"], len(rows))
	}
	row, ok := recallFind(rows, "Prefer tabs")
	if !ok {
		t.Fatalf("feedback from repo-alpha not recalled from repo-beta: %+v", rows)
	}
	if row.Kind != "feedback" || row.Project != "-work-repo-alpha" {
		t.Errorf("row kind/project: got %q/%q, want feedback/-work-repo-alpha", row.Kind, row.Project)
	}
	retrieval, _ := out["retrieval"].(map[string]any)
	if retrieval["rung"] != "memory" {
		t.Errorf("retrieval.rung: got %v, want memory (provenance stamp)", retrieval["rung"])
	}
	if _, ok := out["freshness"]; !ok {
		t.Errorf("freshness: absent, want the provenance stamp")
	}
}

func TestAgentRecallDecodesTypesFilter(t *testing.T) {
	f, _ := newRecallEngine(t, true, nil)
	scopeAll := map[string]any{"cwd": recallRepoB, "scope": "all"}

	premise := recallOKQuery(t, f.e, "tabs", scopeAll)
	if _, ok := recallFind(recallRows(t, premise), "Prefer tabs"); !ok {
		t.Fatalf("premise: unfiltered scope=all should recall the feedback row: %+v", recallRows(t, premise))
	}

	filtered := map[string]any{"cwd": recallRepoB, "scope": "all", "types": []any{"project"}}
	out := recallOKQuery(t, f.e, "tabs", filtered)
	for _, r := range recallRows(t, out) {
		if r.Kind != "project" {
			t.Errorf("types=[project]: row %q has kind %q", r.Name, r.Kind)
		}
	}
	if _, ok := recallFind(recallRows(t, out), "Prefer tabs"); ok {
		t.Errorf("types=[project]: the feedback row was not filtered out")
	}
}

func TestAgentRecallScopeVocabularyIsItsOwn(t *testing.T) {
	f, _ := newRecallEngine(t, true, nil)

	// "global" is not a retrieval scope (scopePredicate refuses it), but it is
	// a valid agent_recall scope (KD-9).
	out := recallOKQuery(t, f.e, "tabs", map[string]any{"cwd": recallRepoB, "scope": "global"})
	if got, _ := out["banks"].([]string); !slices.Equal(got, []string{ccmemory.GlobalBank}) {
		t.Errorf("scope=global banks: got %v, want [%s]", out["banks"], ccmemory.GlobalBank)
	}

	_, err := recallQuery(t, f.e, "tabs", map[string]any{"scope": "team"})
	if err == nil {
		t.Fatal("scope=team: got nil error, want an error naming the valid scopes")
	}
	for _, want := range []string{"project", "global", "all"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("scope=team error %q does not name %q", err, want)
		}
	}
}

func TestAgentRecallRefusesUnknownArgumentKey(t *testing.T) {
	f, _ := newRecallEngine(t, true, nil)

	_, err := recallQuery(t, f.e, "tabs", map[string]any{"cwd": recallRepoB, "bogus": "x"})
	if err == nil || !strings.Contains(err.Error(), "bogus") {
		t.Fatalf("unknown arg key: err = %v, want a refusal naming bogus", err)
	}
}

func TestAgentRecallUnknownTypeNamesValidTypes(t *testing.T) {
	f, _ := newRecallEngine(t, true, nil)

	_, err := recallQuery(t, f.e, "tabs", map[string]any{"types": []any{"memo"}})
	if err == nil || !strings.Contains(err.Error(), "feedback") {
		t.Fatalf("unknown type: err = %v, want a refusal listing the valid types", err)
	}
}

func TestAgentRecallEmptyQueryNamesTheArgument(t *testing.T) {
	f, _ := newRecallEngine(t, true, nil)

	_, err := recallQuery(t, f.e, "  ", nil)
	if err == nil || !strings.Contains(err.Error(), "query") {
		t.Fatalf("empty query: err = %v, want an error naming query", err)
	}
}

func TestAgentRecallWithoutCentralRootAsksForSyncAndCreatesNothing(t *testing.T) {
	f, home := newRecallEngine(t, false, nil)

	_, err := recallQuery(t, f.e, "tabs", map[string]any{"cwd": recallRepoB})
	if err == nil || !strings.Contains(err.Error(), "leankg memory sync") {
		t.Fatalf("no central root: err = %v, want the message to run leankg memory sync", err)
	}
	if _, statErr := os.Stat(filepath.Join(home, ".leankg")); !os.IsNotExist(statErr) {
		t.Fatalf("answering agent_recall created the central tree: stat err = %v", statErr)
	}
}

func TestAgentRecallFeedbackFromRepoAIsDefaultButProjectMemoryOnlyUnderAll(t *testing.T) {
	f, _ := newRecallEngine(t, true, nil)
	fromB := map[string]any{"cwd": recallRepoB}

	if _, ok := recallFind(recallRows(t, recallOKQuery(t, f.e, "tabs", fromB)), "Prefer tabs"); !ok {
		t.Fatal("default scope from repo-beta: feedback from repo-alpha not recalled")
	}
	if rows := recallRows(t, recallOKQuery(t, f.e, "audits", fromB)); len(rows) != 0 {
		t.Errorf("default scope from repo-beta: repo-alpha project memory leaked: %+v", rows)
	}
	all := map[string]any{"cwd": recallRepoB, "scope": "all"}
	row, ok := recallFind(recallRows(t, recallOKQuery(t, f.e, "audits", all)), "Freeze policy")
	if !ok {
		t.Fatal("scope=all from repo-beta: repo-alpha project memory not recalled")
	}
	if row.Kind != "project" || row.Project != "-work-repo-alpha" {
		t.Errorf("row kind/project: got %q/%q, want project/-work-repo-alpha", row.Kind, row.Project)
	}
}

func TestAgentRecallRunsWithTheEngineEmbedder(t *testing.T) {
	f, _ := newRecallEngine(t, true, embed.Deterministic(8))

	out := recallOKQuery(t, f.e, "tabs", map[string]any{"cwd": recallRepoB})
	if _, ok := recallFind(recallRows(t, out), "Prefer tabs"); !ok {
		t.Errorf("with the dense arm wired, the keyword arm no longer recalls the feedback row: %+v", recallRows(t, out))
	}
}

// args.types belongs to agent_recall only. On every other query{action:"memory"}
// command it must be refused exactly as any unknown args key is: the same error
// text with only the key name changed.
func TestAgentRecallTypesRefusedOnOtherMemoryCommands(t *testing.T) {
	f, _ := newRecallEngine(t, true, nil)
	ctx := context.Background()

	for _, cmd := range []string{"", "search", "view", "snapshot", "session_recall", "memories"} {
		_, unknownErr := f.e.Query(ctx, QueryRequest{Action: "memory", Query: "tabs",
			Args: map[string]any{"command": cmd, "bogus": "x"}})
		if unknownErr == nil {
			t.Fatalf("command %q with bogus key: got nil error, want a refusal", cmd)
		}
		_, typesErr := f.e.Query(ctx, QueryRequest{Action: "memory", Query: "tabs",
			Args: map[string]any{"command": cmd, "types": []any{"project"}}})
		want := strings.Replace(unknownErr.Error(), `"bogus"`, `"types"`, 1)
		if typesErr == nil || typesErr.Error() != want {
			t.Errorf("command %q with types: err = %v, want %q", cmd, typesErr, want)
		}
	}
}
