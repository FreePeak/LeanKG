# Judge Use Cases — Laya / System One judgments in LeanKG

**Status: abstraction LIVE, two call sites LIVE, everything else CANDIDATE.**
`internal/judge` (interface + server + local backends + content-addressed
`Cached`) is implemented and tested. Two judgments exist: conversation
classification (`internal/convo/types.go:ClassifyWithJudge`) and DSH MCP-step
corroboration (`internal/dsusage/laya.go`, UC-9). Every other row below is a
CANDIDATE: a concrete question shape, state, and floor, waiting on a recorded
experiment that shows the native rule failing on real inputs before it
graduates to a call. Native first, judged only where measured.

**Question encoding is part of the question (2026-09-23).** A `Score` must
carry an ordered `Ladder []string`; sending criteria as a map silently
re-labels the levels and answers a different question (measured: 0.1355 vs
0.5377 on identical state). `Question.Validate` rejects the map form. A wide
`choice` is penalized by Laya's entropy confidence — decompose into `Noul`s
(measured: 0.474 wide vs 0.60–0.91 decomposed). See UC-9.

## 1. What Laya is (read from the source, 2026-09-21)

**Repo:** `convaiinnovations/laya` (HF hub, Apache 2.0, `pip install laya`
v0.3.4). Laya is the open-weights replacement for the TypeSafe Jev API: a
**multilingual, non-autoregressive System 1 decision model** — state + typed
questions in, typed answers with calibrated probabilities out, in a **single
forward pass (~33 ms)**, across 100+ languages. Trained with RL against
strictly proper scoring rules (**RLCD**), so honest probabilities are the only
way to maximise reward. **It never generates text: nothing to parse, nothing
to hallucinate.**

| Checkpoint | Backbone | Params | Context | Best at |
|---|---|---|---|---|
| `convaiinnovations/laya` (repo root, the default) | ModernBERT-large | 421M | 512 (`head_max_len` 192) | English text, guardrails, triage |
| `…/laya-multilingual` (subfolder) | mmBERT-base | 322M | 1024 (encoder to 8k) | 100+ languages, ~2.2× faster |
| `…/laya-typed-decisions` (subfolder) | ModernBERT-large | 421M | 1024 | the four typed-decision workflows (0.766 acc) |

**`Router` is the recommended entry point:** detects script/language in
sub-milliseconds of pure Python and dispatches to the optimal checkpoint in
one forward pass — routed accuracy equals the best checkpoint per input
(English 0.783 MASSIVE / 0.860 XNLI; multilingual 0.451 / 0.731 elsewhere;
45/51 languages usable). **Preload for servers** (`Router(preload=True)`):
at the default `max_loaded=1`, alternating languages rebuilds a checkpoint
per request (7.4 s CPU / 10.3 s T4 median); preloaded, language flips cost
detection only (<1 ms) and requests run 32.8 ms GPU / 193–464 ms CPU.

**Wire shape** (`rl_agent_api.py:system_one`, Jev-compatible):
`{state, model, questions:{id: {type, instructions, criteria}}}` →
per question: choice `{choice, probabilities, confidence}`,
score `{score, legend, probabilities, confidence}`,
noul `{noul (= p[1] over [false, true]), confidence}`.
Plus `act_probability` (act/escalate head) and per-(qtype, option-count)
fitted temperatures. Every question in a call answers in **one** forward
pass — batching is free, so independent questions always fan out together.
**`criteria` is type-dependent:** `choice` takes an option **map**
(key → meaning; the keys are the accepted values), `score` takes an ordered
**array** (a map has no order, so the levels would be re-labelled `"0"…"n"`
and the labels never reach the model). Measured cost of getting this wrong:
identical state scored 0.1355 (map) vs 0.5377 (array). Budgets:
`max_len` 512 shares options **and** state; `head_max_len` 192 splits across
options with 48 tokens each — so ~4 options leave roughly 320 tokens (~1.2k
chars) for the state, and option count is what makes confidence collapse.

