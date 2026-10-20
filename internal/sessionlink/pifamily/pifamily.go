// Package pifamily reads the pi-style tree JSONL session stores of pi, omp and
// xdev (plan v4.15 DS-12). Adapters are read-only, never follow symlinks out
// of the store and fail soft. The active branch is followed from the last
// entry back to the root, so abandoned branches never count as turns.
package pifamily

import (
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/FreePeak/LeanKG/internal/sessionlink"
)

type adapter struct {
	name string
	root func(home string) string
}

// New returns the adapter for one pi-family client: "pi", "omp" or "xdev".
// An unknown name yields an adapter whose store never exists.
func New(client string) sessionlink.Adapter {
	a := &adapter{name: client}
	switch client {
	case "pi":
		a.root = func(home string) string {
			if d := os.Getenv("PI_CODING_AGENT_DIR"); d != "" {
				return filepath.Join(d, "sessions")
			}
			return filepath.Join(home, ".pi", "agent", "sessions")
		}
	case "omp":
		a.root = func(home string) string {
			if d := os.Getenv("PI_CODING_AGENT_SESSION_DIR"); d != "" {
				return d
			}
			return filepath.Join(home, ".omp", "agent", "sessions")
		}
	case "xdev":
		a.root = func(home string) string {
			if d := os.Getenv("XDEV_AGENT_DIR"); d != "" {
				return filepath.Join(d, "sessions")
			}
			return filepath.Join(home, ".xdev", "agent", "sessions")
		}
	default:
		a.root = func(string) string { return "" }
	}
	return a
}

// Client is the canonical client name this adapter serves.
func (a *adapter) Client() string { return a.name }

// Roots are the store directory Detect looks at.
func (a *adapter) Roots(home string) []string {
	r := a.root(home)
	if r == "" {
		return nil
	}
	return []string{r}
}

// Detect reports whether the store directory exists.
func (a *adapter) Detect(home string) bool {
	r := a.root(home)
	if r == "" {
		return false
	}
	fi, err := os.Stat(r)
	return err == nil && fi.IsDir()
}

// Locate finds the transcript for c. An exact match on the session id (header
// id or filename uuid) wins; otherwise the heuristic requires the same cwd, a
// file touched inside the time window, and a leankg tool call with the same
// normalized name and args hash.
func (a *adapter) Locate(home string, c sessionlink.CallRef) (sessionlink.TranscriptRef, sessionlink.Confidence, error) {
	root := a.root(home)
	if root == "" {
		return sessionlink.TranscriptRef{}, 0, sessionlink.ErrNotFound
	}
	files := listSessionFiles(root)

	if cid := c.ClientSessionID; cid != "" {
		for _, f := range files {
			id := headerID(f)
			if id == cid || uuidFromName(f) == cid {
				return a.ref(f, id), sessionlink.ConfidenceExact, nil
			}
		}
	}

	if c.Cwd == "" || c.Tool == "" || c.TS.IsZero() {
		return sessionlink.TranscriptRef{}, 0, sessionlink.ErrNotFound
	}
	var best string
	var bestID string
	var bestStart time.Time
	var bestN int
	for _, f := range files {
		hdr, ok := readHeader(f)
		if !ok || filepath.Clean(hdr.cwd) != filepath.Clean(c.Cwd) {
			continue
		}
		if fi, err := os.Stat(f); err != nil || fi.ModTime().Before(c.TS.Add(-sessionlink.TurnHeuristic.LookBack)) {
			continue
		}
		if !hdr.start.IsZero() && hdr.start.After(c.TS.Add(sessionlink.TurnHeuristic.Slack)) {
			continue
		}
		s, err := load(f)
		if err != nil {
			continue
		}
		n := sessionlink.CountTurnMatches(s.turns, c, &sessionlink.TurnHeuristic)
		if n == 0 {
			continue
		}
		if best == "" || hdr.start.After(bestStart) {
			best, bestID, bestStart, bestN = f, s.id, hdr.start, n
		}
	}
	if best == "" {
		return sessionlink.TranscriptRef{}, 0, sessionlink.ErrNotFound
	}
	return a.ref(best, bestID), sessionlink.ConfidenceFor(bestN), nil
}

func (a *adapter) ref(path, id string) sessionlink.TranscriptRef {
	if id == "" {
		id = uuidFromName(path)
	}
	return sessionlink.TranscriptRef{Client: a.name, Path: path, SessionID: id}
}

// Window extracts the bounded context around the matched leankg call.
func (a *adapter) Window(t sessionlink.TranscriptRef, c sessionlink.CallRef, n int) (sessionlink.Window, error) {
	s, err := load(t.Path)
	if err != nil {
		return sessionlink.Window{}, err
	}
	sid := t.SessionID
	if sid == "" {
		sid = s.id
	}
	return sessionlink.WindowFromTurns(a.name, sid, t.Path, s.turns, c, n)
}

// Transcript returns the active branch as capped turns. maxTurns <= 0 keeps all.
func (a *adapter) Transcript(t sessionlink.TranscriptRef, maxTurns int) ([]sessionlink.Turn, error) {
	s, err := load(t.Path)
	if err != nil {
		return nil, err
	}
	return sessionlink.CapTurns(s.turns, maxTurns), nil
}

// listSessionFiles returns regular *.jsonl files one directory below root,
// skipping symlinks so nothing outside the store is opened.
func listSessionFiles(root string) []string {
	var out []string
	dirs, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	for _, d := range dirs {
		if d.Type()&os.ModeSymlink != 0 || !d.IsDir() {
			continue
		}
		entries, err := os.ReadDir(filepath.Join(root, d.Name()))
		if err != nil {
			continue
		}
		for _, e := range entries {
			if !e.Type().IsRegular() || !strings.HasSuffix(e.Name(), ".jsonl") {
				continue
			}
			out = append(out, filepath.Join(root, d.Name(), e.Name()))
		}
	}
	return out
}

// uuidFromName returns the id part of <ts>_<uuid>.jsonl.
func uuidFromName(path string) string {
	base := strings.TrimSuffix(filepath.Base(path), ".jsonl")
	if i := strings.LastIndex(base, "_"); i >= 0 {
		return base[i+1:]
	}
	return base
}

func headerID(path string) string {
	hdr, _ := readHeader(path)
	return hdr.id
}
