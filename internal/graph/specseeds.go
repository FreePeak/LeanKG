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
func ExpandSpecSeeds(ctx context.Context, st graphStore, seeds []string) ([]string, error) {
	x := specExpander{ctx: ctx, st: st, dispatch: map[string][]string{}}
	out := make([]string, 0, len(seeds))
	seen := map[string]bool{}
	add := func(qn string) {
		if !seen[qn] {
			seen[qn] = true
			out = append(out, qn)
		}
	}
	for _, seed := range seeds {
		add(seed)
	}
	for _, seed := range seeds {
		el, err := locate(ctx, st, seed)
		if err != nil {
			return nil, err
		}
		if !IsInterfaceSpec(el) {
			continue
		}
		impls, err := x.dispatchTargets(el)
		if err != nil {
			return nil, err
		}
		if len(impls) == 0 {
			continue
		}
		siblings, err := x.sharingSpecs(el, impls)
		if err != nil {
			return nil, err
		}
		for _, qn := range impls {
			add(qn)
		}
		for _, qn := range siblings {
			add(qn)
		}
	}
	return out, nil
}

// specExpander memoizes the dispatch targets of each spec for one expansion, so
// a name shared by many specs reads each caller's edges once.
type specExpander struct {
	ctx      context.Context
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
func (x specExpander) dispatchTargets(el store.Element) ([]string, error) {
	if got, ok := x.dispatch[el.QualifiedName]; ok {
		return got, nil
	}
	in, err := x.st.Incoming(x.ctx, el.QualifiedName)
	if err != nil {
		return nil, err
	}
	targets := map[string]bool{}
	for _, r := range in {
		if res := resolutionOf(r); res != resInterfaceSpec && res != resInterfaceDispatch {
			continue
		}
		out, err := x.st.Outgoing(x.ctx, r.Source)
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
func (x specExpander) sharingSpecs(el store.Element, impls []string) ([]string, error) {
	cands, err := x.st.FindExact(x.ctx, el.Name)
	if err != nil {
		return nil, err
	}
	want := map[string]bool{}
	for _, qn := range impls {
		want[qn] = true
	}
	var out []string
	for _, c := range cands {
		if c.QualifiedName == el.QualifiedName || c.Name != el.Name || !IsInterfaceSpec(c) {
			continue
		}
		theirs, err := x.dispatchTargets(c)
		if err != nil {
			return nil, err
		}
		if slices.ContainsFunc(theirs, func(qn string) bool { return want[qn] }) {
			out = append(out, c.QualifiedName)
		}
	}
	sort.Strings(out)
	return out, nil
}
