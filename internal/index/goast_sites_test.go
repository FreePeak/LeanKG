package index

import (
	"context"
	"slices"
	"testing"

	"github.com/FreePeak/LeanKG/internal/store"
)

// elementByQN returns the stored element with qualified name qn.
func elementByQN(t *testing.T, st store.Backend, qn string) (store.Element, bool) {
	t.Helper()
	els, err := st.Elements(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range els {
		if e.QualifiedName == qn {
			return e, true
		}
	}
	return store.Element{}, false
}

// linesMeta reads the call-site lines an edge carries in its "lines" metadata.
func linesMeta(r store.Relationship) ([]int, bool) {
	raw, ok := r.Metadata["lines"].([]any)
	if !ok {
		return nil, false
	}
	out := make([]int, 0, len(raw))
	for _, v := range raw {
		f, ok := v.(float64)
		if !ok {
			return nil, false
		}
		out = append(out, int(f))
	}
	return out, true
}

// callersOf lists the sources of the incoming edges of dst of relType.
func callersOf(t *testing.T, st store.Backend, dst, relType string) []string {
	t.Helper()
	rels, err := st.Incoming(context.Background(), dst)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, r := range rels {
		if r.RelType == relType {
			out = append(out, r.Source)
		}
	}
	return out
}

// TestGoRepeatedCallSitesKeepEveryLine: Caller calls Target at lines 6, 8 and 9.
// The edge stays one row, with line = the first call and lines = all three.
func TestGoRepeatedCallSitesKeepEveryLine(t *testing.T) {
	st := indexedGraph(t, map[string]string{"a.go": "package a\n\nfunc Target() {}\n\n" +
		"func Caller() {\n\tTarget()\n\t_ = 1\n\tTarget()\n\tTarget()\n}\n"})
	r, ok := edgeTo(t, st, "a.go::Caller", "a.go::Target", "calls")
	if !ok {
		t.Fatal("repeated call has no calls edge")
	}
	if line, _ := lineMeta(r); line != 6 {
		t.Fatalf("line = %d, want 6 (the first call)", line)
	}
	got, ok := linesMeta(r)
	if !ok || !slices.Equal(got, []int{6, 8, 9}) {
		t.Fatalf("lines = %v (ok %v), want [6 8 9]; metadata %v", got, ok, r.Metadata)
	}
}

// TestGoRepeatedQualifiedCallSitesKeepEveryLine: store.Open is called at lines 6
// and 7 of cmd/app/main.go; both sites are kept on the one cross-package edge.
func TestGoRepeatedQualifiedCallSitesKeepEveryLine(t *testing.T) {
	st, dir := newCrossTree(t)
	putFiles(t, dir, map[string]string{
		"internal/store/store.go": "package store\n\nfunc Open(p string) {}\n",
		"cmd/app/main.go": "package main\n\nimport \"" + modPath + "/internal/store\"\n\n" +
			"func run() {\n\tstore.Open(\"p\")\n\tstore.Open(\"q\")\n}\n",
	})
	indexTree(t, st, dir)
	r, ok := edgeTo(t, st, "cmd/app/main.go::run", "internal/store/store.go::Open", "calls")
	if !ok {
		t.Fatal("repeated qualified call has no calls edge")
	}
	got, ok := linesMeta(r)
	if !ok || !slices.Equal(got, []int{6, 7}) {
		t.Fatalf("lines = %v (ok %v), want [6 7]; metadata %v", got, ok, r.Metadata)
	}
}

// TestGoInterfaceMethodSpecsAreElements: each declared method spec of an
// interface is a method element owned by the interface, with its own line; an
// embedded interface name (Writer inside RW) is not an element.
func TestGoInterfaceMethodSpecsAreElements(t *testing.T) {
	st := indexedGraph(t, map[string]string{"a.go": "package a\n\n" +
		"type Writer interface {\n\tEmit(x int)\n\tDrain()\n}\n\n" +
		"type RW interface {\n\tWriter\n\tRead() int\n}\n"})

	for qn, line := range map[string]int{"a.go::Writer.Emit": 4, "a.go::Writer.Drain": 5, "a.go::RW.Read": 10} {
		e, ok := elementByQN(t, st, qn)
		if !ok {
			t.Fatalf("no element %s", qn)
		}
		if e.ElementType != "method" || e.LineStart != line || e.LineEnd != line {
			t.Fatalf("%s = %s lines %d-%d, want method at line %d", qn, e.ElementType, e.LineStart, e.LineEnd, line)
		}
		if spec, _ := e.Metadata["interface_spec"].(bool); !spec {
			t.Fatalf("%s metadata %v, want interface_spec", qn, e.Metadata)
		}
		if e.ParentQualified != "a.go::"+qn[len("a.go::"):len(qn)-len(e.Name)-1] {
			t.Fatalf("%s parent = %q, want its interface", qn, e.ParentQualified)
		}
	}
	if _, ok := elementByQN(t, st, "a.go::RW.Writer"); ok {
		t.Fatal("embedded interface Writer became a method element of RW")
	}
}

// TestGoInterfaceCallLinksToSpecAndImplementers: s.Emit(1) on a Sink reaches the
// Sink.Emit spec and the Pipe implementer (both 0.3, interface-dispatch); the
// embedded case rw.Emit(2) on RW, which embeds Sink, reaches the Sink.Emit spec.
func TestGoInterfaceCallLinksToSpecAndImplementers(t *testing.T) {
	st := indexedGraph(t, map[string]string{"a.go": "package a\n\n" +
		"type Sink interface {\n\tEmit(x int)\n\tDrain()\n}\n\n" +
		"type Pipe struct{}\n\nfunc (Pipe) Emit(x int) {}\n\nfunc (Pipe) Drain() {}\n\n" +
		"func use(s Sink) {\n\ts.Emit(1)\n}\n\n" +
		"type RW interface {\n\tSink\n\tRead() int\n}\n\n" +
		"func use2(rw RW) {\n\trw.Emit(2)\n}\n"})

	r, ok := edgeTo(t, st, "a.go::use", "a.go::Sink.Emit", "calls")
	if !ok {
		t.Fatal("interface call s.Emit has no edge to the Sink.Emit spec")
	}
	if r.Confidence != 0.3 {
		t.Fatalf("spec edge confidence = %v, want 0.3", r.Confidence)
	}
	if line, _ := lineMeta(r); line != 15 {
		t.Fatalf("spec edge line = %d, want 15", line)
	}
	if _, ok := edgeTo(t, st, "a.go::use", "a.go::Pipe.Emit", "calls"); !ok {
		t.Fatal("interface call s.Emit lost its edge to the Pipe implementer")
	}
	if got := callersOf(t, st, "a.go::Sink.Emit", "calls"); !slices.Contains(got, "a.go::use2") || !slices.Contains(got, "a.go::use") {
		t.Fatalf("callers of Sink.Emit = %v, want use and use2", got)
	}
}

// TestGoCrossPackageCallersKeepEverySite is the shape of the VerifyAdminLogin
// report: a method in internal/mcp calls auth.VerifyAdminLogin twice, a function
// in cmd/leankg calls it once. Both callers and their lines must be listed.
func TestGoCrossPackageCallersKeepEverySite(t *testing.T) {
	st, dir := newCrossTree(t)
	putFiles(t, dir, map[string]string{
		"internal/auth/admin.go": "package auth\n\nimport \"context\"\n\n" +
			"func VerifyAdminLogin(ctx context.Context, user, pw string) error { return nil }\n",
		"internal/mcp/authenticate.go": "package mcp\n\nimport \"" + modPath + "/internal/auth\"\n\n" +
			"type Server struct{}\n\n" +
			"func (s *Server) handleAuthenticate(ctx context.Context) error {\n" +
			"\tif err := auth.VerifyAdminLogin(ctx, \"u\", \"p\"); err != nil {\n\t\treturn err\n\t}\n" +
			"\treturn auth.VerifyAdminLogin(ctx, \"u\", \"q\")\n}\n",
		"cmd/leankg/auth.go": "package main\n\nimport (\n\t\"context\"\n\n\t\"" + modPath + "/internal/auth\"\n)\n\n" +
			"func bootstrapAdmin(ctx context.Context) error {\n\treturn auth.VerifyAdminLogin(ctx, \"u\", \"p\")\n}\n",
	})
	indexTree(t, st, dir)

	const verify = "internal/auth/admin.go::VerifyAdminLogin"
	want := map[string]struct {
		src   string
		lines []int
	}{
		"mcp": {"internal/mcp/authenticate.go::Server.handleAuthenticate", []int{8, 11}},
		"cmd": {"cmd/leankg/auth.go::bootstrapAdmin", []int{10}},
	}
	for name, w := range want {
		r, ok := edgeTo(t, st, w.src, verify, "calls")
		if !ok {
			t.Fatalf("%s: no calls edge to VerifyAdminLogin; outgoing %v", name, targetsFrom(t, st, w.src, "calls"))
		}
		if r.Confidence != 0.8 {
			t.Fatalf("%s: confidence = %v, want 0.8", name, r.Confidence)
		}
		if got, ok := linesMeta(r); !ok || !slices.Equal(got, w.lines) {
			t.Fatalf("%s: lines = %v (ok %v), want %v", name, got, ok, w.lines)
		}
	}
	if got := callersOf(t, st, verify, "calls"); len(got) != 2 {
		t.Fatalf("callers of VerifyAdminLogin = %v, want 2", got)
	}
}
