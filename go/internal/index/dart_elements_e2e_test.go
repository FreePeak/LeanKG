package index

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/FreePeak/LeanKG/go/internal/store"
)

const dartProjectFixture = `class Factory {
  Factory();
  Factory.named(int x);
  factory Factory.create() => Factory();
  const Factory.zero();
}

enum Color { red, green }
`

// TestIndexDartConstructorAndEnumElements proves the dart coverage end to end
// through the index pipeline: constructors and enum values become elements
// with their own qualified names, and the class element keeps its own row
// (which a default constructor sharing the class qualified name would replace,
// since elements upsert by qualified name). Both tiers run this fixture — the
// regex build and the tstree build — so the element names and qualified names
// must agree.
func TestIndexDartConstructorAndEnumElements(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "factory.dart"), []byte(dartProjectFixture), 0o644); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(filepath.Join(dir, ".leankg", "leankg.db"), store.RW)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.Migrate(); err != nil {
		t.Fatal(err)
	}
	if _, err := IndexDir(context.Background(), st, dir); err != nil {
		t.Fatalf("IndexDir: %v", err)
	}

	want := []struct{ name, qn string }{
		{"Factory", "factory.dart::Factory"},         // the class element
		{"Factory", "factory.dart::Factory.Factory"}, // default constructor
		{"named", "factory.dart::Factory.named"},
		{"create", "factory.dart::Factory.create"},
		{"zero", "factory.dart::Factory.zero"},
		{"red", "factory.dart::Color.red"},
		{"green", "factory.dart::Color.green"},
	}
	for _, w := range want {
		els, err := st.FindExact(w.name)
		if err != nil {
			t.Fatal(err)
		}
		var found bool
		for _, e := range els {
			if e.QualifiedName == w.qn {
				found = true
			}
		}
		if !found {
			t.Errorf("element %q with qn %q missing, got %v", w.name, w.qn, els)
		}
	}

	cls, err := st.FindExact("factory.dart::Factory")
	if err != nil {
		t.Fatal(err)
	}
	if len(cls) != 1 {
		t.Fatalf("FindExact(factory.dart::Factory) = %d elements, want 1", len(cls))
	}
	if cls[0].ElementType == "constructor" {
		t.Errorf("Factory element type = %q, want the class element", cls[0].ElementType)
	}
}
