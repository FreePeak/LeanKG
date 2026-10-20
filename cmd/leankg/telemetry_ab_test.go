package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/FreePeak/LeanKG/internal/telemetry"
)

// abStore records InsertABRuns and delegates everything else to a nil Store,
// so any other call panics and fails the test loudly.
type abStore struct {
	telemetry.Store
	got []telemetry.ABRun
	err error
}

func (s *abStore) InsertABRuns(_ context.Context, rs []telemetry.ABRun) error {
	if s.err != nil {
		return s.err
	}
	s.got = append(s.got, rs...)
	return nil
}

func writeAB(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, "results"), 0o755); err != nil {
		t.Fatal(err)
	}
	sha := strings.Repeat("a", 40)
	p64 := strings.Repeat("b", 64)
	rows := `{"repo":"gin","arm":"with","run_idx":1,"repo_sha":"` + sha + `","prompt_sha256":"` + p64 + `","valid":true,"input_tokens":10,"output_tokens":5,"num_turns":2,"duration_s":3,"total_cost_usd":0.1,"tool_calls":1,"file_reads":0}` + "\n" +
		`{"repo":"gin","arm":"without","run_idx":1,"repo_sha":"` + sha + `","prompt_sha256":"` + p64 + `","valid":false,"invalid_reason":"mcp_leaked_into_without_arm","input_tokens":20,"output_tokens":5}` + "\n"
	if err := os.WriteFile(filepath.Join(dir, "results", "gin.jsonl"), []byte(rows), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestImportABDirInsertsParsedRuns(t *testing.T) {
	dir := t.TempDir()
	writeAB(t, dir)
	st := &abStore{}
	var out strings.Builder
	if err := importABDir(context.Background(), dir, st, time.Now(), &out); err != nil {
		t.Fatal(err)
	}
	if len(st.got) != 2 {
		t.Fatalf("inserted %d runs, want 2", len(st.got))
	}
	valid := 0
	for _, r := range st.got {
		if r.Valid {
			valid++
		}
	}
	if valid != 1 {
		t.Fatalf("valid=%d want 1 (leak-flagged run must stay invalid)", valid)
	}
	summary := out.String()
	for _, want := range []string{"2 runs", "1 valid", "1 invalid"} {
		if !strings.Contains(summary, want) {
			t.Fatalf("summary %q lacks %q", summary, want)
		}
	}
}

func TestImportABDirEmptyDirInsertsNothing(t *testing.T) {
	st := &abStore{}
	var out strings.Builder
	if err := importABDir(context.Background(), t.TempDir(), st, time.Now(), &out); err != nil {
		t.Fatal(err)
	}
	if len(st.got) != 0 || !strings.Contains(out.String(), "0 runs") {
		t.Fatalf("got=%d summary=%q", len(st.got), out.String())
	}
}

func TestImportABDirPropagatesStoreError(t *testing.T) {
	dir := t.TempDir()
	writeAB(t, dir)
	st := &abStore{err: errors.New("disk full")}
	var out strings.Builder
	if err := importABDir(context.Background(), dir, st, time.Now(), &out); err == nil {
		t.Fatal("store error must surface to the caller")
	}
}

func TestImportABDirMissingDirIsError(t *testing.T) {
	var out strings.Builder
	if err := importABDir(context.Background(), filepath.Join(t.TempDir(), "nope"), &abStore{}, time.Now(), &out); err == nil {
		t.Fatal("missing dir must be an error")
	}
}
