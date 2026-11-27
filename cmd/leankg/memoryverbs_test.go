package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/FreePeak/LeanKG/internal/ccmemory"
	"github.com/FreePeak/LeanKG/internal/flock"
	"github.com/FreePeak/LeanKG/internal/memory"
	"github.com/FreePeak/LeanKG/internal/sessionlink/claudecode"
)

// memorySyncEnv points HOME and CLAUDE_CONFIG_DIR at a fresh temp tree, so no
// test reads or writes the real ~/.claude or ~/.leankg. It returns the central
// memory root and the Claude Code projects root under that tree.
func memorySyncEnv(t *testing.T) (central, projects string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(home, ".claude"))
	return filepath.Join(home, ".leankg", "memory"), filepath.Join(home, ".claude", "projects")
}

// writeMemoryDoc writes one Claude Code memory file under projects/<slug>/memory.
func writeMemoryDoc(t *testing.T, projects, slug, file, kind, body string) {
	t.Helper()
	dir := filepath.Join(projects, slug, "memory")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := "---\nname: " + strings.TrimSuffix(file, ".md") + "\ndescription: fixture\ntype: " + kind + "\n---\n" + body + "\n"
	if err := os.WriteFile(filepath.Join(dir, file), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// seedMemoryFixture writes three valid memories (a user note in the global
// bank, a project note and a reference in two repository banks) and one file
// with invalid UTF-8, which sync skips.
func seedMemoryFixture(t *testing.T, projects string) {
	t.Helper()
	writeMemoryDoc(t, projects, "-work-alpha", "user.md", "user", "Prefers terse answers.")
	writeMemoryDoc(t, projects, "-work-alpha", "project.md", "project", "Alpha freezes on Friday.")
	writeMemoryDoc(t, projects, "-work-beta", "reference.md", "reference", "Beta dashboards live in Grafana.")
	if err := os.WriteFile(filepath.Join(projects, "-work-beta", "memory", "broken.md"), []byte("\xff\xfe"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// runMemorySync runs `leankg memory sync <args>` in-process and returns its
// exit status and stdout.
func runMemorySync(t *testing.T, args ...string) (int, string) {
	t.Helper()
	var code int
	out := captureStdout(t, func() {
		code = runVerb(context.Background(), append([]string{"memory", "sync"}, args...))
	})
	return code, out
}

// snapshotTree maps every file under root to its contents. A missing root is an
// empty snapshot, so an absent root compares equal to an empty one.
func snapshotTree(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		out[rel] = string(b)
		return nil
	})
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	return out
}

// bankRows lists "bank document_id source_hash" for every row of every cc- bank
// under central, sorted. It is the observable state sync writes.
func bankRows(t *testing.T, central string) []string {
	t.Helper()
	mem, err := memory.Open(context.Background(), "", true)
	if err != nil {
		t.Fatal(err)
	}
	defer mem.Close()
	if mem.Root() != central {
		t.Fatalf("memory root = %s, want %s", mem.Root(), central)
	}
	banks, err := mem.Banks()
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, bank := range banks {
		rows, _, err := mem.List(bank, 0, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, row := range rows {
			id, _ := row.Metadata["document_id"].(string)
			hash, _ := row.Metadata["source_hash"].(string)
			out = append(out, bank+" "+id+" "+hash)
		}
	}
	slices.Sort(out)
	return out
}

func decodeSyncResult(t *testing.T, out string) ccmemory.SyncResult {
	t.Helper()
	var res ccmemory.SyncResult
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("decode sync JSON: %v\n%s", err, out)
	}
	return res
}

func TestMemoryGlobalRootUnderHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	want := filepath.Join(home, ".leankg", "memory")
	if got := memory.GlobalRoot(home); got != want {
		t.Fatalf("GlobalRoot(%q) = %q, want %q", home, got, want)
	}
	mem, err := memory.Open(context.Background(), "", true)
	if err != nil {
		t.Fatal(err)
	}
	defer mem.Close()
	if mem.Root() != want {
		t.Errorf("Open(global).Root() = %q, want %q", mem.Root(), want)
	}
}

func TestMemorySyncIsRegisteredAndListedInUsage(t *testing.T) {
	if _, ok := verbs["memory"]; !ok {
		t.Fatal(`verb "memory" is not registered`)
	}
	stderr := captureStderr(t, usage)
	for _, line := range []string{
		"leankg memory sync [--dry-run] [--rebuild] [--json]",
		"leankg memory migrate [--project SLUG] [--dry-run] [--json]",
	} {
		if !strings.Contains(stderr, line) {
			t.Fatalf("main usage does not list %q:\n%s", line, stderr)
		}
	}
}

func TestMemorySyncMissingOrUnknownSubcommandExitsOne(t *testing.T) {
	memorySyncEnv(t)
	for _, argv := range [][]string{{"memory"}, {"memory", "bogus"}} {
		var code int
		stderr := captureStderr(t, func() { code = runVerb(context.Background(), argv) })
		if code != 1 {
			t.Errorf("runVerb(%v) = %d, want 1", argv, code)
		}
		if !strings.Contains(stderr, "usage: leankg memory sync") {
			t.Errorf("runVerb(%v) stderr lacks usage:\n%s", argv, stderr)
		}
	}
}

func TestMemorySyncJSONShape(t *testing.T) {
	central, projects := memorySyncEnv(t)
	seedMemoryFixture(t, projects)

	code, out := runMemorySync(t, "--json")
	if code != 0 {
		t.Fatalf("exit = %d, want 0\n%s", code, out)
	}

	var keys map[string]json.RawMessage
	if err := json.Unmarshal([]byte(out), &keys); err != nil {
		t.Fatal(err)
	}
	wantKeys := []string{"root", "projects", "documents", "upserted", "deleted", "skipped", "deletes_withheld", "banks"}
	for _, k := range wantKeys {
		if _, ok := keys[k]; !ok {
			t.Errorf("JSON lacks key %q: %s", k, out)
		}
	}
	if len(keys) != len(wantKeys) {
		t.Errorf("JSON has %d keys, want %d: %s", len(keys), len(wantKeys), out)
	}

	res := decodeSyncResult(t, out)
	if res.Root != central || res.Projects != projects {
		t.Errorf("root/projects = %s / %s, want %s / %s", res.Root, res.Projects, central, projects)
	}
	if res.Documents != 3 || res.Upserted != 3 || res.Deleted != 0 || res.Skipped != 1 || res.DeletesWithheld {
		t.Errorf("counts = docs %d upserted %d deleted %d skipped %d withheld %v; want 3 3 0 1 false",
			res.Documents, res.Upserted, res.Deleted, res.Skipped, res.DeletesWithheld)
	}
	wantBanks := []ccmemory.SyncBank{
		{Bank: ccmemory.GlobalBank, Project: "", Rows: 1},
		{Bank: ccmemory.ProjectBank("-work-alpha"), Project: "-work-alpha", Rows: 1},
		{Bank: ccmemory.ProjectBank("-work-beta"), Project: "-work-beta", Rows: 1},
	}
	slices.SortFunc(wantBanks, func(a, b ccmemory.SyncBank) int { return strings.Compare(a.Bank, b.Bank) })
	if !reflect.DeepEqual(res.Banks, wantBanks) {
		t.Errorf("banks = %+v, want %+v", res.Banks, wantBanks)
	}
}

func TestMemorySyncDryRunWritesNothing(t *testing.T) {
	t.Run("absent central root", func(t *testing.T) {
		central, projects := memorySyncEnv(t)
		seedMemoryFixture(t, projects)

		code, out := runMemorySync(t, "--dry-run", "--json")
		if code != 0 {
			t.Fatalf("exit = %d, want 0\n%s", code, out)
		}
		res := decodeSyncResult(t, out)
		if res.Documents != 3 || res.Upserted != 3 || res.Deleted != 0 {
			t.Errorf("dry-run plan = docs %d upserted %d deleted %d, want 3 3 0", res.Documents, res.Upserted, res.Deleted)
		}
		if res.Root != central {
			t.Errorf("dry-run root = %s, want %s", res.Root, central)
		}
		if _, err := os.Stat(central); !os.IsNotExist(err) {
			t.Fatalf("dry-run created the central root (stat err = %v)", err)
		}
	})

	t.Run("existing central root", func(t *testing.T) {
		central, projects := memorySyncEnv(t)
		seedMemoryFixture(t, projects)
		if code, out := runMemorySync(t); code != 0 {
			t.Fatalf("seed sync exit = %d, want 0\n%s", code, out)
		}
		writeMemoryDoc(t, projects, "-work-beta", "extra.md", "feedback", "Use table tests.")

		before := snapshotTree(t, central)
		code, out := runMemorySync(t, "--dry-run", "--json")
		if code != 0 {
			t.Fatalf("exit = %d, want 0\n%s", code, out)
		}
		if res := decodeSyncResult(t, out); res.Upserted != 1 {
			t.Errorf("dry-run upserted = %d, want 1 (the new memory)", res.Upserted)
		}
		if after := snapshotTree(t, central); !reflect.DeepEqual(before, after) {
			t.Fatalf("dry-run changed the central root\nbefore: %v\nafter:  %v", keysOf(before), keysOf(after))
		}

		// Nothing was applied: a real run still has the new memory to write.
		code, out = runMemorySync(t, "--json")
		if code != 0 || decodeSyncResult(t, out).Upserted != 1 {
			t.Fatalf("real sync after dry-run: exit %d, want upserted 1\n%s", code, out)
		}
	})
}

func TestMemorySyncRebuildRefillsCCBanks(t *testing.T) {
	central, projects := memorySyncEnv(t)
	seedMemoryFixture(t, projects)
	if code, out := runMemorySync(t); code != 0 {
		t.Fatalf("seed sync exit = %d, want 0\n%s", code, out)
	}
	clean := bankRows(t, central)

	mem, err := memory.Open(context.Background(), "", true)
	if err != nil {
		t.Fatal(err)
	}
	stale := memory.Entry{
		ID: "ccm-stale", Content: "stale", Source: "test", Timestamp: 1,
		Metadata: map[string]any{"document_id": "ccm-stale", "source_hash": "stale"},
	}
	if _, err := mem.RetainReplacing(context.Background(), ccmemory.GlobalBank, []memory.Entry{stale}, nil); err != nil {
		t.Fatal(err)
	}
	mem.Close()

	code, out := runMemorySync(t, "--rebuild", "--json")
	if code != 0 {
		t.Fatalf("rebuild exit = %d, want 0\n%s", code, out)
	}
	res := decodeSyncResult(t, out)
	if res.Deleted != len(clean)+1 || res.Upserted != 3 {
		t.Errorf("rebuild deleted %d upserted %d, want %d and 3", res.Deleted, res.Upserted, len(clean)+1)
	}
	if got := bankRows(t, central); !reflect.DeepEqual(got, clean) {
		t.Errorf("rows after rebuild = %v, want %v", got, clean)
	}
}

func TestMemorySyncWaitsForLock(t *testing.T) {
	central, projects := memorySyncEnv(t)
	seedMemoryFixture(t, projects)
	if err := os.MkdirAll(central, 0o755); err != nil {
		t.Fatal(err)
	}
	release, err := flock.Lock(filepath.Join(central, "cc-sync.lock"))
	if err != nil {
		t.Fatal(err)
	}

	var (
		code     int
		finished bool
		timedOut bool
	)
	out := captureStdout(t, func() {
		done := make(chan int, 1)
		go func() {
			done <- runVerb(context.Background(), []string{"memory", "sync", "--json"})
		}()
		// The command must still be blocked on the lock after a short window.
		select {
		case <-done:
			finished = true
		case <-time.After(300 * time.Millisecond):
		}
		release()
		select {
		case code = <-done:
		case <-time.After(10 * time.Second):
			timedOut = true
		}
	})
	if finished {
		t.Fatal("sync finished while another process held the lock; it must wait")
	}
	if timedOut {
		t.Fatalf("sync did not finish after the lock was released\n%s", out)
	}
	if code != 0 {
		t.Fatalf("exit = %d, want 0\n%s", code, out)
	}
}

func TestMemorySyncErrorsExitOneAndWriteNoBanks(t *testing.T) {
	banksOf := func(central string) []string {
		files, _ := filepath.Glob(filepath.Join(central, "banks", "*.jsonl"))
		return files
	}

	t.Run("bad deny-list line", func(t *testing.T) {
		central, projects := memorySyncEnv(t)
		seedMemoryFixture(t, projects)
		if err := os.MkdirAll(central, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(central, "scrub.txt"), []byte("([\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if code, out := runMemorySync(t); code != 1 {
			t.Fatalf("exit = %d, want 1\n%s", code, out)
		}
		if got := banksOf(central); len(got) != 0 {
			t.Fatalf("bank files written before the error: %v", got)
		}
	})

	t.Run("unopenable central root", func(t *testing.T) {
		home := t.TempDir()
		notDir := filepath.Join(home, "not-a-dir")
		if err := os.WriteFile(notDir, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Setenv("HOME", notDir)
		t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(home, ".claude"))
		if code, out := runMemorySync(t); code != 1 {
			t.Fatalf("exit = %d, want 1\n%s", code, out)
		}
	})

	t.Run("unreadable projects root", func(t *testing.T) {
		central, projects := memorySyncEnv(t)
		if err := os.MkdirAll(filepath.Dir(projects), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(projects, []byte("not a directory"), 0o644); err != nil {
			t.Fatal(err)
		}
		if code, out := runMemorySync(t); code != 1 {
			t.Fatalf("exit = %d, want 1\n%s", code, out)
		}
		if got := banksOf(central); len(got) != 0 {
			t.Fatalf("bank files written before the error: %v", got)
		}
	})
}

// captureStderr runs fn with os.Stderr redirected and returns what it wrote.
func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stderr = w
	done := make(chan string, 1)
	go func() {
		b, _ := io.ReadAll(r)
		done <- string(b)
	}()
	fn()
	_ = w.Close()
	os.Stderr = old
	return <-done
}

func keysOf(m map[string]string) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

// runMemoryMigrate runs `leankg memory migrate <args>` in-process and returns
// its exit status and stdout.
func runMemoryMigrate(t *testing.T, args ...string) (int, string) {
	t.Helper()
	var code int
	out := captureStdout(t, func() {
		code = runVerb(context.Background(), append([]string{"memory", "migrate"}, args...))
	})
	return code, out
}

func decodeMigrateResult(t *testing.T, out string) ccmemory.MigrateResult {
	t.Helper()
	var res ccmemory.MigrateResult
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("decode migrate JSON: %v\n%s", err, out)
	}
	return res
}

func TestMemoryMigrateIsRegisteredAndInUsage(t *testing.T) {
	if _, ok := memorySubcommands["migrate"]; !ok {
		t.Fatal(`memory subcommand "migrate" is not registered`)
	}
	if usage := memoryUsageText("       "); !strings.Contains(usage, "leankg memory migrate [--project SLUG] [--dry-run] [--json]") {
		t.Fatalf("memory usage does not list migrate:\n%s", usage)
	}
}

func TestMemoryMigrateDryRunPrintsInventoryAndWritesNothing(t *testing.T) {
	central, projects := memorySyncEnv(t)
	seedMemoryFixture(t, projects)

	code, out := runMemoryMigrate(t, "--dry-run", "--json")
	if code != 0 {
		t.Fatalf("exit = %d, want 0\n%s", code, out)
	}
	var raw map[string]any
	if err := json.Unmarshal([]byte(out), &raw); err != nil {
		t.Fatalf("decode: %v\n%s", err, out)
	}
	var keys []string
	for k := range raw {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	if want := []string{"documents", "mismatches", "projects", "root", "skipped", "upserted", "verified"}; !slices.Equal(keys, want) {
		t.Errorf("JSON keys = %v, want %v", keys, want)
	}
	res := decodeMigrateResult(t, out)
	if res.Documents != 3 || res.Upserted != 3 || res.Skipped != 1 || res.Verified {
		t.Errorf("dry-run = docs %d upserted %d skipped %d verified %v, want 3 3 1 false",
			res.Documents, res.Upserted, res.Skipped, res.Verified)
	}
	if len(res.Projects) != 2 || res.Projects[0].Slug != "-work-alpha" || res.Projects[1].Skipped != 1 {
		t.Errorf("inventory = %+v, want alpha and beta with one skipped in beta", res.Projects)
	}
	if _, err := os.Stat(central); !os.IsNotExist(err) {
		t.Fatalf("dry run created the central root (stat err = %v)", err)
	}
}

func TestMemoryMigrateRealRunVerifiesAndIsIdempotent(t *testing.T) {
	central, projects := memorySyncEnv(t)
	seedMemoryFixture(t, projects)

	code, out := runMemoryMigrate(t, "--json")
	if code != 0 {
		t.Fatalf("exit = %d, want 0\n%s", code, out)
	}
	res := decodeMigrateResult(t, out)
	if !res.Verified || res.Mismatches == nil || len(res.Mismatches) != 0 || res.Upserted != 3 {
		t.Fatalf("result = verified %v, mismatches %v, upserted %d; want verified, [], 3", res.Verified, res.Mismatches, res.Upserted)
	}
	if rows := bankRows(t, central); len(rows) != 3 {
		t.Fatalf("rows = %d, want 3 (one per file)", len(rows))
	}

	code, out = runMemoryMigrate(t, "--json")
	if code != 0 || decodeMigrateResult(t, out).Upserted != 0 {
		t.Fatalf("second run: exit %d, want 0 with upserted 0\n%s", code, out)
	}
}

func TestMemoryMigrateDenyListAndSourceTreeUnchanged(t *testing.T) {
	central, projects := memorySyncEnv(t)
	seedMemoryFixture(t, projects)
	if err := os.MkdirAll(central, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(central, "scrub.txt"), []byte("Graf[a]na\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	before := snapshotTree(t, projects)

	if code, out := runMemoryMigrate(t, "--json"); code != 0 {
		t.Fatalf("exit = %d, want 0\n%s", code, out)
	}

	if after := snapshotTree(t, projects); !reflect.DeepEqual(before, after) {
		t.Fatal("migrate changed the Claude Code projects tree")
	}
	for path, body := range snapshotTree(t, central) {
		if strings.Contains(body, "Grafana") {
			t.Errorf("deny-listed word found in %s", path)
		}
	}
}

func TestMemoryMigrateProjectScopeLeavesOtherSlugsRows(t *testing.T) {
	central, projects := memorySyncEnv(t)
	seedMemoryFixture(t, projects)
	if code, out := runMemoryMigrate(t); code != 0 {
		t.Fatalf("full migrate exit = %d, want 0\n%s", code, out)
	}
	before := bankRows(t, central)
	// beta keeps one memory, so its scope is not empty (an empty scope withholds
	// deletes, as sync does); its reference memory is removed and its row is stale.
	if err := os.Remove(filepath.Join(projects, "-work-beta", "memory", "reference.md")); err != nil {
		t.Fatal(err)
	}
	writeMemoryDoc(t, projects, "-work-beta", "extra.md", "project", "Beta freezes on Fridays.")

	code, out := runMemoryMigrate(t, "--project", "-work-beta", "--json")
	if code != 0 {
		t.Fatalf("scoped migrate exit = %d, want 0\n%s", code, out)
	}
	// The JSON shape omits the delete count; the stale row check below covers it.
	if res := decodeMigrateResult(t, out); res.Upserted != 1 || !res.Verified {
		t.Fatalf("scoped result upserted %d verified %v, want 1 true", res.Upserted, res.Verified)
	}
	after := bankRows(t, central)
	betaBank := ccmemory.ProjectBank("-work-beta")
	staleRow := betaBank + " " + ccmemory.DocID("-work-beta", "reference.md") + " "
	for _, row := range before {
		if strings.HasPrefix(row, betaBank+" ") {
			continue
		}
		if !slices.Contains(after, row) {
			t.Errorf("row outside the scope was removed or changed: %s", row)
		}
	}
	for _, row := range after {
		if strings.HasPrefix(row, staleRow) {
			t.Errorf("stale beta row survived: %s", row)
		}
	}
}

func TestMemoryMigrateExitCodeFollowsVerification(t *testing.T) {
	if got := migrateExitCode(ccmemory.MigrateResult{Verified: true}); got != 0 {
		t.Errorf("verified exit = %d, want 0", got)
	}
	bad := ccmemory.MigrateResult{Verified: false, Mismatches: []ccmemory.Mismatch{{Bank: "cc-global", DocID: "d", Reason: "missing"}}}
	if got := migrateExitCode(bad); got != 1 {
		t.Errorf("unverified exit = %d, want 1", got)
	}
}

func TestMemoryMigrateBadFlagExitsTwo(t *testing.T) {
	memorySyncEnv(t)
	var code int
	captureStderr(t, func() {
		code = runVerb(context.Background(), []string{"memory", "migrate", "-bogus"})
	})
	if code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
}

func TestMemoryMigrateUnknownProjectExitsOne(t *testing.T) {
	_, projects := memorySyncEnv(t)
	seedMemoryFixture(t, projects)

	var code int
	stderr := captureStderr(t, func() {
		code = runVerb(context.Background(), []string{"memory", "migrate", "--project", "-work-typo"})
	})
	if code != 1 {
		t.Fatalf("exit = %d, want 1\n%s", code, stderr)
	}
	if !strings.Contains(stderr, "no memory found for project -work-typo") {
		t.Fatalf("stderr lacks the unknown-project message:\n%s", stderr)
	}
}

// memoryRecallFixture gives two repositories their own Claude Code memory
// banks and syncs them into the central root: repository A holds a feedback
// note and a project note, B a reference. Each repository is a temp dir, and
// its slug names its memory directory, so --dir can select it.
func memoryRecallFixture(t *testing.T) (repoA, repoB string) {
	t.Helper()
	_, projects := memorySyncEnv(t)
	repoA, repoB = t.TempDir(), t.TempDir()
	writeMemoryDoc(t, projects, claudecode.Slug(repoA), "tabs.md", "feedback", "Use tabs in every deploy script.")
	writeMemoryDoc(t, projects, claudecode.Slug(repoA), "freeze.md", "project", "Alpha freezes deploys on Friday.")
	writeMemoryDoc(t, projects, claudecode.Slug(repoB), "dashboards.md", "reference", "Beta dashboards live in Grafana.")
	if code, out := runMemorySync(t); code != 0 {
		t.Fatalf("memory sync exit = %d\n%s", code, out)
	}
	return repoA, repoB
}

// runMemoryRecall runs `leankg memory recall <args>` in-process and returns its
// exit status, stdout and stderr.
func runMemoryRecall(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var code int
	var stdout string
	stderr := captureStderr(t, func() {
		stdout = captureStdout(t, func() {
			code = runVerb(context.Background(), append([]string{"memory", "recall"}, args...))
		})
	})
	return code, stdout, stderr
}

// directRecall is ccmemory.Recall called the way the MCP command calls it, for
// the same inputs the CLI receives. home is the HOME the test set.
func directRecall(t *testing.T, req func(home string) ccmemory.RecallRequest) ccmemory.RecallResult {
	t.Helper()
	home := os.Getenv("HOME")
	h := ccmemory.NewHandle(home, nil)
	defer h.Close()
	mem, err := h.Get(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	res, err := ccmemory.Recall(context.Background(), mem, req(home))
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func TestMemoryRecallIsRegisteredAndListedInUsage(t *testing.T) {
	if _, ok := memorySubcommands["recall"]; !ok {
		t.Fatal(`memory subcommand "recall" is not registered`)
	}
	stderr := captureStderr(t, usage)
	line := "leankg memory recall <query...> [--scope project|global|all] [--type KIND ...] [--limit N] [--dir DIR] [--json]"
	if !strings.Contains(stderr, line) {
		t.Fatalf("main usage does not list %q:\n%s", line, stderr)
	}
}

func TestMemoryRecallJSONMatchesRecall(t *testing.T) {
	_, repoB := memoryRecallFixture(t)

	code, out, stderr := runMemoryRecall(t, "deploy", "--scope", "all", "--dir", repoB, "--json")
	if code != 0 {
		t.Fatalf("exit = %d, want 0\n%s", code, stderr)
	}
	var got ccmemory.RecallResult
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("decode recall JSON: %v\n%s", err, out)
	}
	want := directRecall(t, func(home string) ccmemory.RecallRequest {
		return ccmemory.RecallRequest{
			Query: "deploy", Limit: memory.RecallLimit, Scope: "all",
			Caller: ccmemory.ProjectKey(claudecode.ProjectsRoot(home), repoB),
		}
	})
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("CLI JSON differs from ccmemory.Recall:\ngot  %+v\nwant %+v", got, want)
	}
	if got.Count == 0 {
		t.Fatalf("fixture query returned no rows; the comparison proves nothing")
	}
}

func TestMemoryRecallTextListsTheSameRowsInOrder(t *testing.T) {
	_, repoB := memoryRecallFixture(t)

	code, out, stderr := runMemoryRecall(t, "deploy", "--scope", "all", "--dir", repoB)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\n%s", code, stderr)
	}
	want := directRecall(t, func(home string) ccmemory.RecallRequest {
		return ccmemory.RecallRequest{
			Query: "deploy", Limit: memory.RecallLimit, Scope: "all",
			Caller: ccmemory.ProjectKey(claudecode.ProjectsRoot(home), repoB),
		}
	})
	if len(want.Memories) == 0 {
		t.Fatal("fixture query returned no rows; the comparison proves nothing")
	}
	last := -1
	for _, m := range want.Memories {
		row := "[" + m.Kind + "] " + m.Name
		i := strings.Index(out, row)
		if i < 0 {
			t.Fatalf("text output lacks row %q:\n%s", row, out)
		}
		if i < last {
			t.Fatalf("text output lists %q out of ranking order:\n%s", row, out)
		}
		last = i
	}
}

func TestMemoryRecallDirSelectsCallerRepository(t *testing.T) {
	repoA, repoB := memoryRecallFixture(t)
	_ = repoA

	// Feedback from repository A is returned for caller B under project scope.
	code, out, stderr := runMemoryRecall(t, "tabs", "--dir", repoB, "--json")
	if code != 0 {
		t.Fatalf("exit = %d\n%s", code, stderr)
	}
	var got ccmemory.RecallResult
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatal(err)
	}
	if _, ok := findRecalledByName(got, "tabs"); !ok {
		t.Fatalf("caller B: feedback from A not returned; memories=%+v", got.Memories)
	}

	// A's project note is returned for caller B only under scope all.
	_, out, _ = runMemoryRecall(t, "freeze", "--dir", repoB, "--json")
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatal(err)
	}
	if got.Count != 0 {
		t.Fatalf("caller B, project scope: A's project note leaked: %+v", got.Memories)
	}
	_, out, _ = runMemoryRecall(t, "freeze", "--scope", "all", "--dir", repoB, "--json")
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatal(err)
	}
	if _, ok := findRecalledByName(got, "freeze"); !ok {
		t.Fatalf("caller B, scope all: A's project note not returned: %+v", got.Memories)
	}
}

func findRecalledByName(res ccmemory.RecallResult, name string) (ccmemory.RecalledMemory, bool) {
	for _, m := range res.Memories {
		if m.Name == name {
			return m, true
		}
	}
	return ccmemory.RecalledMemory{}, false
}

func TestMemoryRecallTypeFilterIsRepeatable(t *testing.T) {
	_, repoB := memoryRecallFixture(t)

	code, out, stderr := runMemoryRecall(t, "deploy", "--scope", "all", "--type", "feedback", "--type", "project", "--dir", repoB, "--json")
	if code != 0 {
		t.Fatalf("exit = %d\n%s", code, stderr)
	}
	var got ccmemory.RecallResult
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatal(err)
	}
	for _, m := range got.Memories {
		if m.Kind != "feedback" && m.Kind != "project" {
			t.Fatalf("--type feedback --type project returned kind %q: %+v", m.Kind, m)
		}
	}
}

