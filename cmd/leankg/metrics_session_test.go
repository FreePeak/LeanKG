package main

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/FreePeak/LeanKG/internal/telemetry"
)

// `leankg metrics --session` reads the telemetry ledger (DS-16): the latest
// session by default, a named one with --session-id, and "" when no ledger
// exists (never creating one).
func TestTelemetrySessionText(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("LEANKG_HOME", home)
	if got := telemetrySessionText(""); got != "" {
		t.Fatalf("no ledger must render nothing, got %q", got)
	}
	st, err := telemetry.OpenSQLite(telemetry.DBPath(home), false)
	if err != nil {
		t.Fatal(err)
	}
	ts := time.Now().Add(-time.Hour)
	if err := st.InsertCalls(context.Background(), []telemetry.CallEvent{
		{ID: "a1", TS: ts, Method: telemetry.MethodToolsCall, Tool: "query", Outcome: telemetry.OutcomeOK,
			Identity: telemetry.Identity{ClientName: "pi", ClientSessionID: "old"}},
		{ID: "b1", TS: ts.Add(time.Minute), Method: telemetry.MethodToolsCall, Tool: "query", Outcome: telemetry.OutcomeZeroHit,
			Identity: telemetry.Identity{ClientName: "claude-code", ClientSessionID: "new"}},
	}); err != nil {
		t.Fatal(err)
	}
	_ = st.Close()
	if got := telemetrySessionText(""); !strings.Contains(got, "claude-code:new") {
		t.Fatalf("latest session expected, got:\n%s", got)
	}
	if got := telemetrySessionText("pi:old"); !strings.Contains(got, "pi:old") {
		t.Fatalf("named session expected, got:\n%s", got)
	}
	if got := telemetrySessionText("missing"); got != "" {
		t.Fatalf("unknown session must render nothing, got %q", got)
	}
}
