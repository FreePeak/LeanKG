package projects

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
)

// fakeProjectStore stands in for a project store. Close is counted, so a test
// can tell whether the router released the project.
type fakeProjectStore struct{ closed atomic.Int32 }

func (s *fakeProjectStore) Engine() string { return "fake" }
func (s *fakeProjectStore) Close() error   { s.closed.Add(1); return nil }

// fakeOpener replaces the router's opener, so registration, eviction and
// removal are tested without a real store on disk.
type fakeOpener struct {
	mu     sync.Mutex
	opens  map[string]int
	stores map[string]*fakeProjectStore
}

func newFakeOpener() *fakeOpener {
	return &fakeOpener{opens: map[string]int{}, stores: map[string]*fakeProjectStore{}}
}

func (o *fakeOpener) open(_ context.Context, dir string) (*Project, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.opens[dir]++
	st := &fakeProjectStore{}
	o.stores[dir] = st
	return &Project{Dir: dir, Name: filepath.Base(dir), Store: st}, nil
}

func (o *fakeOpener) storeOf(dir string) *fakeProjectStore {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.stores[dir]
}

func (o *fakeOpener) openCount(dir string) int {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.opens[dir]
}

func newFakeRouter(def string, cfg Config) (*Router, *fakeOpener) {
	r := NewRouter(def, cfg)
	fo := newFakeOpener()
	r.open = fo.open
	return r, fo
}

// leaseAndRelease opens dir and releases the lease at once, as a request does
// when it finishes.
func leaseAndRelease(t *testing.T, r *Router, dir string) {
	t.Helper()
	_, release, err := r.Open(context.Background(), dir)
	if err != nil {
		t.Fatalf("open %s: %v", dir, err)
	}
	release()
}

func closedCount(t *testing.T, fo *fakeOpener, dir string) int32 {
	t.Helper()
	st := fo.storeOf(canonical(dir))
	if st == nil {
		t.Fatalf("%s was never opened", dir)
	}
	return st.closed.Load()
}

func TestAddRegistersARoutableProjectAtRuntime(t *testing.T) {
	base := t.TempDir()
	def := filepath.Join(base, "primary")
	alpha := filepath.Join(base, "alpha")
	r, _ := newFakeRouter(def, Config{})

	if r.MultiProject() {
		t.Fatal("a router with one project must not be multi-project")
	}
	if _, err := r.resolve(alpha); err == nil {
		t.Fatal("alpha must be unknown before Add")
	}
	if err := r.Add(alpha); err != nil {
		t.Fatalf("Add: %v", err)
	}
	// A second spelling of the same directory is the same registration.
	if err := r.Add(alpha + string(filepath.Separator)); err != nil {
		t.Fatalf("second Add: %v", err)
	}
	if got := r.List(); len(got) != 2 || got[1] != canonical(alpha) {
		t.Fatalf("List = %v, want [default alpha]", got)
	}
	if got, err := r.resolve(alpha); err != nil || got != canonical(alpha) {
		t.Fatalf("resolve(alpha) = %s,%v; a runtime Add must be routable", got, err)
	}
	if !r.MultiProject() {
		t.Fatal("after Add the router serves two projects")
	}
}

func TestRemoveUnregistersClosesAndIsIdempotent(t *testing.T) {
	base := t.TempDir()
	def := filepath.Join(base, "primary")
	alpha := filepath.Join(base, "alpha")
	r, fo := newFakeRouter(def, Config{})
	if err := r.Add(alpha); err != nil {
		t.Fatal(err)
	}
	leaseAndRelease(t, r, alpha)

	if err := r.Remove(alpha); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if n := closedCount(t, fo, alpha); n != 1 {
		t.Fatalf("Remove closed the store %d times, want 1", n)
	}
	if _, err := r.resolve(alpha); err == nil {
		t.Fatal("a removed project must be unknown")
	}
	if err := r.Remove(alpha); err != nil {
		t.Fatalf("second Remove must be a no-op, got %v", err)
	}
	if err := r.Remove(def); err == nil {
		t.Fatal("the serving process's own project must not be removable")
	}
}

func TestRemoveWaitsForInFlightRequests(t *testing.T) {
	base := t.TempDir()
	def := filepath.Join(base, "primary")
	alpha := filepath.Join(base, "alpha")
	r, fo := newFakeRouter(def, Config{ExtraDirs: []string{alpha}})

	_, release, err := r.Open(context.Background(), alpha)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Remove(alpha); err != nil {
		t.Fatal(err)
	}
	if n := closedCount(t, fo, alpha); n != 0 {
		t.Fatalf("store closed under a live request (%d closes)", n)
	}
	release()
	release() // a second release must not close it twice
	if n := closedCount(t, fo, alpha); n != 1 {
		t.Fatalf("store closed %d times after the last lease, want 1", n)
	}
}

