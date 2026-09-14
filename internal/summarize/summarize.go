// Package summarize ports the deterministic LLM-meaning pipeline of
// trailhq/Graft 05760b0 onto the Go engine (issue #297).
//
// Two passes, both deterministic in shape and both resumable:
//
//	PASS 1 (per-file prose, see summarizeFile): one completion per source file
//	— temperature 0, a token cap, 3-8 sentences naming concrete identifiers,
//	code clipped to MaxCodeChars. Each result is persisted in file_summaries
//	keyed by the SHA-256 of the bytes that produced it, so an unchanged file
//	is a HIT that costs no call (the resume contract). A file whose call fails
//	is recorded and the pass continues; the gate decides when the provider has
//	clearly stopped serving and the pass stops spending (the failure gate).
//
//	PASS 2 (curated synthesis, see synthesize.go): summaries are packed into
//	batches of BatchCharBudget and each batch goes to ONE call returning a
//	curated node set — "system" nodes that GROUP files, "file" nodes (rare),
//	and cross-cutting "concept" nodes. Graft forces that shape through a tool
//	call; here the model must answer with a JSON object and the code validates
//	it against closed enums, dropping junk rather than trusting the provider.
//	Nodes merge by slug, links resolve only to defined nodes, an unattributed
//	concept inherits the provenance of what it links to, and the result is
//	written as elements + relationships (so the existing graph verbs traverse
//	the meaning tier) and as markdown node files whose human-written region
//	survives every regeneration (see nodefile.go).
//
// Nothing here runs by itself. The pipeline is reachable from `leankg
// summarize` and — only when LEANKG_SUMMARIZE_AFTER_INDEX names a truthy value
// — from the post-index hook. The lazy-activation contract says an idle engine
// does no work and spends no tokens, so the default is OFF.
package summarize

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/FreePeak/LeanKG/internal/store"
)

// The pipeline's contract-level constants, verbatim from graft.
const (
	// MaxCodeChars caps the code sent for one file (graft summarize.ts:26,
	// "so a single giant file can't blow the context").
	MaxCodeChars = 24_000

	// BatchCharBudget is the summary-text budget of one synthesis call
	// (graft build.ts:51).
	BatchCharBudget = 48_000

	// Concurrency is how many files pass 1 summarizes at once
	// (graft build.ts:189).
	Concurrency = 8
)

// Graft's prompts, ported (summarize.ts:19-24, synthesize.ts:40-54). The
// synthesis prompt trades graft's forced tool call for "respond with ONLY this
// JSON object": the Go port validates the payload instead of relying on a
// schema-enforced tool call, and NodeTypes/RelationVerbs are that schema.
const summarySystemPrompt = `You document source code for a team knowledge base. Given one source file, write a compact plain-English summary covering:
1. The purpose of the file — what it exists to do.
2. The key exported functions/classes/types and what each is for.
3. Important dependencies: internal modules it builds on, external libraries or services it talks to.
4. Notable design decisions, constraints, or gotchas evident in the code.

Write 3-8 sentences of flowing prose. Name concrete identifiers (modules, classes, services) so they can become graph entities. No code blocks, no line-by-line narration, no filler.`

const synthesisSystemPrompt = `You build an ARCHITECTURE graph of a codebase from per-file summaries. The reader is an AI agent that will read this graph before working on the code, so it must describe the system at the level a senior engineer would explain it — not file by file.

Produce a CURATED set of nodes of mixed granularity:
- "system" nodes: GROUP files that collaborate as one component (usually a directory or a cohesive set) into a SINGLE node. This should be the most common node type. Prefer one system node over several file nodes.
- "file" nodes: only for a substantial, standalone module that genuinely deserves its own node apart from its system.
- "concept" nodes: cross-cutting ideas, design decisions, or invariants that span multiple files. Include several — they are the most valuable nodes for an agent.

Rules:
- Every summary must earn its tokens with NON-OBVIOUS information: invariants, ordering constraints, conventions, failure modes, and the WHY behind a design. Never restate what a README says or what a directory listing already makes obvious; an agent reading the node already sees the file paths.
- Strongly prefer FEWER, larger, meaningful nodes. For a repo of N files, aim for well under N nodes. Do NOT emit one node per file, and never a node per incidental identifier (a local interface, helper, or third-party symbol).
- Merge duplicates and surface-form variants into one node.
- For each node give: a canonical human-readable name; a type (one of "system" | "file" | "concept"); a 1-3 sentence summary of its ROLE in the system; "sources" = the exact file paths (from the input) it is grounded in; and "links" to other nodes you define, each with a relation and a short description of what concretely happens on that edge.
- The relation MUST be one of exactly these verbs: part_of, uses, depends_on, produces, configures, validates, implements.
- Only link to nodes you actually define in this response.

Respond with ONLY a JSON object of the form {"nodes":[{"name":"...","type":"...","summary":"...","sources":["..."],"links":[{"to":"...","relation":"...","description":"..."}]}]}. No prose, no code fence.`

