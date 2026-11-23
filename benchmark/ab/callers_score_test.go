package ab_test

// Precision/recall scorer for the `callers` graph verb. The ground truth is
// benchmark/ab/testdata/callers_truth.json (rg-derived call sites, keyed by
// (file, enclosing func)). The integration scorer is gated by
// LEANKG_CALLERS_SCORE=1 so CI never runs it.
//
// Run:
//
//	LEANKG_CALLERS_SCORE=1 LEANKG_CALLERS_OUT=/path/out.json \
//	  go test ./benchmark/ab -run Callers -count=1 -v

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/FreePeak/LeanKG/internal/core"
	"github.com/FreePeak/LeanKG/internal/index"
	"github.com/FreePeak/LeanKG/internal/langs"
	"github.com/FreePeak/LeanKG/internal/store"
)

// truthCaller is one true caller in the ground truth.
type truthCaller struct {
	File string `json:"file"`
	Func string `json:"func"`
	Test bool   `json:"test"`
	Line int    `json:"line,omitempty"`
}

// callerKey is one caller as LeanKG reports it: the file and the function
// half of the qualified name.
type callerKey struct{ File, Func string }

// Score holds the match counts for one symbol or an aggregate of symbols.
type Score struct{ TP, FP, FN int }

// PR returns precision and recall. With no predictions precision is 1 only
// when nothing was expected either, else 0; with nothing expected recall is 1.
func (s Score) PR() (precision, recall float64) {
	precision, recall = 1, 1
	if s.TP+s.FP > 0 {
		precision = float64(s.TP) / float64(s.TP+s.FP)
	} else if s.FN > 0 {
		precision = 0
	}
	if s.TP+s.FN > 0 {
		recall = float64(s.TP) / float64(s.TP+s.FN)
	}
	return precision, recall
}

func (s *Score) add(o Score) { s.TP += o.TP; s.FP += o.FP; s.FN += o.FN }

// scoreCallers compares got against truth as sets. With withTests false, test
// callers are dropped from both sides, so a test-only answer is neither a hit
// nor a false positive.
func scoreCallers(truth []truthCaller, got []callerKey, withTests bool) Score {
	want := map[callerKey]bool{}
	for _, c := range truth {
		if withTests || !c.Test {
			want[callerKey{c.File, c.Func}] = true
		}
	}
	have := map[callerKey]bool{}
	for _, g := range got {
		if withTests || !strings.HasSuffix(g.File, "_test.go") {
			have[g] = true
		}
	}
	var s Score
	for k := range have {
		if want[k] {
			s.TP++
		} else {
			s.FP++
		}
	}
	for k := range want {
		if !have[k] {
			s.FN++
		}
	}
	return s
}

// TestCallersScoreMath pins the pure scoring rule the callers scorer uses:
// truth and got are sets of (file, func) callers; test callers are dropped
// from both sides when withTests is false.
func TestCallersScoreMath(t *testing.T) {
	a := truthCaller{File: "a.go", Func: "F"}
	b := truthCaller{File: "b.go", Func: "G"}
	c := truthCaller{File: "c.go", Func: "H"}
	tst := truthCaller{File: "a_test.go", Func: "TestF", Test: true}
	gotA := callerKey{File: "a.go", Func: "F"}
	gotB := callerKey{File: "b.go", Func: "G"}
	gotD := callerKey{File: "d.go", Func: "X"}
	gotTst := callerKey{File: "a_test.go", Func: "TestF"}

	cases := []struct {
		name      string
		truth     []truthCaller
		got       []callerKey
		withTests bool
		want      Score
	}{
		{"partial overlap", []truthCaller{a, b, c}, []callerKey{gotA, gotB, gotD}, true, Score{TP: 2, FP: 1, FN: 1}},
		{"nothing returned", []truthCaller{a, b}, nil, true, Score{TP: 0, FP: 0, FN: 2}},
		{"nothing expected, nothing returned", nil, nil, true, Score{}},
		{"nothing expected, something returned", nil, []callerKey{gotA}, true, Score{FP: 1}},
		{"test caller counted with tests", []truthCaller{a, tst}, []callerKey{gotA, gotTst}, true, Score{TP: 2}},
		{"test caller dropped without tests", []truthCaller{a, tst}, []callerKey{gotA, gotTst}, false, Score{TP: 1}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := scoreCallers(tc.truth, tc.got, tc.withTests)
			if got != tc.want {
				t.Fatalf("scoreCallers = %+v, want %+v", got, tc.want)
			}
		})
	}

	if p, r := (Score{TP: 2, FP: 1, FN: 1}).PR(); p != 2.0/3.0 || r != 2.0/3.0 {
		t.Fatalf("PR = (%v, %v), want (2/3, 2/3)", p, r)
	}
	if p, r := (Score{FN: 2}).PR(); p != 0 || r != 0 {
		t.Fatalf("PR with only misses = (%v, %v), want (0, 0)", p, r)
	}
	if p, r := (Score{}).PR(); p != 1 || r != 1 {
		t.Fatalf("PR on empty = (%v, %v), want (1, 1)", p, r)
	}
}

