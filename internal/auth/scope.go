package auth

import (
	"context"
	"net/http"

	"github.com/FreePeak/LeanKG/internal/store"
)

// ResourceProject is the ResourceClaim type a registered project is recorded
// under. The claim's org_id is the org that may see the project.
const ResourceProject = "project"

// ProjectScope returns the projects a caller may be served, as a predicate on
// canonical project dirs. A caller with no OrgID sees every project (nil). A
// caller with an OrgID sees only the projects whose ResourceClaim names that
// org. It keys on OrgID alone: account_id is not consulted, so a token minted
// with org_id only is scoped the same way.
func ProjectScope(ctx context.Context, st Store, ac AuthContext) (func(dir string) bool, error) {
	if ac.OrgID == "" {
		return nil, nil
	}
	claimed := map[string]bool{}
	if st != nil {
		dirs, err := st.ResourceIDs(ctx, ResourceProject, ac.OrgID)
		if err != nil {
			return nil, store.Fail("project scope", err)
		}
		for _, d := range dirs {
			claimed[d] = true
		}
	}
	return func(dir string) bool { return claimed[dir] }, nil
}

// RequestScope resolves the project scope of the request r (see ProjectScope).
// A request with no Authorization header has no org and gets no scope: the
// gate has already refused it when a token is configured, and with no token
// configured no org can exist.
func RequestScope(ctx context.Context, st Store, r *http.Request) (func(dir string) bool, error) {
	if r.Header.Get("Authorization") == "" {
		return nil, nil
	}
	ac, err := ResolveFor(st, r, Target{})
	if err != nil {
		return nil, err
	}
	return ProjectScope(ctx, st, ac)
}
