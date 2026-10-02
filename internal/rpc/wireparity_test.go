package rpc

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/FreePeak/LeanKG/internal/core"
	leankgv1connect "github.com/FreePeak/LeanKG/internal/rpc/leankg/v1/leankgv1connect"
	"github.com/FreePeak/LeanKG/internal/store"
)

// TestTheSecondWireRejectsTheSameShapeTheFirstDoes pins ConnectRPC to the same
// request contract the MCP schema advertises — the wire wave 23 fixed.
//
// Wave 23 established that the MCP schema presented two names for one value
// (`limit` and `args.limit`) with no stated precedence, advertised six ontology
// sub-commands nowhere, and listed four actions whose mandatory argument it never
// mentioned. The fix was in the MCP schema, because MCP is where an agent reads
// the contract.
//
// ConnectRPC is the SECOND wire over the same engine, with its own descriptor,
// and wave 24 drove it. The same two limit names reach it over the same JSON,
// and the top-level one is the one that works:
//
//	{"query":"rank","action":"search","limit":2}                     -> 2 hits
//	{"query":"rank","action":"search","limit":2,"args":{"limit":1}}  -> 0 hits
//
// The second form is not silently "limit 2" as it is over MCP — it is a REJECTED
// REQUEST (`unmarshal message: … proto: …`), because the protobuf descriptor
// types `args` as a structured message rather than a free-form map. So a client
// that learned the shape over MCP and replays it over ConnectRPC gets an error
// where MCP would have honoured the other name, and an agent that learned it
// over ConnectRPC cannot even express it. Two wires, one engine, two contracts —
// and the second one has no schema text to fix, because protobuf descriptors
// cannot carry prose.
//
// That is the finding this wave takes, and it is a product decision rather than
// a defect to patch: the honest options are (a) document that `args` is
// free-form over MCP and structured over ConnectRPC, (b) add a typed `args`
// message so both wires accept the same shapes, or (c) give the RPC an explicit
// `limit` and let `args` carry only what the descriptor can type. This test pins
// what the engine actually does on each wire, so whichever option is chosen is
// chosen against a measurement rather than a guess.
func TestTheSecondWireRejectsTheSameShapeTheFirstDoes(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(dir+"/.leankg/leankg.db", store.RW)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.Migrate(); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertElements([]store.Element{
		{QualifiedName: "p.go::Alpha", ElementType: "function", Name: "Alpha", FilePath: "p.go", Language: "go", Content: "alpha"},
		{QualifiedName: "p.go::Beta", ElementType: "function", Name: "Beta", FilePath: "p.go", Language: "go", Content: "beta"},
	}); err != nil {
		t.Fatal(err)
	}
	eng := core.New(st, nil, nil)
	eng.SetProjectDir(dir)
	svc := NewLeanKGService(eng)
	_, handler := leankgv1connect.NewLeanKGHandler(svc)
	mux := ArgsRefusalRecovery(handler)

	// The shape that works on BOTH wires: the top-level limit. Assert on the
	// ANSWERED limit, not on a hit count — whether hits come back depends on
	// which rung answers, and this test is about the request contract.
	code, body := post(t, mux, `{"query":"Alpha","action":"search","limit":3}`)
	if code != http.StatusOK {
		t.Fatalf("the top-level limit must be accepted over ConnectRPC, got %d: %s", code, body)
	}
	if got := rpcPayload(t, body)["limit"]; got != float64(3) {
		t.Errorf("limit=3 must be honoured over the second wire, answered %v", got)
	}

	// The shape MCP accepts and silently overrides: over ConnectRPC it is a
	// rejected request, because `args` is typed rather than free-form. Pin the
	// difference, because it is the whole finding.
	code, body = post(t, mux, `{"query":"Alpha","action":"search","limit":1,"args":{"limit":2}}`)
	if code == http.StatusOK {
		got := rpcPayload(t, body)["limit"]
		if got != float64(1) {
			t.Errorf("if the typed args.limit IS accepted, the TOP-LEVEL limit must still win (limit=1), answered %v", got)
		}
		return // the wire accepted it and honoured precedence: nothing to fix
	}
	// It was rejected. That is a DIFFERENT contract from MCP's — `args` here is
	// map<string,string>, so EVERY non-string action arg is unreachable on this
	// wire, and the rejection names an internal field the caller never sent:
	//
	//   invalid value for string field value: 2
	//
	// A caller reading that has no idea which of their arguments was refused, so
	// the refusal must say which one. That is wave 24's fix, and it is the
	// smallest change that makes the limitation actionable: the descriptor stays
	// as generated, and the SERVICE translates the wire's type error into a
	// sentence naming the arg and the wire's limitation.
	// The codec reports a byte OFFSET, not a field path, so the KEY is not
	// recoverable here — and pretending otherwise would need the wrapper to
	// buffer every request body to improve one message. What the caller CAN act
	// on is the wire's limitation and the value they sent, so that is what the
	// answer must carry, and the codec's own field wording must not survive.
	msg := connectMessage(t, body)
	if !strings.Contains(msg, "map<string,string>") {
		t.Errorf("the refusal must state the wire's own limitation, got: %s", msg)
	}
	// The codec's own wording is KEPT, in parentheses, so nothing is lost — so
	// the assertion is not "the phrase is gone" but "the actionable sentence
	// comes first", which is what a reader actually needs.
	if !strings.HasPrefix(msg, "args value ") {
		t.Errorf("the actionable sentence must lead the answer, with the codec detail after it: %s", msg)
	}
}