const callersTruthPath = "testdata/callers_truth.json"

type defectSite struct {
	Site string `json:"site"`
	File string `json:"file"`
	Func string `json:"func"`
}

type truthSymbol struct {
	ID            string        `json:"id"`
	QualifiedName string        `json:"qualified_name"`
	Class         string        `json:"class"`
	Callers       []truthCaller `json:"callers"`
	Possible      []truthCaller `json:"possible,omitempty"`
	Defects       []defectSite  `json:"defects,omitempty"`
}

// knownMiss is a truth caller the product is known to miss at the baseline.
type knownMiss struct {
	Symbol string `json:"symbol"`
	File   string `json:"file"`
	Func   string `json:"func"`
	Reason string `json:"reason"`
}

// floorPR is one pair of failing thresholds.
type floorPR struct {
	Precision float64 `json:"precision"`
	Recall    float64 `json:"recall"`
}

// truthFloors are the failing thresholds. Overall applies to the with-tests
// scoring, OverallNoTests to the scoring without test callers.
type truthFloors struct {
	Overall        floorPR            `json:"overall"`
	OverallNoTests floorPR            `json:"overall_without_tests"`
	Classes        map[string]floorPR `json:"classes"`
}

type truthFile struct {
	Schema       string        `json:"schema"`
	SourceCommit string        `json:"source_commit"`
	Generator    string        `json:"generator"`
	Rules        []string      `json:"rules"`
	Symbols      []truthSymbol `json:"symbols"`
	KnownMisses  []knownMiss   `json:"known_misses"`
	Floors       truthFloors   `json:"floors"`
}

type symbolReport struct {
	ID           string   `json:"id"`
	Class        string   `json:"class"`
	Got          []string `json:"got"`
	WithTests    report   `json:"with_tests"`
	WithoutTests report   `json:"without_tests"`
}

type report struct {
	Precision float64 `json:"precision"`
	Recall    float64 `json:"recall"`
	TP        int     `json:"tp"`
	FP        int     `json:"fp"`
	FN        int     `json:"fn"`
}

func summarize(s Score) report {
	p, r := s.PR()
	return report{Precision: p, Recall: r, TP: s.TP, FP: s.FP, FN: s.FN}
}

