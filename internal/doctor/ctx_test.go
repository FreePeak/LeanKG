package doctor

import (
	"context"
	"errors"
	"net"
	"path/filepath"
	"testing"
	"time"
)

// hungProbes stalls every probe until its ctx is done, the way a connection
// that accepted and never answered does. It returns only through ctx.
type hungProbes struct{}

func (hungProbes) PingMS(ctx context.Context) (int64, error) {
	<-ctx.Done()
	return 0, ctx.Err()
}
func (hungProbes) AppliedMigrations(ctx context.Context) ([]int, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}
func (hungProbes) IndexedFiles(ctx context.Context) ([]string, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}
func (hungProbes) RecordedFiles(ctx context.Context) ([]string, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}
func (hungProbes) QualifiedNames(ctx context.Context) ([]string, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}
func (hungProbes) RelationshipEdges(ctx context.Context) ([]Edge, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}
func (hungProbes) EmbeddedNames(ctx context.Context) ([]string, bool, error) {
	<-ctx.Done()
	return nil, false, ctx.Err()
}
func (hungProbes) EngineName() string { return "hung" }

// hungFleet stalls the registry read until its ctx is done.
type hungFleet struct{}

func (hungFleet) Fleet(ctx context.Context) ([]FleetStatus, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

// TestRunAllBoundsEveryHungProbe: a probe that never answers must come back as
// a FAIL finding within the per-probe timeout, and the rest of the report must
// still be produced.
func TestRunAllBoundsEveryHungProbe(t *testing.T) {
	env := healthyEnv()
	env.ProbeTimeout = 50 * time.Millisecond
	env.Fleet = hungFleet{}

	start := time.Now()
	r, err := RunAll(context.Background(), Defaults(), hungProbes{}, env)
	if err != nil {
		t.Fatalf("RunAll returned %v; a probe timeout is a finding, not an error", err)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("RunAll took %s with 50ms probe timeouts; hung probes are not bounded", elapsed)
	}
	if len(r.Findings) != len(Defaults()) {
		t.Fatalf("got %d findings, want %d: a hung probe must not drop the rest of the report",
			len(r.Findings), len(Defaults()))
	}
	for _, check := range []string{"pg-latency", "migrations", "index-freshness", "fleet"} {
		if f := finding(t, r, check); f.Status != StatusFail {
			t.Errorf("%s = %+v; want FAIL for a probe that timed out", check, f)
		}
	}
	// A check that reads no probe is unaffected by the stalled store.
	if f := finding(t, r, "pool-env"); f.Status != StatusPass {
		t.Errorf("pool-env = %+v; want PASS, it reads no probe", f)
	}
}

// TestRunAllStopsOnCancelledCtx: a cancelled run (SIGINT) runs no check and
// reports the cancellation as an error, not as findings.
func TestRunAllStopsOnCancelledCtx(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r, err := RunAll(ctx, Defaults(), healthyStub(), healthyEnv())
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if len(r.Findings) != 0 {
		t.Fatalf("a cancelled run produced %d findings; want none", len(r.Findings))
	}
}

// TestOpenProbesHonoursCtxOnHungPostgres: a Postgres that accepts the TCP
// connection and never answers must not block the open past the caller's ctx.
func TestOpenProbesHonoursCtxOnHungPostgres(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			defer c.Close() // held open, never answered
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	start := time.Now()
	dsn := "postgres://u:p@" + ln.Addr().String() + "/db?sslmode=disable"
	if _, err := openProbes(ctx, filepath.Join(t.TempDir(), "proj"), "postgres", dsn); err == nil {
		t.Fatal("openProbes against a hung server returned no error")
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("openProbes took %s with a 200ms ctx; the open ignores its ctx", elapsed)
	}
}
