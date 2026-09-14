package memory

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func openTest(t *testing.T) *Memory {
	t.Helper()
	m, err := Open(t.TempDir(), false)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { m.Close() })
	return m
}

func TestOpenCreatesLayout(t *testing.T) {
	dir := t.TempDir()
	m, err := Open(dir, false)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer m.Close()
	want := filepath.Join(dir, ".leankg", "memory")
	if m.Root() != want {
		t.Errorf("Root() = %q, want %q", m.Root(), want)
	}
	for _, p := range []string{"MEMORY.md", "USER.md", "topics", "index.db"} {
		if _, err := os.Stat(filepath.Join(want, p)); err != nil {
			t.Errorf("%s missing after Open: %v", p, err)
		}
	}
}

func TestPathValidation(t *testing.T) {
	m := openTest(t)
	outside := []string{"/etc/passwd", "../x.md", "topics/../../x.md", "a/../b.md"}
	for _, p := range outside {
		if _, _, err := m.resolve(p); !errors.Is(err, ErrOutsideRoot) {
			t.Errorf("resolve(%q) err = %v, want ErrOutsideRoot", p, err)
		}
	}
	invalid := []string{"", "notes.txt", "sub/x.md", "topics", "topics/x.txt", "topics/a/b.md", "MEMORY.md.bak"}
	for _, p := range invalid {
		if _, _, err := m.resolve(p); !errors.Is(err, ErrInvalidPath) {
			t.Errorf("resolve(%q) err = %v, want ErrInvalidPath", p, err)
		}
	}
	valid := []string{"MEMORY.md", "USER.md", "topics/goals.md"}
	for _, p := range valid {
		if _, _, err := m.resolve(p); err != nil {
			t.Errorf("resolve(%q) unexpected err: %v", p, err)
		}
	}
}

