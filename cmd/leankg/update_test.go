package main

import (
	"context"
	"io"
	"testing"

	"github.com/FreePeak/LeanKG/internal/cliexit"
)

// TestUpdateCancelExitsWith130 pins the exit status of a cancelled `update`:
// the cancel is an interrupt (130), not an ordinary failure (1).
func TestUpdateCancelExitsWith130(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := cmdUpdate(ctx, []string{"--check"})
	if got := cliexit.Finish(err, io.Discard); got != 130 {
		t.Fatalf("cancelled update exit = %d, want 130 (err: %v)", got, err)
	}
}
