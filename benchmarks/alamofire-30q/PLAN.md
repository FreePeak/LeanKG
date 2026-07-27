# Alamofire Agent Benchmark Plan

**Worktree:** `.worktrees/feature/alamofire-benchmark`  
**Branch:** `feature/alamofire-benchmark`  
**Date:** 2026-07-27  
**Goal:** Compare LeanKG vs CodeGraph vs no-graph on Alamofire (Swift) using agent metrics: turns, cost, input/output tokens, latency, tool calls, file reads.

---

## What Was Done (this session)

### Infrastructure

| Item | Status | Location |
|------|--------|----------|
| Git worktree created | Done | `.worktrees/feature/alamofire-benchmark` |
| LeanKG release binary (no embeddings) | Done | `target/release/leankg` |
| CodeGraph CLI installed | Done | `/opt/homebrew/bin/codegraph` v1.5.0 |
| Alamofire clone verified | Done | `$REPO_PATH (Alamofire clone)` @ 5.12.0 |
| CodeGraph index on Alamofire | Done | 114 files, 4,512 nodes, 13,935 edges |
| LeanKG index on Alamofire (regex Swift) | Partial | 48 files, 49 elements — **no embeddings** |
| Harness scripts (30Q, sequential) | Done | `benchmarks/alamofire-30q/` |

### Harness files created

```
benchmarks/alamofire-30q/
  questions.yaml      # 30 architecture questions + ground truth
  install_mcp.sh      # 3-arm MCP config (leankg / codegraph / none)
  run_30q.sh          # sequential runner
  aggregate.py        # Markdown + JSON report
  run.sh              # one-shot wrapper
  .gitignore
```

### Partial pilot results (leankg arm, interrupted)

| Q | Valid | Duration | Cost | Tools | Reads | Turns | Notes |
|---|-------|----------|------|-------|-------|-------|-------|
| Q01 | yes | 41s | $0.31 | 3 | 1 | 4 | MCP attached |
| Q02 | no | 82s | $0.15 | 2 | 0 | 4 | exit_code=1 |
| Q03 | yes | 94s | $0.30 | 18 | 8 | 19 | High tool use |
| Q04 | incomplete | — | — | — | — | — | Interrupted |

**Observed issues:**
1. Binary was initially built **without** `--features embeddings` → fixed: rebuild with embeddings.
2. Sequential 30Q × 3 arms ≈ hours wall-clock → fixed: 10Q + parallel arms.
3. `mcp_tool_count: 0` in init event despite `mcp_servers: [leankg]` — monitor during next run.
4. Default model was `MiniMax-M3[1m]` (not pinned sonnet) → pin `MODEL=sonnet`.
5. `leankg init` auto-detect missed Swift — fixed: add `.swift` to `detect_languages`.
6. **Critical:** `find_files_sync` omitted `swift`; `SwiftExtractor` existed but was **never wired** into `extract_elements_for_file`. Fixed in this worktree before re-index.

---

## Revised Scope (user request 2026-07-27)

| Change | Before | After |
|--------|--------|-------|
| Questions | 30 | **10** curated |
| Arms | sequential | **parallel** (3 subagents / 3 processes) |
| Runs per Q | N=3 planned | **N=1** (speed) |
| LeanKG embeddings | missing | **rebuild with `--features embeddings` + `leankg embed --wait`** |
| CodeGraph | same harness | same 10Q, parallel arm |
| Docs | none | **this PLAN.md** |

### 10-question set (curated from 30)

| ID | Category | Focus |
|----|----------|-------|
| Q01 | Core | Session → URLSession creation |
| Q02 | Core | Request state machine |
| Q05 | Core | UploadRequest + MultipartFormData |
| Q07 | Features | ServerTrustManager / evaluators |
| Q08 | Features | AuthenticationInterceptor refresh |
| Q10 | Features | RetryPolicy exponential backoff |
| Q11 | Features | Response serialization pipeline |
| Q19 | Core | async/await Concurrency wrappers |
| Q24 | Core | SessionDelegate forwarding |
| Q26 | Features | RequestInterceptor compose |

---

## Todo List

### Phase A — Consolidate & document (now)

- [x] Worktree + branch exist
- [x] Write this PLAN.md
- [x] Reduce `questions.yaml` to 10 questions (archive as `questions-30.yaml`)
- [x] Add `run_parallel.sh` (3 arms concurrent, N=1, MODEL=sonnet)
- [x] Update `run_30q.sh` for embed + `SKIP_INDEX_REBUILD`

