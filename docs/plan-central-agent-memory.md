# Plan: central agent memory (cross-repository recall for Claude Code sessions)

**Status:** Implemented in PRs #619 (sync, migrate), #620 (recall) and #621 (hook, install, docs); see section 12 for slice status
**Date:** 2026-11-27
**Owner:** maintainer
**Tracks:** `CAM-00..CAM-20` (rows in `docs/prd-task-tracker.md`, added by CAM-01; the prefix does not occur under `docs/` before this wave)
**Evidence pin:** every `file:line` below was read first-hand from the checkout of `origin/main` reported as commit `15221142` (the commit id itself is UNVERIFIED by this document; re-pin if `main` has moved).

This is the working plan for one wave. When the wave lands, its outcome moves into the PRD changelog and this file is archived under `docs/archive/` (same convention as `docs/plan-dashboard.md:6`).

---

## 1. Objective

### 1.1 Problem

Claude Code keeps auto-memory per repository under `~/.claude/projects/<project-slug>/memory/`. A preference or correction learned in one repository is invisible in every other one, because only the current repository's `MEMORY.md` index is loaded at session start. LeanKG already has a memory layer with ranked recall, redaction and an injection block, but nothing connects it to Claude Code's files, and its "global" mode is only partly global (section 4, rows 6 and 7).

### 1.2 Users

- Primary: one developer running Claude Code in many repositories on one machine.
- Later, gated on Open Question OQ-1: a team sharing one LeanKG server.

### 1.3 Goals

| ID | Goal |
|---|---|
| G1 | Memories of type `user` and `feedback` written in any repository are recallable from, and injected into, sessions in every other repository. |
| G2 | Memories of type `project` and `reference` are recallable from their own repository by default and from other repositories only on explicit request. |
| G3 | Claude Code keeps writing its native files. LeanKG is the authority for history, fed by hook write-through, with archive and restore (KD-12). The sync stays idempotent and rebuildable from the files at any time. |
| G4 | A session start never fails or stalls because LeanKG is absent, slow or broken. |
| G5 | Secrets and user-listed patterns never reach the central store. |
| G6 | The MCP surface stays at exactly four tools. |
| G7 | A developer can migrate all existing Claude Code memory into LeanKG in one verified, idempotent, non-destructive command (`leankg memory migrate`). |

### 1.4 Non-goals

- Writing back into Claude Code's memory files, or any two-way sync.
- A file watcher or daemon (see KD-5).
- Mixing memory hits into code search results (see KD-3).
- Graph edges for `[[links]]`. Links are stored as metadata only.
- Team mode in the first release. It is specified as a gated module (`team-store`) and not built until OQ-1 is answered.
- Other agent clients. The source adapter is Claude Code only.
- Custom `autoMemoryDirectory` locations (OQ-5).

---

## 2. Assumptions

| # | Assumption | Status |
|---|---|---|
| A1 | The memory directory is `~/.claude/projects/<project>/memory/`, where `<project>` is derived from the git repository, so worktrees and subdirectories share one directory. Outside a git repository the project root is used. | Verified in the Claude Code documentation (`memory` page, read 2026-11-27). |
| A2 | The exact `<project>` string is the main repository root with every character outside `[A-Za-z0-9]` replaced by `-` (controlled experiment, 2026-11-28; on macOS `/tmp` becomes `/private/tmp`). Claude Code truncates names over 200 characters and appends a hash (observed in the installed binary, version 2.1.329; not documented by Claude Code); LeanKG matches the first 200 characters. | Supported, not verified for linked worktrees (2026-11-27): no linked-worktree session with a memory directory was observed, so the main-repository slug is the expected name. `ProjectKey` tries the main-root slug first, then the directory's own slug. CAM-00 step (a) stays open. |
| A3 | A memory file is Markdown with YAML frontmatter carrying `name`, `description` and a type (`user`, `feedback`, `project`, `reference`) either as top-level `type` or nested under `metadata`. `MEMORY.md` is an index, one line per memory. | Verified on a real install (2026-11-27): all 274 memory files across 160 project directories have top-level `name`, `description`, `metadata`, with `type` nested under `metadata`; none has a top-level `type` or lacks frontmatter. Extra nested keys (`originSessionId`, `node_type`, `modified`) are ignored. |
| A4 | At session start Claude Code loads the first 200 lines or 25 KB of the current repository's `MEMORY.md` and no topic files. | Verified in the Claude Code documentation (`memory` page). |
| A5 | Plain stdout of a `SessionStart` command hook is added to context. Each output string is capped at 10,000 characters; beyond that only a 2,000-character preview is shown. A non-zero exit does not block the session. The default command timeout is 600 s and a per-hook `timeout` field exists. | Verified in the Claude Code documentation (`hooks` page, read 2026-11-27). Verified end to end on 2026-11-27 with Claude Code 2.1.329: the stdout of a synchronous `SessionStart` command hook was visible to the model (a one-shot probe printed a random token and the model repeated it). |
| A6 | Output of an `async: true` hook is not added to context. | UNVERIFIED for async hooks (the documentation read did not state it). Verified end to end on 2026-11-27 with Claude Code 2.1.329 for synchronous hooks: the stdout of a synchronous `SessionStart` command hook was visible to the model (a one-shot probe printed a random token and the model repeated it). The memory hook is therefore synchronous. |
| A7 | `CLAUDE_PROJECT_DIR` is set for hook commands and for stdio MCP servers; the hook also receives `cwd` and `session_id` as JSON on stdin. | Verified in the Claude Code documentation (`hooks` page). |
| A8 | One developer, one machine. Every reader and writer of the central store runs as the same OS user. | Agreed for the first release. |
| A9 | Volume is small: up to a few thousand memory files across up to a few hundred repositories, each file under 64 KiB. | Assumed. Drives the choice of a file-backed store with a linear vector scan. |
| A10 | The central root is `~/.leankg/memory`, the same directory `serve --memory-global` uses. | Follows from `internal/memory/memory.go:88-93`. |

---

## 3. Key decisions

Each decision lists the alternatives considered. KD-2, KD-3, KD-5, KD-6 and KD-7 refine the direction agreed before this plan; the reason is stated in each.

### KD-1: Claude Code's files are canonical; LeanKG is derived

**Superseded by KD-12** where it says LeanKG is derived and one-way. Claude Code's native files still load `MEMORY.md` as described below; the `cc-` banks are now the authority for history.

Agreed at the time. `MEMORY.md` is loaded natively with no tool call, while a tool-based store only helps when the model calls it. Alternative rejected: LeanKG as the write target.

### KD-2: The first central store is the existing file-backed memory root, not a new database table

The agreed direction was a Postgres-backed store with a `project` column on `knowledge_entries`, on both engines. Reading the code showed the memory package already provides what that table would have to rebuild:

- a global root (`cmd/leankg/main.go:409`, `:541`);
- document upsert and delete by id (`internal/memory/banks.go:415-443`, `:447-467`);
- tags and tag-filtered recall (`internal/rest/hindsight.go:273-296`);
- BM25 plus dense recall fused by reciprocal rank (`internal/memory/bankindex.go:213-299`, `internal/memory/dense.go:282-310`);
- write-side and read-side redaction (`internal/memory/memory.go:442-470`);
- a budgeted injection block (`internal/memory/banks.go:868-920`);
- telemetry feeding the dashboard (`internal/memory/telemetry_hook.go:72-158`).

Decision: the single-machine release stores synced memories in dedicated banks of the central memory root. It needs no schema change on SQLite or Postgres. A store-backed table is specified for team mode only (`team-store`, section 6.7), which is where a database is actually required: durability across container restarts, concurrent writers, and per-user ownership.

| Alternative | Why not (now) |
|---|---|
| `project` column on `knowledge_entries` | The table lives in each per-project store (one SQLite file or one Postgres schema per project, `internal/store/store_pg.go:86`, `:144-154`), so it is not central. Its rows are human notes anchored on an element, feature and environment (`internal/store/schema.go:254-272`) with a promotion flow that moves rows between environments (`internal/orgknowledge/notes.go:124-150`); derived, bulk-deleted agent memories have a different lifecycle. It has no owner, no content hash and no vectors. |
| Dedicated store table on both engines in the first release | Duplicates the bank store for no single-machine benefit and makes a Postgres server a prerequisite for a local feature. |
| Index the Claude Code memory tree as a LeanKG project through `docindex` | Memory rows would become code elements: they would appear in code search and the graph, and scoping by memory type would need element filters that do not exist. |

Postgres is deferred, not dropped. If the maintainer runs the self-host on Postgres and wants memory there in the first release, `team-store` can be pulled forward; its seam is described in section 6.7.

### KD-3: Reuse the memory layer's own dense arm; do not write memory vectors into `embedding_vectors`

The agreed direction was to embed memory entries into `embedding_vectors`. Bank rows are already embedded lazily into `memory_vectors` and fused with BM25 (`internal/memory/search.go:48-50`, `internal/memory/dense.go:165-210`, `:214-278`). The store's vector tables are keyed by code-element qualified name (`internal/store/store_pg.go:668-680`), the embed pipeline enumerates `st.Elements` (`internal/embed/read.go:42-52`), and the maintenance sweep deletes vectors whose qualified name has no element (`internal/store/roles.go:175-182`), so memory vectors written there would be collected as orphans. Alternative rejected: making memories code elements (see KD-2, third row).

### KD-4: Bank layout and identifiers

- `cc-global` holds every `user` and `feedback` memory from every repository.
- `cc-p-<16 hex of sha256(project slug)>` holds the `project`, `reference` and untyped memories of one repository.
- Document id and row id are `ccm-<24 hex of sha256(slug + NUL + file name)>`.

