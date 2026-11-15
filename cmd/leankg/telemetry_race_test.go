package main

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/FreePeak/LeanKG/internal/telemetry"
)

// TestTelemetryRecorderIsRaceFree pins CX-07 residual (2): the SIGHUP reload
// builds the process recorder while closeTelemetry reads it at shutdown. Run
// with -race, the base code reports a DATA RACE on telemetryRec.
func TestTelemetryRecorderIsRaceFree(t *testing.T) {
	home := t.TempDir()
	t.Setenv("LEANKG_HOME", home)
	t.Setenv("LEANKG_TELEMETRY", "")
	cfg := telemetry.Config{
		Capture: telemetry.Metadata,
		Consent: telemetry.Consent{Version: telemetry.ConsentVersion, GrantedAt: time.Now(), GrantedBy: "cli"},
	}
	if err := telemetry.SaveConfig(home, cfg); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(closeTelemetry)

	// Each round is one lazy build racing one shutdown. Nothing orders the two
	// sides: the delay only makes the build land before the shutdown reads the
	// recorder, which is the interleaving the detector can observe. Any lock
	// both sides share would order them and hide the race.
	for round := 0; round < 10; round++ {
		telemetryBuildOnce = sync.Once{}
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			if err := ReloadTelemetry(context.Background()); err != nil {
				t.Error(err)
			}
		}()
		go func() {
			defer wg.Done()
			time.Sleep(50 * time.Millisecond)
			closeTelemetry()
		}()
		wg.Wait()
	}
}

// TestLateSinkGetsLiveRecorder pins CX-07 residual (3): a per-project MCP
// server is registered as a sink when it is built lazily, possibly after a
// reload. It must receive the recorder the reload installed, not the startup
// value it was built with.
func TestLateSinkGetsLiveRecorder(t *testing.T) {
	var r sinkRegistry
	r.install(telemetry.Nop{})
	live := &stubRecorder{}
	r.install(live)

	var got telemetry.Recorder
	r.add(func(rec telemetry.Recorder) { got = rec })
	if got != telemetry.Recorder(live) {
		t.Fatalf("late sink got %T, want the live recorder installed by the reload", got)
	}
}

// stubRecorder is a distinct Recorder value for identity checks.
type stubRecorder struct{ telemetry.Nop }
