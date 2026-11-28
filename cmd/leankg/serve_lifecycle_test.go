package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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

	err := serve(context.Background(), []string{"--project", dir, "--stdio", "--http", "127.0.0.1:0"})
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

	err = serve(context.Background(), []string{"--project", t.TempDir(), "--http", addr})
	if err == nil {
		t.Fatalf("serve on busy %s returned nil, want a bind error", addr)
	}
	if !errors.Is(err, syscall.EADDRINUSE) {
		t.Fatalf("serve error %q does not wrap EADDRINUSE", err)
	}
}

// TestServeRefusedExposureLeavesNoStore pins the CX-06 gap closed by CX-18: an
// exposed bind with no token is refused before the store is opened, so the
// refused serve leaves no database file behind.
func TestServeRefusedExposureLeavesNoStore(t *testing.T) {
	t.Setenv("LEANKG_HOME", t.TempDir())
	for _, k := range []string{"LEANKG_TOKEN_ADMIN", "LEANKG_TOKEN_CONTRIBUTOR", "LEANKG_TOKEN_VIEWER", "LEANKG_DB_PATH"} {
		t.Setenv(k, "")
	}
	t.Setenv("LEANKG_DB_ENGINE", "sqlite")
	dir := t.TempDir()

	err := serve(context.Background(), []string{"--project", dir, "--http", "0.0.0.0:0"})
	if err == nil || !strings.Contains(err.Error(), "no auth token is configured") {
		t.Fatalf("serve on 0.0.0.0 with no token = %v, want the exposure refusal", err)
	}
	if _, statErr := os.Stat(filepath.Join(dir, ".leankg", "leankg.db")); !os.IsNotExist(statErr) {
		t.Fatalf("store file exists after a refused exposure (stat err %v)", statErr)
	}
}

// TestServeGateStateNeverCreatesTheStore pins the no-create gate probe that
// runs before serve touches the store: an env token configures the gate with
// no store at all, and a missing store with no env token leaves it off.
func TestServeGateStateNeverCreatesTheStore(t *testing.T) {
	for _, k := range []string{"LEANKG_TOKEN_ADMIN", "LEANKG_TOKEN_CONTRIBUTOR", "LEANKG_TOKEN_VIEWER", "LEANKG_DB_PATH"} {
		t.Setenv(k, "")
	}
	t.Setenv("LEANKG_DB_ENGINE", "sqlite")
	dir := t.TempDir()

	if serveGateOn(context.Background(), dir, "sqlite", "") {
		t.Fatal("gate on with no store and no env token, want off")
	}
	t.Setenv("LEANKG_TOKEN_ADMIN", "lkg_test_token")
	if !serveGateOn(context.Background(), dir, "sqlite", "") {
		t.Fatal("gate off with LEANKG_TOKEN_ADMIN set, want on")
	}
	if _, statErr := os.Stat(filepath.Join(dir, ".leankg", "leankg.db")); !os.IsNotExist(statErr) {
		t.Fatalf("gate probe created the store file (stat err %v)", statErr)
	}
}

// TestServeStdioEOFDoesNotWaitForTheFleet pins the shutdown path where serve
// returns without its root context cancelled: stdin reaching EOF ends the stdio
// session, and the maintenance fleet must stop with it. The fleet join is
// checked through its stderr log line, not by timing the process.
func TestServeStdioEOFDoesNotWaitForTheFleet(t *testing.T) {
	bin := cliBinary(t)
	t.Setenv("LEANKG_HOME", t.TempDir())
	cmd := exec.Command(bin, "serve", "--stdio", "--project", t.TempDir())
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		t.Fatalf("serve --stdio after EOF: %v\n%s", err, stderr.String())
	}
	if strings.Contains(stderr.String(), "fleet did not stop") {
		t.Fatalf("serve waited out the fleet join budget:\n%s", stderr.String())
	}
}

// TestServeStdioLogsCentralMemoryRoot pins the one stderr line that names the
// central memory root agent_recall reads, so an operator can see which
// <home>/.leankg/memory serve is pointed at.
func TestServeStdioLogsCentralMemoryRoot(t *testing.T) {
	bin := cliBinary(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("LEANKG_HOME", t.TempDir())
	cmd := exec.Command(bin, "serve", "--stdio", "--project", t.TempDir())
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		t.Fatalf("serve --stdio after EOF: %v\n%s", err, stderr.String())
	}
	want := "leankg central memory root " + filepath.Join(home, ".leankg", "memory")
	if !strings.Contains(stderr.String(), want) {
		t.Fatalf("serve stderr lacks the central memory root line %q:\n%s", want, stderr.String())
	}
}