func TestMemoryRecallWithoutCentralRootExitsOneAndCreatesNothing(t *testing.T) {
	central, _ := memorySyncEnv(t)

	code, _, stderr := runMemoryRecall(t, "deploy")
	if code != 1 {
		t.Fatalf("exit = %d, want 1\n%s", code, stderr)
	}
	if !strings.Contains(stderr, "no central memory root") || !strings.Contains(stderr, "leankg memory sync") {
		t.Fatalf("stderr lacks the no-central-root message naming the sync command:\n%s", stderr)
	}
	if _, err := os.Stat(central); !os.IsNotExist(err) {
		t.Fatalf("central root was created by a read: stat err = %v", err)
	}
}

func TestMemoryRecallMissingQueryExitsTwo(t *testing.T) {
	memoryRecallFixture(t)
	for _, args := range [][]string{{}, {"--json"}, {"--scope", "all"}} {
		code, _, stderr := runMemoryRecall(t, args...)
		if code != 2 {
			t.Errorf("recall %v: exit = %d, want 2\n%s", args, code, stderr)
		}
	}
}

func TestMemoryRecallInvalidScopeOrTypeExitsOne(t *testing.T) {
	_, repoB := memoryRecallFixture(t)
	for _, args := range [][]string{
		{"deploy", "--scope", "bogus", "--dir", repoB},
		{"deploy", "--type", "bogus", "--dir", repoB},
	} {
		code, _, stderr := runMemoryRecall(t, args...)
		if code != 1 {
			t.Errorf("recall %v: exit = %d, want 1\n%s", args, code, stderr)
		}
	}
}