Hashes are used because bank names are cut at 64 characters (`internal/memory/banks.go:77-94`), so long slugs with a common prefix would collide, and because row ids and document ids pass through redaction (`internal/memory/memory.go:465-470`): a readable id containing a word such as `token` followed by `:` matches the key-value rule (`internal/telemetry/redact.go:48`) and several documents would collapse into one id. Alternatives rejected: reuse the per-directory bank from `BankName` (it needs the working directory, not the slug, and would mix curated memories with transcript rows); reuse `leankg-shared` (a rebuild could not drop the bank wholesale).

### KD-5: Sync runs at session start, inside the hook; no watcher

**Superseded in part by KD-12.** The session-start sync and the no-watcher decision stand. The sync now also runs on PostToolUse (Write or Edit of a memory file) and SessionEnd, which narrows the staleness described below.

A memory written in session A matters to session B only when B starts, and B's `SessionStart` hook runs a bounded sync before it injects. Alternatives rejected: an `fsnotify` watcher (`internal/obsidian` has one, but it means a long-lived process); a `Stop` hook (adds a second hook for no extra freshness). Consequence: a long-running session's on-demand recall can miss memories written elsewhere since the last session start on the machine. `leankg memory sync` closes the gap manually.

### KD-6: The hook injects only what Claude Code does not load itself

The agreed direction was "top-N relevant memories (current repository plus global)". Two facts change the shape. The current repository's index is already in context (A4), so re-injecting it spends tokens on duplicates. And `SessionStart` carries no prompt, so there is nothing to rank against. Decision: the hook injects the newest global-type memories that originate from other repositories. Relevance-ranked recall, including the current repository's own memories, is on demand through the MCP command in KD-7. Alternative deferred: a `UserPromptSubmit` hook that ranks against the prompt (OQ-3).

### KD-7: One new read command inside the existing `query` tool; no MCP write path for derived banks

`query {action:"memory", args:{command:"agent_recall"}}` is added to the existing sub-command registry (`internal/core/actions.go:169-176`). No tool is added (the registry is pinned at four, `internal/core/core.go:45-50`). The agreed "expose the global root over MCP" is met on the read side. The write side is Claude Code's own file write followed by sync: an MCP write into a derived bank would create rows with no canonical file, which the next sync would have to delete. Existing commands, including `session_retain` with `scope=global`, are unchanged.

### KD-8: `agent_recall` is server-level, not project-routed

On a multi-project server a call must name a registered project or it is refused (`internal/mcp/server.go:143-168`). A session in a repository LeanKG has never indexed must still recall its memories. Decision: for this one command the MCP layer answers from the default engine, and `project` or `args.cwd` is used only as the caller's repository identity. A caller whose token is restricted to some projects is refused for this command in the first release (fail closed). Alternative rejected: a dedicated user-scope stdio server per session, which opens a store and a memory tree inside every repository a session starts in (expected from `cmd/leankg/main.go:507-516` and `internal/memory/memory.go:100-110`; UNVERIFIED by running it).

### KD-9: Scope vocabulary for `agent_recall` is `project`, `global`, `all`

`project` (default) reads the caller's repository bank plus `cc-global`; `global` reads `cc-global`; `all` reads every `cc-` bank. Alternative rejected: the existing `per-project | global | per-project-tagged` vocabulary (`internal/memory/scope.go:34-44`). Its default is the project bank alone, which contradicts G1, and it has no cross-repository mode.

### KD-10: The deny-list masks; it extends the one existing redaction function

A user-maintained list of regular expressions is applied inside `memory.Redact` (`internal/memory/memory.go:460`), the function every memory write and read already passes through. Matches are replaced, not refused, so one hostname does not discard a whole memory. Alternative deferred: refuse the document (OQ-7).

### KD-11: Team mode uses a dedicated table with an owner column

See section 6.7. Not built until OQ-1 is answered.

### KD-12: Claude Code's files are the working copy; LeanKG is the authority, fed by hooks

Supersedes KD-1 and KD-5 where they describe a one-way derived mirror. Claude Code keeps writing its native files under `~/.claude/projects/<slug>/memory/`, so native loading is unchanged. LeanKG's `cc-` banks hold the history. Three hook events copy changes in: PostToolUse on a Write or Edit of a memory `.md` file (scoped to that project, `internal/ccmemory/hook.go:114-118`, `:128-145`), SessionEnd (the session's project, `hook.go:119-120`), and SessionStart (all projects, then injection, `hook.go:121-122`). A row whose file disappears is copied to `ccarchive` before removal (`internal/ccmemory/sync.go:27`, `:509-523`), so deleting a file loses nothing; `memory restore` rebuilds files from the banks (`internal/ccmemory/restore.go:64`). Alternatives rejected:

- Relocate the directory with `autoMemoryDirectory`: one path per settings scope, one shared `MEMORY.md` across projects, and symlinks are refused.
- An MCP save tool: a memory is lost when the server is down, and built-in saving would have to be turned off.
- A launchd watcher: a long-lived daemon for a change a hook already sees.

Limits. The PostToolUse trigger assumes Claude Code writes memory files through the Write and Edit tools. That is observed, not documented by Claude Code, so it is stated as an assumption. A change made by a shell command waits for the next SessionEnd, SessionStart or `memory sync`. The PostToolUse hook is async, so its copy can land after the tool returns. Excluded projects are listed in `~/.config/leankg/memory-exclude`, one absolute path per line; a relative line, `.`, `/` or `~` alone stops the run with the file and line number. The matcher compares the as-written slug and the symlink-resolved slug (for a nonexistent path, of its longest existing ancestor with the rest appended), and the scrub-masked form of each; every slug is capped at 200 characters and matched by prefix. Known limits: `CLAUDE_CODE_PROJECT_DIR_NAME` and Unicode normalisation of paths are not handled, and `ProjectKey` in `internal/ccmemory/identity.go:21` does not resolve symlinks. Their rows are never archived, and their archive rows are purged (`internal/ccmemory/exclude.go:67-122`, `internal/ccmemory/sync.go:420-446`).

---

## 4. Current state (verified)

