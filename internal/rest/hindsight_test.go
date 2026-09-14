package rest

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// postJSON sends body to url and decodes the JSON response into out.
func postJSON(t *testing.T, url string, body any, out any) int {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.Post(url, "application/json", bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if out != nil {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			t.Fatalf("decode %s response: %v", url, err)
		}
	}
	return resp.StatusCode
}

func TestHindsightCompatWire(t *testing.T) {
	e, mem := newEngine(t)
	srv := httptest.NewServer(Handler(e, mem, WithHindsightCompat()))
	defer srv.Close()
	base := srv.URL + "/v1/default/banks/demo"

	// PUT bank ensure answers 200 (the client swallows errors but expects ok).
	req, err := http.NewRequest(http.MethodPut, base, bytes.NewReader([]byte(`{"object":"bank"}`)))
	if err != nil {
		t.Fatal(err)
	}
	r1, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer r1.Body.Close()
	if r1.StatusCode != http.StatusOK {
		t.Fatalf("PUT bank status = %d, want 200", r1.StatusCode)
	}

	// Retain two tagged items, then a third in a SECOND call: the cursor-free
	// path must write both batches (Retain's 0-cursor gate would drop them).
	var retain struct {
		Written int `json:"written"`
	}
	if code := postJSON(t, base+"/memories", map[string]any{
		"items": []map[string]any{
			{"content": "preflight runs go run, not docker", "tags": []string{"proj-a"}},
			{"content": "preflight also needs make available", "context": "session 1"},
		},
	}, &retain); code != http.StatusOK {
		t.Fatalf("retain status = %d", code)
	}
	if retain.Written != 2 {
		t.Fatalf("written = %d, want 2", retain.Written)
	}
	if code := postJSON(t, base+"/memories", map[string]any{
		"items": []map[string]any{
			{"content": "preflight gate script is scripts/preflight.sh", "tags": []string{"proj-b"}},
		},
	}, &retain); code != http.StatusOK || retain.Written != 1 {
		t.Fatalf("second retain: status=%d written=%d", code, retain.Written)
	}
	// Empty-content items are dropped, not stored.
	if code := postJSON(t, base+"/memories", map[string]any{
		"items": []map[string]any{{"content": "   "}},
	}, &retain); code != http.StatusOK || retain.Written != 0 {
		t.Fatalf("blank retain: status=%d written=%d", code, retain.Written)
	}

	type recallOut struct {
		Results []struct {
			Text string   `json:"text"`
			Tags []string `json:"tags"`
		} `json:"results"`
	}
	recall := func(body map[string]any) recallOut {
		var out recallOut
		if code := postJSON(t, base+"/memories/recall", body, &out); code != http.StatusOK {
			t.Fatalf("recall status = %d", code)
		}
		return out
	}

	// Ungagged recall sees all three (the client reads results[].text).
	all := recall(map[string]any{"query": "preflight", "budget": "mid"})
	if len(all.Results) != 3 {
		t.Fatalf("ungagged recall = %d results, want 3: %+v", len(all.Results), all.Results)
	}

	// Tag scoping round-trips: same tag admits, disjoint tags exclude.
	tagged := recall(map[string]any{"query": "preflight", "tags": []string{"proj-a"}, "tags_match": "all"})
	if len(tagged.Results) != 1 || tagged.Results[0].Text != "preflight runs go run, not docker" {
		t.Fatalf("tagged recall = %+v", tagged.Results)
	}
	if len(tagged.Results[0].Tags) != 1 || tagged.Results[0].Tags[0] != "proj-a" {
		t.Fatalf("tags did not round-trip: %+v", tagged.Results[0].Tags)
	}
	disjoint := recall(map[string]any{"query": "preflight", "tags": []string{"proj-z"}, "tags_match": "all"})
	if len(disjoint.Results) != 0 {
		t.Fatalf("disjoint tags leaked results: %+v", disjoint.Results)
	}
	// "any" with one overlapping tag admits the row, disjoint ones exclude it.
	anyHit := recall(map[string]any{"query": "preflight", "tags": []string{"proj-a", "proj-z"}, "tags_match": "any"})
	if len(anyHit.Results) != 1 {
		t.Fatalf("any-match recall = %+v", anyHit.Results)
	}

	// Reflect answers the client's required shape: {text}.
	var reflectOut struct {
		Text string `json:"text"`
	}
	if code := postJSON(t, base+"/reflect", map[string]any{"query": "preflight"}, &reflectOut); code != http.StatusOK {
		t.Fatalf("reflect status = %d", code)
	}
	if !bytes.Contains([]byte(reflectOut.Text), []byte("preflight")) {
		t.Fatalf("reflect text does not digest the bank: %q", reflectOut.Text)
	}
}

func TestHindsightCompatNotMountedByDefault(t *testing.T) {
	e, mem := newEngine(t)
	srv := httptest.NewServer(Handler(e, mem))
	defer srv.Close()
	resp, err := http.Post(srv.URL+"/v1/default/banks/demo/memories", "application/json", bytes.NewReader([]byte(`{"items":[]}`)))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("compat route without the option: status = %d, want 404", resp.StatusCode)
	}
}
