# Graft (trailhq) — deep-dive, comparison with LeanKG, and learnings

**Date:** 2026-09-08 · **Method:** README (716 lines) + full source clone `trailhq/Graft@05760b0` (27,208 LOC TypeScript, v0.17.0, MIT, 5.9k★) + five subsystem deep-reads (graph core, retrieval, blast, agent surface, LLM layer) + direct line-by-line review of `src/ai/summarize.ts`, `src/ai/synthesize.ts`, `src/context/build.ts`, `src/context/node-file.ts`. All Graft claims are `file:line` at that commit. All LeanKG claims verified against HEAD `8f747d27` (v4.4.1).

---

## 1. What Graft is

An npm CLI (`@nanonets/graft`) + MCP server + agent-hook pack that gives coding agents a **pre-built, always-fresh, greppable model of the repository**. Its thesis, in one sentence: *the context graph should be a committed, regenerable, deterministic artifact that agents can query for $0 — and LLM meaning is an optional layer bolted on top, never a query-time dependency.*

Two cooperating graphs live in `<repo>/graft/` (visible, not dot-prefixed — "default ripgrep skips hidden dirs, so the agent's grep/ls/find reflex must be able to land on the graph", `context/node-file.ts:104-108`):

| Graph | File | Built by | Content |
|---|---|---|---|
| **Wiring graph** (Tier 1) | `graft/.graph/wiring.json` | tree-sitter only — no key, no network | every symbol + `contains/calls/imports/references/implements/extends` edges, line spans, `body_hash`, signatures |
| **Concept graph** (Tier 2, `--deep`) | `graft/*.md` (one markdown file per node) | two LLM passes | curated system/file/concept nodes with prose summaries, typed `[[wikilinks]]`, per-source content hashes |

Retrieval (`ask`/`grep`/`callers`/`skeleton`/`map`/`blast`) is **entirely deterministic and embedding-free** — verified: zero hits for `embedding|vector|cosine|hnsw` in `src/` except comments asserting their absence (`ask/ask.ts:13`). LLMs are confined to build time.

---

## 2. How it works

### 2.1 Tier-1 extraction — deterministic, three fidelity tiers

