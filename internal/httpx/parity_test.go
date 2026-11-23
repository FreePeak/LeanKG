package httpx_test

import (
	"context"
	"encoding/json"
	"github.com/FreePeak/LeanKG/internal/auth"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"connectrpc.com/connect"

	"github.com/FreePeak/LeanKG/internal/core"
	"github.com/FreePeak/LeanKG/internal/memory"
	"github.com/FreePeak/LeanKG/internal/rest"
	"github.com/FreePeak/LeanKG/internal/rpc"
	leankgv1 "github.com/FreePeak/LeanKG/internal/rpc/leankg/v1"
	"github.com/FreePeak/LeanKG/internal/rpc/leankg/v1/leankgv1connect"
	"github.com/FreePeak/LeanKG/internal/store"
	"github.com/FreePeak/LeanKG/internal/telemetry"
	"github.com/FreePeak/LeanKG/internal/web"
)

// newParityEngine builds one engine over a migrated store; the three
// transports under test all serve it.
func newParityEngine(t *testing.T) *core.Engine {
	t.Helper()
	dir := t.TempDir()
	st, err := store.OpenBackend(context.Background(), store.OpenOptions{DBPath: filepath.Join(dir, ".leankg", "leankg.db"), Mode: store.RW})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	mem, err := memory.Open(context.Background(), dir, false)
	if err != nil {
		t.Fatal(err)
	}
	return core.New(st, core.Options{Memory: mem})
}

// envelopeOf reads the error text and code from a JSON error body. REST writes
// {error, code}; the web API writes {success, data, error, code}.
func envelopeOf(t *testing.T, body string) (msg, code string) {
	t.Helper()
	var env struct {
		Error string `json:"error"`
		Code  string `json:"code"`
	}
	if err := json.Unmarshal([]byte(body), &env); err != nil {
		t.Fatalf("error body is not JSON: %v\n%s", err, body)
	}
	return env.Error, env.Code
}

func post(t *testing.T, url, body string) (int, string) {
	t.Helper()
	res, err := http.Post(url, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return res.StatusCode, string(b)
}

// TestBadInputAnswersOneEnvelopeOnEveryTransport sends the same bad query to
// REST, the web API and ConnectRPC and requires one status and one error
// text. RPC carries the text in the connect error message.
func TestBadInputAnswersOneEnvelopeOnEveryTransport(t *testing.T) {
	engine := newParityEngine(t)
	restSrv := httptest.NewServer(auth.MiddlewareWithStore(nil, rest.Handler(engine, nil)))
	t.Cleanup(restSrv.Close)
	webSrv := httptest.NewServer(web.APIHandler(engine, nil))
	t.Cleanup(webSrv.Close)
	rpcMux := http.NewServeMux()
	path, handler := rpc.Handler(engine, telemetry.Nop{})
	rpcMux.Handle(path, handler)
	rpcSrv := httptest.NewServer(rpcMux)
	t.Cleanup(rpcSrv.Close)
	client := leankgv1connect.NewLeanKGClient(rpcSrv.Client(), rpcSrv.URL)

	t.Run("unknown action", func(t *testing.T) {
		rs, rb := post(t, restSrv.URL+"/api/v1/query", `{"action":"nope","query":"x"}`)
		ws, wb := post(t, webSrv.URL+"/api/query", `{"query":"x","params":{"action":"nope"}}`)
		_, rerr := client.Query(context.Background(), connect.NewRequest(&leankgv1.QueryRequest{Action: "nope", Query: "x"}))
		if rerr == nil {
			t.Fatal("rpc: unknown action answered without an error")
		}
		rmsg, rcode := envelopeOf(t, rb)
		wmsg, wcode := envelopeOf(t, wb)
		if rs != http.StatusBadRequest || ws != http.StatusBadRequest {
			t.Fatalf("status rest=%d web=%d, want 400 on both", rs, ws)
		}
		if rmsg != wmsg || !strings.Contains(rerr.Error(), rmsg) {
			t.Fatalf("error text differs:\n rest %q\n web  %q\n rpc  %q", rmsg, wmsg, rerr.Error())
		}
		if connect.CodeOf(rerr) != connect.CodeInvalidArgument {
			t.Fatalf("rpc code = %v, want invalid_argument (HTTP 400)", connect.CodeOf(rerr))
		}
		if rcode != wcode || rcode != "LEANKG_ERROR_UNKNOWN_ACTION" {
			t.Fatalf("code rest=%q web=%q, want LEANKG_ERROR_UNKNOWN_ACTION", rcode, wcode)
		}
	})

	t.Run("fractional limit", func(t *testing.T) {
		rs, rb := post(t, restSrv.URL+"/api/v1/query", `{"query":"x","limit":2.5}`)
		ws, wb := post(t, webSrv.URL+"/api/query", `{"query":"x","params":{"limit":2.5}}`)
		rmsg, rcode := envelopeOf(t, rb)
		wmsg, wcode := envelopeOf(t, wb)
		if rs != http.StatusBadRequest || ws != http.StatusBadRequest {
			t.Fatalf("status rest=%d web=%d, want 400 on both", rs, ws)
		}
		if rmsg != wmsg || rcode != wcode || rcode != "LEANKG_ERROR_INVALID_PARAM" {
			t.Fatalf("rest %q %q, web %q %q; want one InvalidParam envelope", rcode, rmsg, wcode, wmsg)
		}
	})

	t.Run("malformed JSON", func(t *testing.T) {
		rs, rb := post(t, restSrv.URL+"/api/v1/query", `{"query":`)
		ws, wb := post(t, webSrv.URL+"/api/query", `{"query":`)
		rmsg, rcode := envelopeOf(t, rb)
		wmsg, wcode := envelopeOf(t, wb)
		if rs != http.StatusBadRequest || ws != http.StatusBadRequest {
			t.Fatalf("status rest=%d web=%d, want 400 on both", rs, ws)
		}
		if rmsg != wmsg || !strings.HasPrefix(rmsg, "invalid JSON body: ") {
			t.Fatalf("rest %q, web %q; want the same invalid JSON body text", rmsg, wmsg)
		}
		if rcode != wcode || rcode != "LEANKG_ERROR_INVALID_PARAM" {
			t.Fatalf("code rest=%q web=%q, want LEANKG_ERROR_INVALID_PARAM", rcode, wcode)
		}
	})
}
