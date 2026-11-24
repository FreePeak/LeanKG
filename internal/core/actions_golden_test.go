package core

// Golden responses for every query and import action core resolves (CX-42).
//
// This is a characterization suite: each golden records what the CURRENT
// engine answers for one request against a fixed corpus, so the handler
// refactors that follow (CX-43 registry, CX-44 typed args, CX-45 options) can
// be checked against it. The mechanism mirrors internal/golden: one JSON file
// per golden under testdata/golden/actions, compared in sorted-key compact
// form after the same redaction, and rewritten with `-update`:
//
//	go test ./internal/core/ -run TestActionGoldens -update
//
// Every case calls the public entry points (Query, Import, Status,
// ResolveEnvelope). actions_vocab_test.go fails when core gains an action that
// has no case here.

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"github.com/FreePeak/LeanKG/internal/embed"
	"github.com/FreePeak/LeanKG/internal/gitobj/gittest"
	"github.com/FreePeak/LeanKG/internal/memory"
	"github.com/FreePeak/LeanKG/internal/portfolioreg"
	"github.com/FreePeak/LeanKG/internal/store"
)

var update = flag.Bool("update", false, "rewrite the action goldens in testdata/golden/actions")

// coreDir is this package's source directory. The suite resolves its paths from
// it, not from the working directory: importpath_test.go changes the working
// directory and does not restore it, so a relative path breaks later tests.
var coreDir = func() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Dir(file)
}()

var actionGoldenDir = filepath.Join(coreDir, "testdata", "golden", "actions")

// actionFixture is one fresh project: a store under <project>/.leankg, the
// memory tree, the deterministic embedder, and an engine wired the way the
// serving transports wire it (New, then SetProjectDir).
type actionFixture struct {
	t       *testing.T
	root    string // t.TempDir: holds the project and the portfolio registry
	project string // the serving project directory
	st      store.Backend
	mem     *memory.Memory
	e       *Engine
}

func newActionFixture(t *testing.T) *actionFixture {
	t.Helper()
	root := t.TempDir()
	return openActionFixture(t, root, filepath.Join(root, "proj"))
}

