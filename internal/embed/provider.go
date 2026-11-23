// Package embed is the leankg-embed pipeline library (issue #368): the model-
// stamped embedding pipeline shared by every vector writer. The library is
// stateless beyond the store — single-flight (flock) is the caller's job.
package embed

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math"

	"github.com/FreePeak/LeanKG/internal/store"
)

// TextKind distinguishes query-time from document-time embedding because
// asymmetric models (query/document prefixes) must not be mixed.
type TextKind int

const (
	Document TextKind = iota
	Query
)

// MaxContentChars bounds the text sent to a provider; content over this
// counts one truncation per element (contract §internal/embed).
const MaxContentChars = 8000

// maxLocalTextChars is the query-side fallback budget for the local sidecar
// family (TextCap, when /props gives no budget). The write path never uses it:
// with no stored cap, no /props budget and no LEANKG_EMBED_TEXT_CAP pin a run
// or export refuses (resolveWriteCap). The reason for the figure: llama.cpp
// serving a BERT-size GGUF embedder (bge-small-en-v1.5,
// all-MiniLM-L6-v2 — both 512-token training context) REJECTS longer inputs
// with HTTP 500 instead of truncating the way the ONNX-era in-process
// pipeline did. One oversized element otherwise aborts the whole run — found
// dogfooding `leankg-embed run` on this repository (an 845-token element;
// then 2000 runes of real Go code measured 733 tokens, ~2.7 chars/token on
// WordPiece over code, well under the prose assumption).
//
// ponytail: a chars budget, not a tokenizer count — 1000 runes sits at
// ~500 tokens even for dense code (worst realistic ~2 chars/token), just
// inside the 512 wall. Elements pathological beyond that are counted in
// Report.Failed by embedFiles' per-item fallback, never fatal to the run.
// Upgrade path: a per-catalog-row MaxTokens plus a real tokenizer budget.
const maxLocalTextChars = 1000

// Provider produces embeddings for one pinned model.
//
// Identity is pure: it reads what the provider already knows and never does
// I/O. Facts that need the endpoint (a sidecar's model revision and text
// budget) are resolved by Probe(ctx), which the run, export and health paths
// call explicitly, and which is cached once it has an answer.
type Provider interface {
	Identity() Identity
	Probe(ctx context.Context) error
	Embed(ctx context.Context, kind TextKind, texts []string) ([][]float32, error)
}

// Identity names the vector space a provider writes and reads.
type Identity struct {
	// Provider is the transport label the stamp records: local, openai or
	// deterministic. It is not part of Key (see store.ModelStamp.Matches).
	Provider string
	// Model is the collection id (the stamp's model_id).
	Model string
	// Revision is the pinned or probed model revision.
	Revision string
	// Dims is the configured vector width.
	Dims int
	// ChunkerVersion and the query/document prefix pair complete the space:
	// they change what the vectors are, so they are part of Key.
	ChunkerVersion int
	QueryPrefix    string
	DocumentPrefix string
	// TextBudget is the per-text rune budget the endpoint reports; 0 = unknown
	// until Probe. Not part of Key: it shapes the write cap, not the space.
	TextBudget int
	// TextCapPin is the operator's LEANKG_EMBED_TEXT_CAP pin; 0 = none. Not
	// part of Key.
	TextCapPin int
}

// Key is the one encoding of a vector space:
// model|revision|provider|dims|chunker|query-prefix|document-prefix. The memory
// index stores each row under this key, so its shape is durable: a change here
// re-embeds every memory row.
func (id Identity) Key() string {
	return fmt.Sprintf("%s|%s|%s|%d|%d|%s|%s", id.Model, id.Revision, id.Provider, id.Dims,
		id.ChunkerVersion, id.QueryPrefix, id.DocumentPrefix)
}

// Stamp is the collection stamp this identity writes under.
func (id Identity) Stamp() store.ModelStamp {
	return store.ModelStamp{
		ModelID:        id.Model,
		Revision:       id.Revision,
		Dimensions:     id.Dims,
		Distance:       Distance,
		Provider:       id.Provider,
		ChunkerVersion: id.ChunkerVersion,
		QueryPrefix:    id.QueryPrefix,
		DocumentPrefix: id.DocumentPrefix,
	}
}

// newIdentity is the identity of a provider serving model: the pipeline
// components (chunker version, catalog prefix pair) are filled in here, once.
func newIdentity(provider, model, revision string, dims, textCapPin int) Identity {
	q, d := Prefixes(model)
	return Identity{Provider: provider, Model: model, Revision: revision, Dims: dims,
		ChunkerVersion: ChunkerVersion, QueryPrefix: q, DocumentPrefix: d, TextCapPin: textCapPin}
}

// EmbedQuery embeds one question under p's query kind. It probes first, so the
// text is cut to the endpoint's budget and the caller sees the probed identity.
func EmbedQuery(ctx context.Context, p Provider, text string) ([]float32, error) {
	if err := p.Probe(ctx); err != nil {
		return nil, err
	}
	vecs, err := p.Embed(ctx, Query, []string{TruncateText(text, TextCap(p))})
	if err != nil {
		return nil, err
	}
	if len(vecs) != 1 {
		return nil, fmt.Errorf("embed: query returned %d vectors, want 1", len(vecs))
	}
	return vecs[0], nil
}

// EmbedDocuments embeds stored texts (memory rows) under p's document kind,
// each cut to the provider's text budget.
func EmbedDocuments(ctx context.Context, p Provider, texts []string) ([][]float32, error) {
	if err := p.Probe(ctx); err != nil {
		return nil, err
	}
	cut := make([]string, len(texts))
	for i, t := range texts {
		cut[i] = TruncateText(t, TextCap(p))
	}
	return p.Embed(ctx, Document, cut)
}

// contentHashHex is the per-element content hash: SHA-256 hex of Content.
// Both Run (dirty planning) and NDJSON import (resume) use this exact fn.
func contentHashHex(content string) string {
	sum := sha256.Sum256([]byte(content))
	return hex.EncodeToString(sum[:])
}

// validateBatch checks a provider response for count, dimensions, and
// finiteness. Any violation fails the whole batch (counted, not fatal).
func validateBatch(out [][]float32, texts int, dims int) error {
	if len(out) != texts {
		return fmt.Errorf("embed: provider returned %d vectors for %d texts", len(out), texts)
	}
	for i, vec := range out {
		if len(vec) != dims {
			return fmt.Errorf("embed: vector %d has %d dims, want %d", i, len(vec), dims)
		}
		for _, x := range vec {
			if math.IsNaN(float64(x)) || math.IsInf(float64(x), 0) {
				return fmt.Errorf("embed: vector %d contains NaN/Inf", i)
			}
		}
	}
	return nil
}