func TestEvictsLeastRecentlyUsedProjectOverTheCap(t *testing.T) {
	base := t.TempDir()
	def := filepath.Join(base, "primary")
	a := filepath.Join(base, "a")
	b := filepath.Join(base, "b")
	c := filepath.Join(base, "c")
	r, fo := newFakeRouter(def, Config{ExtraDirs: []string{a, b, c}, MaxOpen: 2})

	leaseAndRelease(t, r, a)
	leaseAndRelease(t, r, b)
	leaseAndRelease(t, r, a) // a is now the most recently used
	leaseAndRelease(t, r, c) // over the cap: b is the least recently used

	if n := closedCount(t, fo, b); n != 1 {
		t.Fatalf("b closed %d times, want 1 (LRU victim)", n)
	}
	if n := closedCount(t, fo, a); n != 0 {
		t.Fatalf("a closed %d times, want 0 (recently used)", n)
	}
	// An evicted project is still registered and reopens lazily.
	leaseAndRelease(t, r, b)
	if n := fo.openCount(canonical(b)); n != 2 {
		t.Fatalf("b opened %d times, want 2 (evicted, then reopened)", n)
	}
	if got, err := r.resolve(b); err != nil || got != canonical(b) {
		t.Fatalf("evicted project must stay routable: %s,%v", got, err)
	}
}

func TestEvictionSkipsProjectsWithLiveRequests(t *testing.T) {
	base := t.TempDir()
	def := filepath.Join(base, "primary")
	a := filepath.Join(base, "a")
	b := filepath.Join(base, "b")
	c := filepath.Join(base, "c")
	r, fo := newFakeRouter(def, Config{ExtraDirs: []string{a, b, c}, MaxOpen: 1})

	_, releaseA, err := r.Open(context.Background(), a)
	if err != nil {
		t.Fatal(err)
	}
	// a is in use, so it cannot be evicted to make room for b.
	leaseAndRelease(t, r, b)
	if n := closedCount(t, fo, a); n != 0 {
		t.Fatalf("a closed %d times while a request still held it", n)
	}
	releaseA()

	// Both are idle now: the next open evicts them, least recently used first.
	leaseAndRelease(t, r, c)
	if closedCount(t, fo, a) != 1 || closedCount(t, fo, b) != 1 {
		t.Fatal("idle projects must be evicted to honour the cap")
	}
	if n := closedCount(t, fo, c); n != 0 {
		t.Fatalf("the project just opened closed %d times", n)
	}
}

func TestOnEvictReportsEvictedAndRemovedProjects(t *testing.T) {
	base := t.TempDir()
	def := filepath.Join(base, "primary")
	a := filepath.Join(base, "a")
	b := filepath.Join(base, "b")
	r, _ := newFakeRouter(def, Config{ExtraDirs: []string{a, b}, MaxOpen: 1})

	var mu sync.Mutex
	var got []string
	r.OnEvict(func(dir string) {
		mu.Lock()
		defer mu.Unlock()
		got = append(got, dir)
	})
	leaseAndRelease(t, r, a)
	leaseAndRelease(t, r, b) // evicts a
	if err := r.Remove(b); err != nil {
		t.Fatal(err)
	}
	want := []string{canonical(a), canonical(b)}
	mu.Lock()
	defer mu.Unlock()
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("evicted/removed = %v, want %v", got, want)
	}
}

func TestRegistryIsSafeUnderConcurrentUse(t *testing.T) {
	base := t.TempDir()
	def := filepath.Join(base, "primary")
	r, _ := newFakeRouter(def, Config{MaxOpen: 3})

	var dirs []string
	for i := 0; i < 8; i++ {
		dirs = append(dirs, filepath.Join(base, "p", string(rune('a'+i))))
	}
	var wg sync.WaitGroup
	for _, d := range dirs {
		wg.Add(1)
		go func(d string) {
			defer wg.Done()
			if err := r.Add(d); err != nil {
				t.Errorf("Add: %v", err)
				return
			}
			_ = r.List()
			_ = r.MultiProject()
			if _, release, err := r.Open(context.Background(), d); err == nil {
				release()
			}
			if err := r.Remove(d); err != nil {
				t.Errorf("Remove: %v", err)
			}
		}(d)
	}
	wg.Wait()
	if got := r.List(); len(got) != 1 || got[0] != canonical(def) {
		t.Fatalf("after every add is removed, List = %v, want only the default", got)
	}
}

