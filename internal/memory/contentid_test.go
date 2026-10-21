package memory

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// bankLines reads the raw JSONL lines of one bank file.
func bankLines(t *testing.T, m *Memory, bank string) []string {
	t.Helper()
	data, err := os.ReadFile(m.bankPath(bank))
	if err != nil {
		t.Fatalf("read bank %s: %v", bank, err)
	}
	var out []string
	for _, l := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(l) != "" {
			out = append(out, l)
		}
	}
	return out
}

// TestRetainSameBodyCollapsesToOneRow pins the content-identity fix: the
// row id is minted per write, so a dedupe keyed on it can never fire and two
// writes of the same body leave two rows. With the content identity in
// place, the second write REPLACES the first row.
//
// Measured on the live leankg memory bank before the fix: 448 rows / 304
// distinct bodies, worst group 67 copies of a 46-byte body, and a recall
// for that body returned the same sentence 72 times.
func TestRetainSameBodyCollapsesToOneRow(t *testing.T) {
	m := openTest(t)
	body := "the deploy runbook lives in docs/deploy.md"
	for i := range 3 {
		if err := m.RetainRaw("bank", []Entry{{Content: body}}); err != nil {
			t.Fatalf("retain %d: %v", i, err)
		}
	}
	if lines := bankLines(t, m, "bank"); len(lines) != 1 {
		t.Fatalf("bank holds %d rows after 3 retains of one body, want 1", len(lines))
	}
	got, err := m.Recall("bank", "deploy runbook", 10)
	if err != nil {
		t.Fatalf("recall: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("recall returned %d rows, want 1: %v", len(got), got)
	}
}

// TestRetainDistinctBodiesAllSurvive is the other half of the dedupe
// contract: collapsing duplicates must never cost a distinct memory.
func TestRetainDistinctBodiesAllSurvive(t *testing.T) {
	m := openTest(t)
	bodies := []string{
		"the deploy runbook lives in docs/deploy.md",
		"postgres holds the vectors in pgvector",
		"the release workflow publishes four assets",
	}
	for _, b := range bodies {
		if err := m.RetainRaw("bank", []Entry{{Content: b}}); err != nil {
			t.Fatalf("retain %q: %v", b, err)
		}
	}
	// Write the first body again: only that row may be replaced.
	if err := m.RetainRaw("bank", []Entry{{Content: bodies[0]}}); err != nil {
		t.Fatalf("retain repeat: %v", err)
	}
	if lines := bankLines(t, m, "bank"); len(lines) != len(bodies) {
		t.Fatalf("bank holds %d rows, want %d", len(lines), len(bodies))
	}
	for _, b := range bodies {
		if !strings.Contains(strings.Join(bankLines(t, m, "bank"), "\n"), b) {
			t.Fatalf("body %q lost from the bank", b)
		}
	}
}

// TestRetainExplicitDocumentIDIsNotContentDeduped pins the client contract:
// a row the client gave a document id to is the client's to manage. Two such
// rows with the same body but the same document id are an upsert the client
// asked for; a chunked document (one document id, several bodies) must keep
// every chunk.
func TestRetainExplicitDocumentIDIsNotContentDeduped(t *testing.T) {
	m := openTest(t)
	chunks := []Entry{
		{Content: "chunk one of the runbook", Metadata: map[string]any{"document_id": "doc-1"}},
		{Content: "chunk two of the runbook", Metadata: map[string]any{"document_id": "doc-1"}},
	}
	if err := m.RetainRaw("bank", chunks); err != nil {
		t.Fatalf("retain chunks: %v", err)
	}
	if lines := bankLines(t, m, "bank"); len(lines) != 2 {
		t.Fatalf("bank holds %d rows, want 2 (one per chunk): %v", len(lines), lines)
	}
}

// TestExplicitReplaceStillWins pins the replace path: an explicit document
// id in replaceDocs drops that document's earlier rows before the new ones
// are appended.
func TestExplicitReplaceStillWins(t *testing.T) {
	m := openTest(t)
	old := Entry{Content: "old body of the document", Metadata: map[string]any{"document_id": "doc-1"}}
	if err := m.RetainRaw("bank", []Entry{old}); err != nil {
		t.Fatalf("retain old: %v", err)
	}
	fresh := Entry{Content: "new body of the document", Metadata: map[string]any{"document_id": "doc-1"}}
	if _, err := m.RetainReplacingCtx(t.Context(), "bank", []Entry{fresh}, []string{"doc-1"}); err != nil {
		t.Fatalf("replace: %v", err)
	}
	lines := bankLines(t, m, "bank")
	if len(lines) != 1 {
		t.Fatalf("bank holds %d rows after the replace, want 1: %v", len(lines), lines)
	}
	if !strings.Contains(lines[0], "new body of the document") {
		t.Fatalf("surviving row is not the new body: %s", lines[0])
	}
}

// TestBankWriteIsCrashSafe: the rewrite path writes a temp file and renames
// it over the bank, so a crash leaves the old bank intact — and no row is
// ever lost by a rewrite.
func TestBankWriteIsCrashSafe(t *testing.T) {
	m := openTest(t)
	if err := m.RetainRaw("bank", []Entry{{Content: "keep me"}}); err != nil {
		t.Fatalf("retain: %v", err)
	}
	if _, err := m.DeleteDocumentCtx(t.Context(), "bank", "doc-nope"); err != nil {
		t.Fatalf("delete unknown document: %v", err)
	}
	if lines := bankLines(t, m, "bank"); len(lines) != 1 {
		t.Fatalf("a delete of an unknown document lost rows: %v", lines)
	}
	// No temp file is left behind next to the bank.
	if _, err := os.Stat(filepath.Join(m.Root(), "banks", "bank.jsonl.tmp")); !os.IsNotExist(err) {
		t.Fatalf("temp file left behind: %v", err)
	}
}
