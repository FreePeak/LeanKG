package judge

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

// Local drives a Laya sidecar over HTTP: the same state+questions wire as
// Server, but against a process LeanKG spawns and owns — no provider, no
// key, no per-call cost. The sidecar is the `laya` PyPI server from the
// convaiinnovations/laya repo (Router preload recommended: language flips
// cost detection only, <1 ms, instead of a 7–10 s checkpoint rebuild).
//
//	LEANKG_JUDGE_SIDECAR_CMD        executable (default "laya-serve"; the
//	                                PyPI entry point serving predict over HTTP)
//	LEANKG_JUDGE_SIDECAR_ARGS       shell-quoted extra argv (default: serve
//	                                the English checkpoint preloaded)
//	LEANKG_JUDGE_SIDECAR_PORT       port (default 8090)
//	LEANKG_JUDGE_SIDECAR_READY_SECS startup health-poll bound (default 120)
//
// FromEnv picks the backend: LEANKG_JUDGE_URL set → Server (provider API);
// otherwise, when the sidecar command resolves, Local; neither → nil judge
// (native rules throughout, zero calls). Explicit > local > none.
type Local struct {
	baseURL string
	model   string
	timeout time.Duration
	client  *http.Client
}

// NewLocal attaches to an already-running sidecar (the spawn lifecycle is
// the serve command's job, mirroring embed.StartProvider: attach keeps the
// constructor side-effect free and the tests in-process).
func NewLocal(baseURL, model string, timeout time.Duration) *Local {
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	return &Local{baseURL: strings.TrimRight(baseURL, "/"), model: model, timeout: timeout, client: &http.Client{Timeout: timeout}}
}

// FromEnv builds the configured judge or returns nil when nothing is
// configured. A nil judge is the normal default: every call site checks for
// it first and keeps its native rule.
func FromEnv() Judge {
	url, apiKey, model, timeout := ConfigFromEnv()
	if url != "" {
		return NewServer(url, apiKey, model, timeout)
	}
	if baseURL := strings.TrimSpace(os.Getenv("LEANKG_JUDGE_SIDECAR_URL")); baseURL != "" {
		return NewLocal(baseURL, judgeModel(), timeout)
	}
	return nil
}

func judgeModel() string {
	if m := strings.TrimSpace(os.Getenv("LEANKG_JUDGE_MODEL")); m != "" {
		return m
	}
	// ponytail: the English ModernBERT-large root checkpoint is the default
	// because every current use case is English code/ops text; multilingual
	// routing graduates only with a measured non-English need. Upgrade path:
	// LEANKG_JUDGE_MODEL=convaiinnovations/laya-multilingual + Router preload.
	return "convaiinnovations/laya"
}

// Ask posts one batched call to the sidecar. Same contract as Server.Ask:
// transport failure or non-200 is (nil, nil) — unavailable, never fatal.
func (l *Local) Ask(ctx context.Context, state string, questions map[string]Question) (map[string]Answer, error) {
	if l.baseURL == "" {
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
	body, err := json.Marshal(map[string]any{"state": state, "model": l.model, "questions": wq})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, l.baseURL+"/v1/systemone", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := l.client.Do(req)
	if err != nil {
		return nil, nil
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, nil
	}
	if resp.StatusCode != http.StatusOK {
		return nil, nil
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
				a.Value = strconv.FormatFloat(*w.Score, 'g', -1, 64)
			}
		case "noul":
			if w.Noul != nil {
				a.Value = strconv.FormatFloat(*w.Noul, 'g', -1, 64)
			}
		}
		answers[id] = a
	}
	return answers, nil
}
