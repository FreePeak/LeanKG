package session

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	return New(filepath.Join(t.TempDir(), ".leankg"))
}

// TestOffloadRecallBitForBit covers text, empty, and binary payloads of
// varying sizes: Recall must return the exact offloaded bytes.
func TestOffloadRecallBitForBit(t *testing.T) {
	s := newTestStore(t)

	payloads := [][]byte{
		[]byte(`{"tool":"query_graph","ok":true}`), // text JSON
		{},                                   // empty
		[]byte{0x00, 0xff, 0x10, 0x00, 0xfe}, // small binary with NULs
	}

	for i := range 8 { // larger random blobs, 1B..64KiB
		n := 1 << (i * 2)
		b := make([]byte, n)
		if _, err := rand.Read(b); err != nil {
			t.Fatalf("rand: %v", err)
		}
		payloads = append(payloads, b)
	}

	for i, payload := range payloads {
		nodeID := fmt.Sprintf("offload-%03d", i)
		ref, err := s.Offload("sess-bit", nodeID, payload, fmt.Sprintf("payload %d", i))
		if err != nil {
			t.Fatalf("offload %s: %v", nodeID, err)
		}
		if ref.NodeID != nodeID || ref.SHA256 == "" || ref.Path == "" {
			t.Fatalf("ref incomplete: %+v", ref)
		}
		got, err := s.Recall("sess-bit", nodeID)
		if err != nil {
			t.Fatalf("recall %s: %v", nodeID, err)
		}
		if !bytes.Equal(got, payload) {
			t.Fatalf("payload %s: recall not bit-for-bit (got %d bytes, want %d)", nodeID, len(got), len(payload))
		}
	}
}

// TestRefFileLayout pins the parity layout
// <session>/refs/<node_id>.md.
func TestRefFileLayout(t *testing.T) {
	s := newTestStore(t)
	ref, err := s.Offload("sess1", "offload-001", []byte("hi"), "greeting")
	if err != nil {
		t.Fatalf("offload: %v", err)
	}
	if !filepath.IsAbs(ref.Path) {
		t.Fatalf("ref path should be absolute, got %q", ref.Path)
	}
	want := filepath.Join(s.Root, "sess1", "refs", "offload-001.md")
	if ref.Path != want {
		t.Fatalf("layout: got %q want %q", ref.Path, want)
	}
	raw, err := os.ReadFile(ref.Path)
	if err != nil || string(raw) != "hi" {
		t.Fatalf("ref file content mismatch: %q err=%v", raw, err)
	}
}

// TestRecallChecksumMismatch corrupts the ref file on disk and expects
// ErrChecksum.
func TestRecallChecksumMismatch(t *testing.T) {
	s := newTestStore(t)
	ref, err := s.Offload("sess-corrupt", "offload-001", []byte("original payload"), "orig")
	if err != nil {
		t.Fatalf("offload: %v", err)
	}
	if err := os.WriteFile(ref.Path, []byte("tampered!"), 0o644); err != nil {
		t.Fatalf("corrupt: %v", err)
	}
	_, err = s.Recall("sess-corrupt", "offload-001")
	if !errors.Is(err, ErrChecksum) {
		t.Fatalf("want ErrChecksum, got %v", err)
	}
}

