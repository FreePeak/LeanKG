<!-- GENERATED-BY: go test ./internal/mcp -run TestToolContractDoc -->
<!-- DO NOT EDIT BY HAND -->

# MCP Tool Contract

Generated from the live tool registry (`leankgmcp.New(...).registerTools`, via `ListTools`). **3 tools.**
To change the surface: edit the registry, run `go test ./internal/mcp -run TestToolContractDoc`, commit both.

## Stability tiers

- **stable** — input schema and output shape are contractual; breaking changes follow the deprecation policy below.
- **beta** — may change or be removed in any minor release; feedback welcome.
- New tools enter as **beta** and are promoted after one minor release without schema change.

## Deprecation policy

- Tool removal requires **2 minor releases** of deprecation notices (doc + tool description marked deprecated).
- A breaking input-schema change to a stable tool requires a **minor version bump treated as major-equivalent**, plus a release notice.
- Additive optional properties do not break the contract.

## Deprecation history

_None. The Go registry's tool count has only ever shrunk (76 Rust tools to 3), which is a rewrite, not a deprecation._

## Tools

| Tool | Description | Input properties |
|---|---|---|
| `import` | Import content into LeanKG: index a repository or directory of repositories (action=repo|dir, path), or curate agent memory (action=memory, command=create|str_replace|insert|delete|rename|add|replace|remove; create overwrites an existing file). Legacy tool name 'set' is superseded by this tool. Use action=dir with path="." to import the current directory as a scoped index target (FR-P2). Import... | action, args, command, content, file, insert_line, new, new_path, node_id, old, path, payload, project, session_id, summary, tags, text |
| `query` | Query LeanKG. Empty action routes down the ladder: L1 exact identifier → L2 fuzzy keyword → L3 semantic (vectors). Every answer carries retrieval{rung,reason} + freshness. action=memory searches agent memory (args.command=view with args.path reads one file, =snapshot the MEMORY.md/USER.md core); action=exact|fuzzy|semantic pins a rung; graph verbs (impact/path/callers/callees/context/explai... | action, args, limit, project, query |
| `status` | LeanKG health: inventory, freshness (fresh|possibly_stale|cold), watermark, backend, embeddings state (stamped models, vectors), last embed run. Use status for health and coverage, not as a substitute for query. Agent protocol: query before bash/grep; project= only on multi-project HTTP; import once when cold; inspect retrieval/freshness/guidance; session_recall at start, session_retain (action... | project |
