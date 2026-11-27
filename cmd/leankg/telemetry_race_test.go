package main

import (
	"bytes"
	"context"
	"strings"
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

// blockingRecorder is a Recorder whose flush never finishes until block closes.
type blockingRecorder struct {
	telemetry.Nop
	block <-chan struct{}
}

func (b blockingRecorder) Close() error {
	<-b.block
	return nil
}

// TestCloseTelemetryAbandonsBlockedFlush pins the shutdown bound: a ledger flush
// that never returns must not hold the process. closeTelemetryWithin returns at
// the budget and writes one line naming the abandoned flush.
func TestCloseTelemetryAbandonsBlockedFlush(t *testing.T) {
	block := make(chan struct{})
	t.Cleanup(func() { close(block) })
	telemetrySinks.install(blockingRecorder{block: block})
	t.Cleanup(func() { telemetrySinks.install(telemetry.Nop{}) })

	var out bytes.Buffer
	closeTelemetryWithin(20*time.Millisecond, &out)
	if !strings.Contains(out.String(), "abandoned") {
		t.Fatalf("stderr = %q, want one line naming the abandoned flush", out.String())
	}
}

// TestCloseTelemetryQuietWhenFlushFinishes pins the normal path: a flush that
// returns within the budget writes nothing.
func TestCloseTelemetryQuietWhenFlushFinishes(t *testing.T) {
	telemetrySinks.install(telemetry.Nop{})
	t.Cleanup(func() { telemetrySinks.install(telemetry.Nop{}) })

	var out bytes.Buffer
	closeTelemetryWithin(time.Second, &out)
	if out.Len() != 0 {
		t.Fatalf("stderr = %q, want nothing when the flush finishes", out.String())
	}
}

// TestCloseTelemetryBeforeWaitsOnlyTheFloorWhenDeadlinePassed pins the shared
// budget: a run that has used its deadline still gives a blocked flush the
// floor, and the abandon line names that floor.
func TestCloseTelemetryBeforeWaitsOnlyTheFloorWhenDeadlinePassed(t *testing.T) {
	block := make(chan struct{})
	t.Cleanup(func() { close(block) })
	telemetrySinks.install(blockingRecorder{block: block})
	t.Cleanup(func() { telemetrySinks.install(telemetry.Nop{}) })

	var out bytes.Buffer
	start := time.Now()
	closeTelemetryBefore(time.Now().Add(-time.Second), &out)
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("flush waited %v past a passed deadline, want about the %v floor", elapsed, telemetryFlushFloor)
	}
	if !strings.Contains(out.String(), "within "+telemetryFlushFloor.String()) {
		t.Errorf("stderr = %q, want the abandon line naming the %v floor", out.String(), telemetryFlushFloor)
	}
}
