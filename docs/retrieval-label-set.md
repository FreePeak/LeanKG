# Retrieval label set — the questions the fusion is measured against

Ground truth is the `qualified_name` that actually answers the question, read by
hand from the source. A label counts only if the element is in the graph, so
this file is also a check that the indexer found the thing.

Every question is phrased the way an agent phrases it, NOT as the identifier
alone: an agent that already had `Store.Space` would call `action=exact`. The
set exists to measure the L3 fusion, so a label must not be answerable at L1.

## Method

```
go build -o <scratch>/leankg ./cmd/leankg
LEANKG_EMBED_PROVIDER=... LEANKG_EMBED_BASE_URL=... LEANKG_EMBED_MODEL=... \
LEANKG_EMBED_DIMS=... \
  python3 scripts/retrieval-bench.py --binary <scratch>/leankg --project <the indexed checkout>

The embed endpoint is configured only through the LEANKG_EMBED_* env vars of the
served binary (provider, base URL, model id, dims, API key where needed); the bench
does not pick an endpoint of its own.
`LEANKG_EMBED_MODEL` must be the **catalog id** (for example `embeddinggemma-2` or
`bge-small-en-v1.5-384`), not the `.gguf` file name the
sidecar serves. An id the catalog does not know gets a different
`model_id`/`revision`, the stamp lookup finds no collection, and every query
silently degrades to the keyword rung — which reads as "the vector arm found
nothing" when the vector arm never ran. The bench stops on that now.

`--project` must be the checkout the store was **indexed from**, not the worktree
this script lives in: a store with no elements answers everything with an empty
list, which reads as every label being stale.
```

Reports four arms over the same questions: `semantic` (the order the server
returned, `action=semantic`, top 10), `grep_element` (`rg -F` per question word;
each matching line credited to the innermost candidate element, containers over
200 lines and file/doc/module kinds excluded), `grep_element_names` (the same
candidates ranked by question words in the element name, then body words) and
`grep_file` (the same matches ranked per file).
Each arm gives `Hit@1/3/5/10` and `MRR@10` as counts over one denominator: the
questions answered at rung L3 and not STALE. A question at another rung, or a
STALE label, is listed and excluded from every arm.

The label file quotes every question verbatim, so any hit in it is the question
echoing itself. `docs/retrieval-label-set.md` and the `--labels` JSON file are
dropped from every arm before ranking, and the run prints how many hits each arm
lost. A dropped semantic hit frees its slot, which is refilled from the next
ranks; the top ranks keep the server's order.

## Only extractable symbols are labelled

The indexer's Go extractor emits types, functions, methods and consts it can see;
unexported helpers, interface methods and named constants are **not**
`code_elements` rows, so a label pointing at one could never be retrieved by any
arm and would measure nothing but the indexer's extractor. Every label below was
checked against the live store with `select count(*) from code_elements where
qualified_name=...` — a label that returns 0 is replaced, not tolerated.

The bench now CHECKS this itself: a label whose element the store does not hold
is reported as **STALE** rather than as a retrieval miss, because it measures the
label and not the fusion. Two labels were stale when the check first ran on
today's tree — `StandaloneDBPath` had moved to `internal/projectcfg`, `ftsQuery`
to `internal/store` — which is exactly the drift it exists to catch. A third
check stops the run when the *vector arm never ran at all* (a stamp-degraded
query), because that reads as a retrieval finding and is an environment mistake.

## Where the answers were

Each label is annotated with the file and line range, so a disputed label is
checkable rather than arguable.

## Labels

