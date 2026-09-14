// Package session implements the session offload layer (US-SM-01 /
// FR-SM-01..02): verbose MCP/tool payloads are persisted under
// `.leankg/sessions/<session_id>/refs/<node_id>.md`, a compact canvas index
// stays in context, and `session_recall` restores the original payload
// bit-for-bit (SHA-256 verified).
//
// Layout under Root (<project>/.leankg/sessions):
//
//	<session_id>/refs/<node_id>.md   offloaded payload bytes, verbatim
//	<session_id>/index.json          canvas index (Ref entries), atomic
//	<session_id>/lessons.json        dedup'd lessons (AddLesson)
package session

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

var (
	// ErrChecksum means the ref file on disk no longer matches the
	// SHA-256 recorded in the index at offload time.
	ErrChecksum = errors.New("session: ref checksum mismatch")
	// ErrNotFound means the node_id has no index entry or ref file.
	ErrNotFound = errors.New("session: node not found")
)

// Ref is one offloaded node's canvas metadata.
type Ref struct {
	NodeID  string `json:"node_id"`
	Path    string `json:"path"`
	Summary string `json:"summary"`
	SHA256  string `json:"sha256"`
}

// index is the on-disk shape of index.json.
type index struct {
	SessionID string `json:"session_id"`
	CreatedAt int64  `json:"created_at"`
	Refs      []Ref  `json:"refs"`
}

// lesson is one dedup'd entry in lessons.json.
type lesson struct {
	SHA256 string `json:"sha256"`
	Text   string `json:"text"`
	At     int64  `json:"at"`
}

// Store is the root of all session state for one project.
type Store struct {
	// Root is <projectDir>/.leankg/sessions.
	Root string

	// ponytail: one mutex serializes index/lessons read-modify-writes
	// across ALL sessions; offloads are rare and writes are tiny. Upgrade
	// path: per-session mutex map if profiles ever show contention.
	mu sync.Mutex
}

// New returns a Store rooted at projectDir/.leankg/sessions.
func New(projectDir string) *Store {
	return &Store{Root: filepath.Join(projectDir, ".leankg", "sessions")}
}

func validID(id string, minLen int) bool {
	if len(id) < minLen {
		return false
	}
	if id == "." || id == ".." {
		return false
	}
	for _, c := range id {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		case c == '-' || c == '_' || c == '.':
		default:
			return false
		}
	}
	return true
}

func (s *Store) sessionDir(sessionID string) (string, error) {
	if !validID(sessionID, 1) {
		return "", fmt.Errorf("session: invalid session_id %q", sessionID)
	}
	return filepath.Join(s.Root, sessionID), nil
}

func (s *Store) refsDir(sessionID string) (string, error) {
	dir, err := s.sessionDir(sessionID)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "refs"), nil
}

