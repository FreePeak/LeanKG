package claudecode

import (
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/FreePeak/LeanKG/internal/sessionlink"
)

// TestLinkingAccuracy builds many synthetic sessions that share the same
// LeanKG query (same tool and args) and checks every call links to its own
// session: 100% exact, and the heuristic has no false link on the decoys.
func TestLinkingAccuracy(t *testing.T) {
	home, projects := fixtureHome(t)
	const sessions = 40
	args := map[string]any{"query": "auth handler", "action": "search"}
	dir := filepath.Join(projects, slug(cwd))

	type want struct {
		sid string
		c   sessionlink.CallRef
	}
	var cases []want
	for i := 0; i < sessions; i++ {
		sid := fmt.Sprintf("acc-%02d", i)
		// Stagger starts so each session covers only its own call time.
		off := time.Duration(i) * 10 * time.Minute
		lines := []string{
			prompt(sid, "u0", cwd, off, "work on task"),
			toolUse(sid, "a0", cwd, off+time.Second, "m0", "mcp__leankg__query", "tu-"+sid, args),
			resultLine(sid, "r0", cwd, off+2*time.Second, "tu-"+sid, "ok", false),
		}
		write(t, filepath.Join(dir, sid+".jsonl"), lines...)
		cases = append(cases, want{sid: sid, c: lkCall(sid, off+time.Second, args, true)})
	}

	a := New()
	correct := 0
	for _, tc := range cases {
		ref, conf, err := a.Locate(home, tc.c)
		if err != nil {
			t.Errorf("exact %s: %v", tc.sid, err)
			continue
		}
		w, err := a.Window(ref, tc.c, 6)
		if err != nil {
			t.Errorf("window %s: %v", tc.sid, err)
			continue
		}
		if filepath.Base(ref.Path) == tc.sid+".jsonl" && conf == sessionlink.ConfidenceExact &&
			w.Matched != nil && w.Matched.ID == "tu-"+tc.sid {
			correct++
		}
	}
	if correct != sessions {
		t.Fatalf("exact accuracy %d/%d, want 100%%", correct, sessions)
	}

	// Heuristic: the same calls without the session id. Every one must link
	// to its own transcript (the time window separates the decoys).
	heurCorrect := 0
	for _, tc := range cases {
		c := tc.c
		c.ClientSessionID = ""
		ref, conf, err := a.Locate(home, c)
		if err != nil {
			t.Errorf("heuristic %s: %v", tc.sid, err)
			continue
		}
		if filepath.Base(ref.Path) == tc.sid+".jsonl" && conf >= sessionlink.HeuristicThreshold {
			heurCorrect++
		} else {
			t.Errorf("heuristic %s linked to %s conf %v (false link)", tc.sid, ref.Path, conf)
		}
	}
	if heurCorrect != sessions {
		t.Fatalf("heuristic accuracy %d/%d", heurCorrect, sessions)
	}

	// Calls with no transcript at all must never link (no false link).
	for i := 0; i < 10; i++ {
		c := lkCall(fmt.Sprintf("ghost-%d", i), time.Duration(i)*time.Minute+10*time.Hour, args, true)
		if _, _, err := a.Locate(home, c); !errors.Is(err, sessionlink.ErrNotFound) {
			t.Fatalf("ghost call %d linked or wrong error: %v", i, err)
		}
		c.ClientSessionID = ""
		if _, _, err := a.Locate(home, c); !errors.Is(err, sessionlink.ErrNotFound) {
			t.Fatalf("ghost heuristic call %d linked or wrong error: %v", i, err)
		}
	}
}
