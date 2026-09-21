package judge

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// answerJSON renders one wire answer.
func answerJSON(t *testing.T, typ, choice string, noul *float64, conf float64) string {
	t.Helper()
	prob := map[string]float64{"true": 0.85, "false": 0.15}
	if typ == "choice" {
		prob = map[string]float64{"billing": 0.94, "technical": 0.06}
	}
	b, _ := json.Marshal(map[string]any{"answers": map[string]any{
		"q": map[string]any{"type": typ, "choice": choice, "noul": noul, "probabilities": prob, "confidence": conf},
	}})
	return string(b)
}

func TestServerBatchedNoul(t *testing.T) {
	noul := 0.892
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/systemone" {
			t.Errorf("path = %s, want /v1/systemone", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer k" {
			t.Errorf("auth = %q, want Bearer k", got)
		}
		var body struct {
			State     string                  `json:"state"`
			Model     string                  `json:"model"`
			Questions map[string]wireQuestion `json:"questions"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if len(body.Questions) != 2 {
			t.Errorf("questions = %d, want 2 in one call", len(body.Questions))
		}
		w.Write([]byte(`{"answers":{"churn":{"type":"noul","noul":0.892,"probabilities":{"true":0.892,"false":0.108},"confidence":0.78},"dept":{"type":"choice","choice":"billing","probabilities":{"billing":0.94,"technical":0.06},"confidence":0.9}}}`))
	}))
	defer srv.Close()

	s := NewServer(srv.URL, "k", "jev-1.13.0", 5*time.Second)
	got, err := s.Ask(context.Background(), `{"body":"refund or cancel"}`,
		map[string]Question{
			"churn": {Type: Noul, Instructions: "Does the user threaten to cancel?"},
			"dept":  {Type: Choice, Instructions: "Which department?", Criteria: map[string]string{"billing": "invoices", "technical": "bugs"}},
		})
	if err != nil {
		t.Fatalf("Ask: %v", err)
	}
	if got["churn"].Value != "0.892" || got["dept"].Value != "billing" {
		t.Errorf("answers = %+v", got)
	}
	if got["churn"].Confidence != 0.78 {
		t.Errorf("confidence = %v, want 0.78", got["churn"].Confidence)
	}
	_ = noul
	_ = answerJSON
}

func TestServerUnavailableNeverFails(t *testing.T) {
	// Unconfigured: nil map, nil error — caller keeps native rule.
	s := NewServer("", "", "", 0)
	got, err := s.Ask(context.Background(), "x", map[string]Question{"q": {Type: Noul, Instructions: "y?"}})
	if err != nil || !Unavailable(got) {
		t.Errorf("unconfigured = (%v, %v), want (nil, nil)", got, err)
	}
	// Unreachable host: same posture.
	s2 := NewServer("http://127.0.0.1:1", "", "", time.Second)
	got, err = s2.Ask(context.Background(), "x", map[string]Question{"q": {Type: Noul, Instructions: "y?"}})
	if err != nil || !Unavailable(got) {
		t.Errorf("unreachable = (%v, %v), want (nil, nil)", got, err)
	}
	// 500: same posture.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
	}))
	defer srv.Close()
	s3 := NewServer(srv.URL, "", "", 5*time.Second)
	got, err = s3.Ask(context.Background(), "x", map[string]Question{"q": {Type: Noul, Instructions: "y?"}})
	if err != nil || !Unavailable(got) {
		t.Errorf("500 = (%v, %v), want (nil, nil)", got, err)
	}
}

func TestServerRejectsBadQuestions(t *testing.T) {
	s := NewServer("http://example.com", "", "", time.Second)
	if _, err := s.Ask(context.Background(), "x", map[string]Question{"q": {Type: "bogus", Instructions: "y?"}}); err == nil {
		t.Error("unknown type: want error")
	}
	if _, err := s.Ask(context.Background(), "x", map[string]Question{"q": {Type: Noul}}); err == nil {
		t.Error("empty instructions: want error")
	}
}

func TestFromEnvDefaultsToNil(t *testing.T) {
	t.Setenv("LEANKG_JUDGE_URL", "")
	t.Setenv("LEANKG_JUDGE_SIDECAR_URL", "")
	if FromEnv() != nil {
		t.Error("unconfigured FromEnv: want nil judge")
	}
}

func TestLocalAttachesToSidecar(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"answers":{"q":{"type":"noul","noul":0.4,"probabilities":{"true":0.4,"false":0.6},"confidence":0.5}}}`))
	}))
	defer srv.Close()
	t.Setenv("LEANKG_JUDGE_SIDECAR_URL", srv.URL)
	j := FromEnv()
	if j == nil {
		t.Fatal("sidecar URL set: want non-nil judge")
	}
	got, err := j.Ask(context.Background(), "state", map[string]Question{"q": {Type: Noul, Instructions: "holds?"}})
	if err != nil || got["q"].Value != "0.4" {
		t.Errorf("local = (%v, %v)", got, err)
	}
	if !strings.HasPrefix(got["q"].Value, "0.") {
		t.Errorf("noul value = %q", got["q"].Value)
	}
}
