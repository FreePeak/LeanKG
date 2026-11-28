package main

import (
	"context"
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

// cmdMemoryHook is the memory hook (plan 6.6) for SessionStart, PostToolUse and
// SessionEnd. It always returns nil, so the process exits 0 on every path;
// failures are one line on stderr. A panic is recovered here as well, after the
// deferred telemetry flush has run.
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
	raw, complete := readHookStdin(os.Stdin, hookStdinWait)
	in := ccmemory.ParseHookInput(raw)
	if !complete {
		in.Incomplete = true
	}
	if in.Incomplete {
		// Input that is cut off, late or not JSON is not a known event: it must
		// not fall through to SessionStart and sync or inject.
		return nil
	}
	ctx, flush := withMemoryTelemetryFlush(ctx, telemetry.Identity{ClientName: "claude-code", ClientSessionID: in.SessionID}, func() {
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
		CallerDir: ccmemory.ResolveHookDir(in, os.Getenv("CLAUDE_PROJECT_DIR"), wd),
		Event:     in.Event,
		FilePath:  in.FilePath,
		Stdout:    os.Stdout,
		Stderr:    os.Stderr,
	})
	return nil
}

// readHookStdin reads r to EOF. complete is false when the input is longer than
// hookStdinLimit, when the read fails, or when wait ends first; the bytes are
// then returned as nil or as what was read. The read goroutine is left behind on
// a timeout; the process exits without it.
func readHookStdin(r io.Reader, wait time.Duration) (b []byte, complete bool) {
	type result struct {
		b   []byte
		err error
	}
	got := make(chan result, 1)
	go func() {
		// One byte past the cap tells an input of exactly the cap from a longer one.
		b, err := io.ReadAll(io.LimitReader(r, hookStdinLimit+1))
		got <- result{b, err}
	}()
	select {
	case res := <-got:
		if res.err != nil || len(res.b) > hookStdinLimit {
			return nil, false
		}
		return res.b, true
	case <-time.After(wait):
		return nil, false
	}
}
