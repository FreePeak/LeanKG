package auto

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/FreePeak/LeanKG/internal/core"
	"github.com/FreePeak/LeanKG/internal/store"
)

// Regression: the auto-config work runs in a goroutine that outlives the
// handler, so it must NOT inherit r.Context(). net/http cancels the request
// context the moment the handler returns, which used to kill the debounce
// timer before any embed ever started — leaving the endpoint inert.
//
// This drives the real RegisterAutoConfig over a real net/http server, so the
// request context is genuinely cancelled when the response is received. The
// seam is the single-flight flag: startAutoEmbed sets autoEmbedRunning before
// the debounce wait and clears it on exit. With the request context (the bug)
// the goroutine sees ctx.Done() and clears the flag at once; with the server
// context (the fix) it stays inside the debounce with the flag still set. A
// long debounce makes that stable rather than timing-flaky.
func TestAutoConfigBackgroundWorkSurvivesHandlerReturn(t *testing.T) {
	serverCtx, cancel := context.WithCancel(context.Background())
	defer cancel()

	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "leankg.db"), store.RW)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.Migrate(); err != nil {
		t.Fatal(err)
	}
	eng := core.New(st, nil, nil)
	eng.SetProjectDir(dir)

	// The real entry point, mounted the way cmd/leankg mounts it.
	handler := RegisterAutoConfig(serverCtx, http.NotFoundHandler(), eng)
	srv := httptest.NewServer(handler)
	defer srv.Close()

	// 30s debounce: if the job is bound to the request context it dies the
	// moment this response is received; if it is bound to the server context
	// it is still waiting when we look.
	res, err := http.Post(srv.URL+"/api/v1/mcp/auto-config", "application/json",
		strings.NewReader(`{"embed_enabled":true,"embed_debounce_seconds":30}`))
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, res.Body)
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status %d", res.StatusCode)
	}
	// Receiving the response means the handler returned; net/http has now
	// cancelled the request context.

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		autoEmbedMu.Lock()
		running := autoEmbedRunning
		autoEmbedMu.Unlock()
		if running {
			// Still inside the 30s debounce => it is waiting on the SERVER
			// context. The bug would have cleared this flag already.
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("auto-embed job exited before its debounce elapsed: the background work " +
		"is bound to the request context, so net/http cancellation kills it")
}
