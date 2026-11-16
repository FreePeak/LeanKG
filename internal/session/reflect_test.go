package session

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/FreePeak/LeanKG/internal/memory"
)

func readLessons(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(raw)
}

// The entry format is the Rust contract (GraphEngine::report_query_outcome):
// a blank line, a "## <unix> — <outcome>" heading, then the Question/Nodes/
// Outcome bullet block, the optional Note line, and a trailing blank line.
func TestReflectOutcomeAppendsRustEntry(t *testing.T) {
	dir := t.TempDir()
	path, err := ReflectOutcome(context.Background(), dir, "where is auth?", []string{"src/a.rs::auth", "src/b.rs::login"}, "useful", "found it")
	if err != nil {
		t.Fatalf("ReflectOutcome: %v", err)
	}
	if path != filepath.Join(dir, ".leankg", "reflections", "LESSONS.md") {
		t.Fatalf("path = %s", path)
	}
	entry := readLessons(t, path)
	pattern := regexp.MustCompile(`^\n## \d+ — useful\n\n- Question: where is auth\?\n- Nodes: src/a\.rs::auth, src/b\.rs::login\n- Outcome: useful\n- Note: found it\n\n$`)
	if !pattern.MatchString(entry) {
		t.Fatalf("entry = %q, want Rust LESSONS.md shape", entry)
	}
}

func TestReflectOutcomeNodesNoneAndNoNote(t *testing.T) {
	dir := t.TempDir()
	if _, err := ReflectOutcome(context.Background(), dir, "cold query", nil, "dead_end", ""); err != nil {
		t.Fatalf("ReflectOutcome: %v", err)
	}
	entry := readLessons(t, filepath.Join(dir, ".leankg", "reflections", "LESSONS.md"))
	if !strings.Contains(entry, "- Nodes: (none)\n") {
		t.Fatalf("missing (none) nodes line in %q", entry)
	}
	if strings.Contains(entry, "- Note:") {
		t.Fatalf("unexpected note line in %q", entry)
	}
	if !strings.HasSuffix(entry, "- Outcome: dead_end\n\n") {
		t.Fatalf("entry must end with the Outcome bullet and blank line: %q", entry)
	}
}

func TestReflectOutcomeAppendsEveryCall(t *testing.T) {
	dir := t.TempDir()
	// Rust appended unconditionally — no dedup (unlike session lessons.json).
	for _, outcome := range []string{"useful", "corrected", "useful"} {
		if _, err := ReflectOutcome(context.Background(), dir, "q", nil, outcome, ""); err != nil {
			t.Fatalf("ReflectOutcome(%s): %v", outcome, err)
		}
	}
	entry := readLessons(t, filepath.Join(dir, ".leankg", "reflections", "LESSONS.md"))
	if got := strings.Count(entry, "\n## "); got != 3 {
		t.Fatalf("entry count = %d, want 3 appended reflections:\n%s", got, entry)
	}
}

