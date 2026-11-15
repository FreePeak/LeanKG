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

// maxContentChars bounds the text sent to a provider; content over this
// counts one truncation per element (contract §internal/embed).
const maxContentChars = 8000

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
// The accessors are pure: they never reach the network. Facts that need the
// endpoint (a sidecar's model revision and text budget) are resolved by
// Probe(ctx), which the run, export and health paths call explicitly before
// they read the identity. Probe is cached once it has an answer.
type Provider interface {
	ModelID() string
	Revision() string
	Dimensions() int
	Distance() string
	Provider() string
	Probe(ctx context.Context) error
	Embed(ctx context.Context, kind TextKind, texts []string) ([][]float32, error)
}

// Identity names the vector space a provider writes and reads. Key is the one
// encoding of that space: the memory index keys its rows by it and the noise
// floor calibration is bound to it.
type Identity struct {
	Provider       string
	Model          string
	Revision       string
	Dims           int
	ChunkerVersion int
	QueryPrefix    string
	DocumentPrefix string
}

// Key is model|revision|provider|dims|chunker|query-prefix|document-prefix. Its
// shape is durable: a change re-embeds every memory row.
func (id Identity) Key() string {
	return fmt.Sprintf("%s|%s|%s|%d|%d|%s|%s", id.Model, id.Revision, id.Provider, id.Dims,
		id.ChunkerVersion, id.QueryPrefix, id.DocumentPrefix)
}

// IdentityOf reads the identity p writes under. It is pure: call Probe first
// when the revision of an unpinned sidecar matters.
func IdentityOf(p Provider) Identity {
	q, d := Prefixes(p.ModelID())
	return Identity{Provider: p.Provider(), Model: p.ModelID(), Revision: p.Revision(), Dims: p.Dimensions(),
		ChunkerVersion: ChunkerVersion, QueryPrefix: q, DocumentPrefix: d}
}

// KeyOf is the vector-space key of a stored collection stamp, the same key
// IdentityOf(p).Key() gives for the provider that wrote it.
func KeyOf(s store.ModelStamp) string {
	return identityOfStamp(s).Key()
}

// identityOfStamp is the identity a stamp records.
func identityOfStamp(s store.ModelStamp) Identity {
	return Identity{Provider: s.Provider, Model: s.ModelID, Revision: s.Revision, Dims: s.Dimensions,
		ChunkerVersion: s.ChunkerVersion, QueryPrefix: s.QueryPrefix, DocumentPrefix: s.DocumentPrefix}
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
