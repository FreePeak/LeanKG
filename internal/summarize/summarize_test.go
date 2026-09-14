package summarize

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/FreePeak/LeanKG/internal/store"
)

// --- fixture ---------------------------------------------------------------

// fixture is a temp project with real files on disk plus the matching
// code_files rows, so Run's file list and its reads agree.
type fixture struct {
	t   *testing.T
	dir string
	st  store.Backend
}

func newFixture(t *testing.T, files map[string]string) *fixture {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, ".leankg", "leankg.db"), store.RW)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.Migrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	var recs []store.FileRecord
	for path, content := range files {
		recs = append(recs, writeProjectFile(t, dir, path, content))
	}
	sort.Slice(recs, func(i, j int) bool { return recs[i].Path < recs[j].Path })
	if err := st.UpsertFiles(recs); err != nil {
		t.Fatalf("upsert files: %v", err)
	}
	return &fixture{t: t, dir: dir, st: st}
}

// writeProjectFile writes content under dir and returns its index record.
func writeProjectFile(t *testing.T, dir, path, content string) store.FileRecord {
	t.Helper()
	full := filepath.Join(dir, filepath.FromSlash(path))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(content))
	return store.FileRecord{
		Path: path, Size: int64(len(content)), MtimeNS: 1,
		ContentHash: hex.EncodeToString(sum[:]),
	}
}

// rewrite changes a file on disk and its index row, the way a re-index would.
func (f *fixture) rewrite(path, content string) {
	f.t.Helper()
	rec := writeProjectFile(f.t, f.dir, path, content)
	if err := f.st.UpsertFiles([]store.FileRecord{rec}); err != nil {
		f.t.Fatal(err)
	}
}

// --- fake LLM server -------------------------------------------------------

// fakeServer is an httptest chat-completions endpoint: it records every request
// (so a test can assert the prompt shape) and answers from a canned function.
type fakeServer struct {
	*httptest.Server
	mu       sync.Mutex
	requests []chatRequest
	reply    func(req chatRequest) (status int, body string)
}

func newFakeServer(t *testing.T, reply func(chatRequest) (int, string)) *fakeServer {
	t.Helper()
	if reply == nil {
		reply = cannedReply
	}
	f := &fakeServer{reply: reply}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/chat/completions" {
			t.Errorf("request: got %s %s, want POST /chat/completions", r.Method, r.URL.Path)
		}
		var req chatRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode request: %v", err)
			http.Error(w, "bad json", http.StatusBadRequest)
			return
		}
		f.mu.Lock()
		f.requests = append(f.requests, req)
		f.mu.Unlock()
		status, body := f.reply(req)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(f.Close)
	return f
}

// calls returns every request; fileCalls and batchCalls split them by the
// system prompt the pipeline sent, which is what most assertions want.
func (f *fakeServer) calls() []chatRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]chatRequest(nil), f.requests...)
}

func (f *fakeServer) fileCalls() []chatRequest  { return f.pick(pass1System) }
func (f *fakeServer) batchCalls() []chatRequest { return f.pick(pass2System) }

func (f *fakeServer) pick(want string) []chatRequest {
	var out []chatRequest
	for _, r := range f.calls() {
		if len(r.Messages) > 0 && r.Messages[0].Content == want {
			out = append(out, r)
		}
	}
	return out
}

func (f *fakeServer) chat() Chat {
	return LLMConfig{BaseURL: f.URL, Model: "test-model", MaxTokens: 2048, Timeout: 10 * time.Second}.New()
}

// cannedReply answers both passes deterministically: prose for a file call, and
// a curated node set for a synthesis call (one "system" node per file group the
// batch mentions).
func cannedReply(req chatRequest) (int, string) {
	user := req.Messages[1].Content
	if len(req.Messages) > 0 && req.Messages[0].Content != pass2System {
		path := strings.SplitN(strings.TrimPrefix(user, "File: "), "\n", 2)[0]
		return 200, chatJSON(path + " defines the unit's behaviour and calls into the store.")
	}
	var nodes []string
	for _, line := range strings.Split(user, "\n") {
		if !strings.HasPrefix(line, "## ") {
			continue
		}
		path := strings.TrimPrefix(line, "## ")
		nodes = append(nodes, fmt.Sprintf(`{"name":"%s subsystem","type":"system","summary":"The %s subsystem.","sources":["%s"],"links":[]}`, path, path, path))
	}
	return 200, chatJSON(fmt.Sprintf(`{"nodes":[%s]}`, strings.Join(nodes, ",")))
}

