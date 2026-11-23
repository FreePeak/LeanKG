package graph

import (
	"context"
	"maps"
	"slices"
	"sort"
	"strings"

	"github.com/FreePeak/LeanKG/internal/store"
)

// Resolutions the index writes on a Go method edge (internal/index/crosspkg.go):
// a caller links to an interface spec as interface-spec and to the concrete
// implementers it may dispatch to as interface-dispatch.
const (
	resInterfaceSpec     = "interface-spec"
	resInterfaceDispatch = "interface-dispatch"
)

// ExpandSpecSeeds widens every seed that names an interface spec (a method
// element carrying metadata interface_spec) to the seeds its callers reach it
// through. The implementers its callers dispatch to are added, and so is every
// other spec of the same method name that shares one of those implementers, so
// a local interface in another package joins the answer. Seeds that are not
// specs (concrete methods, functions, unknown names) are kept as they are. The
// given seeds come first, in order; each added seed follows once, without repeats.
// The seeds added by expansion are capped at specSeedLimit; truncated reports
// that the cap or a row-capped lookup may have dropped some.
func ExpandSpecSeeds(ctx context.Context, st graphStore, seeds []string) ([]string, bool, error) {
	x := specExpander{st: st, dispatch: map[string][]string{}}
	out := make([]string, 0, len(seeds))
	seen := map[string]bool{}
	for _, seed := range seeds {
		if !seen[seed] {
			seen[seed] = true
			out = append(out, seed)
		}
	}
	added := 0
	truncated := false
	add := func(qn string) {
		if seen[qn] {
			return
		}
		if added == specSeedLimit {
			truncated = true
			return
		}
		seen[qn] = true
		added++
		out = append(out, qn)
	}
	for _, seed := range seeds {
		el, err := locate(ctx, st, seed)
		if err != nil {
			return nil, false, err
		}
		if !IsInterfaceSpec(el) {
			continue
		}
		impls, err := x.dispatchTargets(ctx, el)
		if err != nil {
			return nil, false, err
		}
		if len(impls) == 0 {
			continue
		}
		siblings, sharedTruncated, err := x.sharingSpecs(ctx, el, impls)
		if err != nil {
			return nil, false, err
		}
		truncated = truncated || sharedTruncated
		for _, qn := range impls {
			add(qn)
		}
		for _, qn := range siblings {
			add(qn)
		}
	}
	return out, truncated, nil
}

// specSeedLimit caps the seeds one expansion adds to the given ones.
const specSeedLimit = 64

// storeFindExactLimit is the row cap of FindExact: LIMIT 200 in
// internal/store/elements.go:559 and internal/store/store_pg.go:329. A lookup
// that returns this many rows may have dropped some.
const storeFindExactLimit = 200

// specExpander memoizes the dispatch targets of each spec for one expansion, so
// a name shared by many specs reads each caller's edges once.
type specExpander struct {
	st       graphStore
	dispatch map[string][]string
}

// IsInterfaceSpec reports whether el is a Go interface method spec.
func IsInterfaceSpec(el store.Element) bool {
	v, _ := el.Metadata["interface_spec"].(bool)
	return v
}

// resolutionOf is the resolution metadata an edge carries, "" when it has none.
func resolutionOf(r store.Relationship) string {
	s, _ := r.Metadata["resolution"].(string)
	return s
}

// dispatchTargets are the implementers the callers of spec dispatch to: for each
// caller that links to spec through an interface edge, the targets of that
// caller's interface-dispatch edges that carry spec's method name. A caller
// often dispatches other interfaces' methods too (Close, Files), and those are
// not implementers of spec. Sorted and unique.
func (x specExpander) dispatchTargets(ctx context.Context, el store.Element) ([]string, error) {
	if got, ok := x.dispatch[el.QualifiedName]; ok {
		return got, nil
	}
	in, err := x.st.Incoming(ctx, el.QualifiedName)
	if err != nil {
		return nil, err
	}
	targets := map[string]bool{}
	for _, r := range in {
		if res := resolutionOf(r); res != resInterfaceSpec && res != resInterfaceDispatch {
			continue
		}
		out, err := x.st.Outgoing(ctx, r.Source)
		if err != nil {
			return nil, err
		}
		for _, o := range out {
			if resolutionOf(o) == resInterfaceDispatch && strings.HasSuffix(o.Target, "."+el.Name) {
				targets[o.Target] = true
			}
		}
	}
	got := slices.Sorted(maps.Keys(targets))
	x.dispatch[el.QualifiedName] = got
	return got, nil
}

// sharingSpecs are the other specs of el's method name whose callers dispatch to
// at least one of impls, sorted.
func (x specExpander) sharingSpecs(ctx context.Context, el store.Element, impls []string) ([]string, bool, error) {
	cands, err := x.st.FindExact(ctx, el.Name)
	if err != nil {
		return nil, false, err
	}
	truncated := len(cands) >= storeFindExactLimit
	want := map[string]bool{}
	for _, qn := range impls {
		want[qn] = true
	}
	var out []string
	for _, c := range cands {
		if c.QualifiedName == el.QualifiedName || c.Name != el.Name || !IsInterfaceSpec(c) {
			continue
		}
		theirs, err := x.dispatchTargets(ctx, c)
		if err != nil {
			return nil, false, err
		}
		if slices.ContainsFunc(theirs, func(qn string) bool { return want[qn] }) {
			out = append(out, c.QualifiedName)
		}
	}
	sort.Strings(out)
	return out, truncated, nil
}
