package memory

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestCompactRewritesTheBankFile pins C.3: Compact touched only the FTS
// side, so the JSONL bank — the source of truth on disk — kept every
// duplicate and grew forever (measured: ~88.6 KB/day, ~45 rows/day).
//
// The fixture writes the bank file directly, the way a bank written before
// the content identity existed looks: duplicate bodies, no content id.
func TestCompactRewritesTheBankFile(t *testing.T) {
	m := openTest(t)
	rows := []Entry{
		{ID: "a1", Content: "the deploy runbook lives in docs/deploy.md", Timestamp: 1},
		{ID: "a2", Content: "the deploy runbook lives in docs/deploy.md", Timestamp: 2},
		{ID: "b1", Content: "postgres holds the vectors in pgvector", Timestamp: 3},
	}
	var buf strings.Builder
	enc := json.NewEncoder(&buf)
	for _, r := range rows {
		if err := enc.Encode(r); err != nil {
			t.Fatalf("encode fixture: %v", err)
		}
	}
	if err := os.MkdirAll(filepath.Dir(m.bankPath("bank")), 0o755); err != nil {
		t.Fatalf("create banks dir: %v", err)
	}
	if err := os.WriteFile(m.bankPath("bank"), []byte(buf.String()), 0o644); err != nil {
		t.Fatalf("write fixture bank: %v", err)
	}
	if err := m.Compact(context.Background()); err != nil {
		t.Fatalf("compact: %v", err)
	}
	lines := bankLines(t, m, "bank")
	if len(lines) != 2 {
		t.Fatalf("bank holds %d rows after compaction, want 2 (one per distinct body): %v", len(lines), lines)
	}
	joined := strings.Join(lines, "\n")
	for _, body := range []string{"the deploy runbook lives in docs/deploy.md", "postgres holds the vectors in pgvector"} {
		if !strings.Contains(joined, body) {
			t.Fatalf("compaction lost the body %q: %v", body, lines)
		}
	}
	// No temp file is left behind, and the index was re-derived from the
	// rewritten file.
	if _, err := os.Stat(m.bankPath("bank") + ".tmp"); !os.IsNotExist(err) {
		t.Fatalf("temp file left behind: %v", err)
	}
	got, err := m.Recall(context.Background(), "bank", "postgres vectors", 5)
	if err != nil {
		t.Fatalf("recall: %v", err)
	}
	if len(got) != 1 || got[0].Content != "postgres holds the vectors in pgvector" {
		t.Fatalf("recall after compaction = %v, want the one surviving row", got)
	}
}

// preRedactionBank writes raw JSONL rows straight into a bank file, the way a
// store written before write-side redaction existed looks on disk.
func preRedactionBank(t *testing.T, m *Memory, bank string, rows ...Entry) {
	t.Helper()
	var buf strings.Builder
	enc := json.NewEncoder(&buf)
	for _, r := range rows {
		if err := enc.Encode(r); err != nil {
			t.Fatalf("encode fixture: %v", err)
		}
	}
	if err := os.MkdirAll(filepath.Dir(m.bankPath(bank)), 0o755); err != nil {
		t.Fatalf("create banks dir: %v", err)
	}
	if err := os.WriteFile(m.bankPath(bank), []byte(buf.String()), 0o644); err != nil {
		t.Fatalf("write fixture bank: %v", err)
	}
}