// chatJSON wraps a canned content string in the chat-completions envelope.
func chatJSON(content string) string {
	b, _ := json.Marshal(content)
	return `{"choices":[{"message":{"content":` + string(b) + `}}]}`
}

// pass1System / pass2System are the prompt identities the fake server uses to
// tell a file call from a batch call.
const (
	pass1System = summarySystemPrompt
	pass2System = synthesisSystemPrompt
)

func runOpts(dir string, chat Chat) Options {
	return Options{ProjectDir: dir, Chat: chat, Concurrency: 1}
}

// --- pass 1 ----------------------------------------------------------------

// TestSummarizeSendsDeterministicPromptAndClips pins the prompt contract: one
// call per file, temperature 0, the configured token cap, the system prompt
// demanding 3-8 sentences, and code clipped to exactly MaxCodeChars runes with
// graft's truncation marker.
func TestSummarizeSendsDeterministicPromptAndClips(t *testing.T) {
	small := "package a\n\nfunc A() {}\n"
	big := strings.Repeat("x", MaxCodeChars+5000)
	f := newFixture(t, map[string]string{"a/small.go": small, "b/big.go": big})
	srv := newFakeServer(t, nil)

	res, err := Run(context.Background(), f.st, runOpts(f.dir, srv.chat()))
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if res.Summarized != 2 || res.Clipped != 1 || res.Failed != 0 {
		t.Fatalf("result: %+v", res)
	}

	calls := srv.fileCalls()
	if len(calls) != 2 {
		t.Fatalf("file calls: got %d, want 2", len(calls))
	}
	byPath := map[string]chatRequest{}
	for _, c := range calls {
		if c.Temperature != 0 {
			t.Errorf("temperature: got %v, want 0", c.Temperature)
		}
		if c.MaxTokens != 2048 {
			t.Errorf("max_tokens: got %d, want 2048", c.MaxTokens)
		}
		if c.Model != "test-model" {
			t.Errorf("model: got %q", c.Model)
		}
		if len(c.Messages) != 2 || c.Messages[0].Role != "system" || c.Messages[1].Role != "user" {
			t.Fatalf("messages: %+v", c.Messages)
		}
		if !strings.Contains(c.Messages[0].Content, "3-8 sentences") {
			t.Errorf("system prompt lost the prose contract: %q", c.Messages[0].Content)
		}
		byPath[strings.SplitN(strings.TrimPrefix(c.Messages[1].Content, "File: "), "\n", 2)[0]] = c
	}
	smallReq, ok := byPath["a/small.go"]
	if !ok {
		t.Fatalf("no call for the small file: %v", byPath)
	}
	if !strings.HasSuffix(smallReq.Messages[1].Content, small) {
		t.Errorf("small file sent unclipped/incorrectly:\n%q", smallReq.Messages[1].Content)
	}
	bigReq, ok := byPath["b/big.go"]
	if !ok {
		t.Fatalf("no call for the big file")
	}
	code := strings.SplitN(bigReq.Messages[1].Content, "\n\n", 2)[1]
	marker := fmt.Sprintf("\n… (truncated at %d characters)", MaxCodeChars)
	if !strings.HasSuffix(code, marker) {
		t.Fatalf("big file has no truncation marker: …%q", code[len(code)-80:])
	}
	if got := utf8.RuneCountInString(strings.TrimSuffix(code, marker)); got != MaxCodeChars {
		t.Errorf("clipped runes: got %d, want %d", got, MaxCodeChars)
	}

	// The checkpoint is written, keyed by the hash of the bytes that were sent.
	got, ok, err := f.st.SummaryGet("a/small.go")
	if err != nil || !ok {
		t.Fatalf("summary not persisted: ok=%v err=%v", ok, err)
	}
	sum := sha256.Sum256([]byte(small))
	if got.ContentHash != hex.EncodeToString(sum[:]) {
		t.Errorf("stored hash: got %q", got.ContentHash)
	}
	if got.Model != "test-model" || !strings.Contains(got.Summary, "a/small.go") {
		t.Errorf("stored row: %+v", got)
	}
}

