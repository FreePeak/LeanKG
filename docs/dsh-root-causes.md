# DSH × LeanKG — root causes and solutions

**Date:** 2026-09-22 (updated 2026-09-23)
**Scope:** why DSH sessions look like they never use LeanKG, what is fixed in this branch, and what still needs a product/process change.

## Optional feature (default off)

`leankg dsh-usage` is **dogfood tooling**, not part of the default binary surface:

| Layer | Default | How to enable |
|---|---|---|
| CLI command | **off** (stub) | rebuild with `-tags dshusage` / `make go-build-dshusage` |
| Laya scoring | **off** (nil judge) | set `LAYA_URL` or `LEANKG_JUDGE_SIDECAR_URL`, or pass `--laya-url` |
| Dashboard `?laya=1` | no-op without a configured judge | same env/flag |
| MCP fail-closed `project` + `Stateless` HTTP | **always on** (product correctness) | n/a — ships in default builds |

```bash
make go-build-dshusage
./bin/leankg dsh-usage --addr 127.0.0.1:9710
./bin/leankg dsh-usage --watch --laya-url http://127.0.0.1:8091
```

Laya itself is a **shared machine service** (container on `:8091`), not a LeanKG process — see [`laya-shared-service.md`](laya-shared-service.md).

## Short answer

DSH **does** call LeanKG. The April `posttooluse.log` is the wrong meter. Real usage is rare (~9 of 152 sessions, 28 LeanKG tool calls) and many of those calls fail for routing/session bugs. Agents still default to bash/read/grep because DSH has no session-start LeanKG nudge.

## Root causes (ranked)

| # | Cause | Evidence | Severity | Solution status |
|---|---|---|---|---|
| 1 | **Wrong meter** | `~/.leankg/sessions/posttooluse.log` is Claude plugin only (Rust-era tool names). DSH never writes it. | info | **Solved** — read `~/.dsh/sessions/**/session.v{3,4}.jsonl[.zstd]` via `leankg dsh-usage` (opt-in `-tags dshusage`); v4 preferred per session |
| 2 | **No DSH LeanKG nudge** | Claude/Cursor inject hooks; DSH only has passive `~/.dsh/AGENTS.md`. bash 7642 / read 2271 / grep 401 vs 28 LeanKG calls | high | **Partial** — `~/.dsh/AGENTS.md` now has a LeanKG-first hard rule + dashboard hook text; full DSH SessionStart plugin still out of scope (harness repo) |
| 3 | **Omitted `project=` searched serve cwd** | Multi-project MCP defaulted to LeanKG process cwd. Menu/promo questions returned LeanKG source. 7/28 steps: `project_not_passed` | critical | **Fixed in branch + live** — `engineFor` fails closed (`LEANKG_ERROR_MISSING_PARAM`); verified against running serve. Old logs still classify as `project_not_passed`; new errors classify as `project_required` |
| 4 | **Sticky MCP session dies on restart** | DSH keeps `Mcp-Session-Id`; launchd restart / per-project handler maps → `session not found` (7/28) | critical | **Fixed + live** — `Stateless: true` built into `~/.local/bin/leankg` (backup `~/.local/bin/leankg.bak-20260923`); `com.freepeak.leankg-serve` restarted 2026-09-23; `initialize` smoke OK |
| 5 | **Cold / unindexed project** | Query before import → cold guidance. 3/28 | high | **Operational** — import once with absolute path + project basename |
| 6 | **Ambiguous project basename** | e.g. `deepseek-harness` matches multiple registered paths | high | **Classifier flags it**. Use absolute path or unique names |
| 7 | **Cursor unwired to local LeanKG** | `~/.cursor/mcp.json` has remote BE KG, not `http://127.0.0.1:9699/mcp` | high | **Not solved** — user config change |
| 8 | **Writer only watches LeanKG checkout** | Other repos go stale unless imported | medium | **Operational / product** — per-project writer or on-demand import |
| 9 | **Laya is a classifier, not chat** | `:9101` is bge-small embeddings; Laya = ModernBERT System One | info | **Solved for scoring** — local sidecar `:8091` scores steps |
| 10 | **Semantic (L3) search is effectively unused** | Root-caused below: the L3 read path is healthy, but per-project vectors were never built (index/import/writer never embed), and REST auto-embed was dead. | medium | **Root-caused + auto-embed fixed** (`064f095`); remaining fix is operational: `leankg-embed run` per project (verified: commitgen/onegw now serve L3) |