// Offload writes the payload verbatim to <session>/refs/<nodeID>.md and
// records the Ref in the session index (index.json, atomic write).
func (s *Store) Offload(sessionID, nodeID string, payload []byte, summary string) (Ref, error) {
	if !validID(nodeID, 3) {
		return Ref{}, fmt.Errorf("session: invalid node_id %q", nodeID)
	}
	refs, err := s.refsDir(sessionID)
	if err != nil {
		return Ref{}, err
	}
	if err := os.MkdirAll(refs, 0o755); err != nil {
		return Ref{}, fmt.Errorf("session: create refs dir: %w", err)
	}
	path := filepath.Join(refs, nodeID+".md")
	if err := writeFileAtomic(path, payload); err != nil {
		return Ref{}, err
	}
	sum := sha256.Sum256(payload)
	ref := Ref{
		NodeID:  nodeID,
		Path:    path,
		Summary: summary,
		SHA256:  hex.EncodeToString(sum[:]),
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	idx, err := s.loadIndex(sessionID)
	if err != nil {
		return Ref{}, err
	}
	idx.Refs = append(idx.Refs, ref)
	if err := s.saveIndex(sessionID, idx); err != nil {
		return Ref{}, err
	}
	return ref, nil
}

// Recall restores the offloaded payload for nodeID bit-for-bit. The file
// bytes are verified against the SHA-256 recorded at offload time;
// ErrChecksum on mismatch, ErrNotFound on a missing node or file.
func (s *Store) Recall(sessionID, nodeID string) ([]byte, error) {
	idx, err := s.loadIndex(sessionID)
	if err != nil {
		return nil, err
	}
	var ref *Ref
	for i := range idx.Refs {
		if idx.Refs[i].NodeID == nodeID {
			ref = &idx.Refs[i]
			break
		}
	}
	if ref == nil {
		return nil, fmt.Errorf("session: node %q: %w", nodeID, ErrNotFound)
	}
	raw, err := os.ReadFile(ref.Path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("session: node %q: %w", nodeID, ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("session: read ref: %w", err)
	}
	sum := sha256.Sum256(raw)
	if hex.EncodeToString(sum[:]) != ref.SHA256 {
		return nil, fmt.Errorf("session: node %q: %w", nodeID, ErrChecksum)
	}
	return raw, nil
}

// Canvas returns the session's node index in offload order. A session with
// no index yet yields an empty slice.
func (s *Store) Canvas(sessionID string) ([]Ref, error) {
	idx, err := s.loadIndex(sessionID)
	if err != nil {
		return nil, err
	}
	if idx.Refs == nil {
		idx.Refs = []Ref{}
	}
	return idx.Refs, nil
}

// AddLesson records a lesson for the session, dedup'd by SHA-256 of the
// text. deduped is true when the text was already present (nothing written).
func (s *Store) AddLesson(sessionID, text string) (deduped bool, err error) {
	dir, err := s.sessionDir(sessionID)
	if err != nil {
		return false, err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return false, fmt.Errorf("session: create session dir: %w", err)
	}
	sum := sha256.Sum256([]byte(text))
	key := hex.EncodeToString(sum[:])

	s.mu.Lock()
	defer s.mu.Unlock()
	var lessons []lesson
	if err := readJSON(filepath.Join(dir, "lessons.json"), &lessons); err != nil && !errors.Is(err, os.ErrNotExist) {
		return false, err
	}
	for _, l := range lessons {
		if l.SHA256 == key {
			return true, nil
		}
	}
	lessons = append(lessons, lesson{SHA256: key, Text: text, At: time.Now().Unix()})
	if err := writeJSONAtomic(filepath.Join(dir, "lessons.json"), lessons); err != nil {
		return false, err
	}
	return false, nil
}

func (s *Store) loadIndex(sessionID string) (*index, error) {
	dir, err := s.sessionDir(sessionID)
	if err != nil {
		return nil, err
	}
	var idx index
	if err := readJSON(filepath.Join(dir, "index.json"), &idx); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return &index{SessionID: sessionID, CreatedAt: time.Now().Unix()}, nil
		}
		return nil, err
	}
	return &idx, nil
}

func (s *Store) saveIndex(sessionID string, idx *index) error {
	dir, err := s.sessionDir(sessionID)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("session: create session dir: %w", err)
	}
	return writeJSONAtomic(filepath.Join(dir, "index.json"), idx)
}

func readJSON(path string, v any) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(raw, v); err != nil {
		return fmt.Errorf("session: parse %s: %w", filepath.Base(path), err)
	}
	return nil
}

// writeFileAtomic writes via a temp file + rename in the same directory so
// readers never observe a partial file.
func writeFileAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return fmt.Errorf("session: create temp: %w", err)
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return fmt.Errorf("session: write temp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("session: close temp: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("session: rename: %w", err)
	}
	return nil
}

func writeJSONAtomic(path string, v any) error {
	raw, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("session: encode: %w", err)
	}
	raw = append(raw, '\n')
	return writeFileAtomic(path, raw)
}
