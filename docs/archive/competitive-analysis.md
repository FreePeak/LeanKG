# Why LeanKG

A one-pager for prospect conversations and cold outreach.

---

## The pitch

**Stop burning tokens.** TOON (Token-Oriented Object Notation) trims response payloads by roughly 40%. LeanKG's A/B benchmarks show 30% fewer input tokens and 3x the tokens-per-result of raw search.

**85 MCP tools - the broadest atomic surface in the category.** Competitors ship 1 to 17. Your agent queries with surgical precision instead of blunt grep and chains narrow calls without round-tripping whole files.

**Built for mobile and the cloud.** LeanKG is the only tool in this space that extracts Android XML resources, Hilt, Room, and Navigation graphs out of the box, and that stitches microservice topologies together with outage postmortems.

**Procedural, not just structural.** The ontology layer (`concepts.yaml`, `workflows.yaml`) and Agent Personas (architect, ops) turn the graph into a carrier of instructions, not just code metadata.

**Verifiable.** Every number on this page comes from a reproducible A/B benchmark, not marketing copy.

---

## When LeanKG wins

- Your team pays per token and needs the graph to do the heavy lifting.
- You ship Android - LeanKG is the only tool here that understands XML resources, Hilt DI, Room schemas, and Navigation graphs.
- You run microservices and want the graph to link services to postmortems and on-call ownership.
- Your agent needs fine-grained tools (impact radius, call graph, blast radius, dead code, clones, semantic context) - not a single "Explore" verb.
- You want procedural knowledge alongside structural code (ontologies, workflows, personas).

## When to look elsewhere

- You only need raw indexing speed on very large C/C++ codebases (consider `codebase-memory-mcp`, which can ingest the Linux kernel in about 3 minutes).
- You want a zero-server browser experience where dropping a repo into a browser produces a graph (consider `GitNexus`, which runs in WebAssembly with LadybugDB).
- You need video, PDF, and images in the same graph (consider `graphify`).
- Your world is Swift-to-Objective-C or React Native bridging across 17 web frameworks (consider `codegraph`).

---

## At a glance

| | LeanKG | codebase-memory-mcp | GitNexus | graphify | codegraph |
| :--- | :--- | :--- | :--- | :--- | :--- |
| **Language** | Rust | C / C++ | TypeScript | Python | TypeScript |
| **MCP tools** | 85 | 15 | 17 | standard set | 1 ("Explore") |
| **Token format** | TOON (~40% saved) | standard | standard | budget-per-token | standard |
| **Android-aware** | yes | no | no | no | partial |
| **Microservice topology** | yes | no | no | no | no |
| **Procedural ontology** | yes | no | no | no | no |

---

## Stack

Rust + CozoDB + tree-sitter. Local-first. No SaaS lock-in. Runs as an MCP HTTP server on `:9699` or stdio.

## Get started

```bash
cargo install leankg --release
leankg init
leankg index ./src
leankg serve
```

See [`README.md`](../README.md) and [`docs/prd.md`](./prd.md) for the full picture.

---

*Last updated: 2026-07-18.*
