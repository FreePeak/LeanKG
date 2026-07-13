# LeanKG Architecture

> **HLD lives in [`prd.md`](prd.md) Section 6.4–6.9.** This page is a short C4 overview only.

## System Design

### C4 Model - Level 2: Component Diagram

```mermaid
graph TB
    subgraph "Developer Machine / Team Server"
        subgraph "LeanKG System"
            direction TB
            CLI["CLI<br/>init, index, mcp-*, watch, update"]

            subgraph "Indexer"
                Parser["Parser<br/>(tree-sitter)"]
                Extractor["Extractor<br/>functions, classes, imports, calls, routes"]
            end

            DB[("CozoDB<br/>SQLite or RocksDB")]

            subgraph "MCP Server"
                Tools["MCP Tools<br/>65 tools — search, impact, ontology, incidents"]
            end

            subgraph "Web UI"
                Graph["Graph Viewer<br/>2D force-directed"]
                API["REST API"]
            end
        end

        AI["AI Tool<br/>(Claude, Cursor, OpenCode, …)"]
    end

    CLI -->|init| DB
    CLI -->|index| Parser
    Parser -->|parse| Extractor
    Extractor -->|store| DB
    CLI -->|mcp-stdio / mcp-http| Tools
    DB -->|query| Tools
    Tools -->|MCP| AI
    CLI -->|web| API
    API -->|fetch| Graph
```

### Data Flow

```mermaid
graph LR
    subgraph "1. Index Phase"
        A["Source Code"] --> B["tree-sitter"]
        B --> C["Code Elements"]
        B --> D["Relationships"]
        C --> E[("CozoDB")]
        D --> E
    end

    subgraph "2. Query Phase"
        E --> F["MCP Tools"]
        F --> G["AI Context<br/>(TOON / RTK)"]
    end
```

## See also

- Full HLD, data model, vacuum/self-test flows: [`prd.md`](prd.md) §6
- Product requirements & status: [`prd.md`](prd.md)
- Roadmap: [`roadmap.md`](roadmap.md)
