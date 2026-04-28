# LeanKG as a "Second Brain" for AI & Humans

This document investigates how **LeanKG** functions as a codebase "Second Brain" that continuously grows with usage, optimizes knowledge retrieval, and significantly reduces LLM token consumption. The analysis is based on LeanKG v0.11.1 capabilities and its MemPalace-inspired architectural roadmap.

---

## 1. Growing the Knowledge Base with Usage

LeanKG is designed to transform a static codebase into an evolving knowledge base that learns and accumulates context over time.

*   **Conversation & Decision Mining:** By indexing standard AI and human chat exports (Claude, ChatGPT, Slack), LeanKG extracts raw *decisions, preferences*, and *milestones*. These are stored as persistent graph nodes linked to code elements via `decided_about` edges. This explicitly captures the *why* behind code changes, which is naturally missed by traditional AST parsers.
*   **Temporal Knowledge Graph:** As the codebase changes, LeanKG does not simply delete old relationships. Instead, it employs `valid_from` and `valid_to` timestamps. When an import is removed or a function refactored, the edge is invalidated but retained. This establishes a historical timeline, allowing agents to query context from prior commits.
*   **Business Logic Annotations:** Both humans and agents can attach semantic business descriptions to technical elements. Because this data lives within the CozoDB embedded graph, as the team interacts more with the codebase, LeanKG aggregates a richer web of traceability mapping requirements straight to functions.
*   **Agent Diaries & Specialist Contexts:** LeanKG supports defining specific agent personas (e.g., architect, reviewer). Each agent maintains its own session diary within the local database, appending observations and context filters that grow the system's explicit memory of past debugging sessions or architectural decisions.

## 2. Quick and Concise Knowledge Retrieval

Instead of relying on flat file-path greps or blind vector similarity, LeanKG retrieves knowledge using heavily structured, semantic mappings.

*   **Folder-As-Graph (Memory Palace Spatiality):** LeanKG maps the codebase like a physical memory palace: `Wing (src/) → Room (src/graph/) → Closet (query.rs) → Drawer (GraphEngine)`. Directories are first-class nodes with `contains` edges. Agents navigate the "rooms" logically instead of guessing file paths.
*   **Cross-Domain Tunnels:** LeanKG auto-detects shared concepts matching different codebase clusters. This allows knowledge retrieval to jump semantic boundaries (e.g., jumping directly from a UI Auth Component to the API Gateway Auth middleware without scanning boilerplate).
*   **Consistency Checking:** To ensure retrieval remains concise and accurate, LeanKG actively guards against staleness. It detects when annotations reference deleted code or when docs are out of date, grading them (🔴 BROKEN, 🟡 STALE, 🟢 CURRENT).

## 3. High-Efficiency Token Saving Strategies

A traditional AI workflow wastes thousands of tokens scanning boilerplate. LeanKG solves this through strict layer loading and "RTK" compression.

*   **Layered Context Loading (L0-L3):**
    *   **L0 (Identity - ~50 tokens):** Project pattern, tech stack.
    *   **L1 (Critical Facts - ~120 tokens):** Module map, critical hotspots. Automatically delivered via a `wake_up` MCP tool at session start.
    *   **L2 & L3 (Cluster & Deep Search):** Loaded *only on demand*. By isolating contexts, agents never process the entire repo simultaneously.
*   **RTK (Rust Token Killer) Compression Engine:**
    *   **8 Adaptive Read Modes:** When an agent queries a file, LeanKG dynamically compresses it. Modes like `signature_only`, `entropy_filtered`, and `map` discard noisy implementations and only return architectural skeletons.
    *   **Specialized Compressors:** Output from git diffs or failed tests is filtered. For example, the `CargoTestCompressor` extracts *only* the failures, achieving 85%+ token savings instantly.
*   **Orchestrated Query Routing:** An intelligent orchestrator intercepts MCP queries, parses the intent, and checks a persistent cache. If the context is unchanged, cached heavily compressed data is returned instantly, bypassing both file I/O and token waste.

## 4. Serving Both Humans and AI Agents

LeanKG bridges the gap between machine-readable structure and human-readable documentation.

*   **For AI Agents:** 35 tightly defined MCP tools expose the graph. The agent operates using token-efficient APIs (`get_impact_radius`, `orchestrate`, `get_review_context`) rather than standard POSIX filesystem commands.
*   **For Humans:** An embedded local Web UI provides graph visualization, cluster-grouped visual searches, and timeline evolution views. Additionally, LeanKG can automatically export the graph straight to a Markdown Wiki or visual formats (HTML, SVG, Mermaid, Neo4j), ensuring that human developers have an up-to-date, explorable mental model of the system.

## Conclusion

LeanKG fundamentally shifts codebase interaction from a "stateless search process" into an "evolving memory palace." As developers and agents work, LeanKG absorbs decisions, compresses structural context, and guards against context-window dilution. It operates as a true Second Brain that is completely autonomous, local-first, and highly optimized for token economy.
