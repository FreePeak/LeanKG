//go:build dshusage

package dsusage

import (
	"os"
	"testing"
	"time"

	"github.com/FreePeak/LeanKG/internal/judge"
)

// Live Laya checks. Skipped by default: the unit suite must stay in-process
// and fast (repo test-layer policy), and loading the model takes ~1–3 min on
// a cold sidecar. Run explicitly:
//
//	LEANKG_TEST_LAYA_URL=http://127.0.0.1:8091 go test ./internal/dsusage/ -run Live -v
func liveLaya(t *testing.T) LayaClient {
	t.Helper()
	url := os.Getenv("LEANKG_TEST_LAYA_URL")
	if url == "" {
		t.Skip("set LEANKG_TEST_LAYA_URL to run live Laya checks")
	}
	return LayaClient{Backend: judge.NewLocal(url, "laya", 120*time.Second), Timeout: 120 * time.Second}
}

// TestLiveLayaBatteryIsAccepted proves the compiled battery — three nouls and
// a LADDER (not a criteria map) — is accepted by the real sidecar. A ladder
// sent as a map is the defect this guards: it is silently re-labelled and the
// model answers a different question.
func TestLiveLayaBatteryIsAccepted(t *testing.T) {
	c := liveLaya(t)
	step := Step{
		Tool:      "mcp__leankg__query",
		Workspace: "--Users-me-work-be-backend--",
		Input:     `{"query":"promo API"}`,
		Output:    "ERROR Error: Streamable HTTP error: session not found",
		IsError:   true,
	}
	v := c.Judge(step)
	if !v.Available {
		t.Fatalf("sidecar unavailable: %s", v.Reason)
	}
	if v.Score < 0 || v.Score > 3 {
		t.Fatalf("urgency %v outside the ladder", v.Score)
	}
	if v.Confidence <= 0 {
		t.Fatalf("no confidence recorded: %+v", v)
	}
}

// TestLiveLayaCorroboratesDeadSession is the behaviour we rely on: on a step
// whose MCP session died, the specific transport question clears its floor and
// the verdict escalates. Measured on this state: transport_dead 0.644 (above
// the 0.6 floor) while the general is_defect question read 0.452 — the
// specific question is the one carrying the signal, which is exactly why the
// battery is decomposed instead of asked as one wide choice.
func TestLiveLayaCorroboratesDeadSession(t *testing.T) {
	c := liveLaya(t)
	v := c.Judge(Step{
		Tool:    "mcp__leankg__query",
		Input:   `{"query":"promo API","project":"be-menu"}`,
		Output:  "Error: session not found",
		IsError: true,
	})
	if !v.Available {
		t.Fatalf("sidecar unavailable: %s", v.Reason)
	}
	if !v.TransportDead {
		t.Errorf("a dead MCP session should read as transport_dead: %+v", v)
	}
	if !v.Critical {
		t.Errorf("corroborated dead transport should escalate: %+v", v)
	}
	// The aggregate is the LEAST certain judgment in the battery, so it sits
	// at or below any single question that cleared its own floor. Asserting
	// ">= floor" here would misstate the contract: per-question floors gate
	// each question, and the aggregate describes the whole call.
	if v.Confidence <= 0 || v.Confidence > 1 {
		t.Errorf("aggregate confidence out of range: %+v", v)
	}
}

// TestLiveLayaCleanStepIsNotCritical is the safety property that lets the
// dashboard raise severity at all: a healthy call must not become critical.
func TestLiveLayaCleanStepIsNotCritical(t *testing.T) {
	c := liveLaya(t)
	v := c.Judge(Step{
		Tool:      "mcp__leankg__query",
		Workspace: "--Users-me-work-harvey-freepeak-leankg--",
		Input:     `{"query":"engineFor project routing","project":"leankg"}`,
		Output:    `{"freshness":"fresh","hits":[{"content":"func (s *Server) engineFor"}]}`,
	})
	if !v.Available {
		t.Fatalf("sidecar unavailable: %s", v.Reason)
	}
	if v.Critical {
		t.Fatalf("a healthy call must never be critical: %+v", v)
	}
}