| # | Fact | Evidence |
|---|---|---|
| 1 | The MCP registry is four tools: `import`, `query`, `status`, `authenticate`. | Names `internal/core/core.go:45-50`; registration `internal/mcp/server.go:457-534` |
| 2 | Actions are registries, not switches. `memory` is a query action and an import action; each has a sub-command map. | `internal/core/actions.go:92-122` (`:106`), `:125-136` (`:132`), `:142-163`, `:169-176` |
| 3 | Memory is file-backed: Markdown core files, topic files, JSONL banks, and a derived SQLite side index holding FTS5 rows, vectors and cursors. Neither store engine has a memory table. | `internal/memory/memory.go:7-13`, `:83-124`; `internal/memory/search.go:30-53`; search of `internal/store` for "memory" finds only a comment (`store.go:9`) |
| 4 | Bank row shape (`Entry`). | `internal/memory/banks.go:24-40` |
| 5 | A global root exists and is reachable: `serve --memory-global` opens `~/.leankg/memory`. Hosted mode refuses the flag. | `cmd/leankg/main.go:409`, `:541`; `cmd/leankg/hosted.go:27-28` |
| 6 | Routed projects on a multi-project server always open their own per-project memory, whatever the flag says. | `internal/projects/projects.go:775-787` (`:781`); also `internal/session/reflect.go:96` |
| 7 | A scope matrix exists (`per-project`, `global`, `per-project-tagged`) with a shared bank `leankg-shared`, and MCP exposes it on `session_retain`, `session_recall` and `memories`. The shared bank lives inside whichever root the engine opened, so with row 6 it is not shared across routed projects. | `internal/memory/scope.go:15`, `:20-28`, `:34-44`, `:60-77`; `internal/core/actions.go:928-946`; `internal/core/core.go:1637` (`SessionMemoryRead`; the `session_recall` case is at `:1649`); `internal/mcp/server.go:247` |
| 8 | Bank rows have a dense recall arm: lazy embedding (256 rows per recall), a calibrated noise floor, queries of three words or more, RRF with k = 60. | `internal/memory/dense.go:148`, `:154-161`, `:165-210`, `:214-278`, `:282-310`; wiring `internal/core/core.go:99` (`wireMemoryVectors`, called at `:130`) |
| 9 | Document upsert and document delete exist in the package. MCP exposes only the delete. REST exposes both on the Hindsight-compatible mount, with tag filters. | `internal/memory/banks.go:415-443`, `:447-467`; `internal/core/actions.go:961-983`; `internal/rest/hindsight.go:71-117`, `:178-187`, `:273-296` |
| 10 | Rows the client gave a document id are never merged by the write-time dedupe. Every row gets a `content_id` (hash of the normalized body). | `internal/memory/banks.go:486-489`, `:728-730`, `:745-758` |
| 11 | An injection block exists: whole entries only, entry and token limits, and a visible "N more hidden" marker. Defaults are 8 entries and 5,000 tokens. | `internal/memory/banks.go:868-920`; `internal/memory/scope.go:82-85`, `:124-151`; REST `internal/rest/rest.go:319-350` |
| 12 | Redaction: one write choke point, masking on read as well, fixed rules for keys, tokens and credentials, and the home directory rewritten to `~`. There is no user-configurable pattern list. | `internal/memory/memory.go:442-470`; `internal/telemetry/redact.go:12-49`, `:53-74`; search of `internal/telemetry/config.go` for "redact" finds nothing |
| 13 | Bank writes are serialized inside one process only. The source says two servers on one root share a JSONL file with no cross-process lock. | `internal/memory/memory.go:67-70`; `cmd/leankg/main.go:535-540` |
| 14 | Memory operations emit `recall`, `retain`, `delete` and `inject` events, and the dashboard has a Memory page with a per-bank table. | `internal/memory/telemetry_hook.go:72-158`; `ui-dashboard/src/pages/memory.tsx:18-26`, `:45-71` |
| 15 | `knowledge_entries` has 13 columns including `element_qualified`, `user_story_id` and `feature_id`; no project column. Both engines index it for keyword search. | `internal/store/schema.go:254-272`, `:444-491`; `internal/store/pgmigrate.go:238-256`, `:376-381` |
| 16 | Knowledge writers are `AddNote` and `PutAnnotation`. The CLI can write and list notes. No MCP action reads notes. Delete is by id. | `internal/orgknowledge/notes.go:16-46`, `:54-81`; `cmd/leankg/orgverbs.go:180-231`; `cmd/leankg/exportverbs.go:320`; `internal/store/store_orgknowledge.go:272-277`; `internal/store/pg_orgknowledge.go:261` |
| 17 | Multi-project serving: `LEANKG_PROJECT_DIRS`, a `project` argument, fail-closed refusal when it is missing, one Postgres schema per project directory. A cross-project read exists for the graph (`query action=portfolio`), not for memory. | `cmd/leankg/main.go:606`; `internal/projects/projects.go:35-45`; `internal/mcp/server.go:126-169`; `internal/store/store_pg.go:86`, `:95`, `:144-154`; `internal/core/portfolio.go:56-62` |
| 18 | Code embeddings: per-model pgvector tables with an HNSW cosine index; default model `bge-small-en-v1.5-384`; providers `local`, `openai`, `deterministic`; search fusion weights 3:1 with k = 60. | `internal/store/store_pg.go:668-680`; `internal/embed/catalog.go:44`; `internal/embed/config.go:23-25`; `internal/core/core.go:983-997`; `internal/store/pg_fts.go:109-113` |
| 19 | Re-index deletes code elements and their relationships by file path only. | `internal/store/elements.go:264-283`; `internal/store/store_pg.go:314-331` |
| 20 | Transports and auth: `serve` flags; roles Viewer, Contributor, Admin; write tools need Contributor; bearer tokens table; login token lifetime 24 h; a non-loopback REST bind without a token is refused; the container image serves `--read-only --ui`. | `cmd/leankg/main.go:397-410`; `internal/auth/auth.go:22-31`, `:147-152`; `internal/store/schema.go:139-150`; `internal/mcp/authenticate.go:15`; `cmd/leankg/servebind_test.go:19-32`; `Dockerfile:59` |
| 21 | The repo already knows where Claude Code keeps things and how to write its hooks: projects root and slug (both unexported), and a writer for a `SessionStart` hook in `~/.claude/settings.json`. | `internal/sessionlink/claudecode/claudecode.go:31-37`, `:46-49`; `cmd/leankg/connect.go:426-434`, `:462-467`, `:548-602`; `cmd/leankg/install.go:16` |
| 22 | Reusable helpers: a frontmatter splitter and a `[[link]]` extractor (both unexported), an advisory file lock, and `yaml.v3` and `fsnotify` as dependencies. | `internal/obsidian/obsidian.go:208-232`; `internal/obsidian/sync.go:172-191`; `internal/flock/flock.go:21-29`; `go.mod:7`, `:14` |
| 23 | There is no `memory` CLI verb, and the `query` CLI verb does not attach a memory layer. | `cmd/leankg/main.go:64-129`; search of `cmd/leankg/query.go` for "memory" finds nothing |
| 24 | Migration ledgers: SQLite is at 20, Postgres at 19; they have diverged at 19 and 20; the stated convention is one number per step on both. | `internal/store/schema.go:405-407`, `:422-423`, `:444`, `:465`; `internal/store/pgmigrate.go:475` |
| 25 | The dual-engine gate, its fixture and CI teardown. | `Makefile:89-90`; `scripts/test-dual-engine.sh:36`, `:45`; `scripts/pgvector.compose.yml:20-23`, `:46-47`; `.github/workflows/ci.yml:82-83`, `:100`, `:108-110` |
| 26 | The tool contract document is generated and guarded by a drift test. | `docs/mcp-tool-contract.md:1-7`; `internal/mcp/contract_doc_test.go:86-112` |

---

## 5. Capability map

| Module id | Responsibility | Depends on |
|---|---|---|
| `cc-source` | Locate Claude Code memory directories, parse one file into a document, derive project keys, bank names and document ids. Pure, no writes. | none |
| `mem-scrub` | User deny-list applied inside the memory layer's redaction function. | none |
| `mem-bank-ops` | Two small extensions of the memory package: batch document delete, and an injection entry point that takes explicit banks, a filter and a budget. | none |
| `cc-sync` | Diff source documents against the central banks and apply the result; cross-process lock; `leankg memory sync`; `leankg memory migrate` (one verified sync run, section 6.4a). | `cc-source`, `mem-scrub`, `mem-bank-ops` |
| `agent-recall` | Scoped, ranked recall over the central banks; the `agent_recall` MCP command; `leankg memory recall`; central-root handle for the server. | `cc-source`, `mem-bank-ops` |
| `cc-hook` | `leankg memory hook` (bounded sync, then injection, always exit 0); hook registration; telemetry from the CLI paths. | `cc-sync`, `mem-bank-ops` |
| `team-store` (gated on OQ-1) | Store-backed memory table on both engines, owner column, remote push. | `cc-sync`, `agent-recall` |

There are no cycles. Build order: `cc-source`, `mem-scrub`, `mem-bank-ops`, `cc-sync`, `agent-recall`, `cc-hook`, then the gate, then `team-store`.

### 5.1 New surface and what was checked first

| New item | Existing thing checked | Why it is not enough as it stands |
|---|---|---|
| Exported `ProjectsRoot` and `Slug` in `internal/sessionlink/claudecode` | `root` and `slug` (`claudecode.go:32-37`, `:47-49`) | They already do the job; they are only unexported. Export, do not copy. |
| Leaf package `internal/mdmeta` holding the frontmatter splitter and link extractor | `splitFrontmatter` (`obsidian.go:208-232`), `wikiLinks` (`sync.go:172-191`) | Moved, not rewritten. Importing `internal/obsidian` from the memory path would pull in the store for two string functions. `obsidian` calls the moved functions. |
| Package `internal/ccmemory` (parse, identity, sync, recall, hook rendering) | `internal/memory` (generic bank store), `internal/sessionlink/claudecode` (transcripts only), `internal/obsidian` (vault of element notes) | None of them knows the memory file format or the type-to-scope rule. The package owns only that knowledge and calls the memory package for all storage. |
| `memory.DeleteDocuments` | `DeleteDocument` (`banks.go:447-467`) | One rewrite of the bank per document. A removed repository can drop hundreds of documents; the batch form does one rewrite. `DeleteDocument` becomes the one-element call. |
| `memory.InjectBanks` | `(*Memory).firstTurn` (`scope.go:124-151`, returns five values), `InjectBlock` (`banks.go:868-871`) | `firstTurn` is tied to the scope and cwd matrix and fixed limits; `InjectBlock` records no inject event. The new method takes banks, filter and budget; `firstTurn` delegates to it so there is still one place that builds and records a block. |
| `memory.SetScrubPatterns`, `memory.LoadScrubFile` | `memory.Redact` (`memory.go:460`), `telemetry.Redact` (`redact.go:53-58`) | Rules are fixed in code. The list is applied inside `memory.Redact`, so every existing write and read path gets it with no new call sites. |
| `agent_recall` sub-command, `args.types`, `core.Options.AgentMemory` | `session_recall`, a case in `SessionMemoryRead` (`core.go:1637`, case at `:1649`), `queryMemoryArgs` (`actions.go:503-510`) | `session_recall` resolves banks from a cwd hash and has no type filter or cross-repository mode. The engine has one memory handle, per project for routed engines (section 4, row 6). |
| Routing exemption in `engineFor` | `engineFor` (`server.go:126-169`), `authenticate` (already server-level) | Fail-closed routing is right for graph questions and wrong for a store that is not per project (KD-8). |
| CLI verb `memory` with `sync`, `recall`, `hook` | Verb table (`main.go:64-129`); `query` verb | No memory verb exists, and `query` has no memory wiring. |
| `install --register-memory` | `RegisterCWD` (`connect.go:548-602`) | It owns exactly one command suffix. Its merge logic is parameterized by a hook spec; the index hook keeps its behavior. |
| Table `agent_memories`, role `AgentMemories` (gated) | `knowledge_entries` and the `Knowledge` role (`roles.go:267-296`) | See KD-2, first row. |

---

## 6. Modules

### 6.1 `cc-source`

**Scope.** Read-only knowledge of Claude Code's memory layout.

**Schema changes.** SQLite: none. Postgres: none.

**Types (package `internal/ccmemory`).**

| Field of `Doc` | Meaning |
|---|---|
| `Slug` | Directory name under the projects root. Identifies the repository. |
| `File` | Base name of the memory file. |
| `Name`, `Description` | From frontmatter. `Name` falls back to the file name without extension; `Description` falls back to the first non-empty body line. |
| `Kind` | `user`, `feedback`, `project`, `reference`, or `unknown`. |
| `Body` | File content after the frontmatter. |
| `Links` | Targets of `[[...]]` in the body, in document order, deduplicated. |
| `ModTime` | File modification time, Unix seconds. |
| `SourceHash` | See section 6.4. |

**Behavior rules.**

