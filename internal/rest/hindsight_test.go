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

// TestHindsightCompatReadSurface covers the K3/K4 read routes: stats, list,
// and read-by-id. The by-id row must come back byte-identically to the same
// row inside a recall, since the client prints whichever it got.
func TestHindsightCompatReadSurface(t *testing.T) {
	e, mem := newEngine(t)
	srv := httptest.NewServer(Handler(e, mem, WithHindsightCompat()))
	defer srv.Close()
	base := srv.URL + "/v1/default/banks/demo"

	var retain struct {
		Written int `json:"written"`
	}
	if code := postJSON(t, base+"/memories", map[string]any{
		"items": []map[string]any{
			{"content": "alpha memory", "tags": []string{"project:t"}, "document_id": "doc-alpha"},
			{"content": "beta memory", "tags": []string{"project:t"}},
		},
	}, &retain); code != http.StatusOK || retain.Written != 2 {
		t.Fatalf("retain: status=%d written=%d", code, retain.Written)
	}

	// stats: the route xdev's /memory stats calls. 404 here is what made a
	// healthy server print "server unreachable".
	var stats struct {
		Bank     string `json:"bank"`
		Entries  int    `json:"entries"`
		Banks    int    `json:"banks"`
		Bytes    int64  `json:"bytes"`
		LastReta int64  `json:"last_retain"`
		Root     string `json:"root"`
	}
	if code := getJSON(t, base+"/stats", &stats); code != http.StatusOK {
		t.Fatalf("stats status = %d", code)
	}
	if stats.Bank != "demo" || stats.Entries != 2 || stats.Banks != 1 || stats.Bytes <= 0 || stats.LastReta == 0 || stats.Root == "" {
		t.Fatalf("stats = %+v", stats)
	}

	// list: newest-first, capped by limit, with the unpaged total alongside.
	var list struct {
		BankID  string `json:"bank_id"`
		Object  string `json:"object"`
		Total   int    `json:"total"`
		Results []struct {
			Text string   `json:"text"`
			ID   string   `json:"id"`
			Tags []string `json:"tags"`
		} `json:"results"`
	}
	if code := getJSON(t, base+"/memories?limit=1", &list); code != http.StatusOK {
		t.Fatalf("list status = %d", code)
	}
	if list.Total != 2 || len(list.Results) != 1 {
		t.Fatalf("list = total %d, %d results", list.Total, len(list.Results))
	}
	rowID := list.Results[0].ID
	if rowID == "" {
		t.Fatal("list row has no id — read-by-id would be unusable")
	}

	// read-by-id, then the same row out of a recall: identical shapes.
	var one struct {
		Text string   `json:"text"`
		ID   string   `json:"id"`
		Tags []string `json:"tags"`
	}
	if code := getJSON(t, base+"/memories/"+rowID, &one); code != http.StatusOK {
		t.Fatalf("by-id status = %d", code)
	}
	if one.ID != rowID || one.Text != list.Results[0].Text {
		t.Fatalf("by-id = %+v, want the listed row %+v", one, list.Results[0])
	}
	if len(one.Tags) != 1 || one.Tags[0] != "project:t" {
		t.Fatalf("by-id tags did not round-trip: %+v", one.Tags)
	}
	// The recall row and the by-id row must be the SAME shape — the client
	// prints whichever it got, so a divergence shows up as a rendering bug.
	var recall struct {
		Results []struct {
			Text string   `json:"text"`
			ID   string   `json:"id"`
			Tags []string `json:"tags"`
		} `json:"results"`
	}
	if code := postJSON(t, base+"/memories/recall", map[string]any{"query": "alpha memory"}, &recall); code != http.StatusOK {
		t.Fatalf("recall status = %d", code)
	}
	hit := false
	for _, r := range recall.Results {
		if r.ID != rowID {
			continue
		}
		hit = true
		if r.Text != one.Text || len(r.Tags) != len(one.Tags) || r.Tags[0] != one.Tags[0] {
			t.Errorf("recall row %+v != by-id row %+v", r, one)
		}
	}
	if !hit {
		t.Errorf("the recalled row %q is missing from recall's %d results", rowID, len(recall.Results))
	}

	// by-id resolves the client's own document_id too, and an unknown id is a
	// 404 rather than an empty 200 (a harness editing from an empty read would
	// otherwise look like a successful edit).
	var byDoc struct {
		ID string `json:"id"`
	}
	if code := getJSON(t, base+"/memories/doc-alpha", &byDoc); code != http.StatusOK {
		t.Fatalf("by document_id status = %d", code)
	}
	if byDoc.ID != rowID {
		t.Fatalf("by document_id ID = %q, want %q", byDoc.ID, rowID)
	}
	if code := getJSON(t, base+"/memories/no-such-id", nil); code != http.StatusNotFound {
		t.Fatalf("unknown id status = %d, want 404", code)
	}
}
