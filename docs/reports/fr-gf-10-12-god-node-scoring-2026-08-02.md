# FR-GF-10/12 live evidence — 2026-08-02

## Environment
- leankg version / commit: v0.19.30 + PR-12 branch `prd/god-node-scoring` (worktree build)
- MCP: local stdio-embedded MCP HTTP on `:9711` (release binary from worktree)
- project=: `/tmp/gf10-live-smoke` (TempDir fixture, indexed fresh)

## Steps
1. Create fixture `src/hub.rs` with `hub()` calling 3 leaves + `main()`
2. `leankg index ./src` (release binary) — index-time scoring hook runs via `refresh_index_inventory`
3. `leankg gods --limit 5` — CLI reads persisted `node_scores`
4. `mcp-http` on `:9711`, `get_architecture(max_items=5)` — god nodes in hotspots
5. `get_god_nodes(limit=3)` — MCP tool reads persisted scores

## Results

### Index
```
Indexed 1 files (5 elements)
```
(warn: `all_elements()` deprecated log — expected, existing behavior)

### CLI `leankg gods --limit 5`
```
Top 5 god nodes:
  1. ./src/hub.rs::hub [function] degree=13 (hub)
  2. ./src/hub.rs::main [function] degree=11 (main)
  3. ./src/hub.rs [File] degree=6 (hub.rs)
  4. proc_1_main [process] degree=4 (Main → Leaf_a)
  5. proc_0_main [process] degree=4 (Main → Unknown)
```
Pass: hub is top god node (degree 13 = in+out over 32 relationships including process/contains edges).

### MCP `get_architecture(max_items=5)` — FR-GF-12
```
god_nodes:
  element[5]{degree,element_type,name,pagerank_score,qualified_name,rank_score}:
    13,function,hub,0.06058688734671503,"./src/hub.rs::hub",0.7181760662040144
    11,function,main,0.05882222072496605,"./src/hub.rs::main",0.6099543585251821
    6,File,hub.rs,0.06681074620901373,./src/hub.rs,0.3431201469396272
    ...
  hotspots:
    file_path[1]: ./src/hub.rs,5
  truncated_sections: original_count 10 -> returned 5 (god_nodes)
```
Pass: `god_nodes` section present with `degree`, `rank_score`, `pagerank_score` per entry; max_items truncation works.

### MCP `get_god_nodes(limit=3)`
```
nodes: hub(13, rank 0.718), main(11, rank 0.610), hub.rs(6, rank 0.343)
```
Pass: persisted scores (not live recompute) — values identical to architecture god_nodes.

## Tracker
- Mark `FR-GF-10`, `FR-GF-12` DONE after merge.