// TestNonStringArgsAreNamedOnRefusal pins the legibility half directly, with a
// representative of each shape the engine takes that this wire cannot carry:
// a number (depth, limit, insert_line) and an array (turns).
func TestNonStringArgsAreNamedOnRefusal(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(dir+"/.leankg/leankg.db", store.RW)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.Migrate(); err != nil {
		t.Fatal(err)
	}
	eng := core.New(st, nil, nil)
	eng.SetProjectDir(dir)
	_, handler := leankgv1connect.NewLeanKGHandler(NewLeanKGService(eng))
	mux := ArgsRefusalRecovery(handler)

	for _, tc := range []struct{ name, body, quotes string }{
		{"a number", `{"query":"x","action":"impact","args":{"depth":2}}`, "2"},
		{"an array", `{"query":"x","action":"memory","args":{"command":"session_retain","turns":["a","b"]}}`, "turns"[:1]},
	} {
		t.Run(tc.name, func(t *testing.T) {
			code, body := post(t, mux, tc.body)
			if code == http.StatusOK {
				t.Fatalf("%s was accepted over ConnectRPC; the wire may have changed, re-read the finding", tc.name)
			}
			msg := connectMessage(t, body)
			// The refusal must show the value the caller sent — the one fact the
			// codec had and the caller cannot otherwise see — and name a surface
			// that DOES accept it.
			if !strings.Contains(msg, tc.quotes) {
				t.Errorf("the refusal must show the rejected value (%s), got: %s", tc.quotes, msg)
			}
			if !strings.Contains(msg, "MCP or REST") {
				t.Errorf("the refusal must name a surface that DOES accept it: %s", msg)
			}
		})
	}
}

func post(t *testing.T, mux http.Handler, body string) (int, string) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/leankg.v1.LeanKG/Query", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec.Code, rec.Body.String()
}

// rpcPayload unwraps the ConnectRPC envelope. The RPC returns map[string]any,
// which protobuf carries as a Struct and which therefore arrives under "json" as
// a QUOTED STRING — not as a nested object — so it must be parsed in two steps.
// Decoding the field as json.RawMessage first (the obvious thing) fails on the
// quotes and silently falls back to reading the ENVELOPE as the payload, which
// is how this helper first reported limit as nil against a live answer that
// plainly carried it.
func rpcPayload(t *testing.T, body string) map[string]any {
	t.Helper()
	var env struct {
		JSON json.RawMessage `json:"json"`
	}
	if err := json.Unmarshal([]byte(body), &env); err != nil {
		// Not an envelope at all: the handler answered with the payload.
		var payload map[string]any
		if err := json.Unmarshal([]byte(body), &payload); err != nil {
			t.Fatalf("rpc payload: %v (%s)", err, body)
		}
		return payload
	}
	var asString string
	if err := json.Unmarshal(env.JSON, &asString); err == nil {
		var payload map[string]any
		if err := json.Unmarshal([]byte(asString), &payload); err != nil {
			t.Fatalf("rpc payload inside envelope: %v (%s)", err, asString)
		}
		return payload
	}
	var payload map[string]any
	if err := json.Unmarshal(env.JSON, &payload); err != nil {
		t.Fatalf("rpc payload: %v (%s)", err, body)
	}
	return payload
}

// connectMessage is the DECODED ConnectRPC error message — what a caller reads,
// not the JSON transport spelling of it.
func connectMessage(t *testing.T, body string) string {
	t.Helper()
	var env struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal([]byte(body), &env); err != nil {
		t.Fatalf("rpc error envelope: %v (%s)", err, body)
	}
	return env.Message
}

func payloadHits(p map[string]any) []any {
	h, _ := p["hits"].([]any)
	return h
}