// Options configures one run.
type Options struct {
	// ProjectDir is the indexed project root (the directory holding .leankg).
	// The file list comes from the store's index, so a run summarizes exactly
	// what `leankg index` claimed.
	ProjectDir string
	// Force ignores every resume check, content-hash hits included, and
	// re-summarizes each file. This is the only way to regenerate the tier
	// after a prompt change — a content hash cannot detect one.
	Force bool
	// DryRun reads and hashes files and reports what the run WOULD spend,
	// without a single completion or write. It needs no LLM environment, and
	// its batch plan covers only the summaries already on hand.
	DryRun bool
	// NodesDir overrides where the markdown node files live. Default is
	// <ProjectDir>/.leankg/summarize.
	NodesDir string
	// Concurrency overrides how many files pass 1 summarizes at once
	// (default Concurrency). Tests pin it to 1 for deterministic ordering.
	Concurrency int
	// Chat overrides the completion provider. Nil builds one from the
	// LEANKG_LLM_* environment; a DryRun never needs one.
	Chat Chat
}

// Result is one run's report. Where graft's BuildResult carries the same
// meaning, the name matches.
type Result struct {
	Files      int `json:"files"`      // files the pass considered
	Summarized int `json:"summarized"` // completions that produced a summary (dry run: that WOULD)
	Resumed    int `json:"resumed"`    // content-hash hits: unchanged, no call spent
	Failed     int `json:"failed"`     // files whose read or call failed
	Skipped    int `json:"skipped"`    // files never attempted because the gate closed
	Clipped    int `json:"clipped"`    // files whose code was truncated to MaxCodeChars

	Batches      int `json:"batches"`       // synthesis calls the batching implies
	BatchResumed int `json:"batch_resumed"` // calls answered from the batch cache

	Nodes            int    `json:"nodes"`
	Systems          int    `json:"systems"`
	FileNodes        int    `json:"file_nodes"`
	Concepts         int    `json:"concepts"`
	Links            int    `json:"links"`
	NodeFilesWritten int    `json:"node_files_written"`
	DeadNodesRemoved int    `json:"dead_nodes_removed"`
	Warning          string `json:"warning,omitempty"`

	// Fatal is why a pass stopped issuing calls early (graft's gate reason).
	// It means the meaning tier is INCOMPLETE: the caller must exit non-zero
	// rather than report a green run (graft #127).
	Fatal string `json:"fatal,omitempty"`
	// Errors carries the per-file and per-batch failures, sorted for a stable
	// report.
	Errors []string `json:"errors,omitempty"`

	Model  string `json:"model"`
	DryRun bool   `json:"dry_run,omitempty"`
}

// Summary renders the CLI footer for one run.
func (r Result) Summary() string {
	if r.DryRun {
		return fmt.Sprintf("dry run (%s): files=%d resumed=%d would_summarize=%d synthesis_calls=%d — nothing sent, nothing written",
			r.Model, r.Files, r.Resumed, r.Summarized, r.Batches)
	}
	line := fmt.Sprintf("summarize (%s): files=%d summarized=%d resumed=%d failed=%d skipped=%d batches=%d nodes=%d (system=%d file=%d concept=%d) links=%d markdown=%d pruned=%d",
		r.Model, r.Files, r.Summarized, r.Resumed, r.Failed, r.Skipped, r.Batches,
		r.Nodes, r.Systems, r.FileNodes, r.Concepts, r.Links, r.NodeFilesWritten, r.DeadNodesRemoved)
	if r.Fatal != "" {
		line += "\nfatal: " + r.Fatal
	}
	if r.Warning != "" {
		line += "\nwarning: " + r.Warning
	}
	return line
}

