package auth

import (
	"context"
	"testing"

	"github.com/FreePeak/LeanKG/internal/store"
)

// roleFake satisfies Store with the two roles embedded as nil interfaces. Only
// the methods a test overrides are callable; any other call panics, so a
// passing test proves the code path needs no other store method.
type roleFake struct {
	store.Tokens
	store.Identity
	tok     store.Token
	touched []string
}

func (f *roleFake) TokenFind(_ context.Context, secret string) (store.Token, bool, error) {
	if secret != "good" {
		return store.Token{}, false, nil
	}
	return f.tok, true, nil
}

func (f *roleFake) TokenTouch(_ context.Context, id string, _ int64) error {
	f.touched = append(f.touched, id)
	return nil
}

// TestVerifyNeedsOnlyTokenRole resolves a bearer through a fake that carries
// only the Tokens role (CX-38): the minted role comes back and last_used_at is
// stamped through the same role.
func TestVerifyNeedsOnlyTokenRole(t *testing.T) {
	f := &roleFake{tok: store.Token{ID: "tok-1", Role: "contributor"}}
	grant, found, err := Verify(context.Background(), f, "good")
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if !found {
		t.Fatal("Verify: known secret not found")
	}
	if grant.Role != Contributor || grant.TokenID != "tok-1" {
		t.Fatalf("grant = %+v, want role contributor on tok-1", grant)
	}
	if len(f.touched) != 1 || f.touched[0] != "tok-1" {
		t.Fatalf("touched = %v, want [tok-1]", f.touched)
	}
}