func TestMemorySyncTelemetryRecordsRetainPerChangedBank(t *testing.T) {
	_, projects := memorySyncEnv(t)
	const slug = "-work-repo-a"
	writeMemoryDoc(t, projects, slug, "a-feedback.md", "feedback", "Use tabs in deploy scripts.")
	writeMemoryDoc(t, projects, slug, "a-project.md", "project", "Freeze on Friday.")
	lk := enableTelemetryForTest(t)

	if code, out := runMemorySync(t); code != 0 {
		t.Fatalf("memory sync exit = %d\n%s", code, out)
	}
	evs := ledgerMemoryEvents(t, lk)
	retains := ledgerEventsByVerb(evs, "retain")
	want := map[string]bool{ccmemory.GlobalBank: true, ccmemory.ProjectBank(slug): true}
	if len(retains) != len(want) {
		t.Fatalf("retain events = %d, want one per changed bank (%d): %+v", len(retains), len(want), retains)
	}
	for _, e := range retains {
		if !want[e.Banks] {
			t.Errorf("retain names bank %q, which the fixture did not change", e.Banks)
		}
	}
	if n := len(ledgerEventsByVerb(evs, "inject")) + len(ledgerEventsByVerb(evs, "recall")); n != 0 {
		t.Errorf("sync recorded %d inject or recall events, want none", n)
	}
}

