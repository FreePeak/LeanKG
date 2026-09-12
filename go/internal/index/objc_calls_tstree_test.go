//go:build tstree

package index

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/FreePeak/LeanKG/go/internal/store"
)

const objcCallProjectFixture = `#import "Greeter.h"

static NSString *describe(void) {
    return [Greeter name];
}

@implementation Greeter
- (void)sayHello {
    [self setup];
    [logger log:@"hi" level:1];
}
- (void)setup {}
- (void)log:(NSString *)msg level:(int)lvl {}
+ (NSString *)name { return @"Greeter"; }
@end
`

// TestIndexObjCMessageSendCallEdges proves the objc message-send port end to
// end: the tree-sitter call walk seeds "calls" edges on the enclosing method
// (or on the C function a file-scope send sits in) and relationships() resolves
// them to the file's method elements — including the multi-keyword selector
// "log:level:", which the identifier heuristic cannot express. (The default
// build has no objc grammar; the Rust reference had none either — it always ran
// tree-sitter for .m files.)
func TestIndexObjCMessageSendCallEdges(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "Greeter.m"), []byte(objcCallProjectFixture), 0o644); err != nil {
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

	want := []struct {
		caller, target string
	}{
		{"Greeter.m::Greeter.sayHello", "Greeter.m::Greeter.setup"},
		{"Greeter.m::Greeter.sayHello", "Greeter.m::Greeter.log:level:"},
		// File-scope send: the enclosing C function owns the call site.
		{"Greeter.m::describe", "Greeter.m::Greeter.name"},
	}
	for _, w := range want {
		rels, err := st.Outgoing(w.caller)
		if err != nil {
			t.Fatal(err)
		}
		var conf float64
		var found bool
		for _, r := range rels {
			if r.RelType == "calls" && r.Target == w.target {
				conf, found = r.Confidence, true
			}
		}
		if !found {
			t.Errorf("call edge %s -> %s missing, outgoing = %v", w.caller, w.target, rels)
			continue
		}
		if conf != 0.7 {
			t.Errorf("call edge %s -> %s confidence = %v, want 0.7", w.caller, w.target, conf)
		}
	}

	// The class head is a type, never a message target.
	rels, err := st.Outgoing("Greeter.m::Greeter.sayHello")
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range rels {
		if r.RelType == "calls" && r.Target == "Greeter.m::Greeter" {
			t.Errorf("class head became a call target with confidence %v", r.Confidence)
		}
	}

	// A method without message sends emits no call edges.
	rels, err = st.Outgoing("Greeter.m::Greeter.setup")
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range rels {
		if r.RelType == "calls" {
			t.Errorf("setup has no call sites, got edge to %q", r.Target)
		}
	}
}