// Run executes both passes over an already-indexed project and persists the
// result. Per-file and per-batch provider failures are REPORTED, not returned:
// the error is non-nil only when the pipeline could not do its job at all (no
// provider for a real run, an unreadable store). A completed-but-degraded run
// returns a Result whose Fatal is set.
func Run(ctx context.Context, st store.Backend, opts Options) (Result, error) {
	dir, err := filepath.Abs(opts.ProjectDir)
	if err != nil {
		return Result{}, fmt.Errorf("summarize: project dir: %w", err)
	}
	if opts.Concurrency <= 0 {
		opts.Concurrency = Concurrency
	}
	nodesDir := opts.NodesDir
	if nodesDir == "" {
		nodesDir = filepath.Join(dir, ".leankg", "summarize")
	}
	if !opts.DryRun && opts.Chat == nil {
		cfg, cerr := ConfigFromEnv()
		if cerr != nil {
			return Result{}, cerr
		}
		opts.Chat = cfg.New()
	}

	p := &pipeline{st: st, opts: opts, dir: dir, nodesDir: nodesDir}
	recs, err := st.Files()
	if err != nil {
		return Result{}, fmt.Errorf("summarize: list indexed files: %w", err)
	}
	sort.Slice(recs, func(i, j int) bool { return recs[i].Path < recs[j].Path })

	ws := p.pass1(ctx, recs)
	nodes := p.pass2(ctx, ws)
	if !opts.DryRun && len(nodes) > 0 {
		if err := p.persist(nodes); err != nil {
			return p.report(), err
		}
	}
	return p.report(), nil
}

// pipeline carries the state the two passes share.
type pipeline struct {
	st       store.Backend
	opts     Options
	dir      string
	nodesDir string
	gate     gate

	mu         sync.Mutex
	files      int
	summarized int
	resumed    int
	clipped    int
	batches    int
	batchHit   int
	nodes      int
	systems    int
	fileNodes  int
	concepts   int
	links      int
	nodeFiles  int
	deadNodes  int
	warning    string
	errors     []string
}

// model is the stamped model id. It is "" in a dry run, where no provider is
// needed, and the resume check then ignores the model column.
func (p *pipeline) model() string {
	if p.opts.Chat == nil {
		return ""
	}
	return p.opts.Chat.Model()
}

func (p *pipeline) tally(files, summarized, resumed, clipped int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.files += files
	p.summarized += summarized
	p.resumed += resumed
	p.clipped += clipped
}

func (p *pipeline) addError(msg string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.errors = append(p.errors, msg)
}

// work is one file's pass-1 outcome.
type work struct {
	path    string
	hash    string
	summary string
	resumed bool // served from the checkpoint; no call spent
	dryRun  bool // a real call would have been needed
	clipped bool
}

// pass1 turns indexed files into prose summaries concurrently, resuming from
// the persisted checkpoint.
func (p *pipeline) pass1(ctx context.Context, recs []store.FileRecord) []work {
	ws := make([]work, len(recs))
	sem := make(chan struct{}, p.opts.Concurrency)
	var wg sync.WaitGroup
	for i := range recs {
		if ctx.Err() != nil {
			break
		}
		wg.Add(1)
		sem <- struct{}{}
		go func(i int) {
			defer wg.Done()
			defer func() { <-sem }()
			w, err := p.summarizeFile(ctx, recs[i].Path)
			if err != nil {
				// A store failure is not a per-file miss, but it is reported
				// like one: the file keeps no checkpoint, so the next run
				// retries it, and one bad file never aborts the pass.
				p.addError(err.Error())
				return
			}
			ws[i] = w
		}(i)
	}
	wg.Wait()
	return ws
}

