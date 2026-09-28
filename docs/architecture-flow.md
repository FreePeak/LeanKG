# LeanKG Architecture — Flow Diagrams & Data Structures

## 1. System Overview

```
┌─────────────────────────────────────────────────────────────────────┐
│                         macOS Boot                                  │
│                                                                     │
│  launchctl loads ~/Library/LaunchAgents/com.freepeak.*.plist        │
│       │                                                             │
│       ├─── com.freepeak.llama-embed ─────────────────────────────┐  │
│       │    llama-server -m bge-small-en-v1.5 --port 9101         │  │
│       │    [Embedding inference server]                          │  │
│       │                                                          │  │
│       ├─── com.freepeak.leankg-serve ──────────────────────────┐ │  │
│       │    leankg-serve-wrapper.sh                              │ │  │
│       │      ├── scan ~/work/ → 279 repos                      │ │  │
│       │      ├── export LEANKG_PROJECT_DIRS=<279 paths>        │ │  │
│       │      └── exec leankg serve --http :9699 --rest :9700   │ │  │
│       │    [MCP + REST API + query-time embedding]              │ │  │
│       │                                                         │ │  │
│       └─── com.freepeak.leankg-writer ───────────────────────┐ │ │  │
│            leankg writer --project <leankg-repo>             │ │ │  │
│            [File watcher → auto-reindex on change]           │ │ │  │
│                                                              │ │ │  │
└──────────────────────────────────────────────────────────────┘ │ │  │
                                                                  │ │  │
        ┌─────────────────────────────────────────────────────────┘ │  │
        │                                                           │  │
        ▼                                                           ▼  │
  ┌──────────────┐    HTTP     ┌──────────────┐    HTTP    ┌─────────┐ │
  │  DSH Session │ ──────────→ │ leankg serve │ ─────────→ │ llama   │ │
  │  (Browser)   │  MCP :9699  │   :9699      │  :9101/v1  │ embed   │ │
  │              │  REST :9700 │              │            │ :9101   │ │
  └──────────────┘             └──────┬───────┘            └─────────┘ │
                                       │                               │
                                       │ reads/writes                  │
                                       ▼                               │
                              ┌─────────────────┐                      │
                              │  SQLite (WAL)    │                      │
                              │  .leankg/leankg.db│                     │
                              │  211 MB          │                      │
                              └─────────────────┘                      │
                                                                       │
  ┌─────────────────────────────────────────────────────────────────────┘
  │
  ▼
┌──────────────────────────────────────────────────────────────────────┐
│ 279 Project Stores (lazy-opened on first query)                      │
│                                                                       │
│  ~/work/harvey/freepeak/leankg/.leankg/leankg.db       ← default    │
│  ~/work/harvey/freepeak/agent-platform/.leankg/leankg.db            │
│  ~/work/be/backend/.leankg/leankg.db                               │
│  ... (279 total)                                                    │
└──────────────────────────────────────────────────────────────────────┘
```

## 2. Data Flow: Index → Embed → Query

```
                        ┌─────────────────────┐
                        │   User's Codebase    │
                        │  (any directory)      │
                        └──────────┬──────────┘
                                   │
                    ┌──────────────┴──────────────┐
                    │                              │
                    ▼                              ▼
         ┌─────────────────┐           ┌─────────────────┐
         │  leankg index    │           │  leankg writer   │
         │  (one-shot)      │           │  (persistent)    │
         │                  │           │                  │
         │  Walks directory │           │  fsnotify watch  │
         │  Extracts:       │           │  On change:      │
         │  - functions     │           │  re-index delta  │
         │  - classes       │           │                  │
         │  - imports       │           └────────┬────────┘
         │  - relationships │                    │
         │  - files         │                    │
         └────────┬────────┘                    │
                  │                              │
                  └──────────┬───────────────────┘
                             │
                             ▼
                  ┌─────────────────────┐
                  │  code_elements       │
                  │  relationships       │  ← Raw indexed data
                  │  code_files          │     (NO vectors yet)
                  │  elements_fts        │     FTS5 for L1/L2
                  └──────────┬──────────┘
                             │
                             │  User runs: leankg-embed run
                             ▼
                  ┌─────────────────────┐
                  │  leankg-embed        │
                  │                      │
                  │  1. Read elements    │
                  │  2. Hash-diff        │
                  │     (skip unchanged) │
                  │  3. Batch → llama    │──→ POST :9101/v1/embeddings
                  │  4. Store vectors    │←── { embedding: [384 floats] }
                  │                      │
                  └──────────┬──────────┘
                             │
                             ▼
                  ┌─────────────────────┐
                  │  embedding_vectors   │
                  │  embedding_state     │  ← Vectors stored
                  │  emb_stamp           │     (384-dim float32)
                  └──────────┬──────────┘
                             │
                             │  User queries via MCP
                             ▼
                  ┌─────────────────────┐
                  │  leankg serve        │
                  │  query engine        │
                  │                      │
                  │  L1: exact match     │──→ FTS5 keyword
                  │  L2: fuzzy fallback  │──→ FTS5 fuzzy
                  │  L3: semantic search │──→ cosine similarity
                  │                      │
                  └─────────────────────┘
```

