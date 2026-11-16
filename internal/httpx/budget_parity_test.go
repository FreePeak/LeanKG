package httpx_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/FreePeak/LeanKG/internal/auth"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"regexp"
	"testing"

	connect "connectrpc.com/connect"

	"github.com/FreePeak/LeanKG/internal/budget"
	"github.com/FreePeak/LeanKG/internal/core"
	leankgmcp "github.com/FreePeak/LeanKG/internal/mcp"
	"github.com/FreePeak/LeanKG/internal/memory"
	"github.com/FreePeak/LeanKG/internal/rest"
	"github.com/FreePeak/LeanKG/internal/rpc"
	leankgv1 "github.com/FreePeak/LeanKG/internal/rpc/leankg/v1"
	"github.com/FreePeak/LeanKG/internal/rpc/leankg/v1/leankgv1connect"
	"github.com/FreePeak/LeanKG/internal/store"
	"github.com/FreePeak/LeanKG/internal/telemetry"
	"github.com/FreePeak/LeanKG/internal/web"
)

// newSeededParityEngine is newParityEngine with forty elements that all match
// the query "Widget", so a small token budget has something to cut.
func newSeededParityEngine(t *testing.T) *core.Engine {
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
	var els []store.Element
	for i := range 40 {
		els = append(els, store.Element{
			QualifiedName: fmt.Sprintf("pkg/f%02d.go::Widget", i), ElementType: "function",
			Name: "Widget", FilePath: fmt.Sprintf("pkg/f%02d.go", i), Language: "go",
			Content: "func Widget() { /* widget pipeline renders the frame */ }",
		})
	}
	if err := st.UpsertElements(context.Background(), els); err != nil {
		t.Fatal(err)
	}
	mem, err := memory.Open(context.Background(), dir, false)
	if err != nil {
		t.Fatal(err)
	}
	return core.New(st, core.Options{Memory: mem})
}

// volatileKey matches the keys whose values change between two identical
// requests (timings, timestamps); everything else must match exactly.
var volatileKey = regexp.MustCompile(`(?i)(_ms$|elapsed|took|duration|_at$|timestamp|generated|updated|time$)`)

func normalizeBody(v any) any {
	switch t := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, x := range t {
			if volatileKey.MatchString(k) {
				out[k] = "<volatile>"
				continue
			}
			out[k] = normalizeBody(x)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, x := range t {
			out[i] = normalizeBody(x)
		}
		return out
	}
	return v
}

func decodeBody(t *testing.T, transport string, raw []byte) any {
	t.Helper()
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatalf("%s: body is not JSON: %v\n%s", transport, err, raw)
	}
	return normalizeBody(v)
}