1. Projects root: `$CLAUDE_CONFIG_DIR/projects` when set, else `~/.claude/projects` (the exported `ProjectsRoot`).
2. A memory directory is `<root>/<slug>/memory`. Every `*.md` directly inside it is a document except `MEMORY.md`, which is an index of the others and is skipped. Subdirectories are not read. Symlinks that resolve outside the projects root are skipped.
3. Kind comes from the frontmatter key `type`. The existing splitter cuts each line on the first colon and then trims the key and the value (`obsidian.go:224-226`), so it yields the same key for a top-level `type:` and for one indented under `metadata:`. Any other value, or no frontmatter, gives `unknown`. Frontmatter is parsed flat, so a nested `metadata:` with `type:` works as designed; a file with two `type` lines takes the last one.
4. `unknown` is treated as repository-scoped. A memory is never made global by default.
5. Files over 64 KiB are skipped and counted.
6. Known limit: multi-line YAML scalars in frontmatter are not parsed; the fallbacks in the table apply.
7. Project key for a directory `dir`. Candidates, in order: the slug of the main worktree root of `dir`, then the slug of `dir`. The first candidate that has a memory directory, or a project bank, wins. The main worktree root is found without running git: walk up to the nearest `.git`; a directory means that parent is the root; a file is a `gitdir:` pointer whose `commondir` file names the shared `.git`, and the root is that directory's parent. If nothing resolves, the slug of `dir` is used.
8. Bank and id derivation as in KD-4. Kinds `user` and `feedback` map to `cc-global`; everything else maps to the project bank.

**Failure modes.**

| Condition | Result |
|---|---|
| Projects root missing | Empty discovery with a flag `RootMissing`; never an error. |
| One file unreadable or malformed | That file is reported in `Skipped`; the rest proceed. |
| `.git` pointer unreadable | Fall through to the next candidate. |

### 6.2 `mem-scrub`

**Scope.** A user deny-list applied by `memory.Redact`.

**Schema changes.** SQLite: none. Postgres: none.

**Config.** File `<central root>/scrub.txt`. One Go regular expression per line; blank lines and lines starting with `#` are ignored. A match is replaced by `[SCRUBBED]`. Example lines use placeholders only, for instance `SERVICE_DNS_PLACEHOLDER` or `other-repo`.

**Behavior rules.**

1. `LoadScrubFile(path)` returns the compiled patterns and a digest of the file bytes. A missing file is an empty list, not an error.
2. `SetScrubPatterns` installs the list process-wide, the same shape as `SetRecorder` (`telemetry_hook.go:31-37`). `memory.Redact` applies the fixed rules first, then the list.
3. Because reads also pass through `Redact` (`memory.go:472-480`), a pattern added later masks rows already stored; the next compaction rewrites them (`banks.go:603-613`).
4. Masking is idempotent.

**Failure modes.**

| Condition | Result |
|---|---|
| A line does not compile | `LoadScrubFile` fails naming the line number. `leankg memory sync` exits non-zero before any write. The hook skips its sync step and still injects from what is already stored. |
| No file | The fixed rules still apply. |

### 6.3 `mem-bank-ops`

**Scope.** Two extensions of `internal/memory`, no behavior change for existing callers.

**Schema changes.** SQLite: none. Postgres: none. The side index layout is unchanged, so `indexVersion` (`bankindex.go:20`) is not bumped.

**API.**

| Function | Contract |
|---|---|
| `(*Memory).DeleteDocuments(ctx, bank, ids) (int, error)` | Removes every row whose document id is in `ids` (raw or redacted form) with one rewrite of the bank, and records one `delete` event with the count. An empty list is a no-op. `DeleteDocument` delegates to it. |
| `(*Memory).InjectBanks(ctx, banks, query, limit, tokenBudget, keep) (text, rows, injected, total, err)` | With a query: ranked recall filtered by `keep` before the limit. Without: newest first, filtered by `keep`. Renders through the existing block renderer and records one `inject` event. The capped `rows` are returned as well, because `firstTurn` needs them for byte-identical output. `(*Memory).firstTurn` (`scope.go:124`) calls it with the existing constants and a nil filter. |

**Failure modes.** Same as the functions they generalize: a cancelled context returns its error and records nothing; a missing bank is empty.

### 6.4 `cc-sync`

**Scope.** Make the central banks equal to the source files.

**Schema changes.** SQLite: none. Postgres: none.

**Row mapping (one file, one row).**

| `Entry` field | Value |
|---|---|
| `ID` | the document id (KD-4) |
| `Content` | `[<kind>] <name> - <description>` on the first line, then the body |
| `Source` | `claude-code-memory` |
| `Timestamp` | `ModTime` |
| `Metadata.document_id` | the document id |
| `Metadata.tags` | `cc`, `type:<kind>` |
| `Metadata.kind`, `.name`, `.description`, `.file` | from the document |
| `Metadata.project` | the slug |
| `Metadata.links` | list of link targets |
| `Metadata.source_hash` | `sha256(render version, deny-list digest, file bytes)` |

**Algorithm.**

1. Take the lock `<central root>/cc-sync.lock` through `internal/flock`: `flock.Lock` (`internal/flock/flock.go:27`) for `leankg memory sync`, which waits; the hook uses `flock.TryLock` (`internal/flock/flock.go:21`) once and skips its sync step if the lock is held.
2. Load the deny-list and install it.
3. Discover documents. Build the desired set keyed by `(bank, document id)` with its `source_hash`.
4. Build the actual set from every bank whose name starts with `cc-`, using the existing `Banks` and `List` reads (`internal/memory/read.go:117-156`).
5. Plan: upsert where the key is missing or the hash differs; delete where an actual key is not desired. A memory whose kind changed appears as a delete in one bank and an upsert in the other.
6. Apply per bank: one `RetainReplacing` call carrying all upserts of that bank, then one `DeleteDocuments` call.
7. `leankg memory sync` then runs `Compact`. The hook never compacts.

**Behavior rules.**

- Idempotency: a second run over unchanged files plans nothing, writes nothing and emits no event.
- Changing the deny-list or the render version changes every hash, so the next run rewrites every row.
- Deletion safety: if the projects root is missing or unreadable, or discovery finds no document while the central banks hold some, nothing is deleted and the run reports `deletes_withheld`. `--rebuild` is the only way to delete in that state.
- `--rebuild` removes every row from every `cc-` bank, then syncs from scratch.
- Only banks with the `cc-` prefix are ever read or written by sync.
- A cancelled context stops between banks; each bank's batch is complete or absent.
- Implementation notes (2026-11-27):
  - `--rebuild` skips deleting a document that is being upserted in the same bank.
  - Rows without a `document_id` cannot be removed by any existing API.
  - The sync JSON `projects` field is the projects root string.
  - An unreadable projects root returns an error, with deletes withheld.
  - A missing root gives `deletes_withheld` with exit 0.
  - The lock wait ignores context cancellation.
  - A dry run takes no lock and creates nothing.

**CLI.**

`leankg memory sync [--dry-run] [--rebuild] [--json]`

JSON result: `{"root", "projects", "documents", "upserted", "deleted", "skipped", "deletes_withheld", "banks": [{"bank", "project", "rows"}]}`. Exit 0 on success, 1 on error. `--dry-run` prints the plan and writes nothing.

**Failure modes.**

| Condition | Result |
|---|---|
| Central root cannot be created or opened | Error, exit 1, nothing written. |
| Lock held (hook mode) | Sync step skipped; injection proceeds from the stored rows. |
| Embedding provider absent | Irrelevant: sync never embeds. Vectors are created lazily at recall. |
| Crash mid-run | Bank rewrites are temp-file-then-rename (`banks.go:560-572`); the next run re-plans from the files. |

### 6.4a `leankg memory migrate`

**Scope.** One explicit, verified run that moves all existing Claude Code memory into the central file-backed memory root. It is a thin wrapper over the section 6.4 sync apply: same code path, no second implementation.

**Storage meaning.** "Storage" here is the central file-backed memory root (KD-2). Migrating into a Postgres-backed table remains the gated `team-store` scope (section 6.7).

**CLI.**

`leankg memory migrate [--project SLUG] [--dry-run] [--json]`

**Steps.**

1. Inventory: projects found, files per kind, how many documents go to `cc-global` and how many to project banks, and skipped files with reasons (over 64 KiB, symlink leaving the root, unreadable).
2. Apply through the sync path. It honours the deny-list and the withheld-deletes rule, and it never deletes on a missing or empty scan. `--project SLUG` limits the run to one project slug.
3. Verify: for every bank, rows equal the desired documents. Any mismatch exits 1 and is listed.
4. Report the inventory, the slug-to-bank map and the verification result.

**Behavior rules.**

- Source files are never modified.
- Re-running is idempotent and produces the same result as a normal `leankg memory sync`.
- `--dry-run` prints the inventory and the plan and writes nothing.
- Implementation notes (2026-11-27):
  - A dry run exits 0 with `verified` false, because nothing is verified. This is the exception to the exit rule above.
  - `--project <unknown slug>` exits 1 with "no memory found for project".
  - With `--project` and an empty source scope, deletes stay withheld.
  - The scope match uses the masked slug.

**JSON result.** `{"root","projects":[{"slug","bank","files","global","skipped"}],"documents","upserted","skipped","verified":true|false,"mismatches":[]}`. Exit 0 when verified, 1 otherwise.

### 6.5 `agent-recall`

**Scope.** Ranked recall over the central banks for a caller in any repository.

**Schema changes.** SQLite: none. Postgres: none.

**MCP contract.** Tool `query`, existing envelope.