func TestMemoryRecallTelemetryRecordsOneRecallEvent(t *testing.T) {
	_, repoB := memoryRecallFixture(t)
	lk := enableTelemetryForTest(t)

	if code, _, stderr := runMemoryRecall(t, "deploy", "--scope", "all", "--dir", repoB); code != 0 {
		t.Fatalf("recall exit = %d\n%s", code, stderr)
	}
	evs := ledgerMemoryEvents(t, lk)
	recalls := ledgerEventsByVerb(evs, "recall")
	if len(recalls) != 1 {
		t.Fatalf("recall events = %d, want 1: %+v", len(recalls), evs)
	}
	if !strings.Contains(recalls[0].Banks, ccmemory.GlobalBank) {
		t.Errorf("recall banks = %q, want it to name %s", recalls[0].Banks, ccmemory.GlobalBank)
	}
	if recalls[0].Returned == 0 {
		t.Error("recall event returned 0 rows; the fixture query matches memories")
	}
}

// seedBankFixtures writes two bank files as JSONL, the way a bank written by
// other tools looks. other-bank is a user's own bank: it holds a duplicate row
// and a row that redaction rewrites (it names the test HOME). cc-global holds a
// duplicate row that the post-run compaction must collapse. It returns the path
// of other-bank.
func seedBankFixtures(t *testing.T, central string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(central, "banks"), 0o755); err != nil {
		t.Fatal(err)
	}
	row := func(id, content string, ts int64) string {
		b, err := json.Marshal(memory.Entry{ID: id, Content: content, Timestamp: ts})
		if err != nil {
			t.Fatal(err)
		}
		return string(b) + "\n"
	}
	foreign := filepath.Join(central, "banks", "other-bank.jsonl")
	own := row("own-1", "team wiki index", 1) + row("own-2", "team wiki index", 2) +
		row("own-3", "notes live in "+os.Getenv("HOME")+"/notes", 3)
	if err := os.WriteFile(foreign, []byte(own), 0o644); err != nil {
		t.Fatal(err)
	}
	cc := row("", "shared note", 1) + row("", "shared note", 2)
	if err := os.WriteFile(filepath.Join(central, "banks", ccmemory.GlobalBank+".jsonl"), []byte(cc), 0o644); err != nil {
		t.Fatal(err)
	}
	return foreign
}

