package rest

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/FreePeak/LeanKG/internal/core"
	"github.com/FreePeak/LeanKG/internal/memory"
	"github.com/FreePeak/LeanKG/internal/store"
)

// TestRESTRetainRejectsTheShapeItDoesNotUnderstand pins the trust boundary on
// the REST retain route: a body the handler cannot read must be REFUSED, never
// acknowledged as a successful retain of nothing.
//
// The defect, found by driving the live REST listener (wave 7, the last surface
// this loop had not touched). The route documents its body as
// `{"entries": [{"content", optional "id", "metadata"}], "through_user_turn"}`
// and even says so in a comment — the Hindsight shape. But an empty body, a
// body with no `entries`, and a body using the OTHER reasonable name for the
// array all returned:
//
//	200 {"ok":true,"retained":0}
//
// The empty-content guard that sits right above the write (added so a
// permanently-unrecallable entry is rejected) makes the omission conspicuous: the
// handler is careful about entries[3].content being empty and completely silent
// about the array being ABSENT. A client that names the field `items` — the
// name the hindsight-compat mount on the same listener uses — gets `ok: true`,
// writes nothing, and has no way to know. That is the same silent-success class
// the v4.13.2 memory incident was, on a different field.
//
// A successful retain of zero entries is a legitimate answer only when the
// caller actually sent zero entries. Absent is not zero, and the difference is
// the difference between "nothing to remember" and "your request did not reach
// the field I read".
func TestRESTRetainRejectsTheShapeItDoesNotUnderstand(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(dir+"/.leankg/leankg.db", store.RW)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.Migrate(); err != nil {
		t.Fatal(err)
	}
	mem, err := memory.Open(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	eng := core.New(st, mem, nil)
	eng.SetProjectDir(dir)
	mux := Handler(eng, mem)

	post := func(body string) (int, string) {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/memory/banks/b1/memories", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		return rec.Code, rec.Body.String()
	}

	// The shape the route documents, and the one empty-but-explicit case, are
	// accepted.
	if code, body := post(`{"entries":[{"content":"remember this"}]}`); code != http.StatusOK {
		t.Fatalf("a documented retain must succeed, got %d: %s", code, body)
	}
	if code, body := post(`{"entries":[]}`); code != http.StatusOK {
		t.Fatalf("an EXPLICIT empty entries array is a valid no-op, got %d: %s", code, body)
	}

	// Everything the handler cannot read is refused, and the refusal names the
	// field the caller should have used.
	for _, tc := range []struct{ name, body, want string }{
		{"no body at all", ``, ""}, // invalid JSON: refused by the decoder, not the field gate
		{"empty object", `{}`, "entries"},
		{"the other name for the array", `{"items":[{"content":"remember this"}]}`, "entries"},
		{"entries is not an array", `{"entries":"remember this"}`, "entries"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			code, body := post(tc.body)
			if code == http.StatusOK {
				t.Fatalf("a body the handler cannot read must NOT report a successful retain (got %d): %s", code, body)
			}
			if code != http.StatusBadRequest {
				t.Errorf("want 400 for a malformed retain, got %d: %s", code, body)
			}
			if tc.want != "" && !strings.Contains(body, tc.want) {
				t.Errorf("the refusal must name the field the caller should use (%q), got %s", tc.want, body)
			}
		})
	}
}

// TestRESTRetainActuallyWrote pins the other half: an accepted retain must be
// visible, so "ok:true, retained:0" can never mean "silently dropped".
func TestRESTRetainActuallyWrote(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(dir+"/.leankg/leankg.db", store.RW)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.Migrate(); err != nil {
		t.Fatal(err)
	}
	mem, err := memory.Open(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	mux := Handler(core.New(st, mem, nil), mem)

	payload, _ := json.Marshal(map[string]any{
		"entries":           []map[string]any{{"content": "the stand must be open whenever development happens"}},
		"through_user_turn": 1,
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/memory/banks/b1/memories", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("retain: %d %s", rec.Code, rec.Body.String())
	}
	var out struct {
		OK       bool `json:"ok"`
		Retained int  `json:"retained"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if !out.OK || out.Retained != 1 {
		t.Fatalf("retain reported ok=%v retained=%d, want true/1: %s", out.OK, out.Retained, rec.Body.String())
	}
	// And the entry is recallable, which is the only reason a retain is worth
	// acknowledging. Recall needs a query by design — zero-match rows never
	// surface — so query the words the entry was written with.
	entries, err := mem.Recall("b1", "stand", 10)
	if err != nil {
		t.Fatalf("recall: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("an acknowledged retain left nothing recallable")
	}

	// The re-send: Retain is cursor-gated by contract (resume-safety, see
	// memory.Retain), so the same batch at the same cursor is SKIPPED — and the
	// route must SAY it was skipped instead of reporting the count it was sent.
	// `retained: 1` twice would be a lie an agent cannot detect.
	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodPost, "/api/v1/memory/banks/b1/memories", bytes.NewReader(payload))
	req2.Header.Set("Content-Type", "application/json")
	mux.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusOK {
		t.Fatalf("re-send retain: %d %s", rec2.Code, rec2.Body.String())
	}
	var again struct {
		OK       bool `json:"ok"`
		Retained int  `json:"retained"`
		Skipped  int  `json:"skipped"`
	}
	if err := json.Unmarshal(rec2.Body.Bytes(), &again); err != nil {
		t.Fatal(err)
	}
	if again.Skipped != 1 {
		t.Errorf("a cursor-gated re-send must report skipped=1, got %s", rec2.Body.String())
	}
	if again.Retained != 0 {
		t.Errorf("a skipped re-send must not claim retained=%d, got %s", again.Retained, rec2.Body.String())
	}
}