## Root cause: why L3 semantic is unused (investigated 2026-09-23)

`semantic_never_consulted` was investigated end to end (own probes + three subagent tracks over the live corpus and the L3 code) and the L3 machinery is **not broken**. L3 serves correctly the moment a project has vectors. Confirmed live on `:9699` for `leankg`: forced `action=semantic` and the ordinary ladder both return `{"rung":"L3","reason":"vector similarity (cosine)"}`. The stamp the live server selects (`local` / `local:local` / 384 / cosine / chunker 2 / no prefixes) matches the stored collection exactly — **no stamp mismatch**.

**A "no L3" rung mix is not proof L3 was unavailable.** Over the 116 query steps that carried a retrieval block, the causes are:

| cause | n | share |
|---|---:|---:|
| Router answered at L1/L2 first (a keyword or exact hit short-circuits the ladder) | 55 | 47% |
| Agent pinned a low rung (`action=exact`/`fuzzy`, which bypass L3) | 49 | 42% |
| **Missing vectors** — L3 attempted and degraded (`no vector collection for model local; degraded from L3`) | 7 | 6% |
| L3 actually served | 5 | 4% |

So building vectors raises the ceiling for genuinely semantic questions, but on their own does **not** make L3 the common path — ~89% of no-L3 answers are the ladder doing its job or an agent pinning a rung. `action=search` is a router (same as empty); only `exact`/`element`, `fuzzy`, and `semantic` pin (`internal/core/core.go:466,485-488`).

The dashboard now reports the distinction rather than one misleading bucket: `semantic_degraded_no_vectors` (medium — L3 attempted and broke, actionable) vs `semantic_never_consulted` (info — L1/L2 answered first or a low rung was pinned). On the live corpus that is 3 real no-vector projects vs 24 routing notes.

The infrastructure layer found and fixed:

| Layer | Finding | Status |
|---|---|---|
| **REST auto-embed** | `handleAutoConfig` passed `r.Context()` to a background goroutine and returned; net/http cancels the request context at return, so the 120s debounce never elapsed and auto-embed **silently did nothing** (200 OK). | **fixed** (`064f095`) — server-lifetime ctx threaded through; regression test drives a real server and fails on the old wiring |
| Build trigger | `leankg index`, MCP `import`, and `leankg writer` only index; they never call the embed pipeline. A project can have elements and 0 vectors legitimately. | by design; fix is operational |
| `leankg index --auto` | Advertises "index (and later embed)" but discards the mode (`_ = mode`); it only indexes. | not wired (product decision) |

**Verified fix (operational).** With the runtime up and one pinned identity, per project:

```bash
export LEANKG_EMBED_PROVIDER=local
export LEANKG_EMBED_BASE_URL=http://127.0.0.1:9101/v1
leankg-embed run --project /abs/path/to/project   # fresh: embeds all
```

Locally proven: `commitgen` 0 → 119 vectors and `onegw` 0 → 2977 vectors (2m5s), both then serving `L3 / cosine`. Without vectors a semantic query degrades with `no vector collection for model local; degraded from L3` — the exact signal the dashboard keys on.

## The embedding runtime (memory)

The embeddings model is **not a container** — it is a host `llama-server` supervised by launchd (`com.freepeak.llama-embed`, `RunAtLoad`), serving bge-small-en-v1.5 on `:9101` at ~82 MB RSS.