## 3. MCP Session Lifecycle

```
  DSH Session                          leankg serve
      │                                     │
      │  1. POST /mcp                       │
      │     { method: "initialize" }        │
      │  ─────────────────────────────────→ │
      │                                     │
      │  ←── 200 OK                         │
      │  { result: { serverInfo, ... } }    │
      │  Header: Mcp-Session-Id: ABC123     │
      │  ←───────────────────────────────── │
      │                                     │
      │  3. POST /mcp                       │
      │     Header: Mcp-Session-Id: ABC123  │
      │     { method: "notifications/       │
      │              initialized" }         │
      │  ─────────────────────────────────→ │
      │                                     │
      │  4. POST /mcp                       │
      │     { method: "tools/call",         │
      │       params: {                     │
      │         name: "query",              │
      │         arguments: {                │
      │           query: "how auth works",  │
      │           project: "agent-platform" │ ← Routes to project engine
      │         }                           │
      │       }                             │
      │     }                               │
      │  ─────────────────────────────────→ │
      │                                     │
      │                                     │ ┌──────────────────┐
      │                                     │ │ Route:           │
      │                                     │ │ resolve("agent-  │
      │                                     │ │  platform")      │
      │                                     │ │   ↓              │
      │                                     │ │ Find registered  │
      │                                     │ │ project dir      │
      │                                     │ │   ↓              │
      │                                     │ │ Lazy-open store  │
      │                                     │ │ (if first use)   │
      │                                     │ │   ↓              │
      │                                     │ │ Execute query    │
      │                                     │ │ on that engine   │
      │                                     │ └──────────────────┘
      │                                     │
      │  ←── 200 OK                         │
      │  { result: { content: [             │
      │    { text: '{"hits":[...],          │
      │            "retrieval":{...}}' }    │
      │  ]}}                                │
      │  ←───────────────────────────────── │
      │                                     │
      ▼                                     ▼
```

## 4. Project Router (Multi-Project)

```
                    LEANKG_PROJECT_DIRS
                    "repo1,repo2,...,repo297"
                            │
                            ▼
                    ┌───────────────┐
                    │  Router        │
                    │               │
                    │  .entries:    │
                    │  {            │
                    │    "repo1" → Project{ engine, store },
                    │    "repo2" → Project{ engine, store },
                    │    ...       │
                    │  }           │
                    │               │
                    │  .def:       │  ← Default project (leankg)
                    │  "leankg"    │
                    └───────┬───────┘
                            │
          ┌─────────────────┼─────────────────┐
          │                 │                 │
          ▼                 ▼                 ▼
   ┌─────────────┐  ┌─────────────┐  ┌─────────────┐
   │  selector="" │  │ selector=   │  │ selector=   │
   │  → default  │  │ "agent-     │  │ "/Users/    │
   │    project  │  │  platform"  │  │  linh.doan/ │
   │             │  │  → exact    │  │  work/be/   │
   │             │  │    name     │  │  backend"   │
   │             │  │    match    │  │  → ancestor  │
   │             │  │             │  │    walk →    │
   │             │  │             │  │    find      │
   │             │  │             │  │    "backend" │
   └─────────────┘  └─────────────┘  └─────────────┘
```

