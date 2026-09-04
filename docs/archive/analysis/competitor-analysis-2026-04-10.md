# LeanKG Competitor Analysis

**Date:** 2026-04-10
**Purpose:** Identify top 5 open-source GitHub competitors to LeanKG (code knowledge graph + MCP server)

---

## LeanKG Positioning

LeanKG is a lightweight knowledge graph for codebase understanding that:
- Indexes code using tree-sitter
- Builds dependency graphs in CozoDB
- Calculates impact radius
- Exposes everything via MCP for AI tool integration
- **GitHub:** `FreePeak/LeanKG`
- **Your related project:** `FreePeak/code-context` (Go + Node.js MCP server for Oracle/TimescaleDB)

---

## Top 5 Open-Source Competitors

### 1. Sourcegraph (sourcegraph/sourcegraph)

| Attribute | Value |
|-----------|-------|
| **GitHub Stars** | 10,300+ |
| **Language** | Go |
| **License** | Apache-2.0 (was proprietary, open-sourced in 2025) |
| **Overlap** | Code intelligence, dependency graphs, code search across repos |

**What it does:** Full-stack code intelligence platform. Provides code search, navigation, cross-references, and dependency understanding across thousands of repositories. Powers "Cody" AI assistant.

**Overlap with LeanKG:** Dependency graph, code navigation, impact analysis.
**LeanKG advantage:** Lightweight, embedded (CozoDB), MCP-native, single-project focused, fast setup.

---

### 2. Continue (continuedev/continue)

| Attribute | Value |
|-----------|-------|
| **GitHub Stars** | 21,000+ |
| **Language** | TypeScript |
| **License** | Apache-2.0 |
| **Overlap** | AI code context, MCP integration, code understanding |

**What it does:** Open-source AI code assistant (VS Code / JetBrains extension). Provides code context, tab-autocomplete, chat with codebase. Integrates with MCP servers for context providers.

**Overlap with LeanKG:** Uses MCP protocol, provides code context to AI models.
**LeanKG advantage:** LeanKG is a context *provider* (knowledge graph), Continue is a context *consumer* (IDE extension). They are complementary, but Continue's built-in context features compete.

---

### 3. ast-grep (AstGrep/ast-grep)

| Attribute | Value |
|-----------|-------|
| **GitHub Stars** | 7,800+ |
| **Language** | Rust |
| **License** | MIT |
| **Overlap** | Tree-sitter-based code search, AST pattern matching |

**What it does:** Code search and refactoring tool using tree-sitter AST patterns. Supports 20+ languages. Can find, lint, and rewrite code patterns structurally.

**Overlap with LeanKG:** Both use tree-sitter. Both provide code understanding. ast-grep is a search/refactoring tool, not a graph engine.
**LeanKG advantage:** Knowledge graph with relationships, dependency tracking, impact radius calculation, persistent storage (CozoDB). ast-grep is stateless pattern matching.

---

### 4. Context7 (nicholaschenai/context7-mcp)

| Attribute | Value |
|-----------|-------|
| **GitHub Stars** | 10,000+ |
| **Language** | TypeScript |
| **License** | MIT |
| **Overlap** | MCP server providing code/library context to AI tools |

**What it does:** MCP server that fetches up-to-date documentation and code context for libraries and frameworks. Helps AI coding tools understand library APIs without hallucinating.

**Overlap with LeanKG:** Both are MCP servers providing code context. Context7 focuses on external library documentation; LeanKG focuses on internal codebase structure.
**LeanKG advantage:** Internal codebase graph (dependencies, call graphs, impact radius). Context7 only provides library docs, not project-specific knowledge.

---

### 5. repomix (yamadashy/repomix)

| Attribute | Value |
|-----------|-------|
| **GitHub Stars** | 8,600+ |
| **Language** | TypeScript |
| **License** | MIT |
| **Overlap** | Codebase context for AI, repository understanding |

**What it does:** Packs entire codebase into a single file optimized for AI consumption. Supports MCP server mode to provide repository context to AI tools. Handles file selection, token counting, output formatting.

**Overlap with LeanKG:** Both provide codebase context via MCP. Both help AI tools understand code.
**LeanKG advantage:** Structured knowledge graph with relationships, dependency tracking, query engine. repomix is flat text packing -- no graph, no relationships, no impact analysis.

---

## Competitive Landscape Summary

```
                    Graph/Relationships    MCP Native    Lightweight    Impact Analysis
                    ===================    ==========    ===========    ===============
LeanKG              YES                    YES           YES            YES
Sourcegraph         YES                    NO            NO (heavy)     YES
Continue            NO (consumer)          YES           YES            NO
ast-grep            NO (stateless)         NO            YES            NO
Context7            NO                     YES           YES            NO (library docs)
repomix             NO (flat text)         YES           YES            NO
```

## LeanKG Differentiators

1. **Knowledge Graph vs Flat Context:** Only LeanKG and Sourcegraph build actual graph structures with typed relationships (`imports`, `calls`, `tested_by`, `references`).

2. **MCP-Native:** LeanKG is designed from the ground up as an MCP server. Sourcegraph requires its own platform.

3. **Embedded & Lightweight:** CozoDB embedded, no external DB needed. Sourcegraph needs PostgreSQL + Redis + extensive infrastructure.

4. **Impact Radius:** Unique blast-radius calculation for change impact analysis.

5. **Rust + Tree-sitter:** Same performance foundation as ast-grep, but with persistent storage and graph queries.

---

## Recommended Actions

1. **Position against Context7/repomix:** Emphasize graph structure + relationships vs flat text/docs
2. **Position against Sourcegraph:** Emphasize lightweight, MCP-native, zero-infra setup
3. **Integrate with Continue:** LeanKG can be a context provider for Continue (complementary)
4. **Add ast-grep patterns:** Consider integrating ast-grep's pattern language for advanced queries
5. **Highlight impact radius:** This is unique among all competitors

---

*Data sources: GitHub API (api.github.com), verified 2026-04-10*
