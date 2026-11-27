package memory

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/FreePeak/LeanKG/internal/telemetry"
)

// seedDocs retains one row per document id in bank "b".
func seedDocs(t *testing.T, m *Memory, ids ...string) {
	t.Helper()
	entries := make([]Entry, 0, len(ids))
	for _, id := range ids {
		entries = append(entries, Entry{
			ID:       "row-" + id,
			Content:  "body of " + id,
			Metadata: map[string]any{"document_id": id},
		})
	}
	if err := m.RetainRaw(context.Background(), "b", entries); err != nil {
		t.Fatal(err)
	}
}

// TestDeleteDocumentsRemovesExactlyThoseRows: three ids remove their three
// rows, leave the fourth, rewrite the bank file once (the file is replaced),
// and record one delete event carrying the count 3.
func TestDeleteDocumentsRemovesExactlyThoseRows(t *testing.T) {
	rec := useRecorder(t, telemetry.Metadata)
	m := openTest(t)
	seedDocs(t, m, "d1", "d2", "d3", "d4")
	before, err := os.Stat(m.bankPath("b"))
	if err != nil {
		t.Fatal(err)
	}

	n, err := m.DeleteDocuments(identityCtx(), "b", []string{"d1", "d2", "d3"})
	if err != nil {
		t.Fatalf("DeleteDocuments: %v", err)
	}
	if n != 3 {
		t.Fatalf("removed = %d, want 3", n)
	}
	lines := bankLines(t, m, "b")
	if len(lines) != 1 || !strings.Contains(lines[0], "body of d4") {
		t.Fatalf("surviving rows = %v, want only the d4 row", lines)
	}
	after, err := os.Stat(m.bankPath("b"))
	if err != nil {
		t.Fatal(err)
	}
	if os.SameFile(before, after) {
		t.Fatalf("bank file was not rewritten")
	}

	evs := verbEvents(rec.memEvents(), "delete")
	if len(evs) != 1 || evs[0].Deleted != 3 || evs[0].Banks != "b" {
		t.Fatalf("delete events = %+v, want one event with Deleted=3 on bank b", evs)
	}
}

// TestDeleteDocumentsEmptyListIsNoOp: no ids means no rewrite and no event.
func TestDeleteDocumentsEmptyListIsNoOp(t *testing.T) {
	rec := useRecorder(t, telemetry.Metadata)
	m := openTest(t)
	seedDocs(t, m, "d1")
	before, err := os.Stat(m.bankPath("b"))
	if err != nil {
		t.Fatal(err)
	}

	n, err := m.DeleteDocuments(identityCtx(), "b", nil)
	if err != nil || n != 0 {
		t.Fatalf("DeleteDocuments(nil) = %d, %v; want 0, nil", n, err)
	}
	after, err := os.Stat(m.bankPath("b"))
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(before, after) {
		t.Fatalf("empty id list rewrote the bank file")
	}
	if evs := verbEvents(rec.memEvents(), "delete"); len(evs) != 0 {
		t.Fatalf("empty id list recorded delete events: %+v", evs)
	}
}

// TestDeleteDocumentsUnknownIdsAndMissingBank: ids that match nothing, and a
// bank that does not exist, delete nothing and do not touch the bank file.
func TestDeleteDocumentsUnknownIdsAndMissingBank(t *testing.T) {
	m := openTest(t)
	seedDocs(t, m, "d1")
	before, err := os.Stat(m.bankPath("b"))
	if err != nil {
		t.Fatal(err)
	}

	n, err := m.DeleteDocuments(context.Background(), "b", []string{"nope-1", "nope-2"})
	if err != nil || n != 0 {
		t.Fatalf("unknown ids = %d, %v; want 0, nil", n, err)
	}
	after, err := os.Stat(m.bankPath("b"))
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(before, after) {
		t.Fatalf("unknown ids rewrote the bank file")
	}
	if lines := bankLines(t, m, "b"); len(lines) != 1 {
		t.Fatalf("unknown ids lost rows: %v", lines)
	}

	n, err = m.DeleteDocuments(context.Background(), "missing", []string{"d1"})
	if err != nil || n != 0 {
		t.Fatalf("missing bank = %d, %v; want 0, nil", n, err)
	}
}

// TestDeleteDocumentsMatchesRedactedForm: a document id that redaction masks
// is stored in its redacted form, and the raw id the client sends still
// removes it, as DeleteDocument always did.
func TestDeleteDocumentsMatchesRedactedForm(t *testing.T) {
	m := openTest(t)
	const apiKey = "sk-abc123def456ghi789jkl"
	seedDocs(t, m, apiKey, "d2")

	n, err := m.DeleteDocuments(context.Background(), "b", []string{apiKey})
	if err != nil || n != 1 {
		t.Fatalf("delete by raw id = %d, %v; want 1, nil", n, err)
	}
	if lines := bankLines(t, m, "b"); len(lines) != 1 || !strings.Contains(lines[0], "body of d2") {
		t.Fatalf("surviving rows = %v, want only d2", lines)
	}
}

// TestDeleteDocumentsCancelledContextRecordsNothing: a cancelled context
// returns its error, removes nothing and records no event.
func TestDeleteDocumentsCancelledContextRecordsNothing(t *testing.T) {
	rec := useRecorder(t, telemetry.Metadata)
	m := openTest(t)
	seedDocs(t, m, "d1")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	n, err := m.DeleteDocuments(ctx, "b", []string{"d1"})
	if err != context.Canceled || n != 0 {
		t.Fatalf("cancelled = %d, %v; want 0, context.Canceled", n, err)
	}
	if lines := bankLines(t, m, "b"); len(lines) != 1 {
		t.Fatalf("cancelled delete removed rows: %v", lines)
	}
	if evs := verbEvents(rec.memEvents(), "delete"); len(evs) != 0 {
		t.Fatalf("cancelled delete recorded events: %+v", evs)
	}
}
