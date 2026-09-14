package embed

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

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
	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("embed: read response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("embed: POST %s/embeddings: status %d: %s",
			o.baseURL, resp.StatusCode, snippet(respBody))
	}
	var out embeddingsResponse
	if err := json.Unmarshal(respBody, &out); err != nil {
		return nil, fmt.Errorf("embed: decode response: %w (body: %s)", err, snippet(respBody))
	}
	vecs := make([][]float32, len(out.Data))
	for i, d := range out.Data {
		vecs[i] = d.Embedding
	}
	return vecs, nil
}

// snippet returns the first 200 bytes of a response body for error messages.
func snippet(b []byte) string {
	s := string(b)
	if len(s) > 200 {
		s = s[:200]
	}
	return s
}
