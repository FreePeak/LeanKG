package embed

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"math"
)

// Deterministic returns a hash-seeded unit-vector provider for tests and
// offline smoke ONLY — its vectors carry no real semantics.
func Deterministic(dims int) Provider { return deterministic{dims: dims} }

type deterministic struct{ dims int }

func (d deterministic) ModelID() string  { return fmt.Sprintf("deterministic-%d", d.dims) }
func (d deterministic) Revision() string { return "test:deterministic-v1" }
func (d deterministic) Dimensions() int  { return d.dims }
func (d deterministic) Distance() string { return "cosine" }
func (d deterministic) Provider() string { return "deterministic" }

func (d deterministic) Embed(ctx context.Context, kind TextKind, texts []string) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i, text := range texts {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		out[i] = unitVector(text, kind, d.dims)
	}
	return out, nil
}

// unitVector derives a deterministic unit vector: SHA-256 of (kind, text)
// seeds a xorshift64 stream whose outputs are L2-normalized.
func unitVector(text string, kind TextKind, dims int) []float32 {
	buf := make([]byte, 0, len(text)+1)
	buf = append(buf, byte(kind))
	buf = append(buf, text...)
	sum := sha256.Sum256(buf)

	state := binary.LittleEndian.Uint64(sum[:8])
	if state == 0 {
		state = 0x9e3779b97f4a7c15 // xorshift64 degenerates at 0
	}
	vec := make([]float32, dims)
	var norm float64
	for i := range vec {
		state ^= state << 13
		state ^= state >> 7
		state ^= state << 17
		vec[i] = float32(float64(int64(state)) / float64(1<<63)) // [-1, 1)
		norm += float64(vec[i]) * float64(vec[i])
	}
	norm = math.Sqrt(norm)
	if norm == 0 {
		vec[0] = 1
		return vec
	}
	for i := range vec {
		vec[i] /= float32(norm)
	}
	return vec
}