// mcpResultText returns the text of a tools/call answer. The HTTP MCP surface
// answers in server-sent events; the JSON-RPC message is the data line.
func mcpResultText(t *testing.T, raw []byte) []byte {
	t.Helper()
	var msg []byte
	for _, line := range bytes.Split(raw, []byte("\n")) {
		if rest, ok := bytes.CutPrefix(line, []byte("data: ")); ok {
			msg = rest
		}
	}
	if msg == nil {
		msg = raw
	}
	var env struct {
		Result struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
			IsError bool `json:"isError"`
		} `json:"result"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(msg, &env); err != nil {
		t.Fatalf("mcp: not a JSON-RPC message: %v\n%s", err, raw)
	}
	if env.Error != nil || env.Result.IsError || len(env.Result.Content) == 0 {
		t.Fatalf("mcp: tool call failed: %s", msg)
	}
	return []byte(env.Result.Content[0].Text)
}

// TestSameQueryReturnsOneBodyOnEveryTransport sends one query through REST,
// ConnectRPC, the web API and MCP, with and without a small token budget, and
// requires the same result body after normalisation. The budget case is the
// red-first one: before the budget lived in core, only MCP cut the answer.
func TestSameQueryReturnsOneBodyOnEveryTransport(t *testing.T) {
	engine := newSeededParityEngine(t)

	restSrv := httptest.NewServer(auth.MiddlewareWithStore(nil, rest.Handler(engine, nil)))
	t.Cleanup(restSrv.Close)
	webSrv := httptest.NewServer(web.APIHandler(engine, nil))
	t.Cleanup(webSrv.Close)
	rpcMux := http.NewServeMux()
	path, handler := rpc.Handler(engine, telemetry.Nop{})
	rpcMux.Handle(path, handler)
	rpcSrv := httptest.NewServer(rpcMux)
	t.Cleanup(rpcSrv.Close)
	mcpSrv := httptest.NewServer(leankgmcp.New(engine).HTTPHandler())
	t.Cleanup(mcpSrv.Close)
	client := leankgv1connect.NewLeanKGClient(rpcSrv.Client(), rpcSrv.URL)

	for _, tc := range []struct {
		name   string
		budget int
	}{
		{"table default", 0},
		{"token_budget 60", 60},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bodies := map[string]any{}

			// REST: POST /api/v1/query, the query fields at the top level.
			restArgs := `{}`
			webParams := `{"action":"exact"`
			rpcArgs := map[string]string{}
			mcpArgs := map[string]any{"action": "exact", "query": "Widget"}
			if tc.budget > 0 {
				restArgs = fmt.Sprintf(`{"token_budget":%d}`, tc.budget)
				webParams += fmt.Sprintf(`,"token_budget":%d`, tc.budget)
				rpcArgs["token_budget"] = fmt.Sprint(tc.budget)
				mcpArgs["args"] = map[string]any{"token_budget": tc.budget}
			}
			webParams += "}"

			_, rb := post(t, restSrv.URL+"/api/v1/query",
				fmt.Sprintf(`{"action":"exact","query":"Widget","args":%s}`, restArgs))
			bodies["REST"] = decodeBody(t, "REST", []byte(rb))

			_, wb := post(t, webSrv.URL+"/api/query",
				fmt.Sprintf(`{"query":"Widget","params":%s}`, webParams))
			var env struct {
				Success bool `json:"success"`
				Data    struct {
					Result json.RawMessage `json:"result"`
				} `json:"data"`
			}
			if err := json.Unmarshal([]byte(wb), &env); err != nil || !env.Success {
				t.Fatalf("web: %v / %s", err, wb)
			}
			bodies["web"] = decodeBody(t, "web", env.Data.Result)

			rres, err := client.Query(context.Background(), connect.NewRequest(&leankgv1.QueryRequest{
				Action: "exact", Query: "Widget", Args: rpcArgs,
			}))
			if err != nil {
				t.Fatalf("rpc: %v", err)
			}
			bodies["RPC"] = decodeBody(t, "RPC", []byte(rres.Msg.Json))

			mreq, _ := json.Marshal(map[string]any{
				"jsonrpc": "2.0", "id": 7, "method": "tools/call",
				"params": map[string]any{"name": "query", "arguments": mcpArgs},
			})
			mhReq, _ := http.NewRequest(http.MethodPost, mcpSrv.URL+"/mcp", bytes.NewReader(mreq))
			mhReq.Header.Set("Content-Type", "application/json")
			mhReq.Header.Set("Accept", "application/json, text/event-stream")
			mres, err := http.DefaultClient.Do(mhReq)
			if err != nil {
				t.Fatal(err)
			}
			mraw, _ := io.ReadAll(mres.Body)
			mres.Body.Close()
			bodies["MCP"] = decodeBody(t, "MCP", mcpResultText(t, mraw))

			want, _ := json.Marshal(bodies["REST"])
			for name, got := range bodies {
				b, _ := json.Marshal(got)
				if !bytes.Equal(b, want) {
					t.Errorf("%s body differs from REST:\n got  %s\n want %s", name, b, want)
				}
			}

			marker, _ := bodies["REST"].(map[string]any)[budget.BudgetMarkerKey].(map[string]any)
			if tc.budget > 0 {
				if marker == nil || marker["max"] != float64(tc.budget) || marker["truncated"] != true {
					t.Fatalf("token_budget %d: REST marker = %v, want truncated at max %d", tc.budget, marker, tc.budget)
				}
			} else if marker != nil {
				t.Fatalf("table default: the answer is cut (marker %v); the case must be under its cap", marker)
			}
		})
	}
}
