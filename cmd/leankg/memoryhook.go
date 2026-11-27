package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/FreePeak/LeanKG/internal/ccmemory"
	"github.com/FreePeak/LeanKG/internal/telemetry"
)

// hookStdinWait bounds how long the hook waits for its JSON on stdin, so an
// interactive run with no input does not hang. hookStdinLimit caps what is read.
const (
	hookStdinWait  = 500 * time.Millisecond
	hookStdinLimit = 1 << 20
)

// memoryHookRun is the hook's run. It is a variable so a test can make it panic.
var memoryHookRun = ccmemory.RunHook

// cmdMemoryHook is the SessionStart hook (plan 6.6). It always returns nil, so
// the process exits 0 on every path; failures are one line on stderr. A panic
// is recovered here as well, after the deferred telemetry flush has run.
func cmdMemoryHook(ctx context.Context, _ []string) (err error) {
	defer func() {
		if r := recover(); r != nil {
			fmt.Fprintf(os.Stderr, "leankg memory hook: panic: %v\n", r)
			err = nil
		}
	}()
	// One deadline for the whole run, the telemetry flush included (plan 6.6 rule 1).
	deadline := time.Now().Add(ccmemory.HookDeadline)
	ctx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()
	stdin := readHookStdin(os.Stdin, hookStdinWait)
	ctx, flush := withMemoryTelemetryFlush(ctx, telemetry.Identity{ClientName: "claude-code", ClientSessionID: hookSessionID(stdin)}, func() {
		closeTelemetryBefore(deadline, os.Stderr)
	})
	defer flush()
	home, err := os.UserHomeDir()
	if err != nil {
		fmt.Fprintf(os.Stderr, "leankg memory hook: %v\n", err)
		return nil
	}
	wd, _ := os.Getwd()
	memoryHookRun(ctx, ccmemory.HookOptions{
		Home:      home,
		CallerDir: ccmemory.ResolveHookDir(stdin, os.Getenv("CLAUDE_PROJECT_DIR"), wd),
		Stdout:    os.Stdout,
		Stderr:    os.Stderr,
	})
	return nil
}

// hookSessionID is the session_id of the hook JSON on stdin, or "" when the input
// is empty or not JSON.
func hookSessionID(stdin []byte) string {
	var in struct {
		SessionID string `json:"session_id"`
	}
	if json.Unmarshal(stdin, &in) != nil {
		return ""
	}
	return in.SessionID
}

// readHookStdin reads r to EOF, or returns what it has after wait. The read
// goroutine is left behind on a timeout; the process exits without it.
func readHookStdin(r io.Reader, wait time.Duration) []byte {
	got := make(chan []byte, 1)
	go func() {
		b, _ := io.ReadAll(io.LimitReader(r, hookStdinLimit))
		got <- b
	}()
	select {
	case b := <-got:
		return b
	case <-time.After(wait):
		return nil
	}
}
