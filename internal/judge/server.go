package judge

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// Server talks to a System One provider API (TypeSafe Jev or any
// Jev-compatible endpoint, including a self-hosted Laya server exposing the
// same state+questions shape — see rl_agent_api.py system_one in the
// convaiinnovations/laya repo).
//
//	LEANKG_JUDGE_URL       API root (required; no default — a wrong guess
//	                       silently judges against the wrong model, same rule
//	                       as LEANKG_LLM_MODEL in internal/summarize)
//	LEANKG_JUDGE_API_KEY   bearer token; omitted when empty (same contract
//	                       as internal/embed/openai.go)
//	LEANKG_JUDGE_MODEL     model id (default jev-1.13.0, the #433 pin)
//	LEANKG_JUDGE_TIMEOUT_SECS per-call deadline (default 30 — judgments ride
//	                       the query path, not the 120 s summarize tier)
type Server struct {
	baseURL string
	apiKey  string
	model   string
	timeout time.Duration
	client  *http.Client
}

// ConfigFromEnv reads the LEANKG_JUDGE_* environment. Empty URL means
// "no judge configured" — callers treat that as permanently unavailable,
// never as an error (native rules are the default; judgment is opt-in).
func ConfigFromEnv() (url, apiKey, model string, timeout time.Duration) {
	url = strings.TrimRight(strings.TrimSpace(os.Getenv("LEANKG_JUDGE_URL")), "/")
	apiKey = os.Getenv("LEANKG_JUDGE_API_KEY")
	model = strings.TrimSpace(os.Getenv("LEANKG_JUDGE_MODEL"))
	if model == "" {
		model = "jev-1.13.0"
	}
	timeout = 30 * time.Second
	if v := strings.TrimSpace(os.Getenv("LEANKG_JUDGE_TIMEOUT_SECS")); v != "" {
		var n int
		if _, err := fmt.Sscanf(v, "%d", &n); err == nil && n > 0 {
			timeout = time.Duration(n) * time.Second
		}
	}
	return url, apiKey, model, timeout
}

// NewServer builds the provider judge. Empty baseURL yields a judge that
// always reports unavailable (nil map) — zero config, zero calls.
func NewServer(baseURL, apiKey, model string, timeout time.Duration) *Server {
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	return &Server{baseURL: strings.TrimRight(baseURL, "/"), apiKey: apiKey, model: model, timeout: timeout, client: &http.Client{Timeout: timeout}}
}

type wireQuestion struct {
	Type         string            `json:"type"`
	Instructions string            `json:"instructions"`
	Criteria     map[string]string `json:"criteria,omitempty"`
}

type wireResponse struct {
	Answers map[string]struct {
		Type          string             `json:"type"`
		Choice        string             `json:"choice,omitempty"`
		Score         *float64           `json:"score,omitempty"`
		Noul          *float64           `json:"noul,omitempty"`
		Probabilities map[string]float64 `json:"probabilities,omitempty"`
		Confidence    *float64           `json:"confidence,omitempty"`
	} `json:"answers"`
}

// Ask posts one batched state+questions call. Transport failure or a
// non-200 is (nil, nil): the caller keeps its native rule with a reason —
// the same posture as the L3 provider degrade in internal/core.
func (s *Server) Ask(ctx context.Context, state string, questions map[string]Question) (map[string]Answer, error) {
	if s.baseURL == "" {
		return nil, nil
	}
	for id, q := range questions {
		switch q.Type {
		case Choice, Score, Noul:
		default:
			return nil, fmt.Errorf("judge: question %q: unknown type %q", id, q.Type)
		}
		if strings.TrimSpace(q.Instructions) == "" {
			return nil, fmt.Errorf("judge: question %q: empty instructions", id)
		}
	}
	wq := make(map[string]wireQuestion, len(questions))
	for id, q := range questions {
		wq[id] = wireQuestion{Type: string(q.Type), Instructions: q.Instructions, Criteria: q.Criteria}
	}
	body, err := json.Marshal(map[string]any{"state": state, "model": s.model, "questions": wq})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.baseURL+"/v1/systemone", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if s.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+s.apiKey)
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, nil // unreachable → unavailable, caller keeps native rule
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, nil
	}
	if resp.StatusCode != http.StatusOK {
		return nil, nil // non-200 → unavailable, never fail the outer op
	}
	var out wireResponse
	if err := json.Unmarshal(respBody, &out); err != nil {
		return nil, nil
	}
	answers := make(map[string]Answer, len(questions))
	for id := range questions {
		w, ok := out.Answers[id]
		if !ok {
			return nil, nil
		}
		a := Answer{Distribution: w.Probabilities}
		if w.Confidence != nil {
			a.Confidence = *w.Confidence
		}
		switch w.Type {
		case "choice":
			a.Value = w.Choice
		case "score":
			if w.Score != nil {
				a.Value = fmt.Sprintf("%g", *w.Score)
			}
		case "noul":
			if w.Noul != nil {
				a.Value = fmt.Sprintf("%g", *w.Noul)
			}
		}
		answers[id] = a
	}
	return answers, nil
}