- `leankg serve` **attaches, never owns** it: the launchd wrapper exports `LEANKG_EMBED_BASE_URL`, so `embed.StartProvider` takes the attach branch and returns a no-op release. `serve` can neither start nor stop it.
- `leankg-embed run` / `refresh.Run` **spawn on demand and tear down** only when `LEANKG_EMBED_BASE_URL` is unset: `StartSidecar` spawns and health-polls, and `Sidecar.Shutdown` kills the process group (SIGTERM → 3 s → SIGKILL). There is **no idle/TTL unload** — a spawned sidecar lives as long as its owning process.
- With the runtime down, L3 degrades to L2 keyword (`retrieval.reason` = `embedding provider failed… degraded from L3`). That is by design, never a crash, and now visible in `by_rung`.

Turn it on only when embedding, to keep it off your RAM the rest of the time:

```bash
scripts/embed-runtime.sh status   # exit 0 up · 1 down
scripts/embed-runtime.sh off      # bootout — NOT SIGTERM; KeepAlive would race the exit
scripts/embed-runtime.sh on       # bootstrap + wait for /health
```

## What this branch already ships

1. **`leankg dsh-usage`** — dashboard at `http://127.0.0.1:9710`  
   - tool input / output / agent before+after / user prompt  
   - rule classifier (`mcp_session_lost`, `project_not_passed`, `cold_store`, …)
   - session-level issues (`no_leankg_in_code_session`, `semantic_never_served`)
   - per-step ladder `rung` + `retrieval.reason`, `by_rung` summary, and a derived `laya {configured, scored, unavailable, skipped_info}` block
2. **MCP streamable HTTP `Stateless: true`** in `internal/mcp/server.go` (needs serve restart)
3. **Local Laya sidecar** (`POST http://127.0.0.1:8091/v1/systemone`) for step scoring
4. **Continuous watch + notify** (see `leankg dsh-usage --watch`)  
   - polls DSH session logs  
   - classifies + Laya-scores new LeanKG steps  
   - macOS notification on high/critical  
   - pending “help the agent?” asks on the dashboard  
   - optional DSH `session/prompt` inject when a browser cookie is provided

## What is still open (needs you / product)

| Item | Owner | Action |
|---|---|---|
| Restart serve with stateless MCP | ops | rebuild `leankg`, restart `com.freepeak.leankg-serve` |
| DSH session-start LeanKG hook | DSH / config | inject: query LeanKG before grep; always pass `project` |
| Default or require `project` | LeanKG | reject omit when >1 project, or map from client roots |
| Wire Cursor to local LeanKG | user | add MCP url `http://127.0.0.1:9699/mcp` |
| DSH cookie for auto-steer | user | export cookie from the GUI so the watcher can queue a prompt in the live session |
| Build vectors for the vector-less stores | ops | `scripts/embed-runtime.sh on`, then `leankg-embed run` per project — until then L3 never serves |

## How to run the loop

```bash
# Laya (once)
~/venvs/laya/bin/python /path/to/laya-sidecar.py --port 8091

# Dashboard + continuous watch
leankg dsh-usage --addr 127.0.0.1:9710 --watch \
  --laya-url http://127.0.0.1:8091 \
  --notify \
  --dsh-url http://127.0.0.1:3081 \
  --dsh-cookie-file /path/to/dsh-cookies.txt   # optional, for in-session ask

# Open
open http://127.0.0.1:9710
# Pending asks: GET /api/asks  · answer: POST /api/asks/{id}  body {"help":true|false}
```

When a step is high/critical, the watcher:

1. records metrics  
2. fires a macOS notification (if `--notify`)  
3. opens a pending ask: **Help the agent in this session?**  
4. if you answer **yes** and a DSH cookie is available, queues a steer on that session explaining the defect and the fix  
5. if no cookie, the ask stays on the dashboard for you to paste/act in DSH yourself
