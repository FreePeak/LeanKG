package summarize

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

// Chat is the one-shot completion seam both passes call. It is deliberately
// tiny: the pipeline owns the prompts, the batching, the validation and the
// resume bookkeeping, so a provider only has to answer with text.
type Chat interface {
	// Complete issues ONE deterministic completion (temperature is pinned to 0
	// by the implementation) and returns the assistant message text.
	Complete(ctx context.Context, req Request) (string, error)
	// Model reports the model id the pipeline stamps persisted summaries with.
	Model() string
}

// Request is one completion request: a system instruction, the user payload
// (already clipped by the caller) and whether the reply must be a JSON object.
type Request struct {
	System string
	User   string
	JSON   bool
}

// LLMConfig is the LEANKG_LLM_* environment of the meaning pipeline.
//
//	LEANKG_LLM_MODEL         chat model id. Required for a real run: the
//	                         pipeline has no default because a wrong guess
//	                         silently writes a wrong meaning tier, and the id
//	                         is stamped on every summary row (a model switch
//	                         invalidates the tier's resume state).
//	LEANKG_LLM_BASE_URL      OpenAI-compatible API root, default
//	                         https://api.openai.com/v1. Point it at
//	                         llama.cpp's llama-server (/v1/chat/completions),
//	                         Ollama's OpenAI endpoint, vLLM, or a gateway.
//	LEANKG_LLM_API_KEY       bearer token; the Authorization header is
//	                         omitted entirely when empty (same contract as
//	                         internal/embed/openai.go).
//	LEANKG_LLM_MAX_TOKENS    per-call output cap, default 2048 (graft
//	                         summarize.ts:19's maxTokens). It bounds BOTH
//	                         passes: pass 2's batch budget bounds its input,
//	                         not its output.
//	LEANKG_LLM_TIMEOUT_SECS  per-call deadline, default 120. A completion is
//	                         slow; a hung request must still fail the gate
//	                         rather than stall a run forever.
type LLMConfig struct {
	BaseURL   string
	APIKey    string
	Model     string
	MaxTokens int
	Timeout   time.Duration
}

// ConfigFromEnv reads and validates the LEANKG_LLM_* environment.
func ConfigFromEnv() (LLMConfig, error) {
	cfg := LLMConfig{
		BaseURL:   strings.TrimRight(strings.TrimSpace(os.Getenv("LEANKG_LLM_BASE_URL")), "/"),
		APIKey:    os.Getenv("LEANKG_LLM_API_KEY"),
		Model:     strings.TrimSpace(os.Getenv("LEANKG_LLM_MODEL")),
		MaxTokens: 2048,
		Timeout:   120 * time.Second,
	}
	if cfg.BaseURL == "" {
		cfg.BaseURL = "https://api.openai.com/v1"
	}
	if cfg.Model == "" {
		return LLMConfig{}, fmt.Errorf("summarize: LEANKG_LLM_MODEL is required (plus LEANKG_LLM_BASE_URL/LEANKG_LLM_API_KEY for a remote provider)")
	}
	if v := strings.TrimSpace(os.Getenv("LEANKG_LLM_MAX_TOKENS")); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			return LLMConfig{}, fmt.Errorf("summarize: LEANKG_LLM_MAX_TOKENS=%q: want a positive integer", v)
		}
		cfg.MaxTokens = n
	}
	if v := strings.TrimSpace(os.Getenv("LEANKG_LLM_TIMEOUT_SECS")); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			return LLMConfig{}, fmt.Errorf("summarize: LEANKG_LLM_TIMEOUT_SECS=%q: want a positive integer", v)
		}
		cfg.Timeout = time.Duration(n) * time.Second
	}
	return cfg, nil
}

// New builds the HTTP chat client for this configuration.
func (c LLMConfig) New() Chat {
	return &httpChat{
		baseURL:   strings.TrimRight(c.BaseURL, "/"),
		apiKey:    c.APIKey,
		model:     c.Model,
		maxTokens: c.MaxTokens,
		client:    &http.Client{Timeout: c.Timeout},
	}
}

// httpChat talks the OpenAI /chat/completions wire shape — the same provider
// family internal/embed's /embeddings client covers, so one local llama-server
// sidecar can serve both tiers.
type httpChat struct {
	baseURL   string
	apiKey    string
	model     string
	maxTokens int
	client    *http.Client
}

func (c *httpChat) Model() string { return c.model }

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatRequest struct {
	Model       string          `json:"model"`
	Messages    []chatMessage   `json:"messages"`
	Temperature float64         `json:"temperature"`
	MaxTokens   int             `json:"max_tokens"`
	Format      *responseFormat `json:"response_format,omitempty"`
}

// responseFormat is the JSON-object switch. Providers that do not support it
// ignore the field; the caller recovers the JSON from prose either way (see
// extractJSON), so a non-supporting server degrades instead of failing.
type responseFormat struct {
	Type string `json:"type"`
}

type chatResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// Complete posts one chat completion. Temperature is pinned to 0 here rather
// than exposed: determinism is the pipeline's contract, not a caller option.
func (c *httpChat) Complete(ctx context.Context, req Request) (string, error) {
	body := chatRequest{
		Model:       c.model,
		Temperature: 0,
		MaxTokens:   c.maxTokens,
		Messages: []chatMessage{
			{Role: "system", Content: req.System},
			{Role: "user", Content: req.User},
		},
	}
	if req.JSON {
		body.Format = &responseFormat{Type: "json_object"}
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return "", err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.baseURL+"/chat/completions", bytes.NewReader(payload))
	if err != nil {
		return "", err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if c.apiKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)
	}
	resp, err := c.client.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("summarize: POST %s/chat/completions: %w", c.baseURL, err)
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return "", fmt.Errorf("summarize: read response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		// The status travels in the message on purpose: the failure gate's
		// terminal detection is message-based (graft failure.ts:29-36), which
		// is what keeps it provider-neutral.
		return "", fmt.Errorf("summarize: POST %s/chat/completions: status %d: %s",
			c.baseURL, resp.StatusCode, snippet(respBody))
	}
	var out chatResponse
	if err := json.Unmarshal(respBody, &out); err != nil {
		return "", fmt.Errorf("summarize: decode response: %w (body: %s)", err, snippet(respBody))
	}
	if len(out.Choices) == 0 {
		if out.Error != nil && out.Error.Message != "" {
			return "", fmt.Errorf("summarize: provider error: %s", out.Error.Message)
		}
		return "", fmt.Errorf("summarize: provider returned no choices (body: %s)", snippet(respBody))
	}
	return out.Choices[0].Message.Content, nil
}

// snippet returns the first 200 bytes of a response body for error messages
// (the shape internal/embed/openai.go uses).
func snippet(b []byte) string {
	s := string(b)
	if len(s) > 200 {
		s = s[:200]
	}
	return s
}