**Laya vs Jev, honestly** (their benchmark page; Jev figures third-party
published, never measured there): typed-decisions 0.766 vs 0.727 (beats the
0.735 teacher ceiling), AG News 0.950 vs 0.910, DAIR Emotion 0.595 vs 0.480,
ECE 0.081 vs 0.246 (post-temperature), 7.8× faster p50, open weights, $0
self-hosted. **Where Jev still leads:** >20-option choice at default settings
(Banking77 0.870 vs 0.425 — options share the fixed `head_max_len` budget, so
77 labels get ~3–4 tokens each; raise `head_max_len`/`max_len` or split
coarse-to-fine), soft distribution matching, out-of-box raw calibration.
**Honest limits that shape our use:** base checkpoints are near chance on
typed-decisions zero-shot (0.362 vs 0.461 majority baseline) — Laya is a fast
base to specialise, not a zero-shot engine; ordinal `score` is the weakest
primitive (SST-5 0.372); it ships over-confident (refit one temperature per
question type on your own data before trusting probabilities).

## 2. Cookbook patterns we adopt (docs.typesafe.ai, function_calling + patterns)

From the **function_calling cookbook** (trading assistant: NL → 10 typed
functions, `jev-1.12`):

- **Closed sets from signatures:** `Literal` → one `Choice` per argument;
  `list[Literal]` (set) → one **noul per member** (`Does the user want {}…?`);
  `bool` (flag) → one noul; `int`/free-text/dates get **no question** and the
  function's default stands. Whatever reaches the function is a value it
  accepts — no label→argument mapping step (option keys ARE the strings).
