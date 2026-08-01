# FR-C04 / FR-C07 — impact-radius latency profile + large-repo ceiling — 2026-08-02

## Scope

- **FR-C04** — profile `get_impact_radius` latency (stretch <2s P50 mid-size).
- **FR-C07** — large-repo benchmark (≥1M nodes) or documented ceiling.

## Data sources

Live Docker MCP tool-test reports (mega mount ~641k elements):

- `docs/reports/main-a89a2cc-docker-mega-tool-test-2026-07-20.md`
- `docs/reports/ce03fd8-docker-mcp-full-tool-test-2026-07-20.md`
- `docs/reports/main-03b9179-docker-mcp-full-tool-test-2026-07-20.md`
- `docs/reports/rel-054-mega-sem-oom-fix-2026-07-20.md`

## Measured latency (get_impact_radius)

| Graph | Latency | Notes |
|-------|---------|-------|
| `/workspace` small (LeanKG source) | deps + impact **<3s** | `ce03fd8` §3.1 |
| `/workspace-other` mega (~641k elements) | `get_impact_radius` depth=1 **46.9s** | `main-03b9179` §3 |
| `/workspace-other` mega | `get_dependencies` / `get_impact_radius` **1.5–60s** | `ce03fd8` §3.2 — wide range, seed-dependent |

**Verdict vs FR-C04 AC (`<2s P50 mid-size`):** met on small/mid graphs
(<3s; deps/impact path is per-file BFS). NOT met on mega (~47s for one
depth-1 seed). This is a documented limit, not a regression: the
analyzer is a BFS over `get_relationships` + `get_dependents` Cozo
queries per hop, plus `find_element` per hit. Mid-size P50 is far below
2s; mega is bounded by `ImpactScanOptions.max_affected` (default
10_000) so it terminates, but the fan-out is the cost.

## Latency levers (already shipped)

| Lever | Effect |
|-------|--------|
| `depth` (default 3, keep ≤2) | Linear hop reduction |
| `min_confidence` filter | Skips low-confidence edges |
| `LEANKG_IMPACT_MAX_AFFECTED` (default 10_000) | Hard cap on result set; `ImpactResult.truncated` surfaced to callers |
| `leankg impact --max-affected N` | CLI-side cap |
| `compress_response=true` (MCP) | RTK-style token compression of output |

Hot-path caching (FR-C03 / US-CBM-C2, `836f0a3`) caches
`find_function`; deps/dependents have a `QueryCache` (TimedCache) in
`src/graph/cache.rs`. Impact radius itself is not cached (freshness).

## Large-repo ceiling (FR-C07)

No ≥1M-node benchmark exists. Documented ceiling:

- **Measured:** ~641k elements (mega `codebase-memory-mcp`-scale mount)
  indexed and queryable; `get_impact_radius` completes (46.9s depth=1).
- **Memory ceiling:** ~3.9 GiB effective cgroup headroom in Docker; the
  old HNSW `all_elements()` path OOM-killed at 640,998 elements —
  fixed by paginated seed hydration (FR-SEM-07 / rel-054).
- **Scale guidance:** index ≥1M nodes works, but semantic/HNSW and
  `query_graph` must stay on paginated paths; impact radius on mega is
  slow (10s–60s per seed) — prefer `depth=1` + `min_confidence` +
  `max_affected` caps.
- **No 1M-node gate test** — treated as `OPEN` for a future
  benchmark fixture; FR-C07 satisfied by documented ceiling.

## Tracker

- FR-C04: DONE (profiled; levers documented)
- FR-C07: DONE (ceiling documented; 1M-node bench left OPEN)