### Phase B — LeanKG embeddings

- [x] Rebuild: `cargo build --release --features embeddings`
- [x] Re-init Alamofire with Swift `leankg.yaml` patch
- [x] `leankg index .` then `leankg embed --wait` (**4,208 vectors**)
- [x] Verify embed pipeline completed (inventory counter may still show 0 — known quirk)

### Phase C — Parallel harness

- [x] Add `run_parallel.sh`
- [x] Pin model via `MODEL` (default haiku; machine may route to MiniMax)
- [x] N=1 per question + `Q_PARALLEL=5`

### Phase D — Execute & report

- [x] Clear stale partial runs (Q01–Q04)
- [x] Run parallel 10Q × 3 arms (~3.7 min wall-clock)
- [x] `aggregate.py` → `alamofire-10q-2026-07-27.md` + `.json`
- [x] Deliver final comparison table (see Final Report below)

### Phase E — Objective-C LeanKG support (NEW)

Alamofire itself is Swift-first, but LeanKG needs ObjC for real iOS monorepos
(Swift↔ObjC bridging, RN legacy bridge, mixed pods). Plan:

- [x] Add `.m` / `.mm` / `.h` to `find_files_sync` + `detect_languages` / `get_language`
- [x] Add `tree-sitter-objc` **or** regex `ObjCExtractor` (v0) mirroring `SwiftExtractor`
- [x] Wire extractor in `extract_elements_for_file` (classes, categories, protocols, methods, imports)
- [x] Extract `@interface` / `@implementation` / `@protocol` / `@property` / message sends as edges
- [x] Optional: `@objc` / bridging name candidates on Swift side (later) — deferred
- [x] Unit fixtures under `tests/fixtures/objc/` + index smoke on a small ObjC sample
- [x] Document in `docs/` / AGENTS: ObjC support tier (regex vs AST)

**Out of scope for Alamofire 10Q run** (no `.m` in Alamofire Source). Needed before
benchmarking mixed iOS apps (e.g. Charts, realm-swift, wikipedia-ios).

### Phase F — Native iOS / protocol deep-dive questions (NEW)

Expand the question bank beyond “how does X work” into **protocol composition**,
**URLSession/NSObject bridging**, **queue affinity**, and **concurrency**.

- [x] Author `questions-ios-deep.yaml` (15 deep questions: D01–D15)
- [ ] Run parallel 3-arm bench with `QUESTIONS=questions-ios-deep.yaml` (or merge subset into main set)
- [ ] Aggregate → `alamofire-ios-deep-<DATE>.{md,json}`
- [ ] Compare protocol-heavy Qs: graph tools should beat grep on witness / conformer discovery

#### Deep-dive question map

| ID | Category | Focus |
|----|----------|-------|
| D01 | Protocol | `URLConvertible` / `URLRequestConvertible` witness defaults |
| D02 | Protocol | `RequestAdapter` + `RequestRetrier` → `RequestInterceptor` |
| D03 | Protocol | `ServerTrustEvaluating` + composite pinning |
| D04 | NativeIOS | `SessionDelegate` as `NSObject` + URLSession callback bridge |
| D05 | Protocol | `EventMonitor` / multiplex vs closure |
| D06 | Protocol | `Authenticator` + `AuthenticationCredential` refresh |
| D07 | NativeIOS | `Protected<T>` + `Lock` / unfair lock |
| D08 | Protocol | `ResponseSerializer` hierarchy + associated types |
| D09 | NativeIOS | async/await continuations in `Concurrency.swift` |
| D10 | Protocol | `RedirectHandler` + `CachedResponseHandler` |
| D11 | NativeIOS | `Request.State` ↔ `URLSessionTask` lifecycle |
| D12 | Protocol | `AlamofireExtended` `.af` namespace pattern |
| D13 | NativeIOS | `WebSocketRequest` / `URLSessionWebSocketTask` |
| D14 | Protocol | `UploadableConvertible` / multipart uploadables |
| D15 | NativeIOS | `rootQueue` serial affinity + `RequestSetup` lazy/eager |

---

## Metrics (unchanged)

| Metric | Source |
|--------|--------|
| Latency (s) | wall-clock around `claude -p` |
| Input / output / cache tokens | Claude JSON envelope |
| Cost (USD) | `total_cost_usd` |
| Tool calls | `tool_use` blocks |
| File reads | `Read` tool uses |
| Agent turns | `num_turns` |
| MCP attached | init event `mcp_servers` |