// TestCallersScoreIntegration indexes the repository root into a fresh SQLite
// store, asks the callers verb about each truth symbol and scores the answers.
func TestCallersScoreIntegration(t *testing.T) {
	if os.Getenv("LEANKG_CALLERS_SCORE") != "1" {
		t.Skip("set LEANKG_CALLERS_SCORE=1 to run the callers precision/recall scorer")
	}
	ctx := context.Background()
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}

	raw, err := os.ReadFile(callersTruthPath)
	if err != nil {
		t.Fatalf("read truth: %v", err)
	}
	var truth truthFile
	if err := json.Unmarshal(raw, &truth); err != nil {
		t.Fatalf("parse truth: %v", err)
	}

	reg := langs.DefaultRegistry()
	if _, err := reg.Activate(ctx, root); err != nil {
		t.Fatalf("language detection: %v", err)
	}
	st, err := store.OpenBackend(ctx, store.OpenOptions{Engine: store.EngineSQLite, DBPath: filepath.Join(t.TempDir(), "leankg.db"), Mode: store.RW})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	res, err := index.IndexDirWith(ctx, st, root, reg)
	if err != nil {
		t.Fatalf("index: %v", err)
	}
	t.Logf("indexed %s: files=%d elements=%d relationships=%d", root, res.Files, res.Elements, res.Relationships)

	eng := core.New(st, core.Options{ProjectDir: root, Langs: reg})
	defer eng.Close()

	perSymbol := make([]symbolReport, 0, len(truth.Symbols))
	classWith := map[string]*Score{}
	classWithout := map[string]*Score{}
	var allWith, allWithout Score
	var defects []map[string]any

	for _, sym := range truth.Symbols {
		out, err := eng.Query(ctx, core.QueryRequest{Action: "callers", Query: sym.QualifiedName})
		if err != nil {
			t.Fatalf("callers %s: %v", sym.QualifiedName, err)
		}
		var answer struct {
			Callers []string `json:"callers"`
		}
		buf, _ := json.Marshal(out)
		if err := json.Unmarshal(buf, &answer); err != nil {
			t.Fatalf("decode callers %s: %v", sym.QualifiedName, err)
		}
		got := make([]callerKey, 0, len(answer.Callers))
		gotNames := make([]string, 0, len(answer.Callers))
		for _, qn := range answer.Callers {
			file, fn, _ := strings.Cut(qn, "::")
			got = append(got, callerKey{file, fn})
			gotNames = append(gotNames, qn)
		}
		with := scoreCallers(sym.Callers, got, true)
		without := scoreCallers(sym.Callers, got, false)
		perSymbol = append(perSymbol, symbolReport{ID: sym.ID, Class: sym.Class, Got: gotNames,
			WithTests: summarize(with), WithoutTests: summarize(without)})
		if classWith[sym.Class] == nil {
			classWith[sym.Class], classWithout[sym.Class] = &Score{}, &Score{}
		}
		classWith[sym.Class].add(with)
		classWithout[sym.Class].add(without)
		allWith.add(with)
		allWithout.add(without)

		for _, d := range sym.Defects {
			found := false
			for _, g := range got {
				if g.File == d.File && g.Func == d.Func {
					found = true
				}
			}
			defects = append(defects, map[string]any{"symbol": sym.ID, "site": d.Site, "found": found})
		}
	}

	printTable(t, classWith, classWithout, allWith, allWithout, defects)

	if out := os.Getenv("LEANKG_CALLERS_OUT"); out != "" {
		classes := map[string]any{}
		for c, s := range classWith {
			classes[c] = map[string]any{"with_tests": summarize(*s), "without_tests": summarize(*classWithout[c])}
		}
		body := map[string]any{
			"truth_source_commit": truth.SourceCommit,
			"classes":             classes,
			"overall":             map[string]any{"with_tests": summarize(allWith), "without_tests": summarize(allWithout)},
			"defects":             defects,
			"symbols":             perSymbol,
		}
		enc, err := json.MarshalIndent(body, "", "  ")
		if err != nil {
			t.Fatalf("encode results: %v", err)
		}
		if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(out, append(enc, '\n'), 0o644); err != nil {
			t.Fatalf("write %s: %v", out, err)
		}
		t.Logf("wrote %s", out)
	}
}

func printTable(t *testing.T, cw, cwo map[string]*Score, aw, awo Score, defects []map[string]any) {
	t.Helper()
	var b strings.Builder
	fmt.Fprintf(&b, "\n%-12s | %-24s | %-24s\n", "class", "with tests P / R (tp fp fn)", "without tests P / R (tp fp fn)")
	row := func(name string, w, wo Score) {
		pw, rw := w.PR()
		pwo, rwo := wo.PR()
		fmt.Fprintf(&b, "%-12s | %.3f / %.3f (%d %d %d) | %.3f / %.3f (%d %d %d)\n",
			name, pw, rw, w.TP, w.FP, w.FN, pwo, rwo, wo.TP, wo.FP, wo.FN)
	}
	for _, c := range []string{"qualified", "method", "interface", "type"} {
		if cw[c] != nil {
			row(c, *cw[c], *cwo[c])
		}
	}
	row("overall", aw, awo)
	b.WriteString("\ndefect sites:\n")
	for _, d := range defects {
		fmt.Fprintf(&b, "  %-22s %-28s found=%v\n", d["symbol"], d["site"], d["found"])
	}
	t.Log(b.String())
}