- **`spec.json`:** one question per argument (idea, not words — "is amd
  tracking nvidia" matches though *tracking* appears nowhere), one line per
  option, one description per function, one `__tool__` choice over function
  descriptions. `stated` — a second noul asking whether the command says
  anything about that argument at all — is what makes omission safe: without
  it the choice must name *some* window, confidently.
- **One request per command:** the function choice plus every function's
  arguments ride one call; the dispatcher reads only the chosen function's
  answers (54 questions/command in the cookbook).
- **Confidence = the least certain judgment in the call** (never the
  product — one wrong argument spoils the result, and a product falls with
  argument count even when no judgment is shaky). Per-argument distributions
  stay visible; the weakest argument is named.

From the **patterns** pages: **confidence-gated routing** (answer = what,
confidence = whether to act — gate on the second axis), **intent routing**
(classify → deterministic logic / specialist LLM / human), **speculative
fan-out** (send many questions in one call, let code decide relevance),
**composite scoring** (atomic scores, weights in code you control).

Every `internal/judge` call-site rule below is one of these patterns:
keyword/logic first, judge on the uncertain branch, confidence floor decides,
native rule restored with a reason below the floor.

## 3. The abstraction: one interface, two backends

`internal/judge` — `Judge.Ask(ctx, state, questions)` → batched answers.

| | `Server` (provider API) | `Local` (sidecar) |
|---|---|---|
| Talks to | TypeSafe Jev **or any Jev-compatible endpoint, including a self-hosted Laya server** (`rl_agent_api.py` shape) | `LEANKG_JUDGE_SIDECAR_URL` (Laya `laya-serve`, Router preload) |
| Env | `LEANKG_JUDGE_URL` (required, no default) · `_API_KEY` · `_MODEL` (default `jev-1.13.0`, the #433 pin) · `_TIMEOUT_SECS` (default 30 — judgments ride the query path, not the 120 s summarize tier) | `LEANKG_JUDGE_SIDECAR_URL` · `_MODEL` (default `convaiinnovations/laya` English root) |
| Cost | per-call, ~236–276 ms p50 | $0, ~33 ms, in-process-adjacent |
| Select | `FromEnv()`: URL set → Server; else sidecar URL set → Local; else **nil (native rules, zero calls)** | |

**Shared contract, both backends:** malformed questions fail fast (programmer
error); transport failure / non-200 / missing answer is `(nil, nil)` —
**unavailable, never fatal** — and the caller keeps its native rule with a
reason (the L3 provider degrade posture from `internal/core`). Pinned by
`judge_test.go` (batched choice+noul in one HTTP call, auth header, 500 /
unreachable / unconfigured → nil+nil, bad questions → error, nil default).

## 4. Use cases

### UC-3 — Conversation classification — ✅ LIVE (judge path, keyword default)

- **Native today:** `convo.Classify` — prefix labels win, then keyword
  heuristics in Rust's order (problem → decision → milestone → preference),
  else `KindGeneral` (unmined). Labels and specific phrases never needed a
  model; the measured gap is paraphrase ("maybe we try redis next quarter"
  carries no keyword).
- **Question:** one `Choice` over
  `{decision, preference, milestone, problem, general}` with one-line
  criteria; **state:** the lowercased message text (already in memory).
- **Shape:** keyword-first, judge **only on the `KindGeneral` branch**;
  judged label applies at confidence ≥ floor (suggested 0.5), else unmined.
  Non-general keyword verdicts return without a call; nil judge = no call.
- **Why it graduated first:** smallest blast radius (mining-time, off the
  query path), cheapest question (5 short options, well inside
  `head_max_len`), and the uncertain branch is exactly identifiable.
- **Still owed:** a measured gap on real conversation exports
  (keyword-recall vs judged-recall at the floor) to confirm the floor.

### UC-1 — L3 retrieval rerank — CANDIDATE (evidence exists, wiring does not)

- **Native today:** L1 exact → L2 fuzzy → L3 semantic; PG fuses arms with
  fixed `RRFK = 60` (`store/pg_fts.go:FuseRRF`); `FuseRRFWeights` +
  `FuseRRFWeighted` already exist as the weight-receiving seam (from the
  `exp/typesafe-rerank` branch).
- **Evidence:** #433 — 240 live `jev-1.13.0` calls, 8 queries × 30
  candidates: related/unrelated **separation 8/8**, Noul∩BM25 top-5 overlap
  **1.2/5** — complementary, not redundant; Noul a poor standalone ranker
  (the cookbook's BM25-recall + Noul-precision pattern, confirmed).
- **Question (when it graduates):** one `Noul` per candidate —
  *"Does this candidate directly answer: {query}"* with true/false poles —
  fanned out in one call over the fused shortlist; weights/combine in code
  (composite-scoring pattern), confidence floor drops low-confidence
  candidates to their BM25 rank instead of re-weighting them.
- **Why not yet:** per-query latency × shortlist cost on the hot path;
  needs live-repo validation (real elements, not synthetic candidates) plus
  the `FuseRRFWeighted` wiring and a floor fitted on our data (Laya ships
  over-confident — §1).

### UC-2 — Compression mode selection — CANDIDATE

- **Native today:** `compressRead` validates a free-string mode;
  `SelectAdaptive` picks by extension + line count only
  (`compress/modes.go:131`).
- **Question:** one `Choice` over the 8 modes with tradeoff criteria;
  **state:** file facts already in hand (extension, lines, size, diff
  availability) — never file content.
- **Why not yet:** needs an A/B against the extension+lines baseline on real
  reads (tokens-spent vs task-success); the baseline is cheap and often
  right.

### UC-4 — Ontology fallback uncertainty — CANDIDATE

- **Native today:** `ConceptSearch` sets `fallbackUsed = len(matched) == 0`
  (`ontology/query.go:447`) — a boolean from a zero count.
- **Question:** one `Noul` — *"Was this a genuine ontological answer or a
  desperation fallback?"* — so callers get uncertainty instead of a false
  boolean.
- **Why not yet:** cheap to add but the threshold needs calibration against
  downstream trust behavior; zero-match is rarely *wrong*, just coarse.

### UC-5 — Auto-index Fresh/Index boundary — CANDIDATE (weak)

- **Native today:** `indexgate.Decide` — 7-step table, hard cutoff
  `lastCommit <= lastWrite+threshold` (`indexgate.go:104`).
- **Question:** graded choice between `Fresh`/`Index` near the boundary.
- **Why likely never:** the cutoff is rarely wrong in a way that matters
  (a needless reindex is slow but correct); judgment buys little. Kept for
  completeness — challenge before graduating.

### UC-6 — Memory injection ranking — CANDIDATE

- **Native today:** BM25 (Okapi) rank → `RecallLimit=8` cap →
  `InjectionTokenLimit=5000` render (`memory/banks.go`, `scope.go:79-80`).
- **Question:** one `Score` per row over
  `{tangential, somewhat_relevant, core_context, essential}` for
  proportional injection/compression decisions.
- **Why not yet:** the BM25 tier just landed (2026-09-19); Score is Laya's
  weakest primitive (§1) — needs a head-to-head on real banks first.

### UC-7 — Env-conflict severity — CANDIDATE

- **Native today:** exact-map equality (`orgknowledge/conflicts.go:
  metadataEqual/canonicalMeta`).
- **Question:** one `Noul` — *"Is this drift meaningful enough to surface?"*
- **Why not yet:** unmeasured; equality is precise, and "meaningful" needs
  per-project calibration to beat it.

### UC-8 — Mega-graph advisory — CANDIDATE (weak)

- **Native today:** `IsMegaGraph` boolean vs `LEANKG_MAX_CACHE_ELEMENTS`
  (`ontology/discover.go:53`); refusal with hint.
- **Question:** `Noul` softening refusal into graded advisory.
- **Why likely never:** the boolean is a safety rail, not a ranking — coarse
  is correct here.

### UC-9 — DSH MCP-step corroboration — ✅ LIVE (dsh-usage, rules own severity)

- **Native today:** `internal/dsusage/classify.go` — rule classifier over DSH
  session logs (`mcp_session_lost`, `project_not_passed`, `cold_store`, …).
  Rules own severity; this judgment only corroborates.
- **Questions (decomposed, measured 2026-09-23):** three `Noul`
  (`is_defect`, `wrong_project`, `transport_dead`) plus one `Score` ladder
  (`ignore → later → this week → blocking dogfood`), fanned out in **one** call.
- **Measured, 12 real steps from `~/.dsh/sessions`:**

  | | old battery (1 wide choice + map-score) | new battery (3 noul + ladder) |
  |---|---|---|
  | mean confidence | 0.054 | **0.604** |
  | steps above the 0.6 floor | 0/12 | **6/12** |
  | ladder `legend` correct | no (`"0"…"3"` labels) | **yes** |
  | rule agreement | 5/12 | 5/12 |

  Determinism: 5 identical calls on one state returned byte-identical answers,
  so the variance above is signal, not noise — which is also what makes
  `judge.Cached` sound.
- **Two defects this fixed (both were silent):**
  1. **A `Score` sent with a criteria map loses its ladder.** Laya re-labels
     the levels `"0","1","2","3"` and answers a *different* question — the
     same state scored 0.1355 with a map and 0.5377 with the ordered list.
     `judge.Question` now carries `Ladder []string` for `Score`, `Validate`
     rejects a `Score` carrying `Criteria`, and both transports encode the
     ladder as a JSON array (`TestScoreLadderEncodesAsArray`).
  2. **A wide `choice` cannot be gated.** The 5-way verdict answered with
     confidence 0.474 while the same decision as binary questions answered
     0.60–0.91; Laya's confidence is normalized entropy, so it is penalized by
     option count. The battery is decomposed instead.
- **Escalation rule:** a specific, corroborated failure (`transport_dead`
  above floor) escalates on its own, because on a dead-session step that
  question answered 0.644 while the general `is_defect` answered 0.452 —
  requiring general agreement would suppress the one failure we can see.
- **Cost:** ~0.4–0.6 s per step, cached by content hash (`judge.Cached`);
  repeats are free. Live checks are env-gated
  (`LEANKG_TEST_LAYA_URL=http://127.0.0.1:8091 go test ./internal/dsusage/ -run Live`).
- **Known gap (measured, not fixed):** `GET /api/steps` spends ~10 s in
  `ScanRoots` (100 MB / 156 zstd session files) against ~3–5 s of Laya — the
  scan, not the model, is the dashboard's bottleneck. A scan cache or an
  append-only index is the obvious next step.

### Explicit non-goals (do not graduate)

- **Freshness claims** — watermarks belong to writers; a judgment must never
  move them, only report them.
- **Embeddings** — vectors come from the pinned bge-small sidecar
  (`internal/embed`); a decision model does not embed.
- **Text generation** — summarization stays on `summarize.Chat`
  (temperature-0 completions with JSON validation); Laya never generates.
- **NL → query-verb routing** — the L0–L3 ladder already routes
  deterministically; the cookbook's `__tool__` choice answers a problem we
  do not have.

## 5. Graduation checklist (per CANDIDATE → LIVE)

1. Recorded experiment on real inputs: native vs judged (rank overlap /
   recall / task success — the #433 shape, not vibes).
2. Question + state + floor written into §4 with the numbers.
3. Call site: native first, judge on the uncertain branch only, confidence
   floor restores native with a reason; `(nil, nil)` never fails the op.
4. Cost noted: calls × p50 added to the path (query-path sites: Local or
   don't graduate).
5. PRD + tracker updated (FR-TYPE row) in the same PR.

## 6. Diagram

`diagrams/laya-judge.html` — interactive architecture map of this document:
the deterministic backbone (exports → miner → store → L0–L3 ladder → agent),
the judge branch (miner → `internal/judge` → Laya sidecar or Jev provider),
and the explicit non-goals (summarize LLM, bge-small embedder).
Open it in a browser; three guided views isolate each story.