// openActionFixture opens the store, memory and engine for project; root holds
// the portfolio registry and is the redaction base.
func openActionFixture(t *testing.T, root, project string) *actionFixture {
	t.Helper()
	lk := filepath.Join(project, ".leankg")
	if err := os.MkdirAll(lk, 0o755); err != nil {
		t.Fatal(err)
	}
	// The store lives inside the project, so git-backed cases must ignore it.
	if err := os.WriteFile(filepath.Join(project, ".gitignore"), []byte(".leankg/\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	st, err := store.OpenBackend(context.Background(), store.OpenOptions{DBPath: filepath.Join(lk, "leankg.db"), Mode: store.RW})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	mem, err := memory.Open(context.Background(), project, false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = mem.Close() })
	e := New(st, Options{Memory: mem, Embedder: embed.Deterministic(8), ProjectDir: project})
	return &actionFixture{t: t, root: root, project: project, st: st, mem: mem, e: e}
}

func (f *actionFixture) must(err error) {
	f.t.Helper()
	if err != nil {
		f.t.Fatal(err)
	}
}

func (f *actionFixture) writeProject(rel, content string) {
	f.t.Helper()
	p := filepath.Join(f.project, rel)
	f.must(os.MkdirAll(filepath.Dir(p), 0o755))
	f.must(os.WriteFile(p, []byte(content), 0o644))
}

// TestWriteActionCorpus seeds the golden corpus into a real project directory,
// so a live `leankg serve --project <dir>` answers the same requests the goldens
// record. Skipped unless LEANKG_CX42_CORPUS_DIR names the project directory.
func TestWriteActionCorpus(t *testing.T) {
	project := os.Getenv("LEANKG_CX42_CORPUS_DIR")
	if project == "" {
		t.Skip("set LEANKG_CX42_CORPUS_DIR to seed the golden corpus into a project directory")
	}
	seedActionCorpus(openActionFixture(t, project, project))
}

// actionSetup seeds a fixture before the call; actionRun makes the call.
type actionSetup func(*actionFixture)

type actionRun func(*actionFixture) (map[string]any, error)

// actionCase is one golden. action is the vocabulary key the case covers
// ("query/memory", "import/repo", ...); sub, when set, names the sub-command
// it covers too ("query/memory/view"). Error-path cases set only action.
type actionCase struct {
	name   string
	action string
	sub    string
	setup  actionSetup
	run    actionRun
}

func (c actionCase) covers() []string {
	out := []string{c.action}
	if c.sub != "" {
		out = append(out, c.action+"/"+c.sub)
	}
	return out
}

func qry(req QueryRequest) actionRun {
	return func(f *actionFixture) (map[string]any, error) {
		return f.e.Query(context.Background(), req)
	}
}

func imp(req ImportRequest) actionRun {
	return func(f *actionFixture) (map[string]any, error) {
		return f.e.Import(context.Background(), req)
	}
}

func chain(steps ...actionSetup) actionSetup {
	return func(f *actionFixture) {
		for _, s := range steps {
			if s != nil {
				s(f)
			}
		}
	}
}

// seedActionCorpus is the fixed corpus the L0-L3 and graph goldens query: two
// elements, one calls edge, one stamped collection with one vector, and a
// memory tree with one file. It matches internal/golden's corpus.
func seedActionCorpus(f *actionFixture) {
	ctx := context.Background()
	els := []store.Element{
		{
			QualifiedName: "src/alpha.go::Alpha", ElementType: "function", Name: "Alpha",
			FilePath: "src/alpha.go", LineStart: 1, LineEnd: 3, Language: "go",
			Content: "func Alpha() int {\n\treturn Beta() + 1\n}",
		},
		{
			QualifiedName: "src/beta.go::Beta", ElementType: "function", Name: "Beta",
			FilePath: "src/beta.go", LineStart: 6, LineEnd: 8, Language: "go",
			Content: "func Beta() int {\n\treturn 41\n}",
		},
	}
	f.must(f.st.UpsertElements(context.Background(), els))
	f.must(f.st.UpsertRelationships(context.Background(), []store.Relationship{{
		Source: "src/alpha.go::Alpha", Target: "src/beta.go::Beta",
		RelType: "calls", Confidence: 0.5,
	}}))
	prov := embed.Deterministic(8)
	f.must(f.st.WriteStamp(context.Background(), embed.StampOf(prov)))
	vecs, err := prov.Embed(ctx, embed.Document, []string{els[0].Content})
	f.must(err)
	f.must(f.st.UpsertVectors(context.Background(), prov.Identity().Model, []store.VectorRow{{QualifiedName: els[0].QualifiedName, Vec: vecs[0]}}))
	f.writeMemoryFile("MEMORY.md", "# Project Memory\n\n- Golden fixture corpus pins the L0-L3 ladder.\n")
}

// writeMemoryFile places a file at the memory root, where the MEMORY.md
// reads and the file-edit commands resolve it.
func (f *actionFixture) writeMemoryFile(rel, content string) {
	f.t.Helper()
	p := filepath.Join(f.mem.Root(), rel)
	f.must(os.MkdirAll(filepath.Dir(p), 0o755))
	f.must(os.WriteFile(p, []byte(content), 0o644))
}

// seedSourceFiles writes two Go files matching the corpus names, for the
// reader and the directory-indexing cases.
func seedSourceFiles(f *actionFixture) {
	f.writeProject("src/alpha.go", "package src\n\nfunc Alpha() int {\n\treturn Beta() + 1\n}\n")
	f.writeProject("src/beta.go", "package src\n\nfunc Beta() int {\n\treturn 41\n}\n")
}

func seedGitRepo(f *actionFixture) {
	gittest.SkipWithoutGit(f.t)
	gittest.Git(f.t, f.project, "init")
	f.writeProject("main.go", "package main\n\nfunc Main() {}\n")
	gittest.Git(f.t, f.project, "add", ".")
	gittest.Git(f.t, f.project, "commit", "-m", "first")
	f.writeProject("main.go", "package main\n\nfunc Main() { _ = 1 }\n")
	f.writeProject("util.go", "package main\n\nfunc Util() {}\n")
	gittest.Git(f.t, f.project, "add", ".")
	gittest.Git(f.t, f.project, "commit", "-m", "second")
}

func seedGitHistory(f *actionFixture) {
	seedGitRepo(f)
	_, err := f.e.Import(context.Background(), ImportRequest{Action: "git"})
	f.must(err)
}

func writePRD(f *actionFixture) {
	f.writeProject("prd.md", "# Product requirements\n\nFR-AUTH-01 — Users can log in with a password\nAC: a valid password returns a session token\n")
}

func writeCatalog(f *actionFixture) {
	f.writeProject("concepts.json", `{"concepts":[{"id":"alpha","label":"Alpha","aliases":["Alpha"]}]}`)
}

func seedPRD(f *actionFixture) {
	writePRD(f)
	_, err := f.e.Import(context.Background(), ImportRequest{Action: "prd", Path: "prd.md"})
	f.must(err)
}

func seedCatalog(f *actionFixture) {
	writeCatalog(f)
	_, err := f.e.Import(context.Background(), ImportRequest{Action: "ontology", Path: "concepts.json"})
	f.must(err)
}

func seedSession(f *actionFixture) {
	ctx := context.Background()
	_, err := f.e.Import(ctx, ImportRequest{Action: "session", Command: "offload",
		Args: map[string]any{"session_id": "s1", "node_id": "node-1", "payload": "payload-one", "summary": "short"}})
	f.must(err)
	_, err = f.e.Import(ctx, ImportRequest{Action: "session", Command: "lesson",
		Args: map[string]any{"session_id": "s1", "text": "keep the ladder order"}})
	f.must(err)
}

func seedOntologyDir(f *actionFixture) {
	f.writeProject("ontology/workflows.yaml", "workflows:\n  - id: login\n    name: Login\n")
}

func seedDocs(f *actionFixture) {
	f.writeProject("docs/guide.md", "# Guide\n\nHow the ladder works.\n")
}

func seedMemoryFile(rel, content string) actionSetup {
	return func(f *actionFixture) { f.writeMemoryFile(rel, content) }
}

// seedPortfolioRegistry creates an empty portfolio registry: a summary of "no
// registry" is an error, and a fan-out over no projects is a guidance answer.
// stubAstGrep puts a fixed-output ast-grep on PATH alone, so the pattern rung
// runs its real invocation and parsing without the host's CLI deciding the
// answer. The stub prints one match whatever the pattern is.
const (
	astGrepAlpha = `{"file":"src/alpha.go","range":{"start":{"line":2,"column":0},"end":{"line":4,"column":1}},"text":"func Alpha() int {"}`
	astGrepBeta  = `{"file":"src/beta.go","range":{"start":{"line":5,"column":0},"end":{"line":7,"column":1}},"text":"func Beta() int {"}`
)

func stubAstGrep(f *actionFixture) {
	stubAstGrepMatches(f, "["+astGrepAlpha+"]")
}

// stubAstGrepTwo serves two matches, so a limit has something to cut.
func stubAstGrepTwo(f *actionFixture) {
	stubAstGrepMatches(f, "["+astGrepAlpha+","+astGrepBeta+"]")
}

// stubAstGrepMatches puts a fake ast-grep first on PATH that prints matches.
func stubAstGrepMatches(f *actionFixture, matches string) {
	dir := filepath.Join(f.root, "bin")
	f.must(os.MkdirAll(dir, 0o755))
	matchesFile := filepath.Join(dir, "matches.json")
	f.must(os.WriteFile(matchesFile, []byte(matches), 0o644))
	f.must(os.WriteFile(filepath.Join(dir, "ast-grep"), []byte("#!/bin/sh\n/bin/cat "+matchesFile+"\n"), 0o755))
	f.t.Setenv("PATH", dir)
}

func seedPortfolioRegistry(f *actionFixture) {
	regDB := filepath.Join(f.root, "portfolio.db")
	f.t.Setenv(portfolioreg.DBPathEnv, regDB)
	reg, err := portfolioreg.Open(context.Background(), portfolioreg.Options{DBPath: regDB}, store.RW)
	f.must(err)
	f.must(reg.Close())
}

// actionCases is the golden table. Every vocabulary key core dispatches on must
// be covered here; actions_vocab_test.go enforces that.
var actionCases = []actionCase{
	// --- query: the L0-L3 ladder (action empty or search) -------------------
	{name: "query_ladder_l0_cold", action: "query/(empty)", run: qry(QueryRequest{Query: "anything"})},
	{name: "query_ladder_l1_exact", action: "query/(empty)", setup: seedActionCorpus, run: qry(QueryRequest{Query: "Alpha"})},
	{name: "query_ladder_l2_fuzzy", action: "query/(empty)", setup: seedActionCorpus, run: qry(QueryRequest{Query: "beta"})},
	{name: "query_ladder_l3_semantic", action: "query/(empty)", setup: seedActionCorpus, run: qry(QueryRequest{Query: "compute the answer", Limit: 1})},
	{name: "query_ladder_no_match", action: "query/(empty)", setup: seedActionCorpus, run: qry(QueryRequest{Query: "zzqxv"})},
	{name: "query_ladder_missing_query", action: "query/(empty)", setup: seedActionCorpus, run: qry(QueryRequest{})},
	{name: "query_search", action: "query/search", setup: seedActionCorpus, run: qry(QueryRequest{Action: "search", Query: "Alpha"})},

	// --- query: pinned rungs and their alias -------------------------------
	{name: "query_exact", action: "query/exact", setup: seedActionCorpus, run: qry(QueryRequest{Action: "exact", Query: "Alpha"})},
	{name: "query_element", action: "query/element", setup: seedActionCorpus, run: qry(QueryRequest{Action: "element", Query: "Alpha"})},
	{name: "query_fuzzy", action: "query/fuzzy", setup: seedActionCorpus, run: qry(QueryRequest{Action: "fuzzy", Query: "beta"})},
	{name: "query_semantic", action: "query/semantic", setup: seedActionCorpus, run: qry(QueryRequest{Action: "semantic", Query: "compute the answer", Limit: 1})},

	// --- query: graph verbs -------------------------------------------------
	{name: "query_impact", action: "query/impact", setup: seedActionCorpus, run: qry(QueryRequest{Action: "impact", Query: "src/beta.go::Beta"})},
	{name: "query_impact_unknown_seed", action: "query/impact", setup: seedActionCorpus, run: qry(QueryRequest{Action: "impact", Query: "NoSuchSymbol"})},
	{name: "query_path", action: "query/path", setup: seedActionCorpus, run: qry(QueryRequest{Action: "path", Query: "src/alpha.go::Alpha", Args: map[string]any{"to": "src/beta.go::Beta"}})},
	{name: "query_path_reverse", action: "query/path", setup: seedActionCorpus, run: qry(QueryRequest{Action: "path", Query: "src/beta.go::Beta", Args: map[string]any{"to": "src/alpha.go::Alpha"}})},
	{name: "query_path_missing_to", action: "query/path", setup: seedActionCorpus, run: qry(QueryRequest{Action: "path", Query: "src/alpha.go::Alpha"})},
	{name: "query_callers", action: "query/callers", setup: seedActionCorpus, run: qry(QueryRequest{Action: "callers", Query: "src/beta.go::Beta"})},
	{name: "query_callees", action: "query/callees", setup: seedActionCorpus, run: qry(QueryRequest{Action: "callees", Query: "src/alpha.go::Alpha"})},
	{name: "query_context", action: "query/context", setup: seedActionCorpus, run: qry(QueryRequest{Action: "context", Query: "src/alpha.go::Alpha"})},
	{name: "query_explain", action: "query/explain", setup: seedActionCorpus, run: qry(QueryRequest{Action: "explain", Query: "src/alpha.go::Alpha"})},

	// --- query: memory reads (args.command) ---------------------------------
	{name: "query_memory_session_recall", action: "query/memory", sub: "session_recall", setup: seedActionCorpus,
		run: qry(QueryRequest{Action: "memory", Query: "alpha", Args: map[string]any{"command": "session_recall"}})},
	{name: "query_memory_memories", action: "query/memory", sub: "memories", setup: seedActionCorpus,
		run: qry(QueryRequest{Action: "memory", Query: "alpha", Args: map[string]any{"command": "memories"}})},
	{name: "query_memory_view", action: "query/memory", sub: "view", setup: seedActionCorpus,
		run: qry(QueryRequest{Action: "memory", Args: map[string]any{"command": "view", "path": "MEMORY.md"}})},
	{name: "query_memory_snapshot", action: "query/memory", sub: "snapshot", setup: seedActionCorpus,
		run: qry(QueryRequest{Action: "memory", Args: map[string]any{"command": "snapshot"}})},
	{name: "query_memory_search", action: "query/memory", sub: "search", setup: seedActionCorpus,
		run: qry(QueryRequest{Action: "memory", Query: "Golden", Args: map[string]any{"command": "search"}})},
	{name: "query_memory_default_search", action: "query/memory", sub: "(empty)", setup: seedActionCorpus,
		run: qry(QueryRequest{Action: "memory", Query: "Golden"})},
	{name: "query_memory_unknown_command", action: "query/memory", setup: seedActionCorpus,
		run: qry(QueryRequest{Action: "memory", Args: map[string]any{"command": "bogus"}})},

	// --- query: ontology (args.cmd) -----------------------------------------
	{name: "query_ontology_matches", action: "query/ontology", sub: "matches", setup: chain(seedActionCorpus, seedCatalog),
		run: qry(QueryRequest{Action: "ontology", Args: map[string]any{"cmd": "matches"}})},
	{name: "query_ontology_default", action: "query/ontology", sub: "(empty)", setup: chain(seedActionCorpus, seedCatalog),
		run: qry(QueryRequest{Action: "ontology"})},
	{name: "query_ontology_trace", action: "query/ontology", sub: "trace", setup: chain(seedActionCorpus, seedCatalog),
		run: qry(QueryRequest{Action: "ontology", Query: "alpha", Args: map[string]any{"cmd": "trace"}})},
	{name: "query_ontology_status", action: "query/ontology", sub: "status", setup: chain(seedActionCorpus, seedCatalog),
		run: qry(QueryRequest{Action: "ontology", Args: map[string]any{"cmd": "status"}})},
	{name: "query_ontology_concept_search", action: "query/ontology", sub: "concept_search", setup: chain(seedActionCorpus, seedCatalog),
		run: qry(QueryRequest{Action: "ontology", Query: "alpha", Limit: 5, Args: map[string]any{"cmd": "concept_search"}})},
	{name: "query_ontology_feature_flow", action: "query/ontology", sub: "feature_flow", setup: chain(seedActionCorpus, seedCatalog),
		run: qry(QueryRequest{Action: "ontology", Query: "alpha", Args: map[string]any{"cmd": "feature_flow"}})},
	{name: "query_ontology_traceability", action: "query/ontology", sub: "traceability", setup: chain(seedActionCorpus, seedCatalog),
		run: qry(QueryRequest{Action: "ontology", Args: map[string]any{"cmd": "traceability"}})},
	{name: "query_ontology_unknown_cmd", action: "query/ontology", setup: chain(seedActionCorpus, seedCatalog),
		run: qry(QueryRequest{Action: "ontology", Args: map[string]any{"cmd": "bogus"}})},

	// --- query: compression (tool form, command form, reader form) ----------
	{name: "query_compress_tool", action: "query/compress", setup: seedActionCorpus,
		run: qry(QueryRequest{Action: "compress", Args: map[string]any{"tool": "search_code", "response": map[string]any{
			"results": []any{map[string]any{"file": "src/alpha.go", "line": float64(1), "text": "func Alpha() int {"}},
		}}})},
	{name: "query_compress_cmd", action: "query/compress", setup: seedActionCorpus,
		run: qry(QueryRequest{Action: "compress", Query: "On branch main\nnothing to commit, working tree clean\n", Args: map[string]any{"cmd": "git status"}})},
	{name: "query_compress_file", action: "query/compress", setup: chain(seedActionCorpus, seedSourceFiles),
		run: qry(QueryRequest{Action: "compress", Query: "src/alpha.go", Args: map[string]any{"mode": "full"}})},
	{name: "query_compress_no_input", action: "query/compress", setup: seedActionCorpus,
		run: qry(QueryRequest{Action: "compress"})},

	// --- query: languages, LSP, AST pattern -------------------------------
	// The LSP positive path needs a language server on the host, so its golden
	// pins the refusal; the workspace and document commands are env-gated in
	// actions_vocab_test.go. The pattern positive path runs against a stub CLI.
	{name: "query_languages", action: "query/languages", setup: seedActionCorpus,
		run: qry(QueryRequest{Action: "languages"})},
	{name: "query_lsp_no_registry", action: "query/lsp", setup: seedActionCorpus,
		run: qry(QueryRequest{Action: "lsp", Args: map[string]any{"lang": "go"}})},
	{name: "query_pattern", action: "query/pattern", setup: chain(seedActionCorpus, stubAstGrep),
		run: qry(QueryRequest{Action: "pattern", Limit: 5, Args: map[string]any{"pattern": "func $F($A)", "lang": "go"}})},
	{name: "query_pattern_missing_pattern", action: "query/pattern", setup: seedActionCorpus,
		run: qry(QueryRequest{Action: "pattern"})},
	{name: "query_pattern_args_limit", action: "query/pattern", setup: chain(seedActionCorpus, stubAstGrepTwo),
		run: qry(QueryRequest{Action: "pattern", Args: map[string]any{"pattern": "func $F($A)", "lang": "go", "limit": 1}})},
	{name: "query_pattern_top_level_limit_wins", action: "query/pattern", setup: chain(seedActionCorpus, stubAstGrepTwo),
		run: qry(QueryRequest{Action: "pattern", Limit: 2, Args: map[string]any{"pattern": "func $F($A)", "lang": "go", "limit": 1}})},
	{name: "query_lsp_args_limit", action: "query/lsp", setup: seedActionCorpus,
		run: qry(QueryRequest{Action: "lsp", Args: map[string]any{"lang": "go", "limit": 2}})},

	// --- query: session reads (args.command) --------------------------------
	{name: "query_session_recall", action: "query/session", sub: "recall", setup: seedSession,
		run: qry(QueryRequest{Action: "session", Query: "s1", Args: map[string]any{"command": "recall", "node_id": "node-1"}})},
	{name: "query_session_canvas", action: "query/session", sub: "canvas", setup: seedSession,
		run: qry(QueryRequest{Action: "session", Query: "s1", Args: map[string]any{"command": "canvas"}})},
	{name: "query_session_lessons", action: "query/session", sub: "lessons", setup: seedSession,
		run: qry(QueryRequest{Action: "session", Query: "s1", Args: map[string]any{"command": "lessons"}})},
	{name: "query_session_default", action: "query/session", sub: "(empty)", setup: seedSession,
		run: qry(QueryRequest{Action: "session", Query: "s1"})},
	{name: "query_session_unknown_command", action: "query/session", setup: seedSession,
		run: qry(QueryRequest{Action: "session", Query: "s1", Args: map[string]any{"command": "bogus"}})},

	// --- query: PRD trace, incidents, org knowledge, git history, portfolio -
	{name: "query_prd", action: "query/prd", setup: seedPRD,
		run: qry(QueryRequest{Action: "prd", Query: "FR-AUTH-01"})},
	{name: "query_incidents", action: "query/incidents", setup: seedActionCorpus,
		run: qry(QueryRequest{Action: "incidents", Limit: 5, Args: map[string]any{"service": "svc-a", "env": "prod"}})},
	{name: "query_env_conflicts", action: "query/env_conflicts", setup: seedActionCorpus,
		run: qry(QueryRequest{Action: "env_conflicts", Args: map[string]any{"service": "svc-a"}})},
	{name: "query_service_context", action: "query/service_context", setup: seedActionCorpus,
		run: qry(QueryRequest{Action: "service_context", Args: map[string]any{"service": "svc-a", "env": "prod"}})},
	{name: "query_history", action: "query/history", setup: seedGitHistory,
		run: qry(QueryRequest{Action: "history", Query: "main.go"})},
	{name: "query_history_missing_query", action: "query/history", setup: seedGitHistory,
		run: qry(QueryRequest{Action: "history"})},
	{name: "query_cochange", action: "query/cochange", setup: seedGitHistory,
		run: qry(QueryRequest{Action: "cochange", Query: "main.go"})},
	{name: "query_churn", action: "query/churn", setup: seedGitHistory,
		run: qry(QueryRequest{Action: "churn"})},
	{name: "query_portfolio_summary", action: "query/portfolio", setup: chain(seedActionCorpus, seedPortfolioRegistry),
		run: qry(QueryRequest{Action: "portfolio", Args: map[string]any{"cmd": "summary"}})},
	{name: "query_portfolio_fanout", action: "query/portfolio", setup: chain(seedActionCorpus, seedPortfolioRegistry),
		run: qry(QueryRequest{Action: "portfolio", Query: "Alpha", Limit: 5})},
	{name: "query_portfolio_missing_query", action: "query/portfolio", setup: chain(seedActionCorpus, seedPortfolioRegistry),
		run: qry(QueryRequest{Action: "portfolio"})},

	// --- CX-44 typed args: unknown keys and wrong types are refused ----------
	{name: "query_unknown_arg", action: "query/exact", setup: seedActionCorpus,
		run: qry(QueryRequest{Action: "exact", Query: "Alpha", Args: map[string]any{"bogus": "x"}})},
	{name: "query_wrong_type_depth", action: "query/impact", setup: seedActionCorpus,
		run: qry(QueryRequest{Action: "impact", Query: "src/beta.go::Beta", Args: map[string]any{"depth": "banana"}})},
	{name: "query_depth_string_spelling", action: "query/impact", setup: seedActionCorpus,
		run: qry(QueryRequest{Action: "impact", Query: "src/beta.go::Beta", Args: map[string]any{"depth": "2"}})},
	{name: "query_depth_empty_string", action: "query/impact", setup: seedActionCorpus,
		run: qry(QueryRequest{Action: "impact", Query: "src/beta.go::Beta", Args: map[string]any{"depth": ""}})},
	{name: "query_ontology_command_key", action: "query/ontology", sub: "matches", setup: chain(seedActionCorpus, seedCatalog),
		run: qry(QueryRequest{Action: "ontology", Args: map[string]any{"command": "matches"}})},
	{name: "query_wrong_type_include_git", action: "query/exact", setup: seedActionCorpus,
		run: qry(QueryRequest{Action: "exact", Query: "Alpha", Args: map[string]any{"include_git": map[string]any{}}})},
	{name: "import_repo_unknown_arg", action: "import/repo", setup: seedSourceFiles,
		run: imp(ImportRequest{Action: "repo", Path: ".", Args: map[string]any{"path": "."}})},
	{name: "import_memory_unknown_arg", action: "import/memory", sub: "create",
		run: imp(ImportRequest{Action: "memory", Command: "create", Path: "topics/x.md", Content: "x", Args: map[string]any{"bogus": float64(1)}})},
	{name: "import_memory_wrong_type_tags", action: "import/memory", sub: "session_retain",
		run: imp(ImportRequest{Action: "memory", Command: "session_retain", Args: map[string]any{"tags": []any{"a", float64(2)}, "turns": []any{"t"}}})},

	{name: "query_unknown_action", action: "query", setup: seedActionCorpus,
		run: qry(QueryRequest{Action: "bogus", Query: "x"})},

	// --- import: repo indexing, docs, PRD, git history ----------------------
	{name: "import_git", action: "import/git", setup: seedGitRepo, run: imp(ImportRequest{Action: "git"})},
	{name: "import_git_not_a_repo", action: "import/git", run: imp(ImportRequest{Action: "git"})},
	{name: "import_docs", action: "import/docs", setup: seedDocs, run: imp(ImportRequest{Action: "docs", Path: "docs"})},
	{name: "import_docs_missing_path", action: "import/docs", run: imp(ImportRequest{Action: "docs"})},
	{name: "import_prd", action: "import/prd", setup: writePRD, run: imp(ImportRequest{Action: "prd", Path: "prd.md"})},
	{name: "import_prd_missing_path", action: "import/prd", run: imp(ImportRequest{Action: "prd"})},
	{name: "import_repo", action: "import/repo", setup: seedSourceFiles, run: imp(ImportRequest{Action: "repo", Path: "."})},
	{name: "import_repo_missing_path", action: "import/repo", run: imp(ImportRequest{Action: "repo"})},
	{name: "import_dir", action: "import/dir", setup: seedSourceFiles, run: imp(ImportRequest{Action: "dir", Path: "."})},
	{name: "import_dir_subdir_refused", action: "import/dir", setup: seedSourceFiles, run: imp(ImportRequest{Action: "dir", Path: "src"})},

	// --- import: memory writes (action=memory, command=...) ----------------
	{name: "import_memory_create", action: "import/memory", sub: "create",
		run: imp(ImportRequest{Action: "memory", Command: "create", Path: "topics/create.md", Content: "# Create\n"})},
	{name: "import_memory_str_replace", action: "import/memory", sub: "str_replace",
		setup: seedMemoryFile("topics/edit.md", "# Edit\n\nold text\n"),
		run:   imp(ImportRequest{Action: "memory", Command: "str_replace", Args: map[string]any{"path": "topics/edit.md", "old": "old text", "new": "new text"}})},
	{name: "import_memory_insert", action: "import/memory", sub: "insert",
		setup: seedMemoryFile("topics/insert.md", "# Insert\n"),
		run:   imp(ImportRequest{Action: "memory", Command: "insert", Args: map[string]any{"path": "topics/insert.md", "content": "inserted line", "insert_line": float64(1)}})},
	{name: "import_memory_delete", action: "import/memory", sub: "delete",
		setup: seedMemoryFile("topics/del.md", "# Delete\n"),
		run:   imp(ImportRequest{Action: "memory", Command: "delete", Args: map[string]any{"path": "topics/del.md"}})},
	{name: "import_memory_delete_document", action: "import/memory", sub: "delete_document",
		run: imp(ImportRequest{Action: "memory", Command: "delete_document", Args: map[string]any{"document_id": "doc-missing", "scope": "per-project"}})},
	{name: "import_memory_delete_document_missing_id", action: "import/memory", run: imp(ImportRequest{Action: "memory", Command: "delete_document"})},
	{name: "import_memory_rename", action: "import/memory", sub: "rename",
		setup: seedMemoryFile("topics/old.md", "# Old\n"),
		run:   imp(ImportRequest{Action: "memory", Command: "rename", Args: map[string]any{"path": "topics/old.md", "new_path": "topics/new.md"}})},
	{name: "import_memory_add", action: "import/memory", sub: "add",
		setup: seedMemoryFile("topics/add.md", "# Add\n"),
		run:   imp(ImportRequest{Action: "memory", Command: "add", Args: map[string]any{"file": "topics/add.md", "text": "added line"}})},
	{name: "import_memory_replace", action: "import/memory", sub: "replace",
		setup: seedMemoryFile("topics/rep.md", "alpha beta\n"),
		run:   imp(ImportRequest{Action: "memory", Command: "replace", Args: map[string]any{"file": "topics/rep.md", "old": "alpha", "new": "gamma"}})},
	{name: "import_memory_remove", action: "import/memory", sub: "remove",
		setup: seedMemoryFile("topics/rem.md", "keep\nremove me\n"),
		run:   imp(ImportRequest{Action: "memory", Command: "remove", Args: map[string]any{"file": "topics/rem.md", "text": "remove me"}})},
	{name: "import_memory_session_retain", action: "import/memory", sub: "session_retain",
		run: imp(ImportRequest{Action: "memory", Command: "session_retain", Args: map[string]any{
			"session_id": "s1", "scope": "per-project",
			"turns": []any{"first turn", "second turn"}, "retained_through_user_turn": float64(2),
		}})},
	{name: "import_memory_unknown_command", action: "import/memory", run: imp(ImportRequest{Action: "memory", Command: "bogus"})},

	// --- import: session writes (command=offload|lesson) -------------------
	{name: "import_session_offload", action: "import/session", sub: "offload",
		run: imp(ImportRequest{Action: "session", Command: "offload", Args: map[string]any{"session_id": "s1", "node_id": "node-1", "payload": "payload-one", "summary": "short"}})},
	{name: "import_session_lesson", action: "import/session", sub: "lesson",
		run: imp(ImportRequest{Action: "session", Command: "lesson", Args: map[string]any{"session_id": "s1", "text": "keep the ladder order"}})},
	{name: "import_session_lesson_empty", action: "import/session", sub: "lesson",
		run: imp(ImportRequest{Action: "session", Command: "lesson", Args: map[string]any{"session_id": "s1"}})},
	{name: "import_session_unknown_command", action: "import/session", run: imp(ImportRequest{Action: "session", Command: "bogus"})},

	// --- import: compressed reader, ontology -------------------------------
	{name: "import_read", action: "import/read", setup: seedSourceFiles,
		run: imp(ImportRequest{Action: "read", Path: "src/alpha.go", Args: map[string]any{"mode": "full"}})},
	{name: "import_read_fresh_true", action: "import/read", setup: seedSourceFiles,
		run: imp(ImportRequest{Action: "read", Path: "src/alpha.go", Args: map[string]any{"mode": "full", "fresh": true}})},
	{name: "import_read_missing_path", action: "import/read", run: imp(ImportRequest{Action: "read"})},
	{name: "import_read_invalid_mode", action: "import/read", setup: seedSourceFiles,
		run: imp(ImportRequest{Action: "read", Path: "src/alpha.go", Args: map[string]any{"mode": "bogus"}})},
	{name: "import_ontology_dir", action: "import/ontology", setup: seedOntologyDir,
		run: imp(ImportRequest{Action: "ontology", Path: "ontology"})},
	{name: "import_ontology_file", action: "import/ontology", setup: writeCatalog, run: imp(ImportRequest{Action: "ontology", Path: "concepts.json"})},
	{name: "import_ontology_missing_path", action: "import/ontology", run: imp(ImportRequest{Action: "ontology"})},

	// --- import: envelope errors -------------------------------------------
	{name: "import_empty_action", action: "import/(empty)", run: imp(ImportRequest{})},
	{name: "import_unknown_action", action: "import", run: imp(ImportRequest{Action: "bogus"})},

	// --- the four-tool envelope and status ---------------------------------
	{name: "tool_import", action: "tool/import", run: envelopeRun("import")},
	{name: "tool_query", action: "tool/query", run: envelopeRun("query")},
	{name: "tool_status", action: "tool/status", run: envelopeRun("status")},
	{name: "tool_authenticate", action: "tool/authenticate", run: envelopeRun("authenticate")},
	{name: "tool_superseded_get", action: "tool", run: envelopeRun("get")},
	{name: "tool_unknown", action: "tool", run: envelopeRun("delete_everything")},
	{name: "status", action: "status", setup: seedActionCorpus, run: func(f *actionFixture) (map[string]any, error) {
		return f.e.Status(context.Background())
	}},
}

// envelopeRun resolves a tool name through ResolveEnvelope and reports the
// name it resolved to, so the golden pins both the accepted and refused names.
func envelopeRun(tool string) actionRun {
	return func(*actionFixture) (map[string]any, error) {
		resolved, err := ResolveEnvelope(tool)
		if err != nil {
			return nil, err
		}
		return map[string]any{"tool": tool, "resolved": resolved}, nil
	}
}

// TestActionGoldens runs every case against a fresh fixture and compares the
// redacted response to its golden. An error is a golden too: it is recorded as
// {"error": ...}, so refusals are pinned as carefully as answers.
func TestActionGoldens(t *testing.T) {
	seen := map[string]bool{}
	for _, c := range actionCases {
		c := c
		if seen[c.name] {
			t.Fatalf("duplicate golden name %q", c.name)
		}
		seen[c.name] = true
		t.Run(c.name, func(t *testing.T) {
			f := newActionFixture(t)
			if c.setup != nil {
				c.setup(f)
			}
			out, err := c.run(f)
			var v any = out
			if err != nil {
				v = map[string]any{"error": err.Error()}
			}
			got := actionCanon(t, v, f)
			path := filepath.Join(actionGoldenDir, c.name+".json")
			if *update {
				if err := os.MkdirAll(actionGoldenDir, 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, actionPretty(t, got), 0o644); err != nil {
					t.Fatal(err)
				}
				return
			}
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("golden missing (run with -update): %v", err)
			}
			if want := actionCompact(t, raw); !bytes.Equal(want, got) {
				t.Fatalf("golden mismatch for %s\n--- got ---\n%s\n--- want (run with -update) ---\n%s",
					c.name, actionPretty(t, got), string(raw))
			}
		})
	}
}

var (
	actionReSeq     = regexp.MustCompile(`"seq":\s*-?\d+`)
	actionReAt      = regexp.MustCompile(`"at":\s*-?\d+`)
	actionReIndexed = regexp.MustCompile(`"indexed_at":\s*-?\d+`)
	actionReBytes   = regexp.MustCompile(`"(size_bytes|free_bytes|live_bytes|wal_bytes|bloat_fraction)":\s*[0-9.e-]+`)
	actionReSHA     = regexp.MustCompile(`\b[0-9a-f]{40}\b`)
	actionReRFC3339 = regexp.MustCompile(`\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(\.\d+)?(Z|[+-]\d{2}:\d{2})`)
	actionReUnix    = regexp.MustCompile(`"(\w+_unix)":\s*\d+`)
	actionReBank    = regexp.MustCompile(`proj-[a-z0-9]{8,}`)
)

// actionCanon marshals v and redacts what varies between runs: the fixture's
// temp directories, watermark sequence and time, storage byte counts, commit
// hashes and timestamps. The redaction is the one internal/golden applies, plus
// the git and time fields this suite's cases produce.
func actionCanon(t *testing.T, v any, f *actionFixture) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	s = actionReBytes.ReplaceAllString(s, `"$1":"<BYTES>"`)
	s = actionReSeq.ReplaceAllLiteralString(s, `"seq":"<SEQ>"`)
	s = actionReAt.ReplaceAllLiteralString(s, `"at":"<AT>"`)
	s = actionReIndexed.ReplaceAllLiteralString(s, `"indexed_at":"<AT>"`)
	s = actionReSHA.ReplaceAllLiteralString(s, "<SHA>")
	s = actionReRFC3339.ReplaceAllLiteralString(s, "<TIME>")
	s = actionReUnix.ReplaceAllString(s, `"$1":"<UNIX>"`)
	s = actionReBank.ReplaceAllLiteralString(s, "proj-<BANK>")
	for _, p := range f.redactPaths() {
		s = strings.ReplaceAll(s, p, "<PROJECT>")
	}
	s = strings.ReplaceAll(s, f.root, "<TMP>")
	var out any
	if err := json.Unmarshal([]byte(s), &out); err != nil {
		t.Fatalf("redaction produced invalid JSON: %v\n%s", err, s)
	}
	return actionMarshalCompact(t, out)
}

// redactPaths lists the project path as the engine may spell it: as given, and
// with symlinks resolved (on macOS the temp directory is a symlink). The
// longer spelling comes first, so replacing the short one cannot leave the
// symlink prefix behind in front of <PROJECT>.
func (f *actionFixture) redactPaths() []string {
	paths := []string{f.project}
	if real, err := filepath.EvalSymlinks(f.project); err == nil && real != f.project {
		paths = []string{real, f.project}
	}
	return paths
}

func actionCompact(t *testing.T, raw []byte) []byte {
	t.Helper()
	var out any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("golden file is not valid JSON: %v", err)
	}
	return actionMarshalCompact(t, out)
}

// actionMarshalCompact emits sorted-key compact JSON without HTML escaping.
func actionMarshalCompact(t *testing.T, v any) []byte {
	t.Helper()
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		t.Fatal(err)
	}
	return bytes.TrimRight(buf.Bytes(), "\n")
}

func actionPretty(t *testing.T, compactJSON []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := json.Indent(&buf, compactJSON, "", "  "); err != nil {
		t.Fatal(err)
	}
	buf.WriteByte('\n')
	return buf.Bytes()
}
