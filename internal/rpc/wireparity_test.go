package rpc

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestArgsRefusalRecoveryNamesTheCallersValue is the second wire's legibility.
// The ConnectRPC descriptor types `args` as map<string,string>, so a typed
// argument (`limit`, `depth`, `insert_line`, `retained_through_user_turn`, the
// array `turns`) cannot be carried. The codec refuses with
//
//	unmarshal message: … proto: (line 1:48): invalid value for string field value: 2
//
// which names `value` — a descriptor field the caller never sent — and not the
// argument they did. Verified live on main before the fix: a Query call with
// args {limit:3} answered exactly that. A confident client reading a message
// about its own request that cannot be about its own request concludes the
// request was malformed and retries it identically.
func TestArgsRefusalRecoveryNamesTheCallersValue(t *testing.T) {
	codecMsg := "unmarshal message: unmarshal into *leankgv1.QueryRequest: proto: (line 1:33): invalid value for string field value: 3"
	upstream := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"code":"invalid_argument","message":"` + codecMsg + `"}`))
	})
	srv := httptest.NewServer(ArgsRefusalRecovery(upstream))
	defer srv.Close()

	resp, err := http.Post(srv.URL, "application/json", strings.NewReader("{}"))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body := make([]byte, 4096)
	n, _ := resp.Body.Read(body)
	msg := string(body[:n])

	// The codec's wording is kept in PARENTHESES (nothing is lost), so the check
	// is that it no longer LEADS: the caller must learn which value was refused
	// before any descriptor jargon.
	if idx := strings.Index(msg, "string field value"); idx >= 0 {
		if lead := strings.Index(msg, "args value 3 is not a string"); lead < 0 || lead > idx {
			t.Fatalf("the codec's wording leads the message: %s", msg)
		}
	}
	for _, want := range []string{"args value 3 is not a string", "map\\u003cstring,string\\u003e", "MCP or REST"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("message %q does not contain %q", msg, want)
		}
	}
}

// TestArgsRefusalRecoveryPassesEverythingElse pins the safety property: a
// success body and a refusal the translator does not recognise must be
// forwarded byte for byte. A translator that guesses costs correctness.
func TestArgsRefusalRecoveryPassesEverythingElse(t *testing.T) {
	for name, body := range map[string]string{
		"success":         `{"code":"ok","message":"all good"}`,
		"unknown refusal": `{"code":"internal","message":"boom: something else entirely"}`,
	} {
		upstream := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(body))
		})
		srv := httptest.NewServer(ArgsRefusalRecovery(upstream))
		resp, err := http.Get(srv.URL)
		if err != nil {
			t.Fatal(err)
		}
		got := make([]byte, 4096)
		n, _ := resp.Body.Read(got)
		resp.Body.Close()
		srv.Close()
		if string(got[:n]) != body {
			t.Fatalf("%s: body = %q, want it forwarded byte for byte (%q)", name, got[:n], body)
		}
	}
}