func TestSymlinkEscapeRejected(t *testing.T) {
	m := openTest(t)
	root := m.Root()
	if err := os.Symlink("/etc/passwd", filepath.Join(root, "topics", "evil.md")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if _, err := m.View("topics/evil.md", 0); !errors.Is(err, ErrOutsideRoot) {
		t.Errorf("View through escaping symlink: err = %v, want ErrOutsideRoot", err)
	}
	if err := m.Create("topics/evil.md", "x"); !errors.Is(err, ErrOutsideRoot) {
		t.Errorf("Create over escaping symlink: err = %v, want ErrOutsideRoot", err)
	}
	// Dangling symlink pointing outside: also refused.
	if err := os.Symlink(filepath.Join(root, "..", "nowhere.md"), filepath.Join(root, "topics", "dangling.md")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if err := m.Create("topics/dangling.md", "x"); !errors.Is(err, ErrOutsideRoot) {
		t.Errorf("Create over dangling outside symlink: err = %v, want ErrOutsideRoot", err)
	}
}

func TestCoreFileOverflow(t *testing.T) {
	m := openTest(t)
	err := m.Create("MEMORY.md", strings.Repeat("x", CoreFileBytes+1))
	var eo ErrOverflow
	if !errors.As(err, &eo) {
		t.Fatalf("Create overflow: err = %v, want ErrOverflow", err)
	}
	if eo.File != "MEMORY.md" || eo.Used != CoreFileBytes+1 || eo.Limit != CoreFileBytes {
		t.Errorf("ErrOverflow = %+v", eo)
	}
	// The refused write must not have truncated or replaced the file.
	if c, _ := m.View("MEMORY.md", 0); c != "" {
		t.Errorf("failed write mutated file: %q", c)
	}
	if err := m.Create("MEMORY.md", strings.Repeat("x", CoreFileBytes)); err != nil {
		t.Errorf("Create at limit: %v", err)
	}
	// Add pushing past the limit also refuses.
	m2 := openTest(t)
	if err := m2.Add("USER.md", strings.Repeat("y", CoreFileBytes-4)); err != nil {
		t.Fatalf("Add under limit: %v", err)
	}
	if err := m2.Add("USER.md", "z"); !errors.As(err, &eo) {
		t.Errorf("Add overflow: err = %v, want ErrOverflow", err)
	}
	// topics/ is unbounded.
	if err := m.Create("topics/big.md", strings.Repeat("x", CoreFileBytes*3)); err != nil {
		t.Errorf("topics overflow should not apply: %v", err)
	}
}

func TestAmbiguousReplace(t *testing.T) {
	m := openTest(t)
	if err := m.Create("topics/a.md", "alpha beta gamma beta\n"); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := m.StrReplace("topics/a.md", "beta", "B"); !errors.Is(err, ErrAmbiguousMatch) {
		t.Errorf("two occurrences: err = %v, want ErrAmbiguousMatch", err)
	}
	if err := m.StrReplace("topics/a.md", "nope", "B"); !errors.Is(err, ErrAmbiguousMatch) {
		t.Errorf("zero occurrences: err = %v, want ErrAmbiguousMatch", err)
	}
	if err := m.StrReplace("topics/a.md", "alpha", "ALPHA"); err != nil {
		t.Fatalf("unique replace: %v", err)
	}
	if c, _ := m.View("topics/a.md", 0); c != "ALPHA beta gamma beta" {
		t.Errorf("after replace: %q", c)
	}
	// Remove keeps the rest of the line.
	if err := m.Remove("topics/a.md", "gamma"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if c, _ := m.View("topics/a.md", 0); c != "ALPHA beta  beta" {
		t.Errorf("after remove: %q", c)
	}
	if err := m.Remove("topics/a.md", "beta"); !errors.Is(err, ErrAmbiguousMatch) {
		t.Errorf("ambiguous remove: err = %v, want ErrAmbiguousMatch", err)
	}
}

func TestSnapshotHeaderFormat(t *testing.T) {
	m := openTest(t)
	got, err := m.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	wantEmpty := "<!-- leankg-memory usage: MEMORY.md 0% (0/2200) USER.md 0% (0/2200) -->\n# MEMORY.md\n# USER.md\n"
	if got != wantEmpty {
		t.Errorf("empty snapshot =\n%q\nwant\n%q", got, wantEmpty)
	}
	// 748 bytes = 34% of 2200 (contract example); 264 bytes = 12%.
	if err := m.Create("MEMORY.md", strings.Repeat("a", 748)); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := m.Create("USER.md", strings.Repeat("u", 264)); err != nil {
		t.Fatalf("Create: %v", err)
	}
	got, err = m.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	want := "<!-- leankg-memory usage: MEMORY.md 34% (748/2200) USER.md 12% (264/2200) -->\n" +
		"# MEMORY.md\n" + strings.Repeat("a", 748) + "\n" +
		"# USER.md\n" + strings.Repeat("u", 264) + "\n"
	if got != want {
		t.Errorf("snapshot =\n%q\nwant\n%q", got, want)
	}
}

func TestInsertAndView(t *testing.T) {
	m := openTest(t)
	if err := m.Create("topics/a.md", "one\ntwo\n"); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := m.Insert("topics/a.md", "zero", 0); err != nil {
		t.Fatalf("Insert prepend: %v", err)
	}
	if c, _ := m.View("topics/a.md", 0); c != "zero\none\ntwo" {
		t.Errorf("after prepend: %q", c)
	}
	if err := m.Insert("topics/a.md", "mid", 3); err != nil {
		t.Fatalf("Insert at line 3: %v", err)
	}
	if c, _ := m.View("topics/a.md", 0); c != "zero\none\nmid\ntwo" {
		t.Errorf("after insert: %q", c)
	}
	if c, _ := m.View("topics/a.md", 3); c != "mid\ntwo" {
		t.Errorf("View from line 3: %q", c)
	}
	if c, _ := m.View("topics/a.md", 99); c != "" {
		t.Errorf("View past EOF: %q", c)
	}
}

func TestFTSSearchRoundTrip(t *testing.T) {
	m := openTest(t)
	if err := m.Create("topics/a.md", "the quick brown fox\njumps high\n"); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := m.Create("topics/b.md", "a lazy dog sleeps\n"); err != nil {
		t.Fatalf("Create: %v", err)
	}
	hits, err := m.Search("fox", 0)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(hits) != 1 || hits[0].Path != "topics/a.md" || hits[0].Snippet != "the quick brown fox" {
		t.Fatalf("Search(fox) = %+v", hits)
	}
	if hits[0].Score <= 0 {
		t.Errorf("score should be positive, got %v", hits[0].Score)
	}
	// Query punctuation must not abort the MATCH expression; FTS5 AND
	// requires both terms in the same indexed row (line).
	if hits, _ = m.Search(`fox (quick",`, 0); len(hits) != 1 || hits[0].Path != "topics/a.md" {
		t.Errorf("Search with syntax chars = %+v, want 1 hit", hits)
	}
	// StrReplace reindexes: fox is gone, cat replaces it.
	if err := m.StrReplace("topics/a.md", "fox", "cat"); err != nil {
		t.Fatalf("StrReplace: %v", err)
	}
	if hits, _ = m.Search("fox", 0); len(hits) != 0 {
		t.Errorf("Search(fox) after replace = %+v, want none", hits)
	}
	if hits, _ = m.Search("cat", 0); len(hits) != 1 || hits[0].Path != "topics/a.md" {
		t.Errorf("Search(cat) after replace = %+v", hits)
	}
	// Rename re-keys the index.
	if err := m.Rename("topics/b.md", "topics/c.md"); err != nil {
		t.Fatalf("Rename: %v", err)
	}
	if hits, _ = m.Search("lazy", 0); len(hits) != 1 || hits[0].Path != "topics/c.md" {
		t.Errorf("Search(lazy) after rename = %+v", hits)
	}
	// Delete drops the rows.
	if err := m.Delete("topics/c.md"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if hits, _ = m.Search("lazy", 0); len(hits) != 0 {
		t.Errorf("Search(lazy) after delete = %+v", hits)
	}
}

func TestBankNameDeterministic(t *testing.T) {
	// Vectors generated by running the Rust wyhash 0.5 crate directly.
	for _, tc := range []struct {
		in   string
		seed uint64
		want uint64
	}{
		{"abc", 0, 0xe3db0f558c63ddee},
		{"x", 0, 0x7b5b4d1d94700ca9},
		{"/tmp/leankg-proj", 0, 0x99d42b2e6529d1e4},
		// crate doc example: wyhash([0,1,2], seed 3)
		{"\x00\x01\x02", 3, 0xb0f941520b1ad95d},
	} {
		if got := wyhash64([]byte(tc.in), tc.seed); got != tc.want {
			t.Errorf("wyhash64(%q, %d) = %#x, want %#x", tc.in, tc.seed, got, tc.want)
		}
	}
	if got := base36(0); got != "0" {
		t.Errorf("base36(0) = %q", got)
	}

	dir := t.TempDir()
	n1, n2 := BankName(dir), BankName(dir)
	if n1 != n2 {
		t.Fatalf("BankName not deterministic: %q vs %q", n1, n2)
	}
	// BankName hashes the canonicalized path; reconstruct the expectation
	// from the same transform.
	abs, _ := filepath.EvalSymlinks(dir)
	want := sanitizeBank(filepath.Base(abs) + "-" + base36(wyhash64([]byte(abs), 0)))
	if n1 != want {
		t.Errorf("BankName = %q, want %q", n1, want)
	}
	if len(n1) > 64 {
		t.Errorf("BankName %q exceeds 64 chars", n1)
	}
	for _, c := range n1 {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-') {
			t.Errorf("BankName %q has illegal char %q", n1, c)
		}
	}
	// Long/unicode dir names sanitize and truncate.
	long := filepath.Join(dir, strings.Repeat("w", 80)+" ✓")
	if got := BankName(long); len(got) > 64 {
		t.Errorf("BankName long = %d chars", len(got))
	}
	// Different dirs hash differently.
	if BankName(filepath.Join(dir, "a")) == BankName(filepath.Join(dir, "b")) {
		t.Error("distinct dirs must hash to distinct banks")
	}
}

func TestRetainCursorResume(t *testing.T) {
	m := openTest(t)
	es := []Entry{{Content: "deploy kubernetes cluster"}}
	if err := m.Retain("sess1", es, 4); err != nil {
		t.Fatalf("Retain: %v", err)
	}
	before, _ := os.ReadFile(m.bankPath("sess1"))
	// Same or lower cursor: skipped entirely.
	for _, cur := range []int{4, 3} {
		if err := m.Retain("sess1", es, cur); err != nil {
			t.Fatalf("Retain(%d): %v", cur, err)
		}
	}
	after, _ := os.ReadFile(m.bankPath("sess1"))
	if string(before) != string(after) {
		t.Error("re-retain at/below cursor must not append rows")
	}
	// Higher cursor: appended, and the cursor lands in metadata.
	if err := m.Retain("sess1", []Entry{{Content: "second batch", Metadata: map[string]any{"session_id": "s"}}, {Content: "third"}}, 7); err != nil {
		t.Fatalf("Retain(7): %v", err)
	}
	got, err := m.Recall("sess1", "second batch", 0)
	if err != nil {
		t.Fatalf("Recall: %v", err)
	}
	if len(got) != 1 || got[0].Metadata["retained_through_user_turn"].(float64) != 7 {
		t.Errorf("Recall cursor = %+v", got)
	}
	// New bank starts at cursor 0: nothing skipped.
	if err := m.Retain("sess2", es, 1); err != nil {
		t.Fatalf("Retain new bank: %v", err)
	}
}

func TestRetainRawNoCursor(t *testing.T) {
	m := openTest(t)
	if err := m.RetainRaw("hb", []Entry{{Content: "first heartbeat row"}}); err != nil {
		t.Fatalf("RetainRaw: %v", err)
	}
	// Contrast with the cursor-gated path: with no cursor (0) a plain Retain
	// is a silent no-op — the hindsight wire has no cursor concept, so every
	// compat retain must land.
	if err := m.Retain("hb", []Entry{{Content: "dropped"}}, 0); err != nil {
		t.Fatalf("Retain: %v", err)
	}
	if err := m.RetainRaw("hb", []Entry{{Content: "second heartbeat row"}}); err != nil {
		t.Fatalf("RetainRaw: %v", err)
	}
	data, err := os.ReadFile(m.bankPath("hb"))
	if err != nil {
		t.Fatal(err)
	}
	if n := len(strings.Split(strings.TrimSpace(string(data)), "\n")); n != 2 {
		t.Fatalf("bank rows = %d, want 2 (RetainRaw twice, cursorless Retain dropped)", n)
	}
}

func TestRecallZeroMatchFiltered(t *testing.T) {
	m := openTest(t)
	es := []Entry{
		{Content: "kubernetes deployment rolled out"},
		{Content: "pasta recipe with garlic"},
		{Content: "pasta shapes list"},
	}
	if err := m.Retain("cook", es, 2); err != nil {
		t.Fatalf("Retain: %v", err)
	}
	got, err := m.Recall("cook", "pasta", 0)
	if err != nil {
		t.Fatalf("Recall: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("Recall(pasta) = %d entries, want 2", len(got))
	}
	if got[0].Content != "pasta recipe with garlic" && got[0].Content != "pasta shapes list" {
		t.Errorf("unexpected first entry %q", got[0].Content)
	}
	if got, _ = m.Recall("cook", "zebra unicorn", 0); len(got) != 0 {
		t.Errorf("zero-match recall = %+v, want empty", got)
	}
	if got, _ = m.Recall("cook", "", 0); len(got) != 0 {
		t.Errorf("empty query = %+v, want empty", got)
	}
	// limit applies.
	if got, _ = m.Recall("cook", "pasta", 1); len(got) != 1 {
		t.Errorf("limit=1 returned %d", len(got))
	}
}

// --- FR-ZCP-07 remainder: session verbs, scoping matrix, injection ---

func TestParseScopeModes(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want Scope
	}{
		{"", ScopePerProject},
		{"per-project", ScopePerProject},
		{"global", ScopeGlobal},
		{"per-project-tagged", ScopePerProjectTagged},
	} {
		got, err := ParseScope(tc.in)
		if err != nil || got != tc.want {
			t.Fatalf("ParseScope(%q) = %v, %v; want %v", tc.in, got, err, tc.want)
		}
	}
	// An unknown scope must error, not silently retarget (the reference's
	// fallthrough-to-default was the hazard).
	if _, err := ParseScope("gobal"); err == nil {
		t.Fatal("unknown scope must error")
	}
	// Matrix behavior, bank.rs:81-95 semantics with bank "proj".
	if got := ScopeGlobal.WriteBank("proj"); got != SharedBank {
		t.Errorf("global write bank = %q, want %q", got, SharedBank)
	}
	if got := ScopePerProject.WriteBank("proj"); got != "proj" {
		t.Errorf("per-project write bank = %q", got)
	}
	if got := ScopePerProjectTagged.WriteBank("proj"); got != "proj" {
		t.Errorf("tagged write bank = %q", got)
	}
	if got := ScopeGlobal.ReadBanks("proj"); len(got) != 1 || got[0] != SharedBank {
		t.Errorf("global read banks = %v", got)
	}
	if got := ScopePerProject.ReadBanks("proj"); len(got) != 1 || got[0] != "proj" {
		t.Errorf("per-project read banks = %v", got)
	}
	if got := ScopePerProjectTagged.ReadBanks("proj"); len(got) != 2 || got[0] != "proj" || got[1] != SharedBank {
		t.Errorf("tagged read banks = %v, want [proj %s]", got, SharedBank)
	}
}

func TestSessionRetainRoundTripAndCursor(t *testing.T) {
	m := openTest(t)
	turns := []string{"user asked about kubernetes deploys", "assistant explained rolling updates"}
	res, err := m.SessionRetain(ScopePerProject, "", "", "sess-1", turns, 4)
	if err != nil {
		t.Fatalf("SessionRetain: %v", err)
	}
	if res.Written != 2 || res.Skipped != 0 || res.RetainedThroughUserTurn != 4 {
		t.Fatalf("result = %+v", res)
	}
	// Default scope wrote the store's own project bank.
	wantBank := BankName(m.scopeCwd(""))
	if res.Bank != wantBank {
		t.Errorf("bank = %q, want %q", res.Bank, wantBank)
	}
	// Row shape: mnemopi metadata, source, importance, id prefix.
	data, _ := os.ReadFile(m.bankPath(res.Bank))
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("bank rows = %d, want 2", len(lines))
	}
	var row Entry
	if err := json.Unmarshal([]byte(lines[0]), &row); err != nil {
		t.Fatalf("row json: %v", err)
	}
	if row.Source != "coding-agent-transcript" || row.Importance != 0.65 {
		t.Errorf("row source/importance = %q/%v", row.Source, row.Importance)
	}
	if row.Metadata["session_id"] != "sess-1" || row.Metadata["retained_through_user_turn"].(float64) != 4 {
		t.Errorf("row metadata = %#v", row.Metadata)
	}
	if row.Metadata["message_count"].(float64) != 2 {
		t.Errorf("message_count = %v", row.Metadata["message_count"])
	}
	if !strings.HasPrefix(row.ID, "sess-1-") {
		t.Errorf("row id %q must derive from session", row.ID)
	}
	// Recall merges the same bank and ranks the match.
	banks, got, err := m.SessionRecall(ScopePerProject, "", "", "kubernetes deploys", 8)
	if err != nil {
		t.Fatalf("SessionRecall: %v", err)
	}
	if len(banks) != 1 || banks[0] != wantBank {
		t.Errorf("recall banks = %v", banks)
	}
	if len(got) != 1 || got[0].Content != turns[0] {
		t.Errorf("recall got = %+v", got)
	}
	// Idempotency is session-keyed: cursor 4 (or lower) skips the batch.
	res, err = m.SessionRetain(ScopePerProject, "", "", "sess-1", []string{"duplicate"}, 4)
	if err != nil {
		t.Fatalf("re-retain: %v", err)
	}
	if res.Written != 0 || res.Skipped != 1 || res.RetainedThroughUserTurn != 4 {
		t.Fatalf("re-retain result = %+v", res)
	}
	// A different session in the SAME bank is not suppressed.
	res, err = m.SessionRetain(ScopePerProject, "", "", "sess-2", []string{"other session kubernetes note"}, 1)
	if err != nil {
		t.Fatalf("sess-2 retain: %v", err)
	}
	if res.Written != 1 {
		t.Fatalf("sess-2 result = %+v", res)
	}
	_, got, _ = m.SessionRecall(ScopePerProject, "", "", "kubernetes", 8)
	if len(got) != 2 {
		t.Errorf("merged recall = %d rows, want 2", len(got))
	}
	// Validation errors.
	if _, err := m.SessionRetain(ScopePerProject, "", "", "", turns, 1); err == nil {
		t.Error("missing session_id must error")
	}
	if _, err := m.SessionRetain(ScopePerProject, "", "", "s", nil, 1); err == nil {
		t.Error("missing turns must error")
	}
	if _, err := m.SessionRetain(ScopePerProject, "", "", "s", []string{"ok", "  "}, 1); err == nil {
		t.Error("empty turn must error (unrecallable row)")
	}
}

func TestSessionScopesEndToEnd(t *testing.T) {
	proj := openTest(t) // cwd falls back to proj's own dir
	cwd := proj.scopeCwd("")
	projBank := BankName(cwd)

	// global: writes + reads the shared bank only.
	res, err := proj.SessionRetain(ScopeGlobal, cwd, "", "s-g", []string{"global memory about postgres"}, 1)
	if err != nil {
		t.Fatalf("global retain: %v", err)
	}
	if res.Bank != SharedBank {
		t.Fatalf("global write bank = %q, want %q", res.Bank, SharedBank)
	}
	// per-project write stays out of the shared bank.
	if _, err := proj.SessionRetain(ScopePerProject, cwd, "", "s-p", []string{"project memory about redis"}, 1); err != nil {
		t.Fatalf("project retain: %v", err)
	}
	if _, err := os.Stat(proj.bankPath(SharedBank)); err != nil {
		t.Fatalf("shared bank must hold only the global write: %v", err)
	}
	sharedData, _ := os.ReadFile(proj.bankPath(SharedBank))
	if strings.Contains(string(sharedData), "redis") {
		t.Error("per-project write leaked into shared bank")
	}
	if _, err := os.Stat(proj.bankPath(projBank)); err != nil {
		t.Fatalf("project bank missing: %v", err)
	}

	// per-project read: shared rows invisible.
	_, got, _ := proj.SessionRecall(ScopePerProject, cwd, "", "postgres", 8)
	if len(got) != 0 {
		t.Errorf("per-project recall saw shared rows: %+v", got)
	}
	// global read: project rows invisible.
	_, got, _ = proj.SessionRecall(ScopeGlobal, cwd, "", "redis", 8)
	if len(got) != 0 {
		t.Errorf("global recall saw project rows: %+v", got)
	}
	// tagged read: both, project rows first on rank ties.
	banks, got, _ := proj.SessionRecall(ScopePerProjectTagged, cwd, "", "memory", 8)
	if len(banks) != 2 || banks[0] != projBank || banks[1] != SharedBank {
		t.Fatalf("tagged banks = %v", banks)
	}
	if len(got) != 2 {
		t.Fatalf("tagged recall = %d rows, want 2", len(got))
	}
	if !strings.Contains(got[0].Content, "redis") || !strings.Contains(got[1].Content, "postgres") {
		t.Errorf("tagged merge order broken: %q then %q", got[0].Content, got[1].Content)
	}

	// bank-name mode: explicit bank overrides scope routing entirely.
	res, err = proj.SessionRetain(ScopeGlobal, cwd, "custom-bank", "s-b", []string{"custom bank memory"}, 1)
	if err != nil {
		t.Fatalf("custom retain: %v", err)
	}
	if res.Bank != "custom-bank" {
		t.Errorf("explicit bank ignored: %+v", res)
	}
	banks, got, _ = proj.SessionRecall(ScopePerProject, cwd, "custom-bank", "custom", 8)
	if len(banks) != 1 || banks[0] != "custom-bank" || len(got) != 1 {
		t.Errorf("bank-mode recall = %v %+v", banks, got)
	}
}

func TestFirstTurnMemoriesInjection(t *testing.T) {
	m := openTest(t)
	cwd := m.scopeCwd("")
	if _, err := m.SessionRetain(ScopePerProject, cwd, "", "s1", []string{"kubernetes rolling update lesson"}, 1); err != nil {
		t.Fatalf("retain: %v", err)
	}
	if _, err := m.SessionRetain(ScopePerProject, cwd, "", "s2", []string{"redis cache lesson"}, 2); err != nil {
		t.Fatalf("retain 2: %v", err)
	}
	// Ranked case: query selects + ranks.
	block, entries, err := m.FirstTurnMemories(ScopePerProject, cwd, "", "kubernetes lesson")
	if err != nil {
		t.Fatalf("FirstTurnMemories: %v", err)
	}
	if len(entries) != 2 || entries[0].Content != "kubernetes rolling update lesson" {
		t.Fatalf("entries = %+v", entries)
	}
	if !strings.HasPrefix(block, "<memories>\n") || !strings.HasSuffix(block, "</memories>") {
		t.Fatalf("block not framed: %q", block)
	}
	if !strings.Contains(block, "- kubernetes rolling update lesson") {
		t.Fatalf("block = %q", block)
	}
	// Cold case: empty query returns the newest rows instead of nothing.
	block, entries, _ = m.FirstTurnMemories(ScopePerProject, cwd, "", "")
	if len(entries) != 2 {
		t.Fatalf("cold entries = %d, want 2", len(entries))
	}
	if !strings.Contains(block, "redis cache lesson") || !strings.Contains(block, "kubernetes rolling update lesson") {
		t.Fatalf("cold block = %q", block)
	}
	// Empty bank: no block at all ("" — callers skip it).
	block, entries, _ = m.FirstTurnMemories(ScopeGlobal, cwd, "", "")
	if block != "" || len(entries) != 0 {
		t.Errorf("empty recall must render \"\", got %q %+v", block, entries)
	}
}

func TestInjectBlockBudgets(t *testing.T) {
	// limit applies.
	es := make([]Entry, 12)
	for i := range es {
		es[i] = Entry{Content: fmt.Sprintf("note %d", i)}
	}
	if got := InjectBlock(es, 3, 0); strings.Count(got, "- note") != 3 {
		t.Errorf("limit 3 rendered %d lines", strings.Count(got, "- note"))
	}
	// Token budget: entries that do not fit whole are dropped whole.
	big := []Entry{
		{Content: strings.Repeat("a", 40)},  // ~11 tokens
		{Content: strings.Repeat("b", 200)}, // ~51 tokens
	}
	got := InjectBlock(big, 8, 12)
	if strings.Contains(got, "bbb") {
		t.Errorf("over-budget entry must be dropped whole: %q", got)
	}
	if !strings.Contains(got, strings.Repeat("a", 40)) {
		t.Errorf("fitting entry missing: %q", got)
	}
	// Zero-budget entry (empty content) never renders a bare "- ".
	if got := InjectBlock(nil, 8, 100); got != "" {
		t.Errorf("no entries must render \"\", got %q", got)
	}
}

func TestSessionHermeticNoHomeWrites(t *testing.T) {
	// The session verbs derive banks from cwd; they must never consult
	// $HOME. Pin that by pointing HOME at a canary dir and asserting it
	// stays empty after retain+recall through every scope.
	home := t.TempDir()
	t.Setenv("HOME", home)
	cwd := t.TempDir()
	m, err := Open(t.TempDir(), false)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer m.Close()
	for _, scope := range []Scope{ScopePerProject, ScopeGlobal, ScopePerProjectTagged} {
		if _, err := m.SessionRetain(scope, cwd, "", "herm", []string{"hermetic kubernetes memory"}, 1); err != nil {
			t.Fatalf("retain %v: %v", scope, err)
		}
		if _, _, err := m.SessionRecall(scope, cwd, "", "kubernetes", 8); err != nil {
			t.Fatalf("recall %v: %v", scope, err)
		}
	}
	if _, _, err := m.FirstTurnMemories(ScopePerProjectTagged, cwd, "", ""); err != nil {
		t.Fatalf("first-turn: %v", err)
	}
	entries, _ := os.ReadDir(home)
	if len(entries) != 0 {
		t.Errorf("HOME canary dir polluted: %v", entries)
	}
}