## 5. Database Schema (Key Tables)

```
┌─────────────────────────────────────────────────────────────────┐
│                    .leankg/leankg.db (SQLite WAL)                │
├─────────────────────────────────────────────────────────────────┤
│                                                                  │
│  ┌──────────────────────┐     ┌──────────────────────┐          │
│  │  code_elements        │     │  relationships        │          │
│  │  ─────────────────── │     │  ─────────────────── │          │
│  │  qualified_name  TEXT │────→│  from_name       TEXT │          │
│  │  kind           TEXT │     │  to_name         TEXT │          │
│  │  language       TEXT │     │  rel_kind        TEXT │          │
│  │  file_path      TEXT │     │  file_path       TEXT │          │
│  │  start_line     INT  │     └──────────────────────┘          │
│  │  end_line       INT  │                                        │
│  │  content        TEXT │     ┌──────────────────────┐          │
│  └──────────┬───────────┘     │  embedding_state      │          │
│             │                 │  ─────────────────── │          │
│             │                 │  model_id        TEXT │          │
│             │                 │  qualified_name  TEXT │          │
│             │                 │  content_hash    TEXT │          │
│             │                 │  state           TEXT │          │
│             │                 │  embedded_at     TEXT │          │
│             │                 └──────────┬───────────┘          │
│             │                            │                      │
│             ▼                            ▼                      │
│  ┌──────────────────────┐     ┌──────────────────────┐          │
│  │  elements_fts         │     │  embedding_vectors    │          │
│  │  (FTS5 virtual table) │     │  ─────────────────── │          │
│  │  ─────────────────── │     │  model_id        TEXT │ PK       │
│  │  qualified_name       │     │  qualified_name  TEXT │ PK       │
│  │  kind                 │     │  vec            BLOB  │          │
│  │  content              │     │                      │          │
│  │                       │     │  vec = struct {       │          │
│  │  Used for:            │     │    float32[384]       │          │
│  │  L1 exact match       │     │  }                    │          │
│  │  L2 fuzzy match       │     │  = 1536 bytes         │          │
│  └──────────────────────┘     │                      │          │
│                                │  BGE-small-en-v1.5   │          │
│                                │  384 dimensions      │          │
│                                │  cosine distance     │          │
│                                └──────────────────────┘          │
│                                                                  │
│  ┌──────────────────────┐     ┌──────────────────────┐          │
│  │  embed_runs           │     │  emb_stamp            │          │
│  │  ─────────────────── │     │  ─────────────────── │          │
│  │  id              INT │     │  model_id        TEXT │ PK       │
│  │  model_id        TEXT │     │  revision        TEXT │          │
│  │  mode            TEXT │     │  dimensions      INT │          │
│  │  status          TEXT │     │  distance        TEXT │          │
│  │  dirty           INT │     │  provider        TEXT │          │
│  │  embedded        INT │     │  chunker_version INT │          │
│  │  skipped         INT │     └──────────────────────┘          │
│  │  failed          INT │                                        │
│  │  started_at      TEXT │                                       │
│  │  finished_at     TEXT │                                       │
│  └──────────────────────┘                                        │
│                                                                  │
└─────────────────────────────────────────────────────────────────┘
```

## 6. Query Resolution (L1 → L2 → L3)

```
        User Query: "how does authentication work"
                        │
                        ▼
                ┌───────────────┐
                │  L1: Exact     │
                │  FTS5 match    │
                │               │
                │  SELECT *     │
                │  FROM elements│
                │  WHERE ...    │
                │  MATCH "..."  │
                └───────┬───────┘
                        │
                   Found? ─── YES → Return results
                        │
                        NO
                        │
                        ▼
                ┌───────────────┐
                │  L2: Fuzzy     │
                │  FTS5 fallback │
                │               │
                │  Broader      │
                │  keyword      │
                │  matching     │
                └───────┬───────┘
                        │
                   Found? ─── YES → Return results
                        │
                        NO
                        │
                        ▼
                ┌───────────────┐
                │  L3: Semantic  │
                │  Vector search │
                │               │
                │  1. Embed     │
                │     query →   │──→ POST :9101/v1/embeddings
                │     [384-dim] │←── { embedding: [...] }
                │               │
                │  2. Cosine    │
                │     similarity│
                │     against   │
                │     ALL       │
                │     vectors   │
                │               │
                │  3. Top-K     │
                │     results   │
                └───────────────┘
```