// TestCompactRedactsPreRedactionRows pins the #591 compaction gap: compaction
// keyed raw rows, so a row written before redaction and its redacted twin
// (the same memory written after the fix) had different keys and both
// survived, and the raw secret stayed on disk. Compaction must key and write
// the redacted form, so the pair collapses to one redacted row.
func TestCompactRedactsPreRedactionRows(t *testing.T) {
	m := openTest(t)
	const (
		raw    = "probe note keeps lkg_0123456789abcdef0123456789abcdef_0a1b2c3d for later"
		twin   = "probe note keeps [REDACTED:key] for later"
		secret = "0123456789abcdef0123456789abcdef"
	)
	preRedactionBank(t, m, "pair",
		Entry{ID: "r1", Content: raw, Timestamp: 1},
		Entry{ID: "r2", Content: twin, Timestamp: 2},
	)
	preRedactionBank(t, m, "solo", Entry{ID: "s1", Content: raw, Timestamp: 1})
	if err := m.Compact(context.Background()); err != nil {
		t.Fatalf("compact: %v", err)
	}
	pair := bankLines(t, m, "pair")
	if len(pair) != 1 {
		t.Fatalf("pair bank holds %d rows after compaction, want 1 (the raw row and its redacted twin are one memory): %v", len(pair), pair)
	}
	if !strings.Contains(pair[0], twin) || strings.Contains(pair[0], secret) {
		t.Fatalf("surviving pair row is not the redacted form: %s", pair[0])
	}
	solo := strings.Join(bankLines(t, m, "solo"), "\n")
	if strings.Contains(solo, secret) {
		t.Fatalf("compaction left the raw secret on disk: %s", solo)
	}
	if !strings.Contains(solo, twin) {
		t.Fatalf("compaction did not write the redacted content: %s", solo)
	}
}

// TestCompactDropsVectorOfRedactedRow pins the stale-vector half: a vector
// embedded from the raw body describes text that is no longer on disk, so a
// row whose content redaction changed loses its vector and is re-embedded
// from the redacted body on the next recall.
func TestCompactDropsVectorOfRedactedRow(t *testing.T) {
	m := openTest(t)
	const raw = "probe note keeps lkg_0123456789abcdef0123456789abcdef_0a1b2c3d for later"
	preRedactionBank(t, m, "solo", Entry{ID: "s1", Content: raw, Timestamp: 1})
	if _, err := m.fts.Exec(`INSERT INTO memory_fts(kind, path, bank, entry_id, doc, body) VALUES ('bank', '', 'solo', 's1', '{}', ?)`, raw); err != nil {
		t.Fatalf("seed index row: %v", err)
	}
	seedVector(t, m, "model-a", "solo", "s1")
	if err := m.Compact(context.Background()); err != nil {
		t.Fatalf("compact: %v", err)
	}
	var n int
	if err := m.fts.QueryRow(`SELECT COUNT(*) FROM memory_vectors WHERE bank = 'solo' AND entry_id = 's1'`).Scan(&n); err != nil {
		t.Fatalf("count vectors: %v", err)
	}
	if n != 0 {
		t.Fatalf("vector of the redacted row survived compaction: %d rows, want 0", n)
	}
}

// TestRetainReplacesPreRedactionTwin pins the same gap on the write path: the
// write-time dedupe keyed raw rows, so a redacted write did not replace the
// raw row it supersedes.
func TestRetainReplacesPreRedactionTwin(t *testing.T) {
	m := openTest(t)
	const raw = "probe note keeps lkg_0123456789abcdef0123456789abcdef_0a1b2c3d for later"
	preRedactionBank(t, m, "solo", Entry{ID: "s1", Content: raw, Timestamp: 1})
	if err := m.RetainRaw(context.Background(), "solo", []Entry{{Content: raw}}); err != nil {
		t.Fatalf("retain: %v", err)
	}
	lines := bankLines(t, m, "solo")
	if len(lines) != 1 {
		t.Fatalf("bank holds %d rows after a redacted rewrite of a pre-redaction row, want 1: %v", len(lines), lines)
	}
	if strings.Contains(strings.Join(lines, "\n"), "0123456789abcdef0123456789abcdef") {
		t.Fatalf("raw secret still on disk after retain: %v", lines)
	}
}

