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

// Provider produces embeddings for one pinned model.
type Provider interface {
	ModelID() string
	Revision() string
	Dimensions() int
	Distance() string
	Provider() string
	Embed(ctx context.Context, kind TextKind, texts []string) ([][]float32, error)
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