// readFileString returns the file's contents, failing the test on error.
func readFileString(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// ccRowLines lists the sync-written rows (those with a document id) of the cc-
// banks under central. The doc-less compaction probe rows are left out.
func ccRowLines(t *testing.T, central string) []string {
	t.Helper()
	var out []string
	for _, line := range bankRows(t, central) {
		if strings.HasPrefix(line, "cc-") && len(strings.Fields(line)) == 3 {
			out = append(out, line)
		}
	}
	return out
}

func TestMemorySyncLeavesForeignBankByteIdentical(t *testing.T) {
	central, projects := memorySyncEnv(t)
	seedMemoryFixture(t, projects)
	foreign := seedBankFixtures(t, central)
	before := sha256.Sum256([]byte(readFileString(t, foreign)))

	if code, out := runMemorySync(t, "--dry-run", "--json"); code != 0 {
		t.Fatalf("dry-run exit = %d, want 0\n%s", code, out)
	}
	if after := sha256.Sum256([]byte(readFileString(t, foreign))); after != before {
		t.Fatalf("dry run rewrote other-bank")
	}

	if code, out := runMemorySync(t, "--json"); code != 0 {
		t.Fatalf("sync exit = %d, want 0\n%s", code, out)
	}
	if after := sha256.Sum256([]byte(readFileString(t, foreign))); after != before {
		t.Fatalf("sync rewrote other-bank: sha256 %x, want %x", after, before)
	}
	if rows := ccRowLines(t, central); len(rows) != 3 {
		t.Fatalf("cc- rows after sync = %d, want 3: %v", len(rows), rows)
	}
	if n := strings.Count(readFileString(t, filepath.Join(central, "banks", ccmemory.GlobalBank+".jsonl")), "shared note"); n != 1 {
		t.Fatalf("cc-global holds %d shared-note rows after sync, want 1 (compaction ran on cc- banks)", n)
	}
}

func TestMemoryMigrateLeavesForeignBankByteIdentical(t *testing.T) {
	central, projects := memorySyncEnv(t)
	seedMemoryFixture(t, projects)
	foreign := seedBankFixtures(t, central)
	before := sha256.Sum256([]byte(readFileString(t, foreign)))

	if code, out := runMemoryMigrate(t, "--dry-run", "--json"); code != 0 {
		t.Fatalf("dry-run exit = %d, want 0\n%s", code, out)
	}
	if after := sha256.Sum256([]byte(readFileString(t, foreign))); after != before {
		t.Fatalf("migrate dry run rewrote other-bank")
	}

	code, out := runMemoryMigrate(t, "--json")
	if code != 0 || !decodeMigrateResult(t, out).Verified {
		t.Fatalf("migrate exit = %d, want 0 and verified\n%s", code, out)
	}
	if after := sha256.Sum256([]byte(readFileString(t, foreign))); after != before {
		t.Fatalf("migrate rewrote other-bank: sha256 %x, want %x", after, before)
	}
	if rows := ccRowLines(t, central); len(rows) != 3 {
		t.Fatalf("cc- rows after migrate = %d, want 3: %v", len(rows), rows)
	}
}
