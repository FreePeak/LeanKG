package memory

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestReadsHonourCancelledContext pins that Search and Space pass the caller's
// ctx to the database, so a cancelled ctx surfaces as context.Canceled.
func TestReadsHonourCancelledContext(t *testing.T) {
	m := openTest(t)
	if _, err := m.Search(cancelledCtx(), "alpha", 5); !errors.Is(err, context.Canceled) {
		t.Errorf("Search err = %v, want context.Canceled", err)
	}
	if _, err := m.Space(cancelledCtx()); !errors.Is(err, context.Canceled) {
		t.Errorf("Space err = %v, want context.Canceled", err)
	}
}

// TestCreateHonourCancelledContextWritesNothing pins that a cancelled ctx stops
// Create before any file is written.
func TestCreateHonourCancelledContextWritesNothing(t *testing.T) {
	m := openTest(t)
	if err := m.Create(cancelledCtx(), "topics/cancelled.md", "alpha"); !errors.Is(err, context.Canceled) {
		t.Fatalf("Create err = %v, want context.Canceled", err)
	}
	if _, err := os.Stat(filepath.Join(m.Root(), "topics", "cancelled.md")); !os.IsNotExist(err) {
		t.Errorf("cancelled Create left a file behind (stat err = %v)", err)
	}
}

