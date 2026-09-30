package maintain

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/FreePeak/LeanKG/internal/store"
)

// bloatFixture opens a migrated store whose freelist is most of the file, so
// a pass has something real to reclaim.
func bloatFixture(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "leankg.db"), store.RW)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.Migrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	for i := 0; i < 400; i++ {
		p := "src/f" + string(rune('a'+i%26)) + itoa(i) + ".go"
		if err := st.UpsertElements([]store.Element{{
			QualifiedName: "pkg.Fn" + itoa(i),
			ElementType:   "function",
			Name:          "Fn" + itoa(i),
			FilePath:      p,
			Language:      "go",
			Content:       strings.Repeat("x", 200) + itoa(i),
		}}); err != nil {
			t.Fatalf("upsert: %v", err)
		}
	}
	for i := 0; i < 350; i++ {
		p := "src/f" + string(rune('a'+i%26)) + itoa(i) + ".go"
		if err := st.DeleteByFile(p); err != nil {
			t.Fatalf("delete: %v", err)
		}
	}
	return st
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	return string(b)
}

func TestPassReclaimsFreelist(t *testing.T) {
	st := bloatFixture(t)
	var logs int
	res, err := Pass(st, Options{Logf: func(string, ...any) { logs++ }})
	if err != nil {
		t.Fatalf("pass: %v", err)
	}
	if !res.Checkpointed {
		t.Error("pass did not checkpoint: Checkpointed=false")
	}
	// Vacuumed reports only that the bounded incremental sweep ran. On a legacy
	// store it may legitimately not: the one-time header upgrade's own VACUUM
	// is what reclaims a big freelist, and it runs before the sweep.
	if res.Reclaimed <= 0 {
		t.Fatalf("pass reclaimed nothing: %+v", res)
	}
	if res.After >= res.Before {
		t.Errorf("no shrink: %d -> %d", res.Before, res.After)
	}
	if res.FreeAfter >= res.FreeBefore {
		t.Errorf("freelist did not drain: %d -> %d", res.FreeBefore, res.FreeAfter)
	}
	if logs == 0 {
		t.Error("expected a reclaim log line")
	}
	// Data survives the pass.
	if n, err := st.ElementCount(); err != nil || n != 50 {
		t.Errorf("element count = %d (err %v), want 50", n, err)
	}
}

func TestPassRunsTheIncrementalSweepOnceAutoVacuumIsOn(t *testing.T) {
	st := bloatFixture(t)
	// First pass upgrades the header; then make a new freelist so the second
	// pass has something the bounded sweep must reclaim.
	if _, err := Pass(st, Options{}); err != nil {
		t.Fatalf("first pass: %v", err)
	}
	for i := 350; i < 400; i++ {
		p := "src/f" + string(rune('a'+i%26)) + itoa(i) + ".go"
		if err := st.DeleteByFile(p); err != nil {
			t.Fatalf("delete: %v", err)
		}
	}
	// MinFreeBytes=1 bypasses the 1 MB floor: the point here is the sweep
	// itself, and the floor has its own test below.
	res, err := Pass(st, Options{MinFreeBytes: 1})
	if err != nil {
		t.Fatalf("second pass: %v", err)
	}
	if !res.Vacuumed {
		t.Errorf("second pass skipped the sweep: %+v", res)
	}
	if res.FreeAfter != 0 {
		t.Errorf("sweep left %d free bytes", res.FreeAfter)
	}
}

func TestPassSkipsReclaimBelowFloor(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "leankg.db"), store.RW)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer st.Close()
	if err := st.Migrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	res, err := Pass(st, Options{})
	if err != nil {
		t.Fatalf("pass: %v", err)
	}
	if res.Vacuumed {
		t.Errorf("empty store was swept: %+v", res)
	}
	if !res.Checkpointed {
		t.Error("an empty store should still be checkpointed")
	}
}

func TestPassHeaderUpgradeIsOneShot(t *testing.T) {
	st := bloatFixture(t)
	if _, err := Pass(st, Options{}); err != nil {
		t.Fatalf("pass: %v", err)
	}
	before, err := st.Space()
	if err != nil {
		t.Fatalf("space: %v", err)
	}
	res, err := Pass(st, Options{})
	if err != nil {
		t.Fatalf("second pass: %v", err)
	}
	if res.Reclaimed != 0 {
		t.Errorf("second pass reclaimed %d bytes; the header upgrade must not repeat", res.Reclaimed)
	}
	after, err := st.Space()
	if err != nil {
		t.Fatalf("space after: %v", err)
	}
	if after.SizeBytes != before.SizeBytes {
		t.Errorf("file moved on the second pass: %d -> %d", before.SizeBytes, after.SizeBytes)
	}
}

func TestRunIsDisabledByNonPositiveInterval(t *testing.T) {
	st := bloatFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// A non-positive interval must start nothing: LEANKG_VACUUM_INTERVAL_HOURS=0
	// promises the loop is disabled.
	Run(ctx, st, nil, Options{}, 0)
	// Give a wrongly-started goroutine far more than enough time to fire.
	time.Sleep(100 * time.Millisecond)
	rep, err := st.Space()
	if err != nil {
		t.Fatalf("space: %v", err)
	}
	if rep.FreeBytes == 0 {
		t.Fatal("a disabled loop still compacted the store")
	}
}

func TestCompactMemoryNilIsNoOp(t *testing.T) {
	// Memory is optional (`serve --memory`); a nil *Memory must not panic.
	CompactMemory(nil, Options{})
}