| Field | Type | Meaning |
|---|---|---|
| `action` | string | `memory` |
| `query` | string | Required search text. |
| `limit` | integer | Default 8. |
| `project` | string | Optional. For this command it is the caller's repository identity, not a routing key (KD-8). |
| `args.command` | string | `agent_recall` |
| `args.cwd` | string | Optional absolute directory of the caller's repository. Used when `project` is absent. Default: the engine's project directory. |
| `args.scope` | string | `project` (default), `global`, `all`. |
| `args.types` | array of string | Optional filter over `user`, `feedback`, `project`, `reference`, `unknown`. |

Response:

| Field | Meaning |
|---|---|
| `command` | `agent_recall` |
| `scope` | the scope applied |
| `project` | the resolved project slug, or empty when the caller's repository has no project bank |
| `banks` | banks consulted, project bank first |
| `count` | number of rows |
| `memories` | rows: `id`, `name`, `kind`, `project`, `description`, `content`, `links`, `timestamp`, `score` |

The existing provenance stamp is added by the dispatcher (`actions.go:525`).

Errors:

| Condition | Error |
|---|---|
| Empty `query` | names the missing argument |
| Unknown `scope` or type | lists the valid values; never a silent default |
| Central root absent | says to run `leankg memory sync` once |
| Caller's token is restricted to some projects | permission denied; no bank names in the message |

**Backward compatibility.** `session_recall`, `memories`, `view`, `snapshot`, `search` and every `import action=memory` command are unchanged. The "unknown memory read command" message gains one name (`actions.go:515`). The `query` schema text gains the command, so `docs/mcp-tool-contract.md` is regenerated. The tool count stays four.

**Behavior rules.**