// TestSummarizeResumesUnchangedFiles is the resume contract: a second run over
// unchanged bytes spends zero calls, an edited file costs exactly one, and a
// model switch invalidates the tier (the stored stamp no longer matches).
func TestSummarizeResumesUnchangedFiles(t *testing.T) {
	f := newFixture(t, map[string]string{"a.go": "a", "b.go": "b"})
	srv := newFakeServer(t, nil)
	ctx := context.Background()

	if _, err := Run(ctx, f.st, runOpts(f.dir, srv.chat())); err != nil {
		t.Fatalf("run 1: %v", err)
	}
	if n := len(srv.fileCalls()); n != 2 {
		t.Fatalf("first run file calls: got %d, want 2", n)
	}

	res, err := Run(ctx, f.st, runOpts(f.dir, srv.chat()))
	if err != nil {
		t.Fatalf("run 2: %v", err)
	}
	if n := len(srv.fileCalls()); n != 2 {
		t.Errorf("unchanged re-run spent %d extra calls, want 0", n-2)
	}
	if res.Resumed != 2 || res.Summarized != 0 {
		t.Errorf("resume report: %+v", res)
	}

	f.rewrite("b.go", "b changed")
	res, err = Run(ctx, f.st, runOpts(f.dir, srv.chat()))
	if err != nil {
		t.Fatalf("run 3: %v", err)
	}
	if n := len(srv.fileCalls()); n != 3 {
		t.Errorf("edited file file-calls: got %d, want 3 total", n)
	}
	if res.Resumed != 1 || res.Summarized != 1 {
		t.Errorf("edit report: %+v", res)
	}

	// Same bytes, different model: the stamp must not match.
	other := LLMConfig{BaseURL: srv.URL, Model: "other-model", MaxTokens: 2048, Timeout: 10 * time.Second}.New()
	res, err = Run(ctx, f.st, runOpts(f.dir, other))
	if err != nil {
		t.Fatalf("run 4: %v", err)
	}
	if res.Summarized != 2 || res.Resumed != 0 {
		t.Errorf("a model switch must re-summarize everything: %+v", res)
	}
}

// TestSummarizeForceIgnoresResume pins --force: the hash matches, the call
// still happens (the only way to regenerate after a prompt change).
func TestSummarizeForceIgnoresResume(t *testing.T) {
	f := newFixture(t, map[string]string{"a.go": "a"})
	srv := newFakeServer(t, nil)
	ctx := context.Background()
	if _, err := Run(ctx, f.st, runOpts(f.dir, srv.chat())); err != nil {
		t.Fatalf("run 1: %v", err)
	}
	opts := runOpts(f.dir, srv.chat())
	opts.Force = true
	res, err := Run(ctx, f.st, opts)
	if err != nil {
		t.Fatalf("run 2: %v", err)
	}
	if res.Summarized != 1 || res.Resumed != 0 {
		t.Errorf("force report: %+v", res)
	}
	if n := len(srv.fileCalls()); n != 2 {
		t.Errorf("file calls: got %d, want 2", n)
	}
}

// TestFailureGateContinuesPastIndividualFailures: a failing file is recorded
// and the pass keeps going — one bad file never aborts the meaning tier.
func TestFailureGateContinuesPastIndividualFailures(t *testing.T) {
	files := map[string]string{}
	for i := 0; i < 8; i++ {
		files[fmt.Sprintf("f%02d.go", i)] = fmt.Sprintf("package p%d", i)
	}
	f := newFixture(t, files)
	srv := newFakeServer(t, func(req chatRequest) (int, string) {
		body := req.Messages[1].Content
		if strings.HasPrefix(body, "File: f02.go") || strings.HasPrefix(body, "File: f05.go") {
			return 500, `{"error":{"message":"upstream exploded"}}`
		}
		return cannedReply(req)
	})

	res, err := Run(context.Background(), f.st, runOpts(f.dir, srv.chat()))
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if res.Fatal != "" {
		t.Errorf("two non-consecutive failures must not trip the gate: %q", res.Fatal)
	}
	if res.Summarized != 6 || res.Failed != 2 || res.Skipped != 0 {
		t.Errorf("report: %+v", res)
	}
	if len(res.Errors) != 2 {
		t.Errorf("errors: %v", res.Errors)
	}
	if _, ok, _ := f.st.SummaryGet("f02.go"); ok {
		t.Error("a failed file must not be checkpointed, or the next run resumes a hole")
	}
	if _, ok, _ := f.st.SummaryGet("f03.go"); !ok {
		t.Error("the pass stopped early: f03.go has no summary")
	}
}

