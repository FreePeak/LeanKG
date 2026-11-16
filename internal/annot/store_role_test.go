package annot

import (
	"context"
	"testing"
)

// kvOnly implements only store.KV, the role annot depends on. Passing it to
// annot proves the dependency is narrowed: it would not compile against
// store.Backend.
type kvOnly struct{ m map[string]string }

func (k *kvOnly) KVSet(_ context.Context, ns, key, value string) error {
	k.m[ns+"/"+key] = value
	return nil
}

func (k *kvOnly) KVGet(_ context.Context, ns, key string) (string, bool, error) {
	v, ok := k.m[ns+"/"+key]
	return v, ok, nil
}

func TestAnnotateRoundTripOverKVRoleOnly(t *testing.T) {
	ctx := context.Background()
	st := &kvOnly{m: map[string]string{}}

	created, err := Annotate(ctx, st, "pkg.Parse", "parses the input", nil, nil)
	if err != nil {
		t.Fatalf("annotate: %v", err)
	}
	if !created {
		t.Fatalf("annotate created = false; want true for a new element")
	}

	r, err := Get(ctx, st, "pkg.Parse")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if r == nil || r.Description != "parses the input" {
		t.Fatalf("get = %+v; want the description written through KV", r)
	}
}