- **Closed vocabularies** (`graph/types.ts`): 12 `Kind`s; exactly 6 `Relation`s (`contains|calls|imports|references|implements|extends`, `:96-102`); 4 `Confidence`s (`lsp_resolved|lsp_dispatch|extracted|inferred`, `:40` — `lsp_dispatch` is dead vocabulary, never assigned). Enforced by `graph/invariants.ts`.
- **Tiers:** *depth* = hand-written extractors over 8 native grammars (TS/JS, Python, Go, Java, Kotlin, Swift, PHP, R — counted: 8 `tree-sitter-*` imports, `extract.ts:14-22`) with scope-aware cross-file resolution; *container* = `.vue` SFC → embedded script re-parsed with span shift (`graph/container.ts`); *breadth* = 16 languages (Rust, C, C++, C#, Ruby, Scala, Elixir, Solidity, OCaml, Zig, Dart, Clojure, Nix, Lua) via vendored `tags.scm` over WASM grammars (`graph/generic.ts:44-60`; counted: 16 `queries/*.scm`). Java is dual-registered; depth wins (`graph/build.ts:203-207`) — so the README's "23 total" = 8 + 16 − 1.
- **Node identity:** `id = <relpath>#<Qualified.Name>`, mint-time `~2/~3` dedup (`extract.ts:448-452,568-570`); `span` is a line-only string `"Lx-Ly"`; every node carries `body_hash` (sha256 of definition text) and `signature`; file nodes carry a module-level *residual* `body_text` (lines outside any symbol, capped 16 000) so a file stays findable by prose outside symbols (`extract.ts:139,156-168`).
- **Receiver-type resolution** (the README's proudest claim, verified): a separate pre-order bindings pass (`graph/bindings.ts:272-279`) builds `Map<"scopePath|name", typeName>` — Python `self.router = APIRouter()`, TS `new Foo()`/annotations/`constructor(private readonly svc: Svc)` (the NestJS DI idiom, their issue #76), Java params/fields/try-with-resources, Go `:=` with `NewX(...)→X` convention, Swift/PHP equivalents. The resolved `recvType` is **stored inside the cached raw edge** (`extract.ts:97-99`) — which is why the extractor-identity stamp hashes the whole `graph/` directory: a bindings bugfix must invalidate every cached parse (`extract-cache.ts:117-121`).
- **Whole-repo resolution** (`graph/resolve.ts`): per-language import resolvers (Go longest-module-prefix, Java FQN-suffix unique-only, C relative-then-unique-suffix, Rust crate roots); typed member calls walk `classParents` BFS depth ≤3 with arity narrowing; **drop-don't-guess**: a non-unique same-file match is dropped, not first-wins (`:368-374`); global name fallback is gated by *language-family reachability* (`FAMILIES`, `:44-56`) — motivated by a measured disaster: Go's `make()` builtin bound to a TS test helper and collected **1 040 false in-edges** (`:34-42`); name-fallback precision measured 73%→37% (their #35, `:291-295`). Precision numbers pinned in comments are a culture.
- **Serialization:** pretty JSON, nodes/edges sorted (byte-identical output), `body_text` stripped (~65% of bytes), atomic tmp+rename (`graph/write.ts:39-88`).

### 2.2 The freshness engine — Graft's real moat

- **Two-level cache:** a ~10 KB stat-only **fingerprint** (`graft/.cache/fingerprint.<stamp>.json`, `[size, mtimeMs, hash]` positional per file) probes the tree in **~3 ms for 280 files** (`graph/fingerprint.ts:4-8`); the multi-MB **extract memo** gates re-parsing. Content hash is authoritative; size+mtime is a trusted fast path, and `GRAFT_REFRESH=hash` disables the trust. Suspect files are **re-hashed before counting as drift** — a `touch` doesn't trigger re-parse (`:164-180`).
- **Pre-query gate** (`graph/refresh.ts:ensureFreshGraph`): every CLI query and every MCP tool call (except `check`) runs the probe; clean → answer; drift → acquire `.sync.lock` (`wx` create, 5-min stale reclaim, signal-safe release), **re-probe after acquiring** (a queued rebuild may have fixed it), else `graphOnly` rebuild, then answer. Lock wait is bounded (2 s @ 50 ms poll) — after that it **answers stale with a note** rather than blocking (`:50-51,186-195`).
- **Build always re-hashes** even when the probe trusted stats — closes the coarse-mtime "repair deadlock" (`graph/build.ts:213-221`).
- **Git-worktree seeding** (`graph/seed.ts`): a linked worktree with no graph copies the parent checkout's `.graph/` + sidecars by reading `.git`/`commondir` files — zero subprocesses on the query path.
- **`graft check`** (CI): re-extracts Tier 1 and diffs by `id + body_hash` against the committed graph → `added/removed/changed/stale`, exit 1 on drift.
- **Extractor identity in the cache filename** (`extract.<16hex>.json`): two graft versions (npx MCP vs installed hook) keep separate memos instead of cold-invalidating each other (`extract-cache.ts:63-75,157-171`).

### 2.3 Tier-2 LLM meaning layer (the four files reviewed line-by-line)

**Pass 1 — `src/ai/summarize.ts` (51 lines).** One call per file: `temperature 0, maxTokens 2048`; prompt demands 3–8 prose sentences covering purpose / key exports / dependencies / design decisions, and to **"name concrete identifiers … so they can become graph entities"** (`:17-23`). `MAX_CODE_CHARS = 24_000` clip with an explicit truncation marker (`:26-33`). The header states the design bet: LLM entity-extraction over raw code is noisy and duplicative; static structure belongs to tree-sitter; prose is what the pipeline is good at (`:5-11`).

**Pass 2 — `src/ai/synthesize.ts` (172 lines).** Batches of summaries (`BATCH_CHAR_BUDGET = 48_000`, `context/build.ts:51`; `MAX_INPUT_CHARS = 60_000` hard clip) go to **one forced-tool call** (`record_graph`, strict JSON schema `:56-89`). The system prompt (`:40-54`) is the most instructive artifact in the repo:
- mixed granularity, curated: `system` nodes group collaborating files ("should be the most common"), `file` nodes rare, **`concept` nodes for cross-cutting invariants are "the most valuable nodes for an agent"**;
- **"aim for well under N nodes"** for N files; never one-per-file, never per incidental identifier;
- an **anti-filler rule**: "Every summary must earn its tokens with NON-OBVIOUS information … If all you can say about a group of files is what their names say, fold them into a larger node";
- a **closed link-verb enum** (`part_of|uses|depends_on|produces|configures|validates|implements`) each mapped to a reviewer question, with "Never invent vague relations like 'influences' … if none of the verbs fit, **drop the link**" (`:52`).
Model output is treated as untrusted: `clean()` field-by-field validation (`:102-124`), and `nodesFromResponse` recovers the payload from `content` when a gateway ignores `tool_choice` (their #129; `llm/recover-tool.ts` — conservative: only accepts JSON matching the exact envelope shapes, else warns).

**Orchestration — `src/context/build.ts` (467 lines).** Phase 1 summarize (concurrency 8, `:189`) → phase 2 synthesize (sequential batches) → phases 3–5 merge/resolve/write. The discipline is the point:
- **15 s checkpoint flush** of the summary cache during phase 1 + a full flush before phase 2, so an interrupted build never re-pays (`:156-168,231-233`; env seam `GRAFT_SUMMARY_CHECKPOINT_MS`);
- **batch cache keyed by content** — sha256 over sorted `path:content-hash` (`:396-401`): one edited file invalidates its own summary and the batches containing it, nothing else;
- **an empty array is a miss, not a hit** — caching `[]` once made silent empty synthesis permanent (their #177, `:252-254`); stale cache entries for batches no longer produced are dropped so the cache can't grow forever, and failed batches are retried, not frozen (`:266-274`);
- **`LlmFailureGate`** (`ai/failure.ts:23-40`): 5 consecutive failures stop the pass; quota/auth messages (402/401/403 regexes) are *immediately* terminal; content-quality misses count as failed files (so they aren't cached as success) but don't trip the cutoff; the build **exits non-zero with a `fatal` reason** — born from their #127: 1 617 doomed calls behind a spent quota, exit 0, degradation discovered days later as bad `ask` results;
- post-synthesis integrity: links resolve only to defined nodes (`:296-306`); **source-less concept nodes inherit the provenance of the nodes they link to, so they still go stale correctly** (`:308-316`); nodes no longer produced are deleted from disk (`:336-339`); `0 links across >1 batches` emits a "model may be degrading" warning (`:348-352`);
- `manifest.json` = full file→hash map + node roster → O(files) LLM-free drift checking; cache writes atomic tmp+rename (`:417-425`).

**Persistence — `src/context/node-file.ts` (373 lines).** The markdown files **are** the graph: YAML frontmatter is machine-owned (`name/slug/type/sources:[{path,hash}]/sources_digest/links/generator`), body fenced by `<!-- context:generated:start/end -->`; **everything below the end marker (`## Notes`) is preserved verbatim across regeneration** (`writeNode`→`preserveHuman`, `:231-252`). `sources_digest` = sha256 over sorted `path:hash` — per-node staleness is computable without an LLM (`:97-104`). File-card vs concept-node disambiguation handles three historical collision bugs (#215/#261) with heading/slug/covers heuristics (`:274-306`). The `.gitignore` entry is auto-added (graph = regenerable cache like `node_modules`) and is **root-anchored (`/graft/`) on purpose: an unanchored `graft/` also matched `.claude/skills/graft/`, so graft silently dropped the skill file it had just written (#79, `:128-132`)**; a `.ignore` block then **re-admits the tree to ripgrep** (`.ignore` outranks `.gitignore`) while negating `.cache/` and `.graph/` so the multi-MB parse memo doesn't flood every repo search (`ensureSearchable`, `:148-186`). Both writers are idempotent, skip out-of-root dirs, and swallow write failures — "a build that already succeeded must not fail over a convenience file."

**Crux pass — `src/graph/enrich.ts` + `src/ai/crux.ts`.** The wiring graph is its own cache: `summary_state: ready|stale|pending` per node, carried over on `body_hash` match (`:92-106`); crux = LLM-chosen ≤8-line focal span, **stored as code text, not line numbers** ("line numbers drift … the lines that matter do not"), trimmed to `MAX_CRUX_LINES = 12`; 15 s checkpoints so a killed `--deep` keeps paid-for crux (their #128); a plain `build` without a summarizer runs cache bookkeeping only and **never wipes the meaning layer**.

### 2.4 Retrieval — deterministic ranking, zero embeddings

`graft ask` pipeline (`ask/ask.ts:1310+`):
1. **Structural route first:** two English regexes detect "who calls X" / "what does X import" (`:336-337`) → direct edge-walk with flat score 1; neither matches → silent lexical fallthrough. **Query shape is consulted before any scoring.**
2. **Lexical:** camelCase-split tokenizer, ~28-stopword list, **binary query weights** (repeated words count once — "pasted issue bodies can't amplify incidental words", `:495-496`); score = `idf-overlap(name)×3 + idf-overlap(path)×2 + BM25(body, k1=1.2, b=0.75)×1`, `IDF = log(1+N/(1+df))` (`:727-733,222-262`); **test-file de-rank ×0.35, suspended when the query itself asks about tests** (`:200-205,505-508`).
3. **Graph re-rank:** personalized PageRank (α=0.25, 25 power iterations, undirected `WALK_RELATIONS`, dangling-mass pooled once per iteration) seeded with lexical scores; blend `lexN + 0.5·pr`; **rescue floor**: a word-unmatched node enters at `pr ≥ 0.15` of top mass (`graphrank.ts:131-181`; `ask.ts:409-415,900-903`).
4. **Bounded file pooling** (`ask/file-rank.ts:141-246`): per-file anchor + residual complement `r_F = A_F(U_F−A_F)/U_F` — complementary evidence can fill at most the anchor's missing coverage, never amplify; `S_F = max(P_F, baselines)` so pooling can only help; `fileTopLock` keeps the best baseline span #1.
5. **Scope fusion** (`ask/fuse.ts`): intra-repo scopes share repo-wide IDF and normalize by `globalMaxLex` (comparable scores); cross-repo children can't compare scores, so **RRF (k=60) + participation gate (best-in-scope ≥ 0.25× global best) + absolute coverage floors (strong ≥ 0.1 OR broad ≥ 0.5)**. The module header carries a post-mortem: per-scope max-normalization once let a barely-relevant scope's rank-1 tie the relevant scope's rank-1 — "measurement across four repositories and four ecosystems put **73% of missed files in that bucket**" (`:20-27`).
6. Output `AskResult{mode, hits[{kind,title,pointer,snippet,…}], coverage, coverageStrong, scopes, ranking}`; `--source` inlines crux-first else span capped `MAX_SPAN_LINES = 80` with `… (+N more lines; open path:Lx-Ly)`; **a measured `[graft] tokens saved ≈ N (P%)` header** (4 chars/token, rendered as a *header* because "a trailing line dies to `head -N`", `context/savings.ts:139-147`).

Siblings: `grep` = exhaustive per-line regex over indexed files, grouped by **innermost enclosing symbol**, ranked by incoming-edge in-degree ("which hit matters, not just where it is"), 300-hit cap with counted overflow (`search/grep.ts`). `skeleton` = signatures-only projection of one file. `map` = token-budgeted orientation: dir clusters with a 60% split refinement, hubs/hotspots by in-degree — **the same centrality metric everywhere** ("so 'important' means the same thing everywhere in graft", `graph/map.ts:12-14`), caps 16/3/12 ≈ 6 000 chars. A build-time sidecar `ask-index.json` pre-tokenizes the corpus (~45% of query time moved to build time, `graph/build.ts:349-352`).

### 2.5 Blast radius — diff → symbols → people → PR comment

- **Diff:** `git diff --unified=0` post-image ranges; a pure deletion (`+N,0`) is recorded as the **single line before the gap** so "foo() lost 10 lines" still seeds `foo()` (`blast/diff.ts:196-203`); rename-aware `-z` NUL parsing (a rename spends three fields); hunk *text* capped 24/200 lines but ranges never capped ("a regenerated lockfile is one 40k-line hunk; ranges still seed the graph, text must not ride in JSON", `:64-65`).
- **Seeds:** changed line → **innermost containing symbol**, strict containers dropped; the file node is seeded only when no symbol matched — co-seeding would pull every importer, "exactly the noise the command exists to avoid" (`blast/blast.ts:154-178,226-235`).
- **Walk:** one BFS **per changed file** (truthful `from` attribution), direction `in`, `WALK_RELATIONS` only, all seeds pre-visited, first-reach depth wins, cross-file merge at min depth; default depth 2, `--depth all|full|max → Infinity` (`graph/traverse.ts:170-207`; `blast-cli.ts:42-62`).
- **Grouping:** concept-else-directory clusters, `coarsen` to `MAX_AREAS = 5` by merging the deepest group into an existing parent; concept label only under unanimity; dependents inside already-changed files dropped ("it is the diff, not reach", `blast.ts:463-466`).
- **Owners:** `git log` (not blame), per area, 36 months / 400 commits, **exponential recency weight, 120-day half-life**, per-commit dedup ("one commit touching six files is one commit"), bots + PR authors (incl. co-author trailers) excluded, `MIN_SHARE = 0.15` floor computed over remaining people, GitHub handle **only** from `@users.noreply` — no handle ⇒ bolded name, never an `@mention` (`blast/owners.ts`).
- **Naming:** optional `--name` = **one cached LLM call per ≤12 clusters** sending *paths + symbol names only, never bodies*, cache `graft/.cache/areas.json` keyed by `sha256(paths:body_hash)[:16]`, forced tool, `temperature 0`, output sanitized (strip anything that could close a Mermaid label or open a tag — "model output is untrusted: symbol names from a fork's diff reach the prompt"), graceful no-key fallback keeps symbol names (`blast/name.ts`).
- **Render:** PR comment = headline → tests signal (✓⚠✗– with an explicit undercount caveat) → `Tag: @a — areas` line → Mermaid `flowchart TB` (caps 5/6/60/8) → impact table → collapsed per-symbol evidence (≤4 snippets × 6 lines, actual source lines, shared with the hosted page "so they can't disagree") → caveats (deleted/unindexed files named) → "A suggestion from history, not a CODEOWNERS rule."
- **CI split (security-shaped architecture):** `pull_request` job computes read-only with the PR's own code, **no comment**; `pull_request_target` job is the *single* comment writer — it checks out **base** to build the tool and treats the PR strictly as data ("never runs a script from the PR"), publishes the self-contained viz page to gh-pages (race-retried), with `pull_request`-token fork hazards designed out; `push`-to-main job primes the `graft/` cache keyed `graft-<sha>` so PRs restore a warm graph. Every workflow value reaches bash via `env:`, never inline `${{ }}` — "a branch name is attacker-controllable on a fork PR, and inline interpolation is how that becomes command injection" (`.github/actions/graft-blast/action.yml:112-114`).
- **Quality gate:** `scripts/graph-quality.mjs` = Tier-0 metrics needing no oracle: calls-resolution %, orphan-symbol %, kind/origin/relation/confidence distributions, plus **structural invariants** (dup id, bad kind/span/relation/confidence, dangling *source* always a violation; unresolved *targets* whitelisted for `imports|extends|implements|references` as "may be external"). `--strict` gates **invariants only** — no numeric thresholds. (Gap: the script isn't actually wired into their CI.)

### 2.6 Agent surface — why it feels magical in a session

- **MCP:** exactly **6 tools** (`graft_find_code`, `graft_file_api`, `graft_check_freshness`, `graft_trace_calls`, `graft_find_all`, `graft_repo_map` — `mcp/tools.ts:42-131`), each a thin wrapper over the same engine functions the CLI calls; hand-rolled newline-delimited JSON-RPC stdio; **refresh gate before every call except the drift check** (rebuilding first would make `check` always say OK, `:207-209`); **advertisement gating**: a repo with no graph gets `tools: []` because the server is registered at *user* scope and would otherwise charge 6 schemas against every turn of every project (`mcp/server.ts:41-64`); the `initialize` `instructions` string carries the tool-usage prose because measured hosts **defer tool schemas** (111 tools arrived as bare strings once) — plus a one-round-trip `ToolSearch "select:…"` batch-load trick (`mcp/instructions.ts`).
- **Hooks (Claude Code/Codex; Cursor gets scoring-only):** `SessionStart` injects a ≤1 500-byte orientation directive + repo-map slice; `UserPromptSubmit` runs `ask -n 3` as a child (budget = installed timeout − 2 s, floor 4 s) and injects a **gated retrieval pack** — strength floors (same calibrated 0.1/0.5 as federation, "one set of calibrated numbers beats two"), **novelty gate** (per-session 40-pointer memory), **push→pull** (pointers + 140-char snippets, never inlined code, because "per-prompt injected tokens are always fresh full-price input"); `PostToolUse Write|Edit|MultiEdit` prints the edited file's **blast radius inline** (≤8 dependents, silent when none); `PostToolUse Bash|Read|Grep|Glob` accumulates token-savings stats; `Stop` spawns a **detached** `graft build .` under `.sync.lock` (120 s) so the graph self-heals after any code-touching turn. Every path fails soft; hook child timeouts are read back from *installed* settings (upgrade-safe without re-init).
- **Statusline:** pure `stats.json` read — graph size, % enriched, `⚠ N stale`, syncing state.
- **10 hosts wired** via marker-fenced sections (`<!-- graft:start/end -->`) or owned skill files, dominant-EOL detection, byte-identical → no write; **`graft uninstall` is a real retract**: targets derived from the same registries init writes, two invariants — never delete what graft didn't write, never leave what graft wrote behind.
- **SaaS half** (`src/app/*`): a GitHub App (webhook → queue → forked review worker → signed-URL viewer pages) exists *only* to serve PR blast comments + hosted viz for private repos (fork `pull_request` tokens are read-only). Everything else is local.
- **Telemetry:** machine-enforced allowlist of **7 events**, bucketed, fixed labels, "never your code, file paths, repo name, symbols, queries, or error messages"; NDJSON queue + detached daily flush; `graft telemetry debug` prints exactly what your machine would send.

---

## 3. Side-by-side

| Dimension | **Graft** @05760b0 | **LeanKG** @8f747d27 |
|---|---|---|
| Core thesis | committed, greppable, regenerable graph; $0 deterministic retrieval | persistent code-graph + **org-memory substrate** (graph + memory + traceability) over MCP |
| Storage | JSON + markdown files in `<repo>/graft/` (gitignored by default) | PostgreSQL (schema-per-project) **or** SQLite/Cozo per project (`.leankg/leankg.db`); migrations, audit ledger |
| Construction | tree-sitter 3 tiers + optional 2-pass LLM; content-hash caches; sub-second incremental | tree-sitter extractor + framework/Android specialization; batch index takes minutes; `index_hashes` content-hash state |
| Languages | 23 = 8 depth + 16 breadth − java dual-registered, + `.vue` container (all counted from code) | **99** `=> self.extract_*` dispatch arms incl. COBOL/ABAP/RPG/JCL/Lean/Coq/Qiskit (`src/indexer/extractor.rs`, counted) — breadth lead; but Graft publishes a **per-language fidelity contract** (full/broad/opt-in-LSP) LeanKG lacks |
| Edge model | 6 relations × 4 confidences, closed vocab, invariants enforced | richer relation set (calls/imports/extends/implements + framework/event/config/FR-traceability edges) + ontology layer |
| Cross-file resolution | receiver-type bindings, per-language import resolvers, family-reachability gate, measured precision notes | call-graph + import extraction + `resolve_with_lsp` at query time (`src/lsp/bridge.rs`) |
| Retrieval | lexical BM25 + PPR graph-rank + bounded file pooling + scope fusion — **no embeddings** | L0–L3 ladder: pgvector ANN + **cross-encoder rerank** (BGE-small + bge-reranker-v2-m3, `embeddings/mod.rs:8-9`) → trigram/FTS+ontology → exact → cold guidance |
| Semantic search | absent by design | present (opt-in `--features embeddings` + `leankg embed`) — capability lead |
| Impact analysis | **diff → symbol seeds → BFS → areas → owners → PR comment + viz page + GH Action** | `get_impact_radius(file, depth)` from a file/symbol; no diff seeding, no owners, no CI artifact — gap |
| Freshness | **per-query probe (~3 ms) → bounded-wait refresh → answer, uncommitted edits included**; `check` drift gate for CI | push-based watcher (debounce+burst, `mcp/watcher.rs`); `freshness` flags partially wired; FR-ZCP-06 contract still open (#274) — gap |
| Org memory | none | recall JSONL (`session/mod.rs:159`), `knowledge_entries` CRUD, incidents/env-conflicts/service graphs, FR→workflow→code traceability — moat |
| Multi-repo | workspace.json federation, per-scope ranking, RRF + gates; submodule/nested-repo opt-ins persisted | portfolio scope designed (FR-ZCP-09, #277) but registry/federation not built; schema-per-project PG exists |
| Agent surface | MCP 6 tools + hooks (4 events) + statusline + 10-host instruction wiring + retract + skill file | MCP 3 tools (`set`/`get`/`status`, `mcp/tools.rs:13-42`) with ~70 actions; HTTP+stdio, Bearer, RO mode; `install --target` open (#272); no hooks/statusline — gap |
| Cost accounting | measured `tokens saved` header per call; blended real $/Mtok incl. cache multipliers (`context/price.ts`) | none surfaced |
| Quality assurance | invariants + `graph-quality.mjs` (ungated), `check` in CI, fixture-calibrated constants | `doctor --deep`, scale harness, 1 333 lib tests — comparable rigor, different focus |
| Distribution | `npx @nanonets/graft` zero-install; local-only core | single Rust binary; Docker; PG or embedded SQLite |
| Privacy | structural graph fully offline; LLM via user's own key; 7-event bucketed telemetry | fully local/self-hosted; no remote embedding; loopback HTTP default — stronger posture |
| Size | 27.2k LOC TS | 135.2k LOC Rust (~5×) |

---

## 4. What LeanKG can learn (ranked)

**L1 — Freshness as a query-path guarantee, not a background hope.** Graft's probe→refresh→answer loop makes every answer describe the working tree *including uncommitted edits*, with a bounded 2 s wait and honest stale-notes (`graph/refresh.ts`). LeanKG's watcher is push-based, single-project-per-process, and the freshness *contract* is still an open P1 (#274). Design: a per-project fingerprint sidecar (`.leankg/cache/fingerprint.json`, `[path,size,mtime,hash]`), probed at the top of `get` dispatch; on drift, serve `freshness: possibly_stale` immediately and kick a coalesced background reconcile (the machinery exists: watcher + `index_hashes`); expose `graft check`-equivalent as a `status` action with exit-code semantics for CI. **Maps: #274 (strengthen AC), new subtask under FR-ZCP-02.**

**L2 — Query-shape routing must precede capability-rung selection.** Graft consults the structural regex route *before* any scoring (`ask.ts:336-403`). LeanKG's `route()` probes capabilities first and the L3 vector arm never consults the Lexical intent (`mcp/router.rs:486-521`) — the exact root cause of **#290** (`create_hnsw_index` → vector rung → `vis-network.min.js::Hx`). Fix is small: in `route()`, when `intent == Lexical`, run `route_lexical` first and fall through to the rung ladder only on zero hits. **Maps: #290 (confirmed root cause + graft precedent).**

**L3 — Blast radius from the diff, with owners and a PR artifact.** LeanKG answers "what breaks if I change X" from a file/symbol; Graft answers "what does *this change* reach" — hunk→innermost-symbol seeding (incl. the pure-deletion single-line trick), per-file BFS with `from` attribution, concept/dir coarsening to ≤5 areas, recency-weighted `git log` owners with bot/author exclusion and noreply-handle-only mentions, and a CI-safe three-workflow comment pipeline. This is the single highest-value *product* gap and it rides LeanKG's existing edge data. **Maps: new FR (propose FR-ZCP-14); no existing issue covers diff-based blast + owners.**

**L4 — Make the graph an inspectable, greppable artifact.** Graft's `graft/` dir is visible-on-purpose so an agent's grep/ls reflexes land on it, and `.gitignore` + `.ignore` split makes it gitignored-but-searchable (`node-file.ts:104-108,182-184`). LeanKG's knowledge is invisible to file tools (PG/Cozo). A `leankg export --markdown` (concept nodes + wiring cards + INDEX.md, content-hash frontmatter, human-notes preservation) would feed FR-ZCP-12's inspectability and give agents a zero-MCP fallback path. **Maps: FR-ZCP-12 (#280) + #297 item 5.**

**L5 — The LLM-meaning pipeline discipline** (the four reviewed files): curated concept synthesis with a closed verb enum + forced-tool schema + output validation; content-hash resume with 15 s checkpoints; empty-is-miss cache hygiene; `LlmFailureGate` (5-consecutive / terminal-quota-auth, non-zero exit); provenance inheritance for source-less concepts; 0-links degradation warning. Filed as **#297** (high priority).

**L6 — Graph-centrality re-ranking in the cheap rungs.** Graft blends lexical scores with personalized PageRank (α 0.25, 25 iters, `contains` excluded so files can't be false hubs, dangling mass pooled once). LeanKG's L2/L1 rungs rank by trigram/ILIKE + ontology only; a PPR pass over the existing `calls/references/imports/implements/extends` subset would reorder near-ties by structural importance — pure Rust over data already in the schema. Also steal the **rescue floor** (structurally-central node with zero word overlap enters at pr ≥ 0.15) — it's the mechanism that answers "what does auth touch" when the word "auth" isn't in the symbol. **Maps: #273 (FTS/RRF remainder) — add a graph-rank signal to the fusion.**

**L7 — Test-symbol de-rank.** Graft multiplies test-path scores by 0.35 *unless the query itself asks about tests* (`ask.ts:200-205,505-508`). LeanKG's **#293** (test symbols outranking the real handler) is the same failure; a path-pattern penalty in `search_code`/L2 fusion is a one-function fix with the query-aware escape clause. **Maps: #293.**

**L8 — Vendor/minified exclusion at ingest.** Graft skips files >1 MB as generated/vendored (`ingest/fs.ts:27`). LeanKG indexed `vis-network.min.js` into the ANN space and it now wins semantic queries (**#291**); a size cap + minified heuristics (entropy / no-newlines / `.min.` name) at index time, plus a purge path in `doctor`, closes it. **Maps: #291.**

**L9 — Measured token-savings accounting.** Every Graft output carries a `[graft] tokens saved ≈ N (P%)` **header** (survives `head -N`), computed against stored whole-file `chars` at 4 chars/token, with a blended real-$ rate including prompt-cache multipliers, and `<$0.01` never rendered as `$0.00`. LeanKG's `compress/` does the work but never *shows* the win — this is adoption copy, and it's cheap. **Maps: FR-ZCP-12 T2 (#280).**

**L10 — Hooks, not just MCP config, in `install --target`.** Graft's SessionStart orientation / UserPromptSubmit retrieval pack / PostToolUse blast / Stop rebuild + statusline is what makes the graph *used* rather than merely available; the novelty gate and push→pull pack discipline are the noise-control lessons. LeanKG's #272 currently plans config writers only. **Maps: #272 (extend scope).**

**L11 — Scope-fusion fairness math for the portfolio.** Graft already ran the experiment LeanKG is about to run (FR-ZCP-09/#277): per-scope max-normalization + RRF produced rank ties between irrelevant and relevant scopes — 73% of missed files (their #117 post-mortem, `fuse.ts:20-27`); the fix is shared-corpus normalization where possible, participation gate (0.25), absolute coverage floors (0.1/0.5), and RRF only across incomparable corpora. Adopt the math, skip the dual-stream RRF lock (their own code calls it the most fragile in the path). **Maps: #277 design input.**

**L12 — Structural invariants as a cheap CI gate.** Dup-id / bad-span / dangling-source checks cost nothing and catch indexer regressions that unit tests miss; LeanKG's `doctor` could run the same invariant set against any project schema (both backends) and `--strict` it in CI. (Graft's own miss: they wrote the script but never wired it into CI — do the wiring.) **Maps: #278 (doctor --deep fleet reconciliation) extension.**

**L13 — Failure gates for long jobs.** `LlmFailureGate` generalizes beyond LLM: LeanKG's embed/index loops should stop on 5 consecutive provider/DB failures with a terminal reason and non-zero exit — directly relevant to **#286** (silent mcp-http death during concurrent embed) and the embed worker isolation in #297. **Maps: #286, #297.**

**L14 — Advertisement gating / schema-cost awareness.** Graft returns `tools: []` for unindexed repos because user-scope registration charges schemas to every unrelated project's context; and it puts usage prose in `initialize.instructions` because hosts defer tool schemas. LeanKG's RO mode hides `set` already; the instructions-field prose + "cold repo advertises less" idea is worth a look for the 3-tool surface. **Maps: FR-ZCP-03 polish.**

---

## 5. What LeanKG already does better (anti-cargo-cult)

1. **Semantic retrieval**: pgvector ANN + cross-encoder rerank — Graft has *no* embeddings at all; its bag-of-words + PPR cannot answer "where do we validate access rights" when the vocabulary differs. The L3 rung is a real moat (keep FR-ZCP-11's model-stamp rigor).
2. **Language breadth**: 99 dispatch arms vs 23 — including enterprise (COBOL/ABAP/RPG/JCL) and formal (Lean/Coq) that Graft skips entirely. (Graft's counter-lesson: its *tiered fidelity contract* per language is the publishable artifact, not the count.)
3. **Org memory & traceability**: incidents, env conflicts, service graphs, FR→workflow→code ontology, recall/knowledge — Graft has zero cross-session memory.
4. **Server-grade multi-project**: schema-per-project PG, per-connection routing, Bearer + DB token store, audit ledger, HTTP+stdio, fleet doctor — Graft is one-repo-per-checkout (workspace federation is file-merge, not a server).
5. **Query-time LSP** (`resolve_with_lsp`) vs Graft's build-time `--lsp` whose edges are **silently dropped on every auto-refresh** (never persisted to the extract cache; `refresh.ts:204` rebuilds without `lsp`) — a real Graft bug; don't copy the plumbing, keep the confidence vocabulary.
6. **Dual embedded backend** (Cozo/SQLite) — Graft has no queryable store beyond what its code walks in JSON.

## 6. Graft weaknesses — do not import

- LSP edges transient (above); `references` edges exist only for TS/PHP named imports — Python/Go/Java identifier uses emit none; generic tier marks every def `exported: true` ("a lie that any exported-filtering consumer inherits", `generic.ts:238`).
- Query understanding is bag-of-words: no phrase, no stemming (naive ±"s"), structural intent = exactly two English regexes; structural hits get flat score 1.
- Magic constants (3/2/1, 0.5, 0.35, 0.15, 0.25, 0.1/0.5, 60) are fixture-calibrated by hand — brittle across domains; LeanKG should prefer learned/tuned signals where it can.
- `graph-quality.mjs` duplicates `invariants.ts` (drift risk) and is not CI-wired; `blast.ts` header contradicts its own code on file-node seeding; no signature-vs-body change distinction in blast.
- Exported blast pages embed verbatim source snippets with no private-repo redaction check.
- 13.9 s MCP tool-arrival gap self-documented as "cause still unknown".

## 7. Issue mapping

| Learning | Existing issue | Action |
|---|---|---|
| L5 LLM-meaning pipeline (the 4 files) | **#297** (new, `enhancement` + `priority:high`) | filed 2026-09-08 |
| L2 intent-before-rung | #290 | confirmed root cause `router.rs:486-521`; graft precedent strengthens fix |
| L7 test de-rank | #293 | graft's ×0.35 + query-aware escape = reference design |
| L8 vendor exclusion | #291 | graft's 1 MB cap + purge path |
| L1 freshness guarantee | #274, #286 | probe/refresh/lock design |
| L10 hooks in install | #272 | extend scope |
| L11 scope fusion | #277 | adopt gates/normalization math |
| L12 invariants CI | #278 | extend doctor --deep |
| L3 diff-blast + owners | — | propose FR-ZCP-14 (not yet filed; awaiting owner decision) |
| L6 PPR rung, L4 markdown export, L9 savings header, L13 gate, L14 ad-gating | #273, #280, #297 | noted in respective issues/bodies |

---

*Prepared by direct source review (clone @ `05760b0`) + five parallel subsystem deep-reads; LeanKG anchors re-verified at HEAD `8f747d27`. Supersedes nothing; linked from `docs/prd.md` §1.1 and §7.*
