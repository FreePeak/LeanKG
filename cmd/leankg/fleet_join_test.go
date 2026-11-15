package main

import (
	"context"
	"testing"
	"time"

	"github.com/FreePeak/LeanKG/internal/maintain"
)

// TestJoinFleetWaitsForTheLoopToExit pins CX-35: shutdown joins the maintenance
// fleet. The fleet's first pass is held inside its target list until released,
// so the loop cannot exit before the join is asked to wait; the join must
// report a clean exit, and only once the loop channel is closed.
func TestJoinFleetWaitsForTheLoopToExit(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	entered := make(chan struct{})
	release := make(chan struct{})
	fleet := maintain.RunFleet(ctx, func() []maintain.Target {
		close(entered)
		<-release
		return nil
	}, maintain.Options{}, time.Hour, 0)
	<-entered

	cancel()
	joined := make(chan bool, 1)
	go func() { joined <- joinFleet(fleet, shutdownTimeout) }()
	close(release)

	if !<-joined {
		t.Fatal("joinFleet gave up on a fleet that exits once its pass returns")
	}
	select {
	case <-fleet:
	default:
		t.Fatal("joinFleet returned while the fleet loop was still running")
	}
}

// TestJoinFleetAbandonsALoopPastTheBudget pins the bound: a loop that never
// exits must not hold shutdown past the shutdown budget.
func TestJoinFleetAbandonsALoopPastTheBudget(t *testing.T) {
	stuck := make(chan struct{})
	if joinFleet(stuck, 0) {
		t.Fatal("joinFleet reported a clean exit for a loop that never closed")
	}
}
