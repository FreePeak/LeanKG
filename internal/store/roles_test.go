package store

import (
	"path/filepath"
	"reflect"
	"sort"
	"testing"
)

// roleTypes lists every role interface declared in roles.go. Backend must be
// exactly the union of these plus its lifecycle methods.
var roleTypes = []reflect.Type{
	reflect.TypeOf((*IndexWriter)(nil)).Elem(),
	reflect.TypeOf((*ElementReader)(nil)).Elem(),
	reflect.TypeOf((*GraphReader)(nil)).Elem(),
	reflect.TypeOf((*Searcher)(nil)).Elem(),
	reflect.TypeOf((*Vectors)(nil)).Elem(),
	reflect.TypeOf((*EmbedRuns)(nil)).Elem(),
	reflect.TypeOf((*Inventories)(nil)).Elem(),
	reflect.TypeOf((*Maintainer)(nil)).Elem(),
	reflect.TypeOf((*KV)(nil)).Elem(),
	reflect.TypeOf((*Audit)(nil)).Elem(),
	reflect.TypeOf((*Tokens)(nil)).Elem(),
	reflect.TypeOf((*Identity)(nil)).Elem(),
	reflect.TypeOf((*Knowledge)(nil)).Elem(),
	reflect.TypeOf((*Metrics)(nil)).Elem(),
	reflect.TypeOf((*Projects)(nil)).Elem(),
	reflect.TypeOf((*Summaries)(nil)).Elem(),
}

// lifecycleMethods are the Backend methods that belong to no role.
var lifecycleMethods = []string{"AppliedMigrations", "Close", "Engine", "Migrate", "Mode", "Path"}

func methodNameSet(t reflect.Type) map[string]bool {
	out := map[string]bool{}
	for i := 0; i < t.NumMethod(); i++ {
		out[t.Method(i).Name] = true
	}
	return out
}

func TestBackendIsUnionOfRoles(t *testing.T) {
	want := map[string]bool{}
	for _, m := range lifecycleMethods {
		want[m] = true
	}
	for _, r := range roleTypes {
		if r.Kind() != reflect.Interface {
			t.Fatalf("%s is not an interface", r.Name())
		}
		if r.NumMethod() == 0 {
			t.Errorf("role %s declares no methods", r.Name())
		}
		for name := range methodNameSet(r) {
			want[name] = true
		}
	}

	got := methodNameSet(reflect.TypeOf((*Backend)(nil)).Elem())

	var missing, extra []string
	for name := range want {
		if !got[name] {
			missing = append(missing, name)
		}
	}
	for name := range got {
		if !want[name] {
			extra = append(extra, name)
		}
	}
	sort.Strings(missing)
	sort.Strings(extra)
	if len(missing) > 0 {
		t.Errorf("Backend lacks methods declared by its roles or lifecycle: %v", missing)
	}
	if len(extra) > 0 {
		t.Errorf("Backend has methods that no role or lifecycle declares: %v", extra)
	}
}

func TestBackendModeReportsOpenMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".leankg", "leankg.db")

	rw, err := openSQLite(path, RW)
	if err != nil {
		t.Fatalf("Open RW: %v", err)
	}
	var b Backend = rw
	if got := b.Mode(); got != RW {
		t.Errorf("Mode() on RW store = %v, want RW", got)
	}
	if err := rw.Close(); err != nil {
		t.Fatalf("Close RW: %v", err)
	}

	ro, err := openSQLite(path, RO)
	if err != nil {
		t.Fatalf("Open RO: %v", err)
	}
	defer ro.Close()
	b = ro
	if got := b.Mode(); got != RO {
		t.Errorf("Mode() on RO store = %v, want RO", got)
	}
}

// The keyword arm reports which ranking source served its hits, typed as Arm,
// so callers and the L2 reason mapping cannot mix arm labels with free text.
func TestSearcherFindFuzzyReturnsArm(t *testing.T) {
	m, ok := reflect.TypeOf((*Searcher)(nil)).Elem().MethodByName("FindFuzzy")
	if !ok {
		t.Fatal("Searcher has no FindFuzzy")
	}
	if m.Type.NumOut() != 3 || m.Type.Out(1) != reflect.TypeOf(Arm("")) {
		t.Fatalf("FindFuzzy returns %v; want ([]FuzzyMatch, Arm, error)", m.Type)
	}
}