func TestMaxOpenProjectsReadsTheEnvironment(t *testing.T) {
	t.Setenv(MaxOpenEnv, "3")
	if r := NewRouter(t.TempDir(), Config{Hosted: true}); r.maxOpen != 3 {
		t.Fatalf("hosted maxOpen = %d, want 3 from %s", r.maxOpen, MaxOpenEnv)
	}
	// An operator who sets the env var outside hosted mode still gets the cap.
	if r := NewRouter(t.TempDir(), Config{}); r.maxOpen != 3 {
		t.Fatalf("non-hosted maxOpen = %d, want 3 from %s", r.maxOpen, MaxOpenEnv)
	}
	for _, bad := range []string{"", "0", "-2", "many"} {
		t.Setenv(MaxOpenEnv, bad)
		if r := NewRouter(t.TempDir(), Config{Hosted: true}); r.maxOpen != defaultMaxOpen {
			t.Fatalf("hosted env %q: maxOpen = %d, want the default %d", bad, r.maxOpen, defaultMaxOpen)
		}
		if r := NewRouter(t.TempDir(), Config{}); r.maxOpen != 0 {
			t.Fatalf("non-hosted env %q: maxOpen = %d, want 0 (no cap)", bad, r.maxOpen)
		}
	}
	if defaultMaxOpen != 64 {
		t.Fatalf("defaultMaxOpen = %d, want 64", defaultMaxOpen)
	}
}

func TestNonHostedRouterNeverEvictsOpenProjects(t *testing.T) {
	t.Setenv("LEANKG_PORTFOLIO_DB", filepath.Join(t.TempDir(), "portfolio.db"))
	t.Setenv(MaxOpenEnv, "")
	base := t.TempDir()
	def := filepath.Join(base, "primary")
	var dirs []string
	for i := 0; i < 120; i++ {
		dirs = append(dirs, filepath.Join(base, fmt.Sprintf("p%03d", i)))
	}
	r, fo := newFakeRouter(def, Config{ExtraDirs: dirs})
	for _, d := range dirs {
		leaseAndRelease(t, r, d)
	}
	for _, d := range dirs {
		if n := closedCount(t, fo, d); n != 0 {
			t.Fatalf("%s closed %d times; a non-hosted router must keep every opened project open", d, n)
		}
	}
}

func TestHostedRouterCapsOpenProjectsAtTheDefault(t *testing.T) {
	t.Setenv("LEANKG_PORTFOLIO_DB", filepath.Join(t.TempDir(), "portfolio.db"))
	t.Setenv(MaxOpenEnv, "")
	base := t.TempDir()
	def := filepath.Join(base, "primary")
	var dirs []string
	for i := 0; i < defaultMaxOpen+2; i++ {
		dirs = append(dirs, filepath.Join(base, fmt.Sprintf("p%03d", i)))
	}
	r, fo := newFakeRouter(def, Config{ExtraDirs: dirs, Hosted: true})
	for _, d := range dirs {
		leaseAndRelease(t, r, d)
	}
	if n := closedCount(t, fo, dirs[0]); n != 1 {
		t.Fatalf("hosted: first project closed %d times, want 1 (LRU victim over the default cap)", n)
	}
	if n := closedCount(t, fo, dirs[len(dirs)-1]); n != 0 {
		t.Fatalf("hosted: last project closed %d times, want 0", n)
	}
}

func TestHostedRouterEvictsOverASmallEnvCap(t *testing.T) {
	t.Setenv("LEANKG_PORTFOLIO_DB", filepath.Join(t.TempDir(), "portfolio.db"))
	t.Setenv(MaxOpenEnv, "2")
	base := t.TempDir()
	def := filepath.Join(base, "primary")
	a := filepath.Join(base, "a")
	b := filepath.Join(base, "b")
	c := filepath.Join(base, "c")
	r, fo := newFakeRouter(def, Config{ExtraDirs: []string{a, b, c}, Hosted: true})
	leaseAndRelease(t, r, a)
	leaseAndRelease(t, r, b)
	leaseAndRelease(t, r, c)
	if n := closedCount(t, fo, a); n != 1 {
		t.Fatalf("a closed %d times, want 1 (cap 2 from %s)", n, MaxOpenEnv)
	}
}

func TestMultiProjectSeesRuntimeAddOnASeededDefault(t *testing.T) {
	t.Setenv("LEANKG_PORTFOLIO_DB", filepath.Join(t.TempDir(), "portfolio.db"))
	base := t.TempDir()
	def := filepath.Join(base, "primary")
	alpha := filepath.Join(base, "alpha")
	r, _ := newFakeRouter(def, Config{})
	// The serving process seeds its own project into the router (SeedDefault),
	// so the open-project count includes it.
	r.SeedDefault(&Project{Dir: canonical(def), Name: "primary", Store: &fakeProjectStore{}})
	if r.MultiProject() {
		t.Fatal("a router with only its default project must not be multi-project")
	}
	if err := r.Add(alpha); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if !r.MultiProject() {
		t.Fatal("after a runtime Add the router serves two projects and must report multi-project")
	}
}