// TestFailureGateStopsAfterFiveConsecutiveFailures: five in a row is a provider
// that is not coming back; the pass stops spending and says why (graft #127).
func TestFailureGateStopsAfterFiveConsecutiveFailures(t *testing.T) {
	files := map[string]string{}
	for i := 0; i < 8; i++ {
		files[fmt.Sprintf("f%02d.go", i)] = "package p"
	}
	f := newFixture(t, files)
	srv := newFakeServer(t, func(chatRequest) (int, string) {
		return 500, `{"error":{"message":"boom"}}`
	})

	res, err := Run(context.Background(), f.st, runOpts(f.dir, srv.chat()))
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if res.Fatal == "" {
		t.Fatal("five consecutive failures must stop the pass")
	}
	if res.Failed != MaxConsecutiveFailures {
		t.Errorf("failed: got %d, want %d", res.Failed, MaxConsecutiveFailures)
	}
	if res.Skipped != 3 {
		t.Errorf("skipped: got %d, want 3 (8 files - 5 attempts)", res.Skipped)
	}
	if n := len(srv.fileCalls()); n != MaxConsecutiveFailures {
		t.Errorf("file calls: got %d, want %d — the pass must stop spending", n, MaxConsecutiveFailures)
	}
	if !strings.Contains(res.Summary(), "fatal:") {
		t.Errorf("summary lost the fatal reason: %s", res.Summary())
	}
}

// TestFailureGateIsTerminalOnQuota: a spent quota or a rejected key is
// immediate — the very first 402 ends the pass (graft failure.ts:29-36).
func TestFailureGateIsTerminalOnQuota(t *testing.T) {
	files := map[string]string{}
	for i := 0; i < 5; i++ {
		files[fmt.Sprintf("f%02d.go", i)] = "package p"
	}
	f := newFixture(t, files)
	srv := newFakeServer(t, func(chatRequest) (int, string) {
		return 402, `{"error":{"message":"insufficient_quota: your credit is exhausted"}}`
	})

	res, err := Run(context.Background(), f.st, runOpts(f.dir, srv.chat()))
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if n := len(srv.fileCalls()); n != 1 {
		t.Errorf("file calls: got %d, want 1 — quota is terminal, not a five-strike count", n)
	}
	if !strings.Contains(res.Fatal, "quota") {
		t.Errorf("fatal: %q", res.Fatal)
	}
	if res.Skipped != 4 {
		t.Errorf("skipped: got %d, want 4", res.Skipped)
	}
}

// TestEmptySummaryIsAQualityMissNotACheckpoint: a provider that answers with
// nothing is reported, never cached (graft #177), and does not trip the cutoff.
func TestEmptySummaryIsAQualityMissNotACheckpoint(t *testing.T) {
	f := newFixture(t, map[string]string{"a.go": "a", "b.go": "b", "c.go": "c"})
	srv := newFakeServer(t, func(req chatRequest) (int, string) {
		if strings.HasPrefix(req.Messages[1].Content, "File: b.go") {
			return 200, chatJSON("   ")
		}
		return cannedReply(req)
	})

	res, err := Run(context.Background(), f.st, runOpts(f.dir, srv.chat()))
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if res.Fatal != "" {
		t.Errorf("one empty answer must not stop the pass: %q", res.Fatal)
	}
	if res.Failed != 1 || res.Summarized != 2 {
		t.Errorf("report: %+v", res)
	}
	if _, ok, _ := f.st.SummaryGet("b.go"); ok {
		t.Error("an empty summary must never be checkpointed")
	}
}