## 7. Embedding Pipeline (leankg-embed)

```
        ┌───────────────────────────────────────────────┐
        │              leankg-embed run                   │
        │                                                │
        │  ┌─────────────┐    ┌─────────────────────┐   │
        │  │ Read DB      │    │ Compute content_hash │   │
        │  │ code_elements│───→│ (SHA-1 per element)  │   │
        │  │              │    └──────────┬──────────┘   │
        │  └─────────────┘               │               │
        │                                 ▼               │
        │                      ┌─────────────────────┐   │
        │                      │ Compare with         │   │
        │                      │ embedding_state      │   │
        │                      │                      │   │
        │                      │ hash == stored?      │   │
        │                      │   YES → skip         │   │
        │                      │   NO  → embed        │   │
        │                      └──────────┬──────────┘   │
        │                                 │               │
        │                    ┌────────────┴───────────┐  │
        │                    │                        │  │
        │                    ▼                        ▼  │
        │           ┌──────────────┐       ┌──────────┐ │
        │           │ Skip (9038)  │       │ Embed    │ │
        │           │ already done │       │ (146)    │ │
        │           └──────────────┘       └────┬─────┘ │
        │                                       │       │
        │                                       ▼       │
        │                              ┌─────────────┐  │
        │                              │ Batch texts  │  │
        │                              │ (prefix by   │  │
        │                              │  element     │  │
        │                              │  kind)       │  │
        │                              └──────┬──────┘  │
        │                                     │         │
        │                                     ▼         │
        │                            ┌────────────────┐ │
        │                            │ POST            │ │
        │                            │ :9101/v1/       │ │
        │                            │ embeddings      │ │
        │                            │                 │ │
        │                            │ {               │ │
        │                            │  "input": [     │ │
        │                            │   "function    │ │
        │                            │    auth_login" │ │
        │                            │  ],            │ │
        │                            │  "model":      │ │
        │                            │  "bge-small"   │ │
        │                            │ }               │ │
        │                            └────────┬───────┘ │
        │                                     │         │
        │                                     ▼         │
        │                            ┌────────────────┐ │
        │                            │ Response:       │ │
        │                            │ { "data": [     │ │
        │                            │   { "embedding":│ │
        │                            │     [0.03,      │ │
        │                            │      -0.01,     │ │
        │                            │      ... ]      │ │
        │                            │   }             │ │
        │                            │ ]}              │ │
        │                            └────────┬───────┘ │
        │                                     │         │
        │                                     ▼         │
        │                    ┌────────────────────────┐ │
        │                    │ Store in DB:            │ │
        │                    │                         │ │
        │                    │ embedding_vectors:      │ │
        │                    │   INSERT (model_id,     │ │
        │                    │     qualified_name,     │ │
        │                    │     vec BLOB)           │ │
        │                    │                         │ │
        │                    │ embedding_state:        │ │
        │                    │   INSERT (model_id,     │ │
        │                    │     qualified_name,     │ │
        │                    │     content_hash,       │ │
        │                    │     state='embedded')   │ │
        │                    └────────────────────────┘ │
        │                                               │
        └───────────────────────────────────────────────┘
```

## 8. Launchctl Boot Sequence

