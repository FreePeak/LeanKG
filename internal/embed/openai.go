package embed

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// ErrEndpointConfig marks a permanent configuration error: the endpoint at
// LEANKG_EMBED_BASE_URL is not an OpenAI-compatible embeddings API, or it
// refuses the bearer token. Retrying or shrinking a batch cannot fix it, so the
// embed pipeline returns it at once (embedFiles).
var ErrEndpointConfig = errors.New("embed: endpoint configuration error")

// respReadCap bounds how much of an embeddings reply is read. A reply that
// reaches it may be truncated, so it is an ordinary batch failure (the shrink
// path retries smaller batches), never a configuration error.
const respReadCap = 1 << 20

// OpenAICompatible talks the OpenAI /embeddings wire shape, which also covers
// the llama.cpp llama-server sidecar.
//
// The kind argument is NOT parity-only: an asymmetric model must see its
// pinned query prefix on queries and its document prefix on documents, so
// Embed applies the catalog pair (Prefixes) for the model at the provider
// boundary — the single place both the embed pipeline (Document) and the L3
// query rung (Query) pass through. The collection stamp carries the same pair
// (see StampOf), so a prefix change is a stamp mismatch, never a mixed
// collection.
func OpenAICompatible(baseURL, apiKey, model string, dims int, revision string) Provider {
	return &openaiCompatible{
		baseURL:  baseURL,
		apiKey:   apiKey,
		model:    model,
		dims:     dims,
		revision: revision,
		client:   &http.Client{},
	}
}

type openaiCompatible struct {
	baseURL  string
	apiKey   string
	model    string
	dims     int
	revision string
	client   *http.Client
}

func (o *openaiCompatible) ModelID() string  { return o.model }
func (o *openaiCompatible) Revision() string { return o.revision }
func (o *openaiCompatible) Dimensions() int  { return o.dims }
func (o *openaiCompatible) Distance() string { return Distance }
func (o *openaiCompatible) Provider() string { return "openai" }

// prefixFor returns the pinned text prefix paired with kind for this model.
func (o *openaiCompatible) prefixFor(kind TextKind) string {
	query, document := Prefixes(o.model)
	if kind == Query {
		return query
	}
	return document
}

// prefixTexts prepends prefix to every text (nil when prefix is empty, which
// is the common symmetric-model case: no copy, no allocation).
func prefixTexts(prefix string, texts []string) []string {
	if prefix == "" {
		return texts
	}
	out := make([]string, len(texts))
	for i, t := range texts {
		out[i] = prefix + t
	}
	return out
}

type embeddingsRequest struct {
	Model string   `json:"model"`
	Input []string `json:"input"`
}

type embeddingsResponse struct {
	Data []struct {
		Embedding []float32 `json:"embedding"`
	} `json:"data"`
}

func (o *openaiCompatible) Embed(ctx context.Context, kind TextKind, texts []string) ([][]float32, error) {
	sent := prefixTexts(o.prefixFor(kind), texts)
	body, err := json.Marshal(embeddingsRequest{Model: o.model, Input: sent})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		strings.TrimRight(o.baseURL, "/")+"/embeddings", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if o.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+o.apiKey)
	}

	resp, err := o.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("embed: POST %s/embeddings: %w", o.baseURL, err)
	}
	defer resp.Body.Close()
	// One byte past the cap tells a reply that fits from one that was cut.
	respBody, err := io.ReadAll(io.LimitReader(resp.Body, respReadCap+1))
	if err != nil {
		return nil, fmt.Errorf("embed: read response: %w", err)
	}
	endpoint := strings.TrimRight(o.baseURL, "/") + "/embeddings"
	switch resp.StatusCode {
	case http.StatusOK:
		if len(respBody) > respReadCap {
			return nil, fmt.Errorf("embed: POST %s: reply exceeds the 1 MiB read cap (%d bytes); the batch is retried in smaller pieces",
				endpoint, respReadCap)
		}
	case http.StatusNotFound, http.StatusMethodNotAllowed:
		return nil, o.configError(endpoint, fmt.Sprintf("answered HTTP %d: no embeddings route at this URL", resp.StatusCode), rootHint)
	case http.StatusUnauthorized, http.StatusForbidden:
		return nil, o.configError(endpoint, fmt.Sprintf("answered HTTP %d: the request was refused", resp.StatusCode),
			"check LEANKG_EMBED_API_KEY (the bearer token; unset or wrong for this endpoint)")
	default:
		return nil, fmt.Errorf("embed: POST %s/embeddings: status %d: %s",
			o.baseURL, resp.StatusCode, endpointSnippet(o.apiKey, respBody))
	}
	var out embeddingsResponse
	if err := json.Unmarshal(respBody, &out); err != nil || out.Data == nil {
		return nil, o.configError(endpoint, fmt.Sprintf("answered 200 with a body that is not an OpenAI embeddings object (%s)",
			endpointSnippet(o.apiKey, respBody)), rootHint)
	}
	vecs := make([][]float32, len(out.Data))
	for i, d := range out.Data {
		vecs[i] = d.Embedding
	}
	return vecs, nil
}

// rootHint is the fix for a base URL that points at llama-server's native
// routes instead of its OpenAI-compatible /v1 root.
const rootHint = "LEANKG_EMBED_BASE_URL must be the OpenAI-compatible root, for example http://127.0.0.1:9101/v1 (llama-server's /v1 route)"

// configError wraps ErrEndpointConfig with the URL that was called and the fix.
func (o *openaiCompatible) configError(endpoint, reason, hint string) error {
	return fmt.Errorf("%w: POST %s %s; %s", ErrEndpointConfig, endpoint, reason, hint)
}

// endpointSnippet is snippet for a message that must stay one line and must not
// echo the bearer token: the key is redacted before the body is cut.
func endpointSnippet(apiKey string, b []byte) string {
	if apiKey != "" {
		b = bytes.ReplaceAll(b, []byte(apiKey), []byte("[redacted]"))
	}
	return strings.Join(strings.Fields(snippet(b)), " ")
}

// snippet returns the first 200 bytes of a response body for error messages.
func snippet(b []byte) string {
	s := string(b)
	if len(s) > 200 {
		s = s[:200]
	}
	return s
}
