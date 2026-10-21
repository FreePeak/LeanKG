package store

import (
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestPruneEmbedRunsClosesStuckRunsAndKeepsTheNewestRow is the regression for
// an append-only ledger: every `leankg-embed run` appended a row and nothing
// ever removed one, and a run that was killed mid-flight stayed in state
// "running" forever — which is what `status` then reported as the last embed
// run. Measured live: 36 rows in the leankg store, one of them (id 21) still
// marked "running" with dirty=1620.
func TestPruneEmbedRunsClosesStuckRunsAndKeepsTheNewestRow(t *testing.T) {
	s := openTestStore(t)
	first, err := s.StartEmbedRun("model-a", "incremental", 10)
	if err != nil {
		t.Fatalf("start run: %v", err)
	}
	if err := s.FinishEmbedRun(first, "completed", 10, 0, 0, 0, 0); err != nil {
		t.Fatalf("finish run: %v", err)
	}
	second, err := s.StartEmbedRun("model-a", "full", 5)
	if err != nil {
		t.Fatalf("start run: %v", err)
	}
	if err := s.FinishEmbedRun(second, "completed", 5, 0, 0, 0, 0); err != nil {
		t.Fatalf("finish run: %v", err)
	}
	stuck, err := s.StartEmbedRun("model-b", "incremental", 7)
	if err != nil {
		t.Fatalf("start run: %v", err)
	}

	// A cutoff in the future: every run is older than it.
	pr, err := s.PruneEmbedRuns(time.Now().Add(time.Hour))
	if err != nil {
		t.Fatalf("prune: %v", err)
	}
	if pr.Closed != 1 {
		t.Errorf("closed %d stuck run(s), want 1", pr.Closed)
	}
	if pr.Pruned != 2 {
		t.Errorf("pruned %d finished run(s), want 2", pr.Pruned)
	}

	// The newest row is never deleted: it names the store's current
	// collection, and the stale-collection sweep derives its keep set from it.
	run, err := s.LastEmbedRunAny()
	if err != nil {
		t.Fatalf("last run: %v", err)
	}
	if run == nil || run.ID != stuck {
		t.Fatalf("the newest run was pruned: %+v", run)
	}
	// The stuck run is closed out, not deleted: the history of the
	// interrupted run survives.
	var status string
	if err := s.db.QueryRow(`SELECT status FROM embed_runs WHERE id = ?`, stuck).Scan(&status); err != nil {
		t.Fatalf("read status: %v", err)
	}
	if status == "running" {
		t.Error("the stuck run still claims to be running")
	}
}

// TestPruneEmbedRunsKeepsRunsInsideTheWindow: the prune is a retention sweep,
// not a purge — a run that started inside the window is still history.
func TestPruneEmbedRunsKeepsRunsInsideTheWindow(t *testing.T) {
	s := openTestStore(t)
	id, err := s.StartEmbedRun("model-a", "incremental", 4)
	if err != nil {
		t.Fatalf("start run: %v", err)
	}
	if err := s.FinishEmbedRun(id, "completed", 4, 0, 0, 0, 0); err != nil {
		t.Fatalf("finish run: %v", err)
	}
	pr, err := s.PruneEmbedRuns(time.Now().Add(-time.Hour))
	if err != nil {
		t.Fatalf("prune: %v", err)
	}
	if pr.Pruned != 0 || pr.Closed != 0 {
		t.Errorf("prune = %+v, want nothing removed", pr)
	}
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM embed_runs`).Scan(&n); err != nil {
		t.Fatalf("count runs: %v", err)
	}
	if n != 1 {
		t.Errorf("the run inside the window was removed: %d rows left", n)
	}
}

// TestCompactFTSKeepsEveryRow: FTS5 keeps its index as a list of b-tree
// segments and every write adds one, so the keyword rung searches a growing
// segment list. Measured live: the leankg store carried a 9.4 MB index over
// 3,065 segments — 31 % of the 138 MB store — and 'optimize' was never
// issued anywhere in the repo.
func TestCompactFTSKeepsEveryRow(t *testing.T) {
	s := openTestStore(t)
	for i := range 200 {
		if err := s.UpsertElements([]Element{{
			QualifiedName: "pkg.Fn" + strconv.Itoa(i),
			ElementType:   "function",
			Name:          "Fn" + strconv.Itoa(i),
			FilePath:      "src/f" + strconv.Itoa(i) + ".go",
			Language:      "go",
			Content:       strings.Repeat("x", 200) + strconv.Itoa(i),
		}}); err != nil {
			t.Fatalf("upsert: %v", err)
		}
	}
	var before int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM elements_fts`).Scan(&before); err != nil {
		t.Fatalf("count fts rows: %v", err)
	}
	if before == 0 {
		t.Fatal("the fixture wrote no keyword rows")
	}
	if err := s.CompactFTS(); err != nil {
		t.Fatalf("compact: %v", err)
	}
	var after int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM elements_fts`).Scan(&after); err != nil {
		t.Fatalf("count fts rows: %v", err)
	}
	if after != before {
		t.Errorf("the compaction changed the keyword index: %d rows -> %d", before, after)
	}
	n, err := s.ElementCount()
	if err != nil {
		t.Fatalf("element count: %v", err)
	}
	if n != 200 {
		t.Errorf("the compaction changed the element count: %d", n)
	}
}

// TestCompactFTSRefusesAReadOnlyHandle: compaction rewrites the index, so a
// read-only handle must refuse it rather than fail halfway.
func TestCompactFTSRefusesAReadOnlyHandle(t *testing.T) {
	dir := t.TempDir()
	rw, err := Open(filepath.Join(dir, "leankg.db"), RW)
	if err != nil {
		t.Fatalf("open rw: %v", err)
	}
	if err := rw.Migrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if err := rw.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	ro, err := Open(filepath.Join(dir, "leankg.db"), RO)
	if err != nil {
		t.Fatalf("open ro: %v", err)
	}
	defer ro.Close()
	if err := ro.CompactFTS(); err == nil {
		t.Error("a read-only handle compacted the keyword index")
	}
}