| # | question | ground-truth qualified_name | where |
|---|---|---|---|
| 1 | reciprocal rank fusion of ranked lists | `internal/memory/dense.go::fuseRRF` | dense.go |
| 2 | the weighted variant of rank fusion | `internal/store/pg_fts.go::FuseRRFWeighted` | pg_fts.go |
| 3 | the Store constructor that opens sqlite | `internal/store/store.go::Open` | store.go |
| 4 | report how many bytes the freelist holds | `internal/store/maintenance.go::Store.Space` | maintenance.go |
| 5 | hand free pages back one page at a time | `internal/store/maintenance.go::Store.IncrementalVacuum` | maintenance.go |
| 6 | adopt incremental vacuum in the header | `internal/store/maintenance.go::Store.ensureIncrementalVacuum` | maintenance.go |
| 7 | resolve the configured sqlite store path | `internal/projectcfg/projectcfg.go::StandaloneDBPath` | projectcfg.go |
| 8 | list the files the indexer would take | `internal/index/walk.go::SupportedFiles` | walk.go |
| 9 | the vector rung of the retrieval ladder | `internal/core/core.go::Engine.rungSemantic` | core.go |
| 10 | freshness label from watermark and snapshot | `internal/store/backend.go::Freshness` | backend.go |
| 11 | find one element by exact identifier | `internal/store/elements.go::Store.FindExact` | elements.go |
| 12 | take the single-flight lock for embedding | `cmd/leankg-embed/main.go::lock` | main.go |
| 13 | the one tool that opens a project store | `internal/projects/projects.go::Config.open` | projects.go |
| 14 | resolve a short name to a qualified name for graph verbs | `internal/core/core.go::Engine.resolveGraphSeed` | core.go |
| 16 | how a batch that fails validation is handled | `internal/embed/provider.go::validateBatch` | provider.go |
| 17 | hash one element's content for the embed state | `internal/embed/provider.go::contentHashHex` | provider.go |
| 18 | compose the model stamp for a provider | `internal/embed/run.go::StampOf` | run.go |
| 19 | which arm does a weighted fusion read its weight from | `internal/store/pg_fts.go::FuseRRFWeights.armWeight` | pg_fts.go |
| 20 | the fraction of elements that hold vectors | `internal/embed/run.go::coverage` | run.go |
| 21 | where the memory search reads from | `internal/store/elements.go::ftsQuery` | search.go |
| 22 | rank a single arm's list of document keys | `internal/store/pg_fts.go::RankList` | pg_fts.go |
| 23 | which type is one fused result row | `internal/store/pg_fts.go::FusedHit` | pg_fts.go |
| 24 | cut a string to n runes for the sidecar budget | `internal/embed/run.go::truncateRunes` | run.go |
| 25 | the keyword search over the FTS index | `internal/store/elements.go::Store.FindFuzzy` | elements.go |
| 26 | the pressure plate that runs vacuum on a ticker | `internal/maintain/maintain.go::Pass` | maintain.go |
| 28 | the graph traversal that returns a blast radius | `internal/graph/graph.go::Impact` | graph.go |
| 30 | truncate the text sent to a provider | `cmd/leankg/featureverbs.go::truncateRunes` | featureverbs.go |

## Interpreting a run

- **`in-pool 0` on both arms** is not a fusion problem. The element was never
  ranked by anybody; the fix is upstream (what text is embedded, or what the
  indexer extracted).
- **`in-pool` on one arm only, wrong rank** is a fusion problem, and the sweep
  in `scripts/retrieval-bench.py --sweep` measures whether any weight pair fixes
  it.
- **A tie at rank 1 across arms** is decided by arm order, not by meaning. See
  the v4.14.2 note in `docs/prd.md`: a same-arm-count tie implies an equal best
  rank, so there is no evidence-based tie-break available and the honest lever
  is a weight, not a sort.

## Wave 8: 60 generated labels (derivation stated, ground truth still hand-checkable)

Wave 7 left 6 misses out of 30 — too few to choose the next lever from. These 60
labels widen the sample. They are derived MECHANICALLY from each symbol's own doc
comment, so the derivation is auditable rather than asserted:

- one symbol per package, sampled with a fixed seed across all 226 packages
  that contain a documented Go symbol;
- the question is the doc comment's predicate with the function name removed —
  the way an agent phrases a question it read the comment for;
- filtered to questions of 4+ words that do not name the answer's own symbol
  (a label that hands over the answer measures nothing) and that are not
  code-moving meta text;
- every label verified to be a live `code_elements` row, and every symbol
  created BY this loop excluded, so the set does not measure the corpus the
  loop built.

Reproduce with `scripts/retrieval-bench.py`, which parses the tables below:

| # | question | ground-truth qualified_name | where |
|---|---|---|---|
| 31 | extracts Hilt modules, providers and injection relationships from Kotlin source | `internal/index/android_hilt.go::ExtractHilt` | internal/index/android_hilt.go |
| 32 | reports whether mapping identifier for occurrences uses is net-positive in tokens | `internal/compress/symbolmap.go::ShouldRegister` | internal/compress/symbolmap.go |
| 33 | is the metadata contract for workflow nodes | `internal/ontology/procedural.go::WorkflowMetadata` | internal/ontology/procedural.go |
| 34 | returns the number of live pooled clients | `internal/lsp/manager.go::Manager.Len` | internal/lsp/manager.go |
| 36 | is the token delta between the pre-truncation response and the enforced one | `internal/budget/tokens.go::Stats.SavedTokens` | internal/budget/tokens.go |
| 37 | is a function found by downward traversal, with the provenance of how it was reached | `internal/ontology/traversal.go::DiscoveredFunction` | internal/ontology/traversal.go |
| 38 | extracts the project element and dependencies from pom.xml | `internal/index/maven.go::ExtractMaven` | internal/index/maven.go |
| 39 | normalizes a possibly "./"-prefixed relative path | `internal/web/api_graph.go::stripDotSlash` | internal/web/api_graph.go |
| 40 | breaks an unbroken failure run | `internal/summarize/gate.go::gate.succeeded` | internal/summarize/gate.go |
| 41 | is Rust project_path.file_name(): the basename, or "unknown" when the path has no file name | `internal/federation/federation.go::serviceName` | internal/federation/federation.go |
| 42 | renders 16 random bytes as a v4 UUID string. crypto/rand.Read is documented never to fail | `internal/orgknowledge/orgknowledge.go::newUUIDv4` | internal/orgknowledge/orgknowledge.go |
| 43 | returns normalized entropy per line | `internal/compress/entropy.go::EntropyAnalyzer.LineEntropies` | internal/compress/entropy.go |
| 44 | returns the package directory of a repo-relative path | `internal/index/index.go::dirOf` | internal/index/index.go |
| 45 | opens the project store | `cmd/leankg/orgverbs.go::withOrgKnowledge` | cmd/leankg/orgverbs.go |
| 46 | carries either the resolved locations | `internal/lsp/bridge.go::ResolveResult` | internal/lsp/bridge.go |
| 47 | renders the H10/FR-PLG-8 usage buckets | `cmd/leankg/metrics.go::cmdDashboard` | cmd/leankg/metrics.go |
| 48 | pulls android:name="..." out of a tag's attribute text | `internal/index/android_manifest.go::manifestAndroidName` | internal/index/android_manifest.go |
| 49 | runs the H9 self-diagnosis suite | `cmd/leankg/main.go::doctorDeep` | cmd/leankg/main.go |
| 50 | renders the aggregate as the Rust MCP tool's JSON object | `internal/orgknowledge/servicecontext.go::Knowledge.ServiceContextJSON` | internal/orgknowledge/servicecontext.go |
| 51 | is what one pass did and how much it recovered | `internal/maintain/maintain.go::Result` | internal/maintain/maintain.go |
| 52 | resolves one org by id | `internal/store/pg_accounts.go::PGStore.OrgByID` | internal/store/pg_accounts.go |
| 53 | returns the cached entry for a path, if present | `internal/compress/sessioncache.go::SessionCache.Get` | internal/compress/sessioncache.go |
| 54 | builds the REST mux over an engine | `internal/rest/rest.go::Handler` | internal/rest/rest.go |
| 55 | is a thin indirection so tests can stub PATH lookups | `internal/langs/lookup.go::execLookPath` | internal/langs/lookup.go |
| 56 | purges orphaned relationship edges | `cmd/leankg/gcverb.go::cmdGC` | cmd/leankg/gcverb.go |
| 57 | is a project root probed for git context, implementing indexgate.GitProbe. Some roots | `internal/setup/gitworkspace.go::Workspace` | internal/setup/gitworkspace.go |
| 58 | parses a trimmed line as an ATX heading of level 1-4 | `internal/docindex/docindex.go::atxHeading` | internal/docindex/docindex.go |
| 59 | spawns the sidecar process and polls its /health endpoint until it reports ready | `internal/embed/sidecar.go::StartSidecar` | internal/embed/sidecar.go |
| 60 | lists the node files on disk as slug -> path | `internal/summarize/nodefile.go::existingSlugs` | internal/summarize/nodefile.go |
| 61 | canonicalizes the details map for hashing | `internal/store/store_audit.go::auditDetailsJSON` | internal/store/store_audit.go |
| 62 | reads JSONL trial rows, refusing the whole load when any row is unpinned or malformed | `benchmark/ab/harness.go::ParseTrials` | benchmark/ab/harness.go |
| 63 | is a handle to one project's SQLite store | `internal/store/store.go::Store` | internal/store/store.go |
| 64 | applies all pending migrations | `internal/store/pgmigrate.go::PGStore.Migrate` | internal/store/pgmigrate.go |
| 65 | is the direct CLI query path | `cmd/leankg/query.go::cmdQuery` | cmd/leankg/query.go |
| 66 | renders a unified diff between oldText and newText with the given context radius | `internal/compress/textdiff.go::UnifiedDiff` | internal/compress/textdiff.go |
| 67 | is the Rust query_incidents_for_service shortcut | `internal/orgknowledge/incidents.go::Knowledge.IncidentsForService` | internal/orgknowledge/incidents.go |
| 68 | is one L2 hit with its bm25 relevance score | `internal/store/elements.go::FuzzyMatch` | internal/store/elements.go |
| 69 | classifies like RoleForRequest but consults the DB-backed token store FIRST | `internal/auth/auth.go::RoleForRequestWithStore` | internal/auth/auth.go |
| 70 | is the fleet seam: enumerate the registered projects and read each one's state. Tests inject a stub | `internal/doctor/fleet.go::FleetSource` | internal/doctor/fleet.go |
| 71 | is the one definition of the savings percentage every compressor's EstimateSavings reports | `internal/compress/compress.go::savingsPercent` | internal/compress/compress.go |
| 72 | writes the five fixed rows of the Rust seed | `internal/metrics/metrics.go::seed` | internal/metrics/metrics.go |
| 73 | returns a hash-seeded unit-vector provider for tests and offline smoke ONLY | `internal/embed/deterministic.go::Deterministic` | internal/embed/deterministic.go |
| 74 | reads a catalog JSON file and validates it: concept ids must be non-empty and unique | `internal/ontology/ontology.go::LoadCatalog` | internal/ontology/ontology.go |
| 75 | returns the config file the writer owns for client | `cmd/leankg/connect.go::clientConfigPath` | cmd/leankg/connect.go |
| 77 | returns the FR-HEA-03 status-tool banner fields for a mega-graph | `internal/ontology/mega.go::MegaGraphBanner` | internal/ontology/mega.go |
| 78 | returns a refusal payload when the graph is a mega-graph, else nil | `internal/ontology/discover.go::RefuseFullScanIfMega` | internal/ontology/discover.go |
| 79 | renders the reason in the Rust variant vocabulary | `internal/indexgate/indexgate.go::Decision.String` | internal/indexgate/indexgate.go |
| 80 | registers a project, or refreshes the row keyed on Dir | `internal/store/pg_projects.go::PGStore.ProjectUpsert` | internal/store/pg_projects.go |
| 81 | returns the stdio command path: the current executable when resolvable, else bare "leankg" | `cmd/leankg/install.go::CurrentCommand` | cmd/leankg/install.go |
| 82 | reports the token delta between two strings | `internal/compress/gitdiff.go::GitDiffCompressor.EstimateSavings` | internal/compress/gitdiff.go |
| 83 | is a no-op: auto_vacuum is a Postgres-wide setting, not a per-database one this client can turn on safely | `internal/store/maintenance_pg.go::PGStore.EnsureIncrementalVacuum` | internal/store/maintenance_pg.go |
| 84 | extracts R.<type>.<name> and getString/getText resource references from Kotlin source. Results are deduped on | `internal/index/android_resrefs.go::ExtractResourceRefs` | internal/index/android_resrefs.go |
| 85 | resolves a plaintext bearer secret against the DB token store.  An unknown secret is | `internal/auth/tokens.go::Verify` | internal/auth/tokens.go |
| 86 | enumerates every indexed element | `internal/store/backend.go::Store.Elements` | internal/store/backend.go |
| 87 | joins the identifier parts of an objc method node. A method with parameters is a selector | `internal/tstree/tstree.go::objcSelector` | internal/tstree/tstree.go |
| 89 | is the LEANKG_WORKSPACE_DIR walk bound | `internal/setup/env.go::WorkspaceMaxDepth` | internal/setup/env.go |
| 90 | removes every leading occurrence of prefix | `internal/compress/signatures.go::trimStartAll` | internal/compress/signatures.go |

