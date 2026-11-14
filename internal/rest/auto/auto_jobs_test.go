package auto

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// writeSource gives the index one element to find.
func writeSource(t *testing.T, dir string) {
	t.Helper()
	src := []byte("package main\n\nfunc Hello() string { return \"hi\" }\n")
	if err := os.WriteFile(filepath.Join(dir, "main.go"), src, 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestAutoConfigIndexJobOutlivesRequest mirrors net/http: the request context
// is cancelled as soon as the handler returns. The index job must still finish
// (CX-05). Before the fix the job ran on the request context and indexed nothing.
func TestAutoConfigIndexJobOutlivesRequest(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".leankg"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeSource(t, dir)
	eng := openEngine(t, dir)
	jobs := NewJobs(context.Background())
	t.Cleanup(jobs.Stop)

	reqCtx, endRequest := context.WithCancel(context.Background())
	body := []byte(`{"index_enabled":true}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/mcp/auto-config", bytes.NewReader(body)).WithContext(reqCtx)
	rec := httptest.NewRecorder()
	RegisterAutoConfig(http.NewServeMux(), eng, WithJobs(jobs)).ServeHTTP(rec, req)
	endRequest()
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body)
	}

	jobs.Wait()
	n, err := eng.Store().ElementCount()
	if err != nil {
		t.Fatal(err)
	}
	if n == 0 {
		t.Fatal("index job was cancelled when the handler returned: 0 elements indexed, want > 0")
	}
}

// TestAutoConfigConflictStartsNoJob pins that a refused request (cwd outside the
// served project, 409) starts no job at all.
func TestAutoConfigConflictStartsNoJob(t *testing.T) {
	base := t.TempDir()
	served := filepath.Join(base, "served")
	caller := filepath.Join(base, "caller")
	for _, d := range []string{filepath.Join(served, ".leankg"), caller} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	writeSource(t, served)
	eng := openEngine(t, served)
	jobs := NewJobs(context.Background())
	t.Cleanup(jobs.Stop)

	body, _ := json.Marshal(AutoConfigRequest{IndexEnabled: true, CWD: caller})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/mcp/auto-config", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	RegisterAutoConfig(http.NewServeMux(), eng, WithJobs(jobs)).ServeHTTP(rec, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409: %s", rec.Code, rec.Body)
	}

	jobs.Wait()
	n, err := eng.Store().ElementCount()
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("refused request still indexed %d elements; the 409 path must start no job", n)
	}
}

// TestJobsStopCancelsRunningJobs pins the shutdown half: Stop cancels the jobs'
// context and returns only after the running jobs have returned.
func TestJobsStopCancelsRunningJobs(t *testing.T) {
	jobs := NewJobs(context.Background())
	exited := make(chan struct{})
	jobs.Go(func(ctx context.Context) {
		<-ctx.Done()
		close(exited)
	})

	stopped := make(chan struct{})
	go func() {
		jobs.Stop()
		close(stopped)
	}()
	select {
	case <-stopped:
	case <-time.After(10 * time.Second):
		t.Fatal("Stop did not return: the running job was never cancelled")
	}
	select {
	case <-exited:
	default:
		t.Fatal("Stop returned before the job exited")
	}
}

// TestEmbedDebounceOutlivesRequest pins the embed half of CX-05: the debounce
// window runs on the job context, not the request context, so the handler
// returning does not cut the window short. The window is driven by a channel,
// not by the clock, so the test takes no time beyond the handler call.
func TestEmbedDebounceOutlivesRequest(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".leankg"), 0o755); err != nil {
		t.Fatal(err)
	}
	eng := openEngine(t, dir)

	orig := embedDebounce
	t.Cleanup(func() { embedDebounce = orig })
	jobs := NewJobs(context.Background())
	t.Cleanup(jobs.Stop)

	armed := make(chan time.Duration, 1)
	release := make(chan struct{})
	ctxErr := make(chan error, 1)
	embedDebounce = func(ctx context.Context, d time.Duration) bool {
		armed <- d
		select {
		case <-release:
		case <-ctx.Done():
		}
		ctxErr <- ctx.Err()
		return false
	}

	reqCtx, endRequest := context.WithCancel(context.Background())
	body := []byte(`{"embed_enabled":true,"embed_debounce_seconds":7}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/mcp/auto-config", bytes.NewReader(body)).WithContext(reqCtx)
	rec := httptest.NewRecorder()
	RegisterAutoConfig(http.NewServeMux(), eng, WithJobs(jobs)).ServeHTTP(rec, req)
	endRequest()
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body)
	}

	close(release)
	if d := <-armed; d != 7*time.Second {
		t.Fatalf("debounce window = %v, want 7s from embed_debounce_seconds", d)
	}
	if err := <-ctxErr; err != nil {
		t.Fatalf("embed debounce ran on a cancelled context after the handler returned: %v", err)
	}
	jobs.Wait()
}