// TestRetainedBankRowIsIndexedWithLiveContext pins that a row retained with a
// live ctx is in the bank file, in the FTS index, and returned by recall.
func TestRetainedBankRowIsIndexedWithLiveContext(t *testing.T) {
	ctx := context.Background()
	m := openTest(t)
	const bank = "ctx-bank"
	if err := m.RetainRaw(ctx, bank, []Entry{{Content: "zebra crossing notes"}}); err != nil {
		t.Fatal(err)
	}
	if lines, indexed := bankRowCounts(t, m, bank); lines != 1 || indexed != 1 {
		t.Errorf("bank file lines = %d, indexed rows = %d; want 1 and 1", lines, indexed)
	}
	got, err := m.RecallBanks(ctx, []string{bank}, "zebra", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Content != "zebra crossing notes" {
		t.Errorf("RecallBanks = %+v, want the retained row", got)
	}
}

// writeDuplicateBank writes a bank file the way a bank written before the
// content identity existed looks: duplicate bodies, no content id.
func writeDuplicateBank(t *testing.T, m *Memory, bank string) string {
	t.Helper()
	rows := []Entry{
		{ID: "a1", Content: "the deploy runbook lives in docs/deploy.md", Timestamp: 1},
		{ID: "a2", Content: "the deploy runbook lives in docs/deploy.md", Timestamp: 2},
		{ID: "b1", Content: "postgres holds the vectors in pgvector", Timestamp: 3},
	}
	var buf strings.Builder
	enc := json.NewEncoder(&buf)
	for _, r := range rows {
		if err := enc.Encode(r); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Dir(m.bankPath(bank)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(m.bankPath(bank), []byte(buf.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	return m.bankPath(bank)
}

// TestCompactHonourCancelledContextLeavesBanksUntouched pins that Compact
// returns the ctx error before it rewrites any bank file.
func TestCompactHonourCancelledContextLeavesBanksUntouched(t *testing.T) {
	m := openTest(t)
	path := writeDuplicateBank(t, m, "ctx-compact")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Compact(cancelledCtx()); !errors.Is(err, context.Canceled) {
		t.Fatalf("Compact err = %v, want context.Canceled", err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Errorf("cancelled Compact rewrote the bank file:\nbefore: %q\nafter:  %q", before, after)
	}
}

// TestCompactBanksStopsOnCancelledContext pins the per-bank ctx check in the
// compactBanks loop: a cancelled ctx rewrites no bank and returns its error.
func TestCompactBanksStopsOnCancelledContext(t *testing.T) {
	m := openTest(t)
	path := writeDuplicateBank(t, m, "ctx-compact")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.compactBanks(cancelledCtx(), ""); !errors.Is(err, context.Canceled) {
		t.Fatalf("compactBanks err = %v, want context.Canceled", err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Errorf("cancelled compactBanks rewrote the bank file:\nbefore: %q\nafter:  %q", before, after)
	}
}

// TestOpenHonourCancelledContextCreatesNothing pins that Open checks ctx before
// it creates the memory root, MEMORY.md, USER.md or topics/.
func TestOpenHonourCancelledContextCreatesNothing(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "proj")
	if _, err := Open(cancelledCtx(), dir, false); !errors.Is(err, context.Canceled) {
		t.Fatalf("Open err = %v, want context.Canceled", err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("cancelled Open left %s behind (stat err = %v)", dir, err)
	}
}

// TestReindexIndexesFileUnderCancelledContext pins the detach invariant: the
// file is already on disk, so reindex ignores a cancelled ctx and the FTS rows
// match the file.
func TestReindexIndexesFileUnderCancelledContext(t *testing.T) {
	m := openTest(t)
	const key = "topics/x.md"
	if err := os.WriteFile(filepath.Join(m.Root(), "topics", "x.md"), []byte("alpha line\nbeta line\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := m.reindex(cancelledCtx(), key); err != nil {
		t.Fatalf("reindex err = %v, want nil", err)
	}
	var indexed int
	if err := m.fts.QueryRow(`SELECT COUNT(*) FROM memory_fts WHERE kind = 'file' AND path = ?`, key).Scan(&indexed); err != nil {
		t.Fatal(err)
	}
	if indexed != 2 {
		t.Errorf("indexed file rows = %d, want 2 (one per line of %s)", indexed, key)
	}
}

// TestIndexAppendIndexesRowsUnderCancelledContext pins the detach invariant for
// bank appends: the row is already in the JSONL, so indexAppendLocked ignores a
// cancelled ctx and the index holds the row.
func TestIndexAppendIndexesRowsUnderCancelledContext(t *testing.T) {
	m := openTest(t)
	const bank = "ctx-append"
	entry := Entry{ID: "r1", Content: "gamma row", Timestamp: 1}
	if err := os.MkdirAll(filepath.Dir(m.bankPath(bank)), 0o755); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(m.bankPath(bank), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.NewEncoder(f).Encode(entry); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	m.bankMu.Lock()
	err = m.indexAppendLocked(cancelledCtx(), bank, []Entry{entry})
	m.bankMu.Unlock()
	if err != nil {
		t.Fatalf("indexAppendLocked err = %v, want nil", err)
	}
	if lines, indexed := bankRowCounts(t, m, bank); lines != 1 || indexed != 1 {
		t.Errorf("bank file lines = %d, indexed rows = %d; want 1 and 1", lines, indexed)
	}
}

// TestViewAndSnapshotHonourCancelledContext pins that View and Snapshot return
// ctx.Err() before they read any file.
func TestViewAndSnapshotHonourCancelledContext(t *testing.T) {
	m := openTest(t)
	if _, err := m.View(cancelledCtx(), "MEMORY.md", 0); !errors.Is(err, context.Canceled) {
		t.Errorf("View err = %v, want context.Canceled", err)
	}
	if _, err := m.Snapshot(cancelledCtx()); !errors.Is(err, context.Canceled) {
		t.Errorf("Snapshot err = %v, want context.Canceled", err)
	}
}

// TestDeleteRowsRemovesFileRowsUnderCancelledContext pins the detach invariant
// for file rows: deleteRows ignores a cancelled ctx and clears the index rows.
func TestDeleteRowsRemovesFileRowsUnderCancelledContext(t *testing.T) {
	m := openTest(t)
	const key = "topics/y.md"
	if err := os.WriteFile(filepath.Join(m.Root(), "topics", "y.md"), []byte("alpha line\nbeta line\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := m.reindex(context.Background(), key); err != nil {
		t.Fatal(err)
	}
	if err := m.deleteRows(cancelledCtx(), key); err != nil {
		t.Fatalf("deleteRows err = %v, want nil", err)
	}
	var indexed int
	if err := m.fts.QueryRow(`SELECT COUNT(*) FROM memory_fts WHERE kind = 'file' AND path = ?`, key).Scan(&indexed); err != nil {
		t.Fatal(err)
	}
	if indexed != 0 {
		t.Errorf("indexed file rows = %d, want 0 after deleteRows", indexed)
	}
}

// TestReindexBankLockedIndexesRewrittenBankUnderCancelledContext pins the detach
// invariant for bank rewrites: the file is already rewritten, so
// reindexBankLocked ignores a cancelled ctx and the index matches the file.
func TestReindexBankLockedIndexesRewrittenBankUnderCancelledContext(t *testing.T) {
	m := openTest(t)
	const bank = "ctx-rewrite"
	if err := m.RetainRaw(context.Background(), bank, []Entry{{Content: "delta one"}, {Content: "delta two"}}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(m.bankPath(bank))
	if err != nil {
		t.Fatal(err)
	}
	firstRow := strings.SplitN(string(data), "\n", 2)[0]
	if err := os.WriteFile(m.bankPath(bank), []byte(firstRow+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	m.bankMu.Lock()
	err = m.reindexBankLocked(cancelledCtx(), bank)
	m.bankMu.Unlock()
	if err != nil {
		t.Fatalf("reindexBankLocked err = %v, want nil", err)
	}
	if lines, indexed := bankRowCounts(t, m, bank); lines != 1 || indexed != 1 {
		t.Errorf("bank file lines = %d, indexed rows = %d; want 1 and 1", lines, indexed)
	}
}
