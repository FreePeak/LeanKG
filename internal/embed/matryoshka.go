package embed

import (
	"context"
	"math"
)

// TruncateNormalize is the Matryoshka truncation: the first n components of
// vec, re-normalized to unit L2 length. It returns a new slice and never
// modifies vec. A zero prefix stays the zero vector (no division by zero). An
// n outside 1..len(vec) returns nil; callers validate the width first.
func TruncateNormalize(vec []float32, n int) []float32 {
	if n <= 0 || n > len(vec) {
		return nil
	}
	out := make([]float32, n)
	var sq float64
	for i, x := range vec[:n] {
		out[i] = x
		sq += float64(x) * float64(x)
	}
	norm := math.Sqrt(sq)
	if norm == 0 {
		return out
	}
	for i := range out {
		out[i] = float32(float64(out[i]) / norm)
	}
	return out
}

// matryoshka serves an inner provider at a truncated width. The inner keeps its
// native width, so each batch is validated against native before it is cut; the
// stamp (ModelID, Revision, Provider, Distance, and the catalog prefixes) is the
// inner's, and only Dimensions reports width.
type matryoshka struct {
	inner  Provider
	native int
	width  int
}

func (m matryoshka) ModelID() string  { return m.inner.ModelID() }
func (m matryoshka) Revision() string { return m.inner.Revision() }
func (m matryoshka) Distance() string { return m.inner.Distance() }
func (m matryoshka) Provider() string { return m.inner.Provider() }
func (m matryoshka) Dimensions() int  { return m.width }

// Unwrap exposes the provider underneath, for capability probes such as the
// sidecar's TextBudget (see unwrapProvider).
func (m matryoshka) Unwrap() Provider { return m.inner }

func (m matryoshka) Embed(ctx context.Context, kind TextKind, texts []string) ([][]float32, error) {
	vecs, err := m.inner.Embed(ctx, kind, texts)
	if err != nil {
		return nil, err
	}
	if err := validateBatch(vecs, len(texts), m.native); err != nil {
		return nil, err
	}
	for i := range vecs {
		vecs[i] = TruncateNormalize(vecs[i], m.width)
	}
	return vecs, nil
}

// withWidth serves inner at width: inner itself when width is its native width,
// otherwise the truncation wrapper. Both env constructors (newLocalProvider and
// openAIProvider) are the only places a catalogued provider is built, so every
// entry point (FromEnv, StartProvider, AttachHookProvider) reaches this one call.
func withWidth(inner Provider, native, width int) Provider {
	if width == native {
		return inner
	}
	return matryoshka{inner: inner, native: native, width: width}
}

// unwrapProvider peels decorators down to the concrete provider they wrap, so a
// capability probe (an interface assertion on the provider) sees the sidecar.
func unwrapProvider(p Provider) Provider {
	for {
		u, ok := p.(interface{ Unwrap() Provider })
		if !ok {
			return p
		}
		p = u.Unwrap()
	}
}