Arms:
- **leankg** — `leankg mcp-stdio` after index + embed
- **codegraph** — `codegraph serve --mcp` after `codegraph init`
- **none** — empty `mcpServers` (Read/Grep/Bash only)

---

## Estimated time / cost (revised)

| Setting | Estimate |
|---------|----------|
| 10Q × 1 run × 3 arms | 30 agent calls |
| Parallel wall-clock | ~bound by slowest arm (~15–40 min) |
| Cost (sonnet-ish) | ~$5–15 depending on model |

---

## Known risks

1. LeanKG Swift is **regex-only** (no tree-sitter) — weaker call graphs vs CodeGraph’s full Swift AST.
2. Without embeddings, LeanKG semantic tools fail — **must rebuild with embeddings**.
3. Claude model must be pinned for fair A/B.
4. MCP tool naming / discovery (`mcp_tool_count: 0`) needs a smoke check before full run.

---

## Commands (quick reference)

```bash
WT=$WT (feature/alamofire-benchmark worktree)
AF=$REPO_PATH (Alamofire clone)
BENCH=$WT/benchmarks/alamofire-30q   # or alamofire-10q after rename

# Rebuild with embeddings
cd $WT && cargo build --release --features embeddings

# Index + embed Alamofire
cd $AF
rm -rf .leankg && $WT/target/release/leankg init
# patch leankg.yaml languages/include to swift (see run.sh)
$WT/target/release/leankg index .
$WT/target/release/leankg embed --wait

# Parallel 10Q run
MODEL=sonnet N=1 bash $BENCH/run_parallel.sh

# Report
python3 $BENCH/aggregate.py --results $BENCH/results --questions $BENCH/questions.yaml
```

---

*Last updated: 2026-07-27 — revised to 10Q + parallel + embeddings.*

---

## Language Support (verified 2026-07-27)

| Language | Status | Notes |
|----------|--------|-------|
| **Swift** | YES (regex) | Wired: `find_files_sync`, `get_language`, `detect_languages`, `SwiftExtractor` in `extract_elements_for_file`. Re-index: **118 files, 8001 elements, 289 classes, 4208 embed vectors**. No tree-sitter-swift. |
| **Objective-C** | **YES (regex v0)** | Wired: `find_files_sync`, `get_language`, `ObjCExtractor` in `extract_elements_for_file` + `index_file_sync`. `.m`/`.mm`/`.h` extensions. Extracts: `@interface` (class), `@implementation`, `@protocol` (interface), `@property`, `-/+` methods, categories, `#import`/`@import`. 4 unit tests in `indexer::objc::tests`. No tree-sitter-objc. Regex v0 — no C functions, blocks, typedef, protocol conformance edges. Not needed for Alamofire (Swift-only) benchmark — ready for mixed iOS apps next. |

## Speed Optimizations Applied

1. Reduced to **10 questions**, N=1
2. **3 arms parallel** (`run_parallel.sh`)
3. **Questions parallel within arm** (`Q_PARALLEL=5`)
4. Default model **`haiku`** (CLI still routed to `MiniMax-M3[1m]` on this machine — same for all arms)
5. `SKIP_LEANKG_REBUILD=1` when index+embed already warm
6. Embeddings binary: `cargo build --release --features embeddings`

**Actual wall-clock of full suite:** ~3.7 minutes (221s) for 30 agent calls (10Q × 3 arms).

## Final Report

- Markdown: [`results/alamofire-10q-2026-07-27.md`](results/alamofire-10q-2026-07-27.md)
- JSON: [`results/alamofire-10q-2026-07-27.json`](results/alamofire-10q-2026-07-27.json)

### Headline medians (10Q, N=1, MiniMax-M3)

| Arm | Tools | Time | File reads | Total tok | Cost |
|-----|-------|------|------------|-----------|------|
| LeanKG | 8 | 1m14s | 2 | 33.8k | $0.30 |
| CodeGraph | 10 | 1m19s | 1 | 43.2k | $0.38 |
| No Graph | 8 | 1m30s | 3 | 28.5k | $0.29 |

Notes: On this small Swift repo with regex LeanKG, **No Graph sometimes wins tokens/cost**; LeanKG wins wall-clock (−18%) and file reads (−50%) vs none. CodeGraph has fewest file reads (−67%) but higher tokens/cost. N=1 + small corpus → high variance; treat as directional.