// TestDryRunSpendsNothingAndNeedsNoProvider: dry-run is a cost preview — no
// environment, no calls, no writes.
func TestDryRunSpendsNothingAndNeedsNoProvider(t *testing.T) {
	f := newFixture(t, map[string]string{"a.go": "a", "b.go": "b"})
	srv := newFakeServer(t, func(chatRequest) (int, string) {
		t.Error("dry run must not call the provider")
		return 500, "{}"
	})

	res, err := Run(context.Background(), f.st, Options{ProjectDir: f.dir, DryRun: true, Concurrency: 1})
	if err != nil {
		t.Fatalf("dry run without a provider: %v", err)
	}
	if res.Summarized != 2 || res.Resumed != 0 || res.Nodes != 0 {
		t.Errorf("plan: %+v", res)
	}
	if got := len(srv.calls()); got != 0 {
		t.Errorf("calls: got %d, want 0", got)
	}
	if _, ok, _ := f.st.SummaryGet("a.go"); ok {
		t.Error("dry run wrote a checkpoint")
	}
	if _, err := os.Stat(filepath.Join(f.dir, ".leankg", "summarize")); !os.IsNotExist(err) {
		t.Error("dry run created the node directory")
	}
	if !strings.Contains(res.Summary(), "dry run") {
		t.Errorf("summary: %s", res.Summary())
	}
}

// TestConfigFromEnv documents and pins the LEANKG_LLM_* contract.
func TestConfigFromEnv(t *testing.T) {
	t.Setenv("LEANKG_LLM_MODEL", "")
	t.Setenv("LEANKG_LLM_BASE_URL", "")
	t.Setenv("LEANKG_LLM_API_KEY", "")
	t.Setenv("LEANKG_LLM_MAX_TOKENS", "")
	t.Setenv("LEANKG_LLM_TIMEOUT_SECS", "")
	if _, err := ConfigFromEnv(); err == nil {
		t.Fatal("a missing LEANKG_LLM_MODEL must be an actionable error, not a default")
	}

	t.Setenv("LEANKG_LLM_MODEL", "m")
	cfg, err := ConfigFromEnv()
	if err != nil {
		t.Fatalf("defaults: %v", err)
	}
	if cfg.BaseURL != "https://api.openai.com/v1" || cfg.MaxTokens != 2048 || cfg.Timeout != 120*time.Second {
		t.Errorf("defaults: %+v", cfg)
	}

	t.Setenv("LEANKG_LLM_BASE_URL", "http://127.0.0.1:8080/v1/")
	t.Setenv("LEANKG_LLM_MAX_TOKENS", "512")
	t.Setenv("LEANKG_LLM_TIMEOUT_SECS", "5")
	cfg, err = ConfigFromEnv()
	if err != nil {
		t.Fatalf("overrides: %v", err)
	}
	if cfg.BaseURL != "http://127.0.0.1:8080/v1" || cfg.MaxTokens != 512 || cfg.Timeout != 5*time.Second {
		t.Errorf("overrides: %+v", cfg)
	}

	t.Setenv("LEANKG_LLM_MAX_TOKENS", "zero")
	if _, err := ConfigFromEnv(); err == nil {
		t.Error("a non-numeric token cap must fail loudly")
	}
}

// TestPostIndexHookIsOptIn pins the lazy-activation default.
func TestPostIndexHookIsOptIn(t *testing.T) {
	for _, tc := range []struct {
		value string
		want  bool
	}{
		{"", false}, {"0", false}, {"false", false}, {"no", false}, {"off", false},
		{"1", true}, {"true", true}, {"yes", true}, {"TRUE", true},
	} {
		t.Setenv(AfterIndexEnv, tc.value)
		if got := AfterIndexEnabled(); got != tc.want {
			t.Errorf("%s=%q: got %v, want %v", AfterIndexEnv, tc.value, got, tc.want)
		}
	}
	t.Setenv(AfterIndexEnv, "")
	f := newFixture(t, map[string]string{"a.go": "a"})
	if _, ran, err := RunAfterIndex(context.Background(), f.st, f.dir); ran || err != nil {
		t.Errorf("hook must be a no-op when unset: ran=%v err=%v", ran, err)
	}
}

// TestClipIsRuneSafe: a cut inside a multi-byte character would corrupt the
// prompt, so the clip counts runes.
func TestClipIsRuneSafe(t *testing.T) {
	in := strings.Repeat("é", 100)
	out, truncated := clip(in, 40)
	if !truncated {
		t.Fatal("expected truncation")
	}
	if !utf8.ValidString(out) {
		t.Fatal("clip produced invalid UTF-8")
	}
	marker := fmt.Sprintf("\n… (truncated at %d characters)", 40)
	if strings.TrimSuffix(out, marker) != strings.Repeat("é", 40) {
		t.Errorf("clip body: %q", strings.TrimSuffix(out, marker))
	}
	if s, tr := clip("short", 40); tr || s != "short" {
		t.Errorf("no-op clip: %q %v", s, tr)
	}
}