1. Banks by scope as in KD-9. A caller with no project bank gets `cc-global` only under `project`, with `project` empty in the response so the answer is not mistaken for "this repository has no memories that match".
2. Ranking is the existing recall: BM25 over OR-ed terms with stopwords dropped, fused with the dense arm when an embedder and a calibrated floor are available, newer row first on ties (`bankindex.go:256-288`). The type filter is applied before the limit.
3. Rows with the same `content_id` are collapsed to the best-ranked one, so a preference saved in several repositories is returned once.
4. The server holds one handle on the central root. It is the `--memory-global` handle when that flag is set. Otherwise it is opened lazily on the first call, and only if `<central root>/index.db` exists (the same existence probe as `main.go:897-899`), so a server never creates the central tree by answering a read. The dense arm is wired with the default engine's embedder and calibration, as in `core.go:99`.
5. Role: Viewer, like every `query` call (`auth.go:147-152`).
6. Implementation notes (2026-11-27):
   - `agent_recall` is wired on every non-hosted server, not gated on `--memory`.
   - Hosted mode wires no central handle and answers "not configured" (central memory is the operator's personal memory).
   - Callers with project grants or an org scope are refused.
   - `?project=` URL handlers do not serve it (known limit).
   - `args.types` is accepted only by `agent_recall`.
   - `content_id` hashes the whole rendered row (header line plus body), so identical memories collapse only when name and description match too.
   - CLI recall is keyword-only.

**CLI.** `leankg memory recall <query> [--scope project|global|all] [--type KIND] [--limit N] [--dir DIR] [--json]` calls the same function. It is keyword-only unless an embedding endpoint is configured.

**REST.** No new route. `POST /api/v1/query` (`internal/rest/rest.go:55`) on the default project serves the command because it shares the core dispatcher.

### 6.6 `cc-hook`

**Scope.** The `SessionStart` integration.

**Schema changes.** SQLite: none. Postgres: none.

**CLI.** `leankg memory hook`. Reads the hook JSON from stdin (`cwd`, `session_id`). Empty stdin runs as SessionStart; valid JSON without a usable `cwd` falls back to `CLAUDE_PROJECT_DIR`, then the working directory.

**Behavior rules.**

1. Overall deadline 3 s, including the telemetry flush, which gets only the time left. Budget for the sync step 2 s.
2. Step 1, sync as in section 6.4 with a non-waiting lock (`flock.TryLock`, `internal/flock/flock.go:21`).
3. Step 2, inject. Banks: `cc-global`. Filter: rows whose `project` differs from the caller's resolved slug. Order: newest first. Limits: `memory.RecallLimit` entries (8, `internal/memory/scope.go:83`) and 1,500 tokens (about 6,000 bytes). This stays under the 10,000-character cap in A5; the existing default of 5,000 tokens would not.
4. Output: one context line, the block from `InjectBanks`, one hint line (section 7.3). Nothing is printed when there is nothing to inject.
5. The process always exits 0. Any error is one line on stderr.
6. When telemetry consent is on, the sync and inject events carry the hook's session id, so the dashboard Memory page shows the `cc-` banks. With consent off nothing is recorded (`telemetry_hook.go:41-47`). The hook uses the existing `TransportStdio` constant and records only when consent is on.
7. Empty stdin is a SessionStart run with the fallback directory. Stdin that is not one JSON object, is cut off at 1 MiB, or is not complete within 500 ms does nothing and prints nothing (fails closed; `cmd/leankg/memoryhook.go`, `internal/ccmemory/hook.go` `ParseHookInput`).

**Registration.** `leankg install --target claude-code --register-memory` merges one entry into `hooks.SessionStart` of `~/.claude/settings.json`: a synchronous command hook with `timeout` 10 and no matcher. It preserves every other hook and is idempotent. This is a plain `settings.json` hook, so it works where plugin-provided hooks are skipped.

**Failure modes (all fail open).**

| Condition | stdout | Exit |
|---|---|---|
| Binary missing | Claude Code reports a hook error; the session starts | n/a |
| Central root absent and no memory files anywhere | empty | 0 |
| Side index locked or rebuilding past the deadline | empty | 0 |
| Deny-list invalid | block from stored rows; sync skipped | 0 |
| stdin empty | block for the fallback directory | 0 |
| stdin not one JSON object, over 1 MiB, or late (500 ms) | empty | 0 |
| Projects root unreadable | block from stored rows | 0 |

### 6.7 `team-store` (gated on OQ-1; specification only)

**Scope.** The same derived memory, stored in the home store of a shared server, with an owner per row. The home store is the store `serve` opens for its own project (`cmd/leankg/hosted.go:17-19`).

**SQLite (migration 21, sketch).**

```sql
CREATE TABLE IF NOT EXISTS agent_memories (
    id           TEXT PRIMARY KEY,
    owner        TEXT NOT NULL DEFAULT '',
    project      TEXT NOT NULL DEFAULT '',
    kind         TEXT NOT NULL,
    name         TEXT NOT NULL,
    description  TEXT NOT NULL DEFAULT '',
    content      TEXT NOT NULL,
    tags         TEXT NOT NULL DEFAULT '',
    source       TEXT NOT NULL,
    content_hash TEXT NOT NULL,
    created_at   INTEGER NOT NULL,
    updated_at   INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_agent_memories_scope ON agent_memories (owner, project, kind);
CREATE VIRTUAL TABLE IF NOT EXISTS agent_memories_fts USING fts5(
    name, description, content,
    tokenize='porter unicode61 remove_diacritics 2');
CREATE TABLE IF NOT EXISTS agent_memory_vectors (
    model_key TEXT NOT NULL,
    memory_id TEXT NOT NULL,
    vec       BLOB NOT NULL,
    PRIMARY KEY (model_key, memory_id)
);
```

FTS rows are kept in step by rowid triggers, the pattern of `internal/store/schema.go:471-491`. Vectors are float32 little-endian blobs with cosine computed in Go, as in `internal/memory/dense.go:312-345`.

**Postgres (migration 21, sketch).**

```sql
CREATE TABLE IF NOT EXISTS agent_memories (
    id           TEXT PRIMARY KEY,
    owner        TEXT NOT NULL DEFAULT '',
    project      TEXT NOT NULL DEFAULT '',
    kind         TEXT NOT NULL,
    name         TEXT NOT NULL,
    description  TEXT NOT NULL DEFAULT '',
    content      TEXT NOT NULL,
    tags         TEXT NOT NULL DEFAULT '',
    source       TEXT NOT NULL,
    content_hash TEXT NOT NULL,
    created_at   BIGINT NOT NULL,
    updated_at   BIGINT NOT NULL,
    fts tsvector GENERATED ALWAYS AS (
        setweight(to_tsvector('simple', name), 'A') ||
        setweight(to_tsvector('simple', description), 'A') ||
        setweight(to_tsvector('simple', content), 'B')) STORED
);
CREATE INDEX IF NOT EXISTS idx_agent_memories_scope ON agent_memories (owner, project, kind);
CREATE INDEX IF NOT EXISTS idx_agent_memories_fts ON agent_memories USING gin (fts);
CREATE TABLE IF NOT EXISTS agent_memory_vectors (
    model_key TEXT NOT NULL,
    memory_id TEXT NOT NULL,
    vec       vector NOT NULL,
    PRIMARY KEY (model_key, memory_id)
);
```

The `tsvector` column follows `internal/store/pgmigrate.go:376-381`. The vector column has no fixed dimension, so there is no HNSW index and search is an exact scan filtered by `model_key`. That is acceptable at the volume in A9; whether pgvector on the fixture image accepts an untyped `vector` column in this form is UNVERIFIED.

**Migration notes.** Use number 21 on both ledgers: SQLite is at 20 and Postgres at 19 (section 4, row 24), and the convention is one number per step on both engines. Additive only; rollback is dropping the three objects. The table is derived data, so no backfill is needed: clients re-sync.

**Interfaces.** A new role `AgentMemories` in `internal/store/roles.go`, embedded in `Backend`, implemented by both engines (the compile-time assertions at `roles.go:16-19` enforce parity). Only at this point is a narrow sink interface introduced in `internal/ccmemory` (manifest, upsert, delete), with the file bank and the store as its two implementations. It is not introduced earlier because it would have a single implementation.

**Remote push.** `leankg memory sync --remote <url> --token-env <VAR>` over the REST listener. Whether it reuses the Hindsight-compatible document routes (`internal/rest/hindsight.go:71-117`, `:178-187`) or gets native routes is decided with OQ-1.

**Behavior that must be decided first.** Owner identity, project identity that does not embed a local path (OQ-8), who may read another owner's `user` and `feedback` rows, and the project-grant rule for `agent_recall`.

---

## 7. Claude Code integration guide

All paths are placeholders.

### 7.1 One-time setup

1. Build or install the binary: `<leankg-binary>`.
2. First sync: `<leankg-binary> memory sync`. This creates `~/.leankg/memory` if needed.
3. Optional deny-list: create `~/.leankg/memory/scrub.txt`, one regular expression per line.
4. Migrate existing memory: `<leankg-binary> memory migrate --dry-run`, then `<leankg-binary> memory migrate`. Run it after the deny-list so secrets are masked on the way in.
5. Register the hook: `<leankg-binary> install --target claude-code --register-memory`, or add the snippet in 7.2 by hand.
6. Optional, for on-demand recall: register the MCP server (7.4).

### 7.2 Hook snippet (`~/.claude/settings.json`)

```json
{
  "hooks": {
    "SessionStart": [
      {
        "hooks": [
          { "type": "command", "command": "<leankg-binary> memory hook", "timeout": 10 }
        ]
      }
    ]
  }
}
```

The hook is synchronous on purpose (A6). It has no matcher, so it also runs after a clear or a compaction, when the earlier block may no longer be in context.

### 7.3 Example `SessionStart` output

```text
LeanKG cross-repository memory: preferences and feedback saved in other repositories. This repository's own memory is loaded by Claude Code itself.
<memories>
- [feedback] table-tests - Prefer table-driven tests for handlers
**Why:** earlier one-off tests missed edge cases.
**How to apply:** one table per handler, one row per case.
- [user] review-order - Wants review findings ordered by severity

<!-- 14 more memories hidden by the injection budget of 8 entries / 1500 tokens -->
</memories>
More on a topic: call the leankg query tool with action "memory", args.command "agent_recall", args.cwd "<project-dir>".
```

### 7.4 MCP registration (user scope)

Recommended: one local server for every repository.

- Start it: `<leankg-binary> serve --http 127.0.0.1:<port> --memory`
- Register it: `claude mcp add --transport http --scope user leankg http://127.0.0.1:<port>/mcp`

The exact `claude mcp add` flags are UNVERIFIED here; check `claude mcp add --help`. `<leankg-binary> install --target claude-code` writes the same entry into the client config (`cmd/leankg/connect.go:103`).

A per-repository stdio registration also works. It is not recommended at user scope, because each session's server opens a store, and with `--memory` a memory tree, inside the repository it starts in (KD-8).

### 7.5 Token budget and fail-open summary

| Item | Value |
|---|---|
| Entries | at most 8 |
| Block budget | 1,500 tokens estimated as bytes / 4 |
| Hard ceiling | under 10,000 characters of stdout |
| Hook deadline | 3 s internal, 10 s in `settings.json` |
| On any failure | empty stdout, exit 0, one stderr line |

---

## 8. Security and privacy

| Concern | Control |
|---|---|
| Secrets pasted into a memory | Fixed redaction rules on every write and read (`internal/telemetry/redact.go:12-49`). |
| Hostnames, endpoints, internal names | User deny-list (`mem-scrub`). The list itself lives only in the user's home directory. |
| Personal paths | The home directory is rewritten to `~` (`redact.go:63-74`). The project slug still encodes the local path; it stays on the machine in the first release and is the reason for OQ-8. |
| Network exposure | The hook and `leankg memory sync` use no network. `agent_recall` is served on the existing listeners: loopback by default, and a non-loopback bind without a token is refused (`cmd/leankg/main.go:414-420`, `:484-488`). |
| Authorization | `agent_recall` needs Viewer. Callers restricted to some projects are refused (KD-8). With the gate off every caller is Admin, which is acceptable only on loopback, as today. |
| Cross-repository leakage | `project` and `reference` memories leave their repository only under `scope=all`. Untyped memories are never global. |
| Ownership | Absent. Every row belongs to the one OS user (A8). Required before team mode. |
| Deletion | Deleting the source file copies the row to `ccarchive` (stamped `deleted_at`) and then removes it, at the next sync, PostToolUse or SessionEnd (KD-12). `memory restore --deleted` brings it back. A project whose memory directory is absent keeps its rows unless `--rebuild`. `--rebuild --project SLUG` forgets one project without archiving. Removing the `cc-` bank files drops the archive as well. |
| File permissions | The central root is created by the existing code with its existing modes (`internal/memory/memory.go:100-108`); this plan does not change them. |
| Telemetry | Off by default. When on, events carry counts, ids and hashes, not memory text (`internal/memory/telemetry_hook.go:94-105`). |
| Claude Code config writes | `install --target claude-code --register-memory` writes the hook entry to `~/.claude/settings.json` and, as `install` already does, the leankg MCP server entry to `~/.claude.json`. With `--hook-only` only the hook entry is written (OQ-11, resolved). |

---

## 9. Testing strategy

- **Method.** One vertical slice at a time: a failing test first, then the minimum code. The seam of each slice is named in section 12.
- **Layer policy** (from `AGENTS.md`). Unit tests are in-process and fast: no network, no real provider, no sleeps. `go test ./...` is the gate. Anything touching a real embedder or Postgres is gated on an environment variable.
- **Locations.** `_test.go` beside the package; fixtures under `testdata/`; `t.TempDir()` for file trees.
- **Home directory.** Tests that reach the central root or the Claude Code root set `HOME` (and `CLAUDE_CONFIG_DIR` where relevant) to a temp directory with `t.Setenv`, so no test reads or writes the real one.
- **Real memory, no mocks.** Sync, recall and hook tests use a real `memory.Open` in a temp directory, as existing tests do (`internal/core/core_test.go:31`).
- **Dense arm.** Tests use the in-package fake provider pattern (`internal/memory/dense_test.go:72`).
- **Goldens.** `agent_recall` gets an action golden next to the existing ones (`internal/core/actions_golden_test.go:562`). Regenerate with `go test ./internal/core/ -run TestActionGoldens -update` (`internal/core/actions_golden_test.go:12`, flag at `:37`).
- **Contract document.** Any change to tool descriptions or schemas is followed by `LEANKG_UPDATE_CONTRACT_DOC=1 go test ./internal/mcp -run TestToolContractDoc`, and both files are committed together.
- **Dual-engine parity.** The first release changes no store code, so parity is proven by absence: `internal/store/schema.go` and `internal/store/pgmigrate.go` are untouched. `team-store` adds one behavioral test per role method that runs against both engines, with the Postgres half named `TestPG...` and gated on `LEANKG_TEST_PG_URL`, matching `scripts/test-dual-engine.sh:45`.
- **Postgres fixture.** Start and teardown are one unit of work. The fixture is never left running:

```bash
docker compose -f scripts/pgvector.compose.yml up -d
make dual-engine; status=$?
docker compose -f scripts/pgvector.compose.yml down
docker ps --filter name=leankg-pg-500mb
exit $status
```

The `docker ps` line must list nothing. CI has an extra "Enable the pgvector extension" step (`.github/workflows/ci.yml:93`); replicate it locally if the store tests report a missing extension.

---

## 10. Commands

| Purpose | Command | Source |
|---|---|---|
| Build | `make go-build` | `Makefile:23-24` |
| Full test suite | `make go-test` | `Makefile:26-27` |
| Vet | `make go-vet` | `Makefile:32-33` |
| One package | `go test ./internal/ccmemory/ -count=1` | pattern in `AGENTS.md` |
| One test | `go test ./internal/memory/ -run TestDeleteDocuments -count=1` | pattern in `AGENTS.md` |
| Contract document check | `go test ./internal/mcp -run TestToolContractDoc -count=1` | `internal/mcp/contract_doc_test.go:86-112` |
| Contract document regenerate | `LEANKG_UPDATE_CONTRACT_DOC=1 go test ./internal/mcp -run TestToolContractDoc` | same |
| Dual-engine gate | `make dual-engine` (needs the fixture; always torn down, section 9) | `Makefile:89-90` |
| Fixture down | `docker compose -f scripts/pgvector.compose.yml down` | `scripts/pgvector.compose.yml:14-15` |

There is no separate lint target; `go vet` is the repository's static gate. The repository has no Markdown linter.

---

## 11. Boundaries

**Always**

- Write a failing test before the code of a slice.
- Route every memory byte through the existing redaction choke points.
- Keep the hook fail-open: exit 0, bounded time.
- Keep `ctx` as the first parameter; use `context.Background()` only in `package main` and tests (`AGENTS.md`, context conventions).
- Regenerate and commit `docs/mcp-tool-contract.md` with any schema or description change.
- Tear the Postgres fixture down in the same unit of work that started it.
- Use placeholders (`<project-dir>`, `other-repo`, `SERVICE_DNS_PLACEHOLDER`) in tests, fixtures, docs and examples.

**Ask first**

- Adding a fifth MCP tool, or a new top-level action.
- Any store migration or new store role (that is `team-store`, gated).
- Changing what `scope=global` means for existing commands (OQ-2).
- Bumping `indexVersion` or changing the side index layout.
- Adding a dependency.
- Adding a telemetry transport constant.
- Enabling anything on a non-loopback listener.

**Never**

- Write into `~/.claude/` other than the one hook entry the install command owns (install also writes the MCP entry to `~/.claude.json`, as it already does, unless `--hook-only` is given; OQ-11).
- Delete central rows on a failed or empty scan without `--rebuild`.
- Put real host paths, usernames, hostnames or secrets in code, tests, fixtures or docs.
- Let a hook path block a session, prompt the user, or start a server or an embedding sidecar.
- Read or write banks without the `cc-` prefix from sync.
- Leave `leankg-pg-500mb` running.

---

## 12. Task breakdown

Slices are ordered. Each is one commit. CAM-00 has no code.

- [x] Task: CAM-00 Confirm three Claude Code facts on a real install
  - Module: `cc-source`, `cc-hook`
  - Acceptance: the notes record (a) the directory name Claude Code uses for a repository's memory when the session runs in a linked worktree; (b) the frontmatter keys of three real memory files, with values replaced by placeholders; (c) that plain stdout of a synchronous `SessionStart` hook containing a `<memories>` block is visible to the model. A2 and A3 are updated in this document (2026-11-27); A6 stays UNVERIFIED. DONE with (a) not fully verified: no worktree session had a memory directory; `ProjectKey` tries the main-root slug first then the directory's own slug. (b) and (c) verified. (c) 2026-11-27 with Claude Code 2.1.329: the stdout of a synchronous `SessionStart` command hook was visible to the model (a one-shot probe printed a random token and the model repeated it).
  - Verify: manual; a throwaway hook `printf 'probe\n<memories>\n- x\n</memories>\n'` and one question to the model.
  - Files: `docs/plan-central-agent-memory.md`

- [x] Task: CAM-01 Tracker rows
  - Module: all
  - Acceptance: `docs/prd-task-tracker.md` has rows `CAM-00..CAM-20` and `docs/prd.md` has a short requirement entry pointing at this plan.
  - Verify: `go test ./internal/archtest/ -count=1`
  - Files: `docs/prd.md`, `docs/prd-task-tracker.md`

- [x] Task: CAM-02 Export the Claude Code projects root and slug
  - Module: `cc-source`
  - Acceptance: `ProjectsRoot(home)` returns `$CLAUDE_CONFIG_DIR/projects` when the variable is set, else `<home>/.claude/projects`; `Slug("/a/b.c")` returns `-a-b-c`; the adapter's existing tests pass unchanged.
  - Verify: `go test ./internal/sessionlink/... -count=1`
  - Files: `internal/sessionlink/claudecode/claudecode.go`, `internal/sessionlink/claudecode/claudecode_test.go`

- [x] Task: CAM-03 Move the frontmatter splitter and link extractor to a leaf package
  - Module: `cc-source`
  - Acceptance: `mdmeta.SplitFrontmatter` and `mdmeta.WikiLinks` behave exactly as the functions they replace; `internal/obsidian` calls them and its tests pass unchanged; new cases cover a top-level `type:` and a `type:` indented under `metadata:`.
  - Verify: `go test ./internal/mdmeta/ ./internal/obsidian/ -count=1`
  - Files: `internal/mdmeta/mdmeta.go`, `internal/mdmeta/mdmeta_test.go`, `internal/obsidian/obsidian.go`, `internal/obsidian/sync.go`

- [x] Task: CAM-04 Parse and discover memory documents
  - Module: `cc-source`
  - Acceptance: over a fixture of three project directories, discovery returns one `Doc` per `*.md` except `MEMORY.md`; kind, name, description, body and links match; a file with no frontmatter is `unknown`; a file over 64 KiB and a symlink leaving the root are reported as skipped; a missing root sets `RootMissing` without an error.
  - Verify: `go test ./internal/ccmemory/ -run 'TestParse|TestDiscover' -count=1`
  - Files: `internal/ccmemory/source.go`, `internal/ccmemory/source_test.go`, `internal/ccmemory/testdata/`

- [x] Task: CAM-05 Project key, bank names and document ids
  - Module: `cc-source`
  - Acceptance: a directory inside a linked worktree resolves to the slug of the main worktree root without running git; a directory outside any repository resolves to its own slug; two slugs sharing their first 80 characters get different banks; every bank name is at most 64 characters and unchanged by `sanitizeBank`; a slug ending in `token` produces ids that `memory.Redact` leaves unchanged.
  - Verify: `go test ./internal/ccmemory/ -run 'TestProjectKey|TestBankFor|TestDocID' -count=1`
  - Files: `internal/ccmemory/identity.go`, `internal/ccmemory/identity_test.go`

- [x] Task: CAM-06 Deny-list inside `memory.Redact`
  - Module: `mem-scrub`
  - Acceptance: with a pattern installed, a create, an append and a bank retain all store `[SCRUBBED]` in place of the match; a row written before the pattern existed is masked on read; a line that does not compile fails the load with its line number; no file means no change; existing redaction tests pass.
  - Verify: `go test ./internal/memory/ -run 'TestScrub|TestRedact' -count=1`
  - Files: `internal/memory/scrub.go`, `internal/memory/scrub_test.go`, `internal/memory/memory.go`

- [x] Task: CAM-07 Batch document delete
  - Module: `mem-bank-ops`
  - Acceptance: `DeleteDocuments` with three ids removes exactly those rows with one rewrite of the bank file and one `delete` event carrying the count 3; unknown ids and a missing bank delete nothing; `DeleteDocument` still passes its tests.
  - Verify: `go test ./internal/memory/ -run 'TestDeleteDocument' -count=1`
  - Files: `internal/memory/banks.go`, `internal/memory/banks_docs_test.go`

- [x] Task: CAM-08 Injection over explicit banks with a filter and a budget
  - Module: `mem-bank-ops`
  - Acceptance: `InjectBanks` with no query returns newest rows first after the filter; rows the filter rejects are never rendered or counted as injected; the hidden-count marker is present when rows were cut; one `inject` event is recorded; `FirstTurnMemories` and `FirstTurnMemoriesWithIDs` produce byte-identical output to before.
  - Verify: `go test ./internal/memory/ -run 'TestInject|TestFirstTurn' -count=1`
  - Files: `internal/memory/scope.go`, `internal/memory/banks.go`, `internal/memory/injectbanks_test.go`

- [x] Task: CAM-09 Sync plan (pure diff)
  - Module: `cc-sync`
  - Acceptance: given desired and actual sets, the plan lists upserts for missing and changed keys and deletes for extra keys; a kind change yields one delete and one upsert in different banks; equal sets give an empty plan; an empty desired set against a non-empty actual set gives `deletes_withheld` unless rebuild is requested.
  - Verify: `go test ./internal/ccmemory/ -run TestPlan -count=1`
  - Files: `internal/ccmemory/sync.go`, `internal/ccmemory/sync_test.go`

- [x] Task: CAM-10 Sync apply against a real memory root
  - Module: `cc-sync`
  - Acceptance: after one run over the fixture, `cc-global` and each project bank hold the expected rows with the mapped fields; a second run changes no bank file and emits no event; editing one file rewrites one row; removing one file removes one row; changing the deny-list rewrites every row; a planted secret in a source file is absent from every file under the central root.
  - Verify: `go test ./internal/ccmemory/ -run TestApply -count=1`
  - Files: `internal/ccmemory/sync.go`, `internal/ccmemory/sync_test.go`

- [x] Task: CAM-11 `leankg memory sync`
  - Module: `cc-sync`
  - Acceptance: the verb is registered and listed in usage; `--json` prints the result shape of section 6.4; `--dry-run` writes nothing; `--rebuild` empties and refills the `cc-` banks; a second process holding the lock makes the command wait; exit codes are 0 and 1 as specified.
  - Verify: `go test ./cmd/leankg/ -run TestMemorySync -count=1`
  - Files: `cmd/leankg/memoryverbs.go`, `cmd/leankg/memoryverbs_test.go`, `cmd/leankg/main.go`

- [x] Task: CAM-20 `leankg memory migrate`
  - Module: `cc-sync`
  - Acceptance: `--dry-run` writes nothing and prints the inventory; a real run produces rows equal to the files of each bank and `verified` is true; `--project` limits the scope to one slug; a planted mismatch (a test hook that removes one row) gives exit 1 and lists the mismatch; the source tree bytes are unchanged before and after; a second run changes nothing; the run works with the deny-list present.
  - Verify: `go test ./cmd/leankg/ -run TestMemoryMigrate -count=1`
  - Files: `cmd/leankg/memoryverbs.go`, `cmd/leankg/memoryverbs_test.go`, `internal/ccmemory/migrate.go`, `internal/ccmemory/migrate_test.go`

- [x] Task: CAM-12 Scoped recall function
  - Module: `agent-recall`
  - Acceptance: from repository B, a `feedback` memory written in repository A is returned under `project` and `global`; A's `project` memory is returned only under `all`; `types` narrows before the limit; two identical global memories from two repositories are returned once; an unknown scope or type is an error naming the valid values; a caller with no project bank gets global rows and an empty `project`.
  - Verify: `go test ./internal/ccmemory/ -run TestRecall -count=1`
  - Files: `internal/ccmemory/recall.go`, `internal/ccmemory/recall_test.go`

- [x] Task: CAM-13 `agent_recall` in the core dispatcher
  - Module: `agent-recall`
  - Acceptance: `query {action:"memory", args:{command:"agent_recall"}}` returns the response of section 6.5 with provenance; `args.types` decodes and an unknown argument key is still refused; with no central root the error tells the caller to run the sync; the central handle is opened lazily and only when its index exists; a golden pins the response.
  - Verify: `go test ./internal/core/ -run 'TestActionGoldens|TestAgentRecall' -count=1`
  - Files: `internal/core/core.go`, `internal/core/actions.go`, `internal/core/agent_recall_test.go`, `internal/core/testdata/golden/actions/`, `internal/ccmemory/handle.go`

- [x] Task: CAM-14 MCP routing, schema text and serve wiring
  - Module: `agent-recall`
  - Acceptance: on a multi-project server, `agent_recall` with a `project` that is not registered is answered from the central banks, while every other action with that `project` is still refused; a caller restricted to some projects is refused; `tools/list` still reports four tools; the regenerated contract document is committed and the drift test passes.
  - Verify: `go test ./internal/mcp/ -run 'TestAgentRecall|TestToolContractDoc' -count=1`
  - Files: `internal/mcp/server.go`, `internal/mcp/agent_recall_test.go`, `docs/mcp-tool-contract.md`, `cmd/leankg/main.go`
  - Known limits (2026-11-27): hosted mode wires no central handle and answers "not configured"; `?project=` URL handlers do not serve `agent_recall`.

- [x] Task: CAM-15 `leankg memory recall`
  - Module: `agent-recall`
  - Acceptance: the CLI prints the same rows as the core function for the same inputs, as text and as `--json`; `--dir` selects the caller repository.
  - Verify: `go test ./cmd/leankg/ -run TestMemoryRecall -count=1`
  - Files: `cmd/leankg/memoryverbs.go`, `cmd/leankg/memoryverbs_test.go`

- [x] Task: CAM-16 `leankg memory hook`
  - Module: `cc-hook`
  - Acceptance: with hook JSON on stdin for repository B, stdout is the context line, a block holding only global memories from other repositories, and the hint line; stdout is under 10,000 characters with 500 fixture memories; the exit status is 0 and stdout is empty for each row of the failure table in section 6.6; a held sync lock skips the sync and still injects.
  - Verify: `go test ./cmd/leankg/ -run TestMemoryHook -count=1`
  - Files: `cmd/leankg/memoryhook.go`, `cmd/leankg/memoryhook_test.go`, `internal/ccmemory/hook.go`, `internal/ccmemory/hook_test.go`

- [x] Task: CAM-17 Hook registration
  - Module: `cc-hook`
  - Acceptance: `install --target claude-code --register-memory` adds the entry of section 7.2 to a settings file, preserving every other key and hook; a second run changes nothing; the existing index hook entry and its tests are unaffected; other targets return the unsupported-client error.
  - Note (2026-11-27): the install verb also writes the leankg MCP server entry to the client config file (`~/.claude.json`), because that is what `install` already does; `--hook-only` skips it (OQ-11, resolved 2026-11-28).
  - Verify: `go test ./cmd/leankg/ -run 'TestRegister|TestInstall' -count=1`
  - Files: `cmd/leankg/connect.go`, `cmd/leankg/connect_test.go`, `cmd/leankg/install.go`, `cmd/leankg/install_test.go`, `cmd/leankg/wire.go`

- [x] Task: CAM-18 Telemetry from the CLI paths
  - Module: `cc-hook`
  - Acceptance: with consent on, one hook run records a retain event per changed bank and one inject event naming `cc-global`, carrying the session id from stdin; with consent off nothing is opened or written; the flush is bounded by the existing close budget. Existing constants are `TransportStdio`, `TransportHTTP`, `TransportREST`, `TransportHS`, `TransportRPC` (`internal/telemetry/types.go:97-101`); reuse one, and adding a new constant needs approval.
  - Verify: `go test ./cmd/leankg/ -run TestMemoryHookTelemetry -count=1`
  - Files: `cmd/leankg/memoryhook.go`, `cmd/leankg/memoryverbs.go`, `cmd/leankg/memoryhook_test.go`

- [x] Task: CAM-19 Reference documentation and full gate
  - Module: all
  - Acceptance: the CLI reference and the quick-reference table list the `memory` verb and the install flag; the integration guide of section 7 is reachable from the docs; the full gate is green.
  - Verify: `make go-build && make go-test && make go-vet`
  - Files: `AGENTS.md`, `docs/cli-reference.md`, `docs/getting-started.md`

Gated, not to be started before OQ-1 is answered: `team-store` slices (migration 21 on both engines, the `AgentMemories` role with both-engine parity tests, the sink interface, remote push, ownership rules). They are broken down when the gate opens.

---

## 13. Success criteria, risks, rollout, observability

### 13.1 Success criteria

| # | Criterion | Checked by |
|---|---|---|
| S1 | A sync over the fixture produces the expected rows; a second sync writes nothing. | CAM-10, CAM-11 |
| S2 | Removing one source file removes exactly one row. | CAM-10 |
| S3 | A `feedback` memory from repository A is recalled from repository B by default; A's `project` memory only under `all`. | CAM-12, CAM-13 |
| S4 | The hook prints under 10,000 characters, exits 0, and finishes within 3 s on a 500-memory fixture; every failure case prints nothing and exits 0. | CAM-16 |
| S5 | A planted secret and a deny-listed string never appear in any file under the central root. | CAM-06, CAM-10 |
| S6 | The MCP registry still has four tools and the contract document matches. | CAM-14 |
| S7 | No file under `internal/store/` changes before the gate. | review of the diff |
| S8 | With telemetry on, the dashboard Memory page lists `cc-global` with inject and retain counts. | CAM-18, manual check |
| S9 | `make go-build && make go-test && make go-vet` is green. | CAM-19 |

### 13.2 Risks and mitigations

| Risk | Mitigation |
|---|---|
| The memory directory name is not the slug assumed in A2. | Two candidates in project-key resolution; CAM-00 before any code. |
| The frontmatter format differs or changes. | Tolerant parser, `unknown` is repository-scoped, fixtures taken from CAM-00. |
| Several processes share the central root (hooks, a server). | Sync writes only `cc-` banks and only under the file lock; the server only reads them; the side index is SQLite in WAL mode with a busy timeout (`internal/memory/search.go:22`). |
| Opening the central root is slow because the side index is rebuilding or locked. | Hook deadline and fail-open; the hook never compacts. |
| A wrong scan deletes the central copy. | Deletes withheld on a missing or empty scan; the store is rebuildable from the files in any case. |
| Readable ids altered by redaction. | Hash-based ids and bank names (KD-4), pinned by CAM-05. |
| Injected block grows past the cap. | Budget constant below the cap, pinned by CAM-16. |
| The dense arm is off for the central root when the default store has no calibration. | Recall degrades to BM25, the existing behavior (`internal/memory/dense.go:26-28`); stated in the response's ranking, not hidden. |
| The routing exemption widens what a restricted token can read. | Restricted callers are refused for this command; pinned by CAM-14. |
| Stale recall in a long session. | Documented bound (KD-5); `leankg memory sync` on demand. |

### 13.3 Rollout and migration

1. Everything is opt-in: no behavior changes until the user runs the sync or registers the hook.
2. Order: land CAM-02 to CAM-11 (sync usable by hand) and CAM-20 (migrate) in the same sync wave, right after CAM-11; then CAM-12 to CAM-15 (recall), then CAM-16 to CAM-18 (hook).
3. No data migration. The first sync is the initial load.
4. Rollback: remove the hook entry from `~/.claude/settings.json`; optionally delete the `cc-` bank files under the central root. Claude Code's files are never touched.
5. Upgrades that change the row format bump the render version inside `source_hash`, so the next sync rewrites every row with no manual step.

### 13.4 Observability

- The dashboard Memory page (`ui-dashboard/src/pages/memory.tsx`) already reports recall volume, writes and injection per bank from memory events. The `cc-` banks appear there with no UI change once CAM-18 lands and telemetry consent is on.
- `leankg memory sync --json` is the store report: documents, rows per bank, skipped files, withheld deletes.
- Project banks appear by hash in the dashboard; the slug-to-bank map is in the sync report and in each row's metadata.
- The hook writes at most one stderr line per run.

---

## 14. Open questions

| # | Question | Blocks |
|---|---|---|
| OQ-1 | Single developer on one machine, or a team-shared server? | `team-store` entirely. |
| OQ-2 | Should the existing `scope=global` (bank `leankg-shared`) be served from the central root on multi-project servers? Today each routed project has its own copy (section 4, rows 6 and 7). Changing it moves where existing rows are read from. | Nothing in this plan; decide separately. |
| OQ-3 | Add a `UserPromptSubmit` hook that ranks memories against the prompt? It gives true relevance at a per-prompt latency cost. | Nothing; candidate follow-up. |
| OQ-4 | Should the hook also inject the current repository's memories that fall beyond the natively loaded 200 lines or 25 KB? | Nothing; KD-6 says no for now. |
| OQ-5 | Support a custom `autoMemoryDirectory`? Its layout is UNVERIFIED. | Nothing; non-goal. |
| OQ-6 | Should the central root honor `LEANKG_HOME`? The telemetry ledger does (`internal/telemetry/types.go:78-87`); the memory root does not (`internal/memory/memory.go:88-93`). | Nothing; pre-existing inconsistency, reported not fixed. |
| OQ-7 | Should a deny-list line be able to refuse a whole document instead of masking? | Nothing; KD-10 masks. |
| OQ-8 | In team mode, what identifies a project across machines without embedding a local path? | `team-store`. |
| OQ-9 | Resolved 2026-11-27: reference memories are repository-scoped. | Nothing; one constant in `cc-source`. |
| OQ-10 | Is the side effect in KD-8 (a stdio server creating a store in every repository) confirmed? If not, user-scope stdio registration is also acceptable. | Guide wording only. |
| OQ-11 | Should `install --register-memory` have a hook-only mode that does not write the MCP entry? | **Resolved 2026-11-28:** `install --target claude-code --register-memory --hook-only` writes only the SessionStart hook and no `~/.claude.json` entry (`cmd/leankg/wire.go`, `cmd/leankg/install.go`). |