## Reading the wave-8 A/B

Measured with `scripts/retrieval-bench.py` on the same 84 labels, depth 100,
against two builds of the SAME source tree: `origin/main` (`c25141b`, none of
this loop's fixes) and this branch. Both corpora were built from scratch with
their own binary and embedded from scratch with their own chunker, so the only
difference measured is the engine.

| | vector in-pool | keyword in-pool | fused in-pool | fused top-1 | fused top-10 |
|---|---|---|---|---|---|
| `origin/main` | 0/84 | 47/84 | 47/84 | 3/84 | 20/84 |
| this branch, full corpus | 55/84 | 47/84 | **65/84** | **8/84** | **34/84** |
| this branch, `scope=code` | 58/84 | 47/84 | **67/84** | **9/84** | 38/84 |

`origin/main`'s vector column is 0/84 because that engine never reaches L3 on
these questions (the wave-1 ladder defect) and never emits the per-arm
`ranks` the bench reads; the bench scores its served answer as the keyword arm,
which is exactly what it returns. The keyword column is unchanged across both
builds — 47/84 — which is the control: the keyword arm was not what this loop
changed, and the fused gain is not an artefact of the label set.

17 labels still miss in both arms. They are the questions whose text shares
almost nothing with the answer's symbol name (`cmdGC` vs "purges orphaned
relationship edges", `coverage` vs "the fraction of elements that hold
vectors"), and no arm built on names or keywords reaches them. That is the
model's vocabulary, and it is the honest boundary of the code-side levers.

## SQLite versus PostgreSQL under the 3:1 default

Same source snapshot, same 11,392 stored vectors (one embedding model, 512-d)
imported into both engines, the same 84 labels, served depth 10, default
`vector:keyword` = 3:1. The grep arms read the SQLite file in both runs.

| served semantic answer | SQLite | PostgreSQL |
|---|---|---|
| Hit@1 | 44/84 | 47/84 |
| Hit@3 | 67/84 | 64/84 |
| Hit@5 | 72/84 | 69/84 |
| Hit@10 | 77/84 | 75/84 |
| MRR@10 | 0.671 | 0.679 |

The net gap is small, but the served rank of 32 of the 84 labels differs between
the engines, in both directions. Two structural differences produce it:

- **Keyword side.** PostgreSQL runs two lexical lists, tsvector and trigram, each
  at weight 1 (`internal/store/pg_fts.go:465-508`). SQLite runs one FTS5 list
  (`internal/store/hybrid_sqlite.go:83-103`). A row that both PostgreSQL lexical
  arms rank first scores 2/61 under 3:1, which outranks vector ranks up to about
  31. The single SQLite keyword rank 1 scores 1/61 and only reaches vector rank
  about 123, beyond the hybrid window.
- **Vector side.** The PostgreSQL vector arm is HNSW with pgvector's default
  `hnsw.ef_search` of 40 (`internal/store/store_pg.go:654`, no `SET` in the
  store). The hybrid window is 50 (`internal/store/pg_fts.go:68-73`), and a
  `LIMIT 50` query on that index returns 40 rows, so the PostgreSQL vector list is
  truncated at 40. SQLite ranks every stored vector exactly
  (`internal/store/vectors.go:439-450`).

Neither difference was fixed here: the measured PostgreSQL MRR is not lower than
SQLite, and the ef_search change touches every PostgreSQL vector query.
