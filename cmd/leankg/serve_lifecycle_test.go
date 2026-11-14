package main

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

// failingListener is a net.Listener whose Accept fails at once with a
// non-temporary error, so http.Server.Serve returns that error.
type failingListener struct {
	err  error
	addr net.Addr
}

func (l failingListener) Accept() (net.Conn, error) { return nil, l.err }
func (l failingListener) Close() error              { return nil }
func (l failingListener) Addr() net.Addr            { return l.addr }

// TestRunServersReturnsListenerServeError pins CX-06: a listener whose Serve
// fails surfaces that error to the caller instead of being printed and swallowed.
func TestRunServersReturnsListenerServeError(t *testing.T) {
	boom := errors.New("accept exploded")
	ln := failingListener{err: boom, addr: &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 1}}
	srv := &http.Server{Handler: http.NotFoundHandler()}

	err := runServers(context.Background(), listener{ln: ln, srv: srv})
	if !errors.Is(err, boom) {
		t.Fatalf("runServers error = %v, want it to wrap %v", err, boom)
	}
}

// TestRunServersAwaitsInFlightRequestOnShutdown pins CX-06: cancelling the
// context shuts the listener down gracefully, and runServers returns only after
// the in-flight request finished, so the caller may close its store next.
func TestRunServersAwaitsInFlightRequestOnShutdown(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	var finished atomic.Bool
	h := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		close(entered)
		<-release
		finished.Store(true)
		w.WriteHeader(http.StatusOK)
	})
	ls, err := bindAll(endpoint{addr: "127.0.0.1:0", h: h})
	if err != nil {
		t.Fatal(err)
	}
	url := "http://" + ls[0].ln.Addr().String() + "/"

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- runServers(ctx, ls...) }()

	reqErr := make(chan error, 1)
	go func() {
		resp, err := http.Get(url)
		if resp != nil {
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
		}
		reqErr <- err
	}()
	<-entered
	cancel()
	select {
	case err := <-done:
		t.Fatalf("runServers returned (%v) while a request was still in flight", err)
	case <-time.After(100 * time.Millisecond):
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatalf("clean shutdown returned %v, want nil", err)
	}
	if !finished.Load() {
		t.Fatal("runServers returned before the in-flight handler finished")
	}
	if err := <-reqErr; err != nil {
		t.Fatalf("in-flight request failed: %v", err)
	}
}

// TestServeRejectsStdioWithListenersBeforeOpeningStore pins CX-06: a bad flag
// combination fails before any store is opened or migrated, so the database
// file is never created.
func TestServeRejectsStdioWithListenersBeforeOpeningStore(t *testing.T) {
	t.Setenv("LEANKG_HOME", t.TempDir())
	dir := t.TempDir()

	err := serve([]string{"--project", dir, "--stdio", "--http", "127.0.0.1:0"})
	if err == nil {
		t.Fatal("serve --stdio --http returned nil, want a refusal")
	}
	if _, statErr := os.Stat(filepath.Join(dir, ".leankg", "leankg.db")); !os.IsNotExist(statErr) {
		t.Fatalf("store file exists after a refused flag combination (stat err %v)", statErr)
	}
}

// TestServeReturnsErrorWhenPortIsInUse pins CX-06: a port another process
// holds is a returned error naming the address, not a silent success.
func TestServeReturnsErrorWhenPortIsInUse(t *testing.T) {
	t.Setenv("LEANKG_HOME", t.TempDir())
	occupied, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = occupied.Close() })
	addr := occupied.Addr().String()

	err = serve([]string{"--project", t.TempDir(), "--http", addr})
	if err == nil {
		t.Fatalf("serve on busy %s returned nil, want a bind error", addr)
	}
	if !errors.Is(err, syscall.EADDRINUSE) {
		t.Fatalf("serve error %q does not wrap EADDRINUSE", err)
	}
}
