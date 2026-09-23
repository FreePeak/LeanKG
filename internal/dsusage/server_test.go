//go:build dshusage

package dsusage

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/FreePeak/LeanKG/internal/judge"
)

func TestSummarizeCountsSessions(t *testing.T) {
	sum := summarize([]Step{
		{SessionID: "a", Tool: "mcp__leankg__query", TopSeverity: SevCritical},
		{SessionID: "a", Tool: "mcp__leankg__status", TopSeverity: SevInfo},
		{SessionID: "b", Tool: "mcp__leankg__query", TopSeverity: SevCritical},
	})
	if sum["steps"] != 3 || sum["sessions"] != 2 {
		t.Fatalf("%v", sum)
	}
}

func TestSummarizeCountsRungs(t *testing.T) {
	sum := summarize([]Step{
		{SessionID: "a", Tool: "mcp__leankg__query", Rung: "L2"},
		{SessionID: "a", Tool: "mcp__leankg__query", Rung: "L3"},
		{SessionID: "b", Tool: "mcp__leankg__status"}, // no retrieval block
	})
	rungs, ok := sum["by_rung"].(map[string]int)
	if !ok {
		t.Fatalf("by_rung missing: %v", sum)
	}
	if rungs["L2"] != 1 || rungs["L3"] != 1 {
		t.Fatalf("by_rung = %v, want L2=1 L3=1", rungs)
	}
	if len(rungs) != 2 {
		t.Fatalf("steps without a rung must not be counted: %v", rungs)
	}
}

func TestSummarizeReportsLayaEngagement(t *testing.T) {
	steps := []Step{
		{SessionID: "a", Tool: "mcp__leankg__query",
			Issues: []Issue{{Rule: "project_not_passed", Severity: SevCritical}, {Rule: "laya_score", Severity: SevInfo}}},
		{SessionID: "a", Tool: "mcp__leankg__query",
			Issues: []Issue{{Rule: "mcp_session_lost", Severity: SevCritical}, {Rule: "laya_critical", Severity: SevCritical}}},
		{SessionID: "b", Tool: "mcp__leankg__query",
			Issues: []Issue{{Rule: "tool_error", Severity: SevHigh}, {Rule: "laya_unavailable", Severity: SevInfo}}},
		{SessionID: "b", Tool: "mcp__leankg__status", Issues: []Issue{{Rule: "ok", Severity: SevInfo}}},
	}
	laya, ok := summarize(steps)["laya"].(map[string]int)
	if !ok {
		t.Fatalf("laya block missing")
	}
	if laya["scored"] != 2 || laya["unavailable"] != 1 || laya["skipped_info"] != 1 {
		t.Fatalf("laya = %v, want scored=2 unavailable=1 skipped_info=1", laya)
	}
}

func TestLayaUnavailableDoesNotClearRules(t *testing.T) {
	steps := []Step{{
		Tool: "mcp__leankg__status", IsError: true,
		Output: "session not found",
	}}
	steps[0].Issues = Classify(steps[0])
	steps[0].TopSeverity = Top(steps[0].Issues)
	ApplyLaya(steps, LayaClient{
		Backend: judge.NewLocal("http://127.0.0.1:1", "laya", 50*time.Millisecond),
		Timeout: 50 * time.Millisecond,
	}, 1)
	if Top(steps[0].Issues) != SevCritical {
		t.Fatalf("laya failure cleared critical: %+v", steps[0].Issues)
	}
}

// TestLayaDecomposedVerdict covers the compiled battery end to end: three
// nouls + one ladder, answered above their floors, fold into a critical
// verdict with the least-certain confidence.
func TestLayaDecomposedVerdict(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Questions map[string]json.RawMessage `json:"questions"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		// The ladder must be on the wire as an ordered array, not a map.
		var urgency struct {
			Criteria []string `json:"criteria"`
		}
		if err := json.Unmarshal(body.Questions["urgency"], &urgency); err != nil || len(urgency.Criteria) != 4 {
			t.Errorf("urgency ladder on the wire = %s (err %v)", body.Questions["urgency"], err)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"model": "english",
			"answers": map[string]any{
				"is_defect":      map[string]any{"type": "noul", "noul": 0.91, "confidence": 0.91},
				"wrong_project":  map[string]any{"type": "noul", "noul": 0.76, "confidence": 0.76},
				"transport_dead": map[string]any{"type": "noul", "noul": 0.20, "confidence": 0.62},
				"urgency":        map[string]any{"type": "score", "score": 3, "confidence": 0.5},
			},
		})
	}))
	defer srv.Close()
	c := LayaClient{Backend: judge.NewLocal(srv.URL, "laya", 5*time.Second), Timeout: 5 * time.Second}
	v := c.Judge(Step{Tool: "mcp__leankg__query", Output: "session not found"})
	if !v.Available {
		t.Fatalf("unavailable: %s", v.Reason)
	}
	if !v.Defect || !v.WrongProject || v.TransportDead {
		t.Fatalf("decomposition wrong: %+v", v)
	}
	if !v.Critical {
		t.Fatalf("defect + wrong project should be critical: %+v", v)
	}
	if v.Confidence != 0.62 {
		t.Fatalf("confidence = %v, want the least certain judgment 0.62", v.Confidence)
	}
	if v.Score != 3 {
		t.Fatalf("score = %v, want 3 from the ladder", v.Score)
	}
}

// TestLayaFloorSuppressesShakyAnswer is the safety property: a confident
// "yes" below the floor must not be acted on, and must still drag the
// aggregate confidence down.
func TestLayaFloorSuppressesShakyAnswer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"answers": map[string]any{
				"is_defect":      map[string]any{"type": "noul", "noul": 0.95, "confidence": 0.30},
				"wrong_project":  map[string]any{"type": "noul", "noul": 0.95, "confidence": 0.30},
				"transport_dead": map[string]any{"type": "noul", "noul": 0.95, "confidence": 0.30},
				"urgency":        map[string]any{"type": "score", "score": 3, "confidence": 0.3},
			},
		})
	}))
	defer srv.Close()
	c := LayaClient{Backend: judge.NewLocal(srv.URL, "laya", 5*time.Second), Timeout: 5 * time.Second}
	v := c.Judge(Step{Tool: "mcp__leankg__query"})
	if !v.Available {
		t.Fatalf("unavailable: %s", v.Reason)
	}
	if v.Defect || v.WrongProject || v.TransportDead || v.Critical {
		t.Fatalf("sub-floor answers must not be actionable: %+v", v)
	}
	if v.Confidence != 0.30 {
		t.Fatalf("confidence = %v, want 0.30 recorded", v.Confidence)
	}
}

func TestReadCookieFileNetscape(t *testing.T) {
	p := t.TempDir() + "/c.txt"
	content := "# Netscape HTTP Cookie File\n#HttpOnly_127.0.0.1\tFALSE\t/\tFALSE\t999\tdsh-auth-x\tv1.abc\n"
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := ReadCookieFile(p)
	if err != nil || got != "dsh-auth-x=v1.abc" {
		t.Fatalf("got %q err %v", got, err)
	}
}

func TestWatcherAnswerIgnore(t *testing.T) {
	w := NewWatcher(WatchConfig{StatePath: t.TempDir() + "/seen.json", MinSeverity: SevHigh})
	w.mu.Lock()
	w.asks = []Ask{{ID: "a1", Status: "pending", Severity: SevHigh, Title: "t"}}
	w.mu.Unlock()
	a, err := w.AnswerAsk("a1", false)
	if err != nil || a.Status != "ignored" {
		t.Fatalf("%+v %v", a, err)
	}
}
