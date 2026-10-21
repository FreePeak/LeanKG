package memory

import (
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
	if err := m.Compact(); err != nil {
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
	got, err := m.Recall("bank", "postgres vectors", 5)
	if err != nil {
		t.Fatalf("recall: %v", err)
	}
	if len(got) != 1 || got[0].Content != "postgres holds the vectors in pgvector" {
		t.Fatalf("recall after compaction = %v, want the one surviving row", got)
	}
}