// summarizeFile does one file's pass-1 work: read, hash, resume check, one
// clipped completion, one checkpoint write.
func (p *pipeline) summarizeFile(ctx context.Context, rel string) (work, error) {
	w := work{path: rel}
	b, err := os.ReadFile(filepath.Join(p.dir, filepath.FromSlash(rel)))
	if err != nil {
		p.tally(1, 0, 0, 0)
		p.addError(fmt.Sprintf("%s: %v", rel, err))
		p.gate.record(fmt.Sprintf("%s: %v", rel, err))
		return w, nil
	}
	sum := sha256.Sum256(b)
	w.hash = hex.EncodeToString(sum[:])

	// Resume: the stored summary came from these exact bytes AND this model.
	// A hit is served even after the gate closes — it costs nothing.
	model := p.model()
	if !p.opts.Force {
		got, ok, serr := p.st.SummaryGet(rel)
		if serr != nil {
			return w, fmt.Errorf("summarize: read summary for %s: %w", rel, serr)
		}
		if ok && got.ContentHash == w.hash && got.Summary != "" && (model == "" || got.Model == model) {
			w.summary, w.resumed = got.Summary, true
			p.tally(1, 0, 1, 0)
			return w, nil
		}
	}
	if p.opts.DryRun {
		p.tally(1, 1, 0, 0) // one call would be spent here
		w.dryRun = true
		return w, nil
	}
	if p.gate.stopped() {
		p.gate.stop()
		return w, nil
	}

	code, clipped := clip(string(b), MaxCodeChars)
	w.clipped = clipped
	text, cerr := p.opts.Chat.Complete(ctx, Request{System: summarySystemPrompt, User: "File: " + rel + "\n\n" + code})
	if cerr != nil {
		p.tally(1, 0, 0, b2i(clipped))
		p.gate.record(cerr.Error())
		p.addError(fmt.Sprintf("%s: %v", rel, cerr))
		return w, nil
	}
	text = strings.TrimSpace(text)
	if text == "" {
		// An empty answer is never checkpointed: caching it would make a silent
		// empty summary permanent (graft #177).
		p.tally(1, 0, 0, b2i(clipped))
		p.gate.recordQuality(rel + ": the model answered with an empty summary")
		p.addError(rel + ": empty summary")
		return w, nil
	}

	// The write IS the checkpoint: graft buffers summaries and flushes the
	// cache every 15s (build.ts:170-180); one row per file is strictly more
	// crash-tolerant and needs no timer.
	if serr := p.st.SummaryUpsert(store.FileSummary{
		Path: rel, ContentHash: w.hash, Model: model, Summary: text,
	}); serr != nil {
		return w, fmt.Errorf("summarize: store summary for %s: %w", rel, serr)
	}
	w.summary = text
	p.tally(1, 1, 0, b2i(clipped))
	p.gate.succeeded()
	return w, nil
}

// report folds the pass counters and the gate's verdict into the result.
func (p *pipeline) report() Result {
	p.mu.Lock()
	defer p.mu.Unlock()
	failed, skipped := p.gate.counts()
	sort.Strings(p.errors)
	return Result{
		Files: p.files, Summarized: p.summarized, Resumed: p.resumed,
		Failed: failed, Skipped: skipped, Clipped: p.clipped,
		Batches: p.batches, BatchResumed: p.batchHit,
		Nodes: p.nodes, Systems: p.systems, FileNodes: p.fileNodes, Concepts: p.concepts,
		Links: p.links, NodeFilesWritten: p.nodeFiles, DeadNodesRemoved: p.deadNodes,
		Warning: p.warning,
		Fatal:   p.gate.fatalReason(),
		Errors:  append([]string(nil), p.errors...),
		Model:   p.model(),
		DryRun:  p.opts.DryRun,
	}
}

// b2i renders a bool as 1 or 0 for the tallies.
func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

// clip truncates s to at most n RUNES and reports whether it truncated,
// appending graft's marker (summarize.ts:28-31). Clipping on runes rather than
// bytes keeps a multi-byte character from being split at the cut.
func clip(s string, n int) (string, bool) {
	if len(s) <= n {
		return s, false // a byte length under the cap proves the rune count is too
	}
	r := []rune(s)
	if len(r) <= n {
		return s, false
	}
	return string(r[:n]) + fmt.Sprintf("\n… (truncated at %d characters)", n), true
}