func TestReflectOutcomeCreatesProjectTree(t *testing.T) {
	// A project dir that does not exist yet (first-run layout).
	dir := filepath.Join(t.TempDir(), "proj")
	if _, err := ReflectOutcome(context.Background(), dir, "q", nil, "corrected", "note"); err != nil {
		t.Fatalf("ReflectOutcome: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".leankg", "reflections", "LESSONS.md")); err != nil {
		t.Fatalf("LESSONS.md missing: %v", err)
	}
}

// TestReflectionInvisibleToTheMemoryLayer is the K5 "honest reflect" check,
// written against the real consumer: memory.Open roots itself at
// <project>/.leankg/memory (internal/memory/memory.go:89), while reflections
// land in <project>/.leankg/reflections/LESSONS.md. If the two trees never
// meet, a reflection is a private diary -- recorded, never recalled -- and the
// package comment's claim that this file feeds the lesson source recall ranks
// is false.
func TestReflectionIsRecallableThroughTheMemoryLayer(t *testing.T) {
	dir := t.TempDir()
	if _, err := ReflectOutcome(context.Background(), dir, "where is the token budget applied", []string{"core.applyBudget"}, "useful", "found it"); err != nil {
		t.Fatalf("ReflectOutcome: %v", err)
	}
	m, err := memory.Open(context.Background(), dir, false)
	if err != nil {
		t.Fatalf("memory.Open: %v", err)
	}
	defer m.Close()
	// The reflection must be reachable the way recall reaches a memory: the
	// layer's own search over its FTS index. A raw file append would leave the
	// file present and the index empty, which is the bug this pins.
	hits, err := m.Search(context.Background(), "token budget", 10)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	var bodies string
	for _, h := range hits {
		bodies += h.Snippet + "\n"
	}
	if !strings.Contains(bodies, "where is the token budget applied") {
		t.Fatalf("a recorded reflection is invisible to the memory layer: %d hits, none mention it", len(hits))
	}
}

// TestMirroredReflectionKeepsTheRustArtifactIntact pins that the K5 mirror is
// additive: the Rust-contract file at .leankg/reflections/LESSONS.md keeps its
// exact shape (parity tests above depend on it) and the copy is what the
// memory layer indexes.
func TestMirroredReflectionKeepsTheRustArtifactIntact(t *testing.T) {
	dir := t.TempDir()
	if _, err := ReflectOutcome(context.Background(), dir, "where is auth?", nil, "useful", "n"); err != nil {
		t.Fatalf("ReflectOutcome: %v", err)
	}
	rust := readLessons(t, filepath.Join(dir, ".leankg", "reflections", "LESSONS.md"))
	pattern := regexp.MustCompile(`^\n## \d+ — useful\n\n- Question: where is auth\?\n- Nodes: \(none\)\n- Outcome: useful\n- Note: n\n\n$`)
	if !pattern.MatchString(rust) {
		t.Fatalf("Rust artifact shape changed: %q", rust)
	}
	mirrored := readLessons(t, filepath.Join(dir, ".leankg", "memory", "topics", "REFLECTIONS.md"))
	if mirrored != rust {
		t.Fatalf("mirror differs from the Rust artifact:\nrust:    %q\nmirror: %q", rust, mirrored)
	}
}

// flipCtx reports no error on its first Err call and context.Canceled on every
// call after, so a test can cancel "after the lesson is written" without timing.
type flipCtx struct {
	context.Context
	calls int
}

func (c *flipCtx) Err() error {
	c.calls++
	if c.calls > 1 {
		return context.Canceled
	}
	return nil
}

// recallBodies returns the snippets the memory layer's search finds for query.
func recallBodies(t *testing.T, dir, query string) string {
	t.Helper()
	m, err := memory.Open(context.Background(), dir, false)
	if err != nil {
		t.Fatalf("memory.Open: %v", err)
	}
	defer m.Close()
	hits, err := m.Search(context.Background(), query, 10)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	var bodies string
	for _, h := range hits {
		bodies += h.Snippet + "\n"
	}
	return bodies
}

// TestReflectOutcomeRefusesCancelledContext pins that a cancelled ctx stops the
// write before any file is touched: no lesson on disk, nothing in memory search.
func TestReflectOutcomeRefusesCancelledContext(t *testing.T) {
	dir := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := ReflectOutcome(ctx, dir, "where is the token budget applied", nil, "useful", "found it")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("ReflectOutcome err = %v, want context.Canceled", err)
	}
	if _, statErr := os.Stat(filepath.Join(dir, ".leankg", "reflections", "LESSONS.md")); !os.IsNotExist(statErr) {
		t.Fatalf("LESSONS.md written under a cancelled ctx (stat err = %v)", statErr)
	}
	if bodies := recallBodies(t, dir, "token budget"); strings.Contains(bodies, "where is the token budget applied") {
		t.Fatalf("reflection written under a cancelled ctx is in memory search: %q", bodies)
	}
}

// TestReflectionMirrorSurvivesCancelAfterLessonWritten pins that a cancel which
// lands after the lesson is on disk does not stop the memory mirror.
func TestReflectionMirrorSurvivesCancelAfterLessonWritten(t *testing.T) {
	dir := t.TempDir()
	ctx := &flipCtx{Context: context.Background()}
	if _, err := ReflectOutcome(ctx, dir, "where is the token budget applied", nil, "useful", "found it"); err != nil {
		t.Fatalf("ReflectOutcome: %v", err)
	}
	lesson := readLessons(t, filepath.Join(dir, ".leankg", "reflections", "LESSONS.md"))
	if !strings.Contains(lesson, "where is the token budget applied") {
		t.Fatalf("lesson missing from LESSONS.md: %q", lesson)
	}
	if bodies := recallBodies(t, dir, "token budget"); !strings.Contains(bodies, "where is the token budget applied") {
		t.Fatalf("reflection written before a late cancel is missing from memory: %q", bodies)
	}
}
