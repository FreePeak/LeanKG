# Roadmap

**Last updated:** 2026-04-11
**Current version:** 0.11.1

---

## Completed Phases

### Phase 1: MVP (v0.1.0) - COMPLETED
- Core indexing (Go, TS/JS, Python, Rust, Java, Kotlin, C++, C#, Ruby, PHP)
- Dependency graph with 10 relationship types
- CLI interface (28+ commands)
- MCP server (35 tools via rmcp)
- Documentation generation
- Business logic annotations

### Phase 2: Enhanced Features (v0.2.0) - COMPLETED
- Pipeline information extraction (Terraform, CI/CD YAML)
- Documentation-structure mapping
- Enhanced business logic tagging
- Impact analysis with confidence scores
- Web UI embedded (20+ routes)
- Git hooks (pre-commit, post-commit, post-checkout)

### Phase 3: Intelligence (v0.3.0) - COMPLETED
- Confidence scoring on relationships
- Pre-commit change detection (detect_changes)
- Multi-repo global registry (register/unregister/list)
- Community detection (Leiden algorithm)
- Cluster-grouped search
- Enhanced 360-degree context (orchestrate)
- RTK compression (8 modes, response compression)
- Orchestrator with persistent cache
- Context metrics tracking
- REST API server
- Wiki generation
- Graph export (HTML, SVG, GraphML, Neo4j)
- Benchmark runner (vs OpenCode, Gemini, Kilo)
- GitWatcher for continuous freshness

---

## Current Sprint (In Progress)

| Feature | Status | Description |
|---------|--------|-------------|
| **npm-based installation** | PENDING | Binary distribution via npm (US-14) |
| **Dart entity extraction** | PENDING | Parser exists, needs extractor (US-LANG-01) |
| **Swift entity extraction** | PENDING | Parser exists, needs extractor (US-LANG-02) |
| **REST API auth wiring** | PENDING | Auth middleware exists but not wired into routes |
| **REST API mutation endpoints** | PENDING | Add index, annotation endpoints |

---

## Planned Features

| Feature | Priority | Description |
|---------|----------|-------------|
| **Cluster-level SKILL.md** | Could Have | Auto-generate SKILL.md per functional cluster |
| **MCP Resources** | Could Have | Read-only URIs for repos, clusters, schema |
| **XML entity extraction** | Could Have | Parser exists, needs extractor (US-LANG-03) |

---

## Future (Phase 4)

| Feature | Description |
|---------|-------------|
| **Semantic Search** | AI-powered code search using embeddings |
| **Cloud Sync** | Optional cloud sync for team features |
| **Team Features** | Shared knowledge graphs |
| **Plugin System** | Extensible plugin architecture |

---

## References

- [PRD](prd.md)
- [ERD/HLD](erd.md)
- [MCP Tools](mcp-tools.md)
- [CLI Reference](cli-reference.md)
- [AGENTS.md](../AGENTS.md)
