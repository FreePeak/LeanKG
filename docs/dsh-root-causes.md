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
| 1 | **Wrong meter** | `~/.leankg/sessions/posttooluse.log` is Claude plugin only (Rust-era tool names). DSH never writes it. | info | **Solved** — read `~/.dsh/sessions/**/session.v3.jsonl[.zstd]` via `leankg dsh-usage` (opt-in `-tags dshusage`) |
| 2 | **No DSH LeanKG nudge** | Claude/Cursor inject hooks; DSH only has passive `~/.dsh/AGENTS.md`. bash 7642 / read 2271 / grep 401 vs 28 LeanKG calls | high | **Partial** — `~/.dsh/AGENTS.md` now has a LeanKG-first hard rule + dashboard hook text; full DSH SessionStart plugin still out of scope (harness repo) |
| 3 | **Omitted `project=` searched serve cwd** | Multi-project MCP defaulted to LeanKG process cwd. Menu/promo questions returned LeanKG source. 7/28 steps: `project_not_passed` | critical | **Fixed in branch + live** — `engineFor` fails closed (`LEANKG_ERROR_MISSING_PARAM`); verified against running serve. Old logs still classify as `project_not_passed`; new errors classify as `project_required` |
| 4 | **Sticky MCP session dies on restart** | DSH keeps `Mcp-Session-Id`; launchd restart / per-project handler maps → `session not found` (7/28) | critical | **Fixed + live** — `Stateless: true` built into `~/.local/bin/leankg` (backup `~/.local/bin/leankg.bak-20260923`); `com.freepeak.leankg-serve` restarted 2026-09-23; `initialize` smoke OK |
| 5 | **Cold / unindexed project** | Query before import → cold guidance. 3/28 | high | **Operational** — import once with absolute path + project basename |
| 6 | **Ambiguous project basename** | e.g. `deepseek-harness` matches multiple registered paths | high | **Classifier flags it**. Use absolute path or unique names |
| 7 | **Cursor unwired to local LeanKG** | `~/.cursor/mcp.json` has remote BE KG, not `http://127.0.0.1:9699/mcp` | high | **Not solved** — user config change |
| 8 | **Writer only watches LeanKG checkout** | Other repos go stale unless imported | medium | **Operational / product** — per-project writer or on-demand import |
| 9 | **Laya is a classifier, not chat** | `:9101` is bge-small embeddings; Laya = ModernBERT System One | info | **Solved for scoring** — local sidecar `:8091` scores steps |

## What this branch already ships

1. **`leankg dsh-usage`** — dashboard at `http://127.0.0.1:9710`  
   - tool input / output / agent before+after / user prompt  
   - rule classifier (`mcp_session_lost`, `project_not_passed`, `cold_store`, …)
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