// TestRecallNotFound covers a node_id absent from the index.
func TestRecallNotFound(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.Recall("nosuch", "offload-999"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
	if _, err := s.Offload("sess2", "offload-001", []byte("x"), "x"); err != nil {
		t.Fatalf("offload: %v", err)
	}
	if _, err := s.Recall("sess2", "offload-999"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

// TestCanvasListing verifies the index lists refs in offload order with
// metadata, persisted to index.json in the session dir.
func TestCanvasListing(t *testing.T) {
	s := newTestStore(t)
	for i := 1; i <= 3; i++ {
		if _, err := s.Offload("sess-canvas", fmt.Sprintf("offload-%03d", i), []byte(fmt.Sprintf("payload %d", i)), fmt.Sprintf("summary %d", i)); err != nil {
			t.Fatalf("offload %d: %v", i, err)
		}
	}
	refs, err := s.Canvas("sess-canvas")
	if err != nil {
		t.Fatalf("canvas: %v", err)
	}
	if len(refs) != 3 {
		t.Fatalf("want 3 refs, got %d", len(refs))
	}
	for i, ref := range refs {
		if ref.NodeID != fmt.Sprintf("offload-%03d", i+1) {
			t.Fatalf("order broken at %d: %s", i, ref.NodeID)
		}
		if ref.Summary != fmt.Sprintf("summary %d", i+1) {
			t.Fatalf("summary lost: %+v", ref)
		}
	}

	// index.json lives in the session dir and parses.
	raw, err := os.ReadFile(filepath.Join(s.Root, "sess-canvas", "index.json"))
	if err != nil {
		t.Fatalf("index.json: %v", err)
	}
	var idx index
	if err := json.Unmarshal(raw, &idx); err != nil {
		t.Fatalf("parse index.json: %v", err)
	}
	if len(idx.Refs) != 3 {
		t.Fatalf("index.json refs: got %d want 3", len(idx.Refs))
	}

}

// TestCanvasEmptySession checks an unknown session returns an empty,
// non-nil canvas without error.
func TestCanvasEmptySession(t *testing.T) {
	s := newTestStore(t)
	refs, err := s.Canvas("ghost")
	if err != nil {
		t.Fatalf("canvas: %v", err)
	}
	if refs == nil || len(refs) != 0 {
		t.Fatalf("want empty non-nil canvas, got %#v", refs)
	}
}

// TestLessonDedup verifies SHA-256 content dedup against lessons.json.
func TestLessonDedup(t *testing.T) {
	s := newTestStore(t)
	dup, err := s.AddLesson("sess-l", "always check the stamp before query")
	if err != nil || dup {
		t.Fatalf("first add: deduped=%v err=%v", dup, err)
	}
	dup, err = s.AddLesson("sess-l", "always check the stamp before query")
	if err != nil || !dup {
		t.Fatalf("second add: deduped=%v err=%v (want true)", dup, err)
	}
	dup, err = s.AddLesson("sess-l", "different lesson")
	if err != nil || dup {
		t.Fatalf("third add: deduped=%v err=%v", dup, err)
	}

	raw, err := os.ReadFile(filepath.Join(s.Root, "sess-l", "lessons.json"))
	if err != nil {
		t.Fatalf("lessons.json: %v", err)
	}
	var lessons []lesson
	if err := json.Unmarshal(raw, &lessons); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(lessons) != 2 {
		t.Fatalf("want 2 lessons, got %d", len(lessons))
	}
}

// TestConcurrentOffloads runs parallel offloads with distinct nodeIDs;
// every ref must land in the index and recall must work afterwards.
func TestConcurrentOffloads(t *testing.T) {
	s := newTestStore(t)
	const n = 32
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			nodeID := fmt.Sprintf("offload-%03d", i)
			payload := []byte(fmt.Sprintf("payload-%d", i))
			if _, err := s.Offload("sess-conc", nodeID, payload, nodeID); err != nil {
				errs <- fmt.Errorf("offload %s: %w", nodeID, err)
				return
			}
			got, err := s.Recall("sess-conc", nodeID)
			if err != nil {
				errs <- fmt.Errorf("recall %s: %w", nodeID, err)
				return
			}
			if !bytes.Equal(got, payload) {
				errs <- fmt.Errorf("recall %s: payload mismatch", nodeID)
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}

	refs, err := s.Canvas("sess-conc")
	if err != nil {
		t.Fatalf("canvas: %v", err)
	}
	if len(refs) != n {
		t.Fatalf("want %d refs after concurrent offloads, got %d", n, len(refs))
	}
}

// TestIDValidation guards the path-traversal trust boundary: session and
// node IDs must not escape the layout.
func TestIDValidation(t *testing.T) {
	s := newTestStore(t)
	bad := [][2]string{
		{"..", "offload-001"},            // session traversal
		{"sess", "../escape"},            // node traversal
		{"sess", ""},                     // empty node
		{"", "offload-001"},              // empty session
		{"sess", "a/b"},                  // slash in node
		{"sess", "no"},                   // below MIN_NODE_ID_LEN
		{"sess has space", "offload-01"}, // space in session
	}
	for _, tc := range bad {
		if _, err := s.Offload(tc[0], tc[1], []byte("x"), "x"); err == nil {
			t.Fatalf("Offload(%q, %q) should fail", tc[0], tc[1])
		}
	}
	// Valid dotted/hyphenated ids pass.
	if _, err := s.Offload("sess.v2-a_1", "offload.001-a", []byte("x"), "x"); err != nil {
		t.Fatalf("valid ids rejected: %v", err)
	}
}