```
    macOS Boot
        │
        ▼
    launchctl bootstrap
        │
        ├──→ com.freepeak.llama-embed.plist
        │     │
        │     ▼
        │    llama-server -m bge-small-en-v1.5-f16.gguf \
        │                  --embeddings -c 8192 --port 9101
        │     │
        │     ▼
        │    Health: GET :9101/health → {"status":"ok"}
        │    (Ready for embedding requests)
        │
        ├──→ com.freepeak.leankg-serve.plist
        │     │
        │     ▼
        │    leankg-serve-wrapper.sh
        │     │
        │     ├── scan ~/work/ → 279 git repos
        │     ├── export LEANKG_PROJECT_DIRS=<279 paths>
        │     │
        │     ▼
        │    leankg serve --http :9699 --rest :9700 \
        │                --memory --embed-provider local
        │     │
        │     ├── Attach to :9101 (LEANKG_EMBED_BASE_URL)
        │     ├── Open default project (leankg repo)
        │     ├── Register 279 projects in router
        │     ├── Start REST listener on :9700
        │     ├── Start MCP listener on :9699
        │     │
        │     ▼
        │    Health: GET :9700/health → {"ok":true}
        │    MCP: POST :9699/mcp → initialize → version 0.34.0
        │
        └──→ com.freepeak.leankg-writer.plist
              │
              ▼
             leankg writer --project <leankg-repo>
              │
              ├── Initial index (IndexDirWith)
              ├── fsnotify watch loop
              │   On file change → re-index delta
              │
              ▼
             Writer running (PID 67413)
```

## 9. Vector Storage Format

```
    embedding_vectors table
    ┌──────────────────────────────────────────────────────┐
    │ model_id  │ qualified_name          │ vec (BLOB)     │
    ├───────────┼─────────────────────────┼────────────────┤
    │ local     │ AGENTS.md::Build & Test │ [1536 bytes]   │
    │           │                         │                │
    │           │                         │ ┌────────────┐ │
    │           │                         │ │ float32[0] │ │ -0.074273
    │           │                         │ │ float32[1] │ │ -0.001519
    │           │                         │ │ float32[2] │ │  0.016927
    │           │                         │ │ ...        │ │  ...
    │           │                         │ │ float32[383]│ │ -0.016199
    │           │                         │ └────────────┘ │
    │           │                         │                │
    │           │                         │ L2 norm = 1.0  │
    │           │                         │ (normalized)   │
    ├───────────┼─────────────────────────┼────────────────┤
    │ local     │ auth.go::LoginFunction  │ [1536 bytes]   │
    │ local     │ auth.go::ValidateToken  │ [1536 bytes]   │
    │ ...       │ ...                     │ ...            │
    └──────────────────────────────────────────────────────┘

    Query-time cosine similarity:
    ┌──────────────────────────────────────────────────────┐
    │ query_vec (384 floats, from llama :9101)              │
    │      ·                                               │
    │      ·  ← dot product                                │
    │      ·                                               │
    │ stored_vec (384 floats, from embedding_vectors)       │
    │                                                      │
    │ similarity = dot(query, stored) /                     │
    │              (||query|| × ||stored||)                  │
    │                                                      │
    │ Since both are unit vectors (L2=1):                   │
    │ similarity = dot(query, stored)                       │
    │                                                      │
    │ Top-K by similarity score DESC                        │
    └──────────────────────────────────────────────────────┘
```

## 10. File Structure

```
    ~/Library/LaunchAgents/
    ├── com.freepeak.llama-embed.plist      # llama-server :9101
    ├── com.freepeak.leankg-serve.plist     # leankg serve :9699/:9700
    └── com.freepeak.leankg-writer.plist    # leankg writer (file watcher)

    ~/.local/bin/
    ├── leankg                              # main binary
    ├── leankg-embed                        # embed pipeline binary
    ├── leankg-scan-repos                   # scan ~/work/ for repos
    └── leankg-serve-wrapper                # wrapper: scan + serve

    ~/.dsh/
    ├── AGENTS.md                           # auto-import instructions
    ├── cordis.patch.yml                    # MCP client config
    └── settings.yaml                       # DSH settings

    ~/work/harvey/freepeak/leankg/
    └── .leankg/
        ├── leankg.db                       # SQLite (211 MB)
        ├── leankg.db-shm                   # WAL shared memory
        ├── leankg.db-wal                   # WAL log
        ├── config.json                     # project config
        ├── memory/                         # memory banks
        ├── embed.lock                      # embed mutex
        └── watch.lock                      # writer mutex

    ~/logs/
    ├── leankg-serve.log                    # serve output
    ├── leankg-writer.log                   # writer output
    └── llama-embed.log                     # embedding server output
```