// writeRawBankLines writes lines to a bank file verbatim, the way a bank
// written before write-side redaction looks on disk.
func writeRawBankLines(t *testing.T, m *Memory, bank string, lines ...string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(m.bankPath(bank)), 0o755); err != nil {
		t.Fatalf("create banks dir: %v", err)
	}
	if err := os.WriteFile(m.bankPath(bank), []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatalf("write fixture bank: %v", err)
	}
}

// TestCompactKeepsUntouchedRowBytes pins that compaction rewrites only the
// rows it changes. A row with a field Entry does not model lost that field
// when every row was re-marshaled through Entry, so a bank with no duplicates
// and nothing to redact was rewritten in full. Such a bank must come back
// byte for byte, in the same file.
func TestCompactKeepsUntouchedRowBytes(t *testing.T) {
	m := openTest(t)
	const row = `{"id":"u1","content":"unknown field survives compaction","source":"test","timestamp":1,"importance":0.5,"cwd":"","metadata":{},"legacy_field":"keep-me"}`
	writeRawBankLines(t, m, "untouched", row)
	before, err := os.Stat(m.bankPath("untouched"))
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Compact(context.Background()); err != nil {
		t.Fatalf("compact: %v", err)
	}
	data, err := os.ReadFile(m.bankPath("untouched"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != row+"\n" {
		t.Fatalf("compaction rewrote an untouched row:\n got %s\nwant %s", data, row)
	}
	after, err := os.Stat(m.bankPath("untouched"))
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(before, after) {
		t.Fatalf("compaction replaced the bank file of an untouched bank")
	}
}

// TestCompactRedactsRowIDs pins that a pre-redaction row whose id carries a
// secret shape leaves the file with the masked id, the same form a write
// stores. The row id is redacted like its content.
func TestCompactRedactsRowIDs(t *testing.T) {
	m := openTest(t)
	const secret = "sk-abc123def456ghi789jkl"
	writeRawBankLines(t, m, "ids", `{"id":"`+secret+`-1","content":"plain body","source":"test","timestamp":1,"importance":0.5,"cwd":"","metadata":{}}`)
	if err := m.Compact(context.Background()); err != nil {
		t.Fatalf("compact: %v", err)
	}
	data, err := os.ReadFile(m.bankPath("ids"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), secret) {
		t.Fatalf("compaction left the raw key in the row id: %s", data)
	}
}

// TestCompactPrefixLeavesOtherBanksAlone pins the scoped pass: CompactPrefix
// rewrites only the banks whose name carries the prefix, so the post-sync
// compaction never rewrites a bank the user owns. Compact (an empty prefix)
// still rewrites every bank.
func TestCompactPrefixLeavesOtherBanksAlone(t *testing.T) {
	m := openTest(t)
	ownPath := writeDuplicateBank(t, m, "cc-own")
	foreignPath := writeDuplicateBank(t, m, "other-bank")
	foreignBefore, err := os.ReadFile(foreignPath)
	if err != nil {
		t.Fatal(err)
	}

	if err := m.CompactPrefix(context.Background(), "cc-"); err != nil {
		t.Fatalf("CompactPrefix: %v", err)
	}
	if got := bankLines(t, m, "cc-own"); len(got) != 2 {
		t.Fatalf("cc- bank holds %d rows after CompactPrefix, want 2 (duplicate collapsed): %v", len(got), got)
	}
	foreignAfter, err := os.ReadFile(foreignPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(foreignAfter) != string(foreignBefore) {
		t.Fatalf("CompactPrefix rewrote a bank outside the prefix:\nbefore: %q\nafter:  %q", foreignBefore, foreignAfter)
	}
	if _, err := os.Stat(ownPath); err != nil {
		t.Fatal(err)
	}

	if err := m.Compact(context.Background()); err != nil {
		t.Fatalf("Compact: %v", err)
	}
	if got := bankLines(t, m, "other-bank"); len(got) != 2 {
		t.Fatalf("Compact (no prefix) left %d rows in other-bank, want 2: %v", len(got), got)
	}
}
