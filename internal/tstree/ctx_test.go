//go:build tstree

package tstree

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

// TestExtractPreCancelledReturnsCancellation: a cancelled ctx is reported
// before any parsing happens and yields no definitions.
func TestExtractPreCancelledReturnsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	defs, err := Extract(ctx, []byte("package a\n\nfunc F() {}\n"), "go")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Extract error = %v, want context.Canceled", err)
	}
	if defs != nil {
		t.Fatalf("Extract returned %d definitions for a cancelled ctx, want none", len(defs))
	}
}

// TestExtractCancelMidParseReturnsPromptly: a deadline that expires while the
// grammar is still parsing stops the parse. The input is far larger than the
// bound, so an uncancelled parse would take much longer than the assertion.
func TestExtractCancelMidParseReturnsPromptly(t *testing.T) {
	var b strings.Builder
	for i := range 400_000 {
		fmt.Fprintf(&b, "func F%d() int { return %d }\n", i, i)
	}
	src := []byte(b.String())

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	start := time.Now()
	defs, err := Extract(ctx, src, "go")
	elapsed := time.Since(start)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Extract error = %v (defs=%d), want context.DeadlineExceeded", err, len(defs))
	}
	if elapsed > 2*time.Second {
		t.Errorf("Extract took %v after its deadline; the parse ignored ctx", elapsed)
	}
}
