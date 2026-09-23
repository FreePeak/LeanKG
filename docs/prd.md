# LeanKG PRD — Unified Product Document

**Version:** 4.13.0-memory-service
**Date:** 2026-09-17
**Status:** Active Development — **single source of truth** (this document + `docs/prd-task-tracker.md`; all historical documents preserved under [`docs/archive/`](archive/)). **Operating focus from 2026-09-14: the self-host dogfood loop (§3.10, M10)** — this repo served by its own dynamic HTTP server (MCP + REST + dashboard), indexed, embedded, memorized; LeanKG builds LeanKG first, then scales outward to nested-repo parents.
**Codebase Version:** 0.34.0 (Go engine at the repository root — module `github.com/FreePeak/LeanKG`, moved out of `go/` per #403; root-tagged releases since v0.33.0; the Rust tree was removed in f7624143)
**Storage:** SQLite WAL default (FTS5 L2 rung, float32-BLOB vectors, DB-resident watermarks); PostgreSQL + pgvector opt-in (`LEANKG_DB_ENGINE=postgres` + `LEANKG_PG_URL`) with schema-per-project, per-model HNSW and the advisory-locked audit chain.

---

## Changelog


### v4.13.0-memory-service — LeanKG IS the memory backend service (FR-ZCP-14) (2026-09-17)

**Trigger:** xdev (the harness) ran with `memory: hindsight` pointed at `leankg serve --hindsight-compat` and the pairing half-worked: retain, tag-scoped recall and injection were live, but three gaps made the service first-class only for writes. Recorded as K1–K10 in xdev's `docs/decisions/leankg-memory-backend.md`, whose §5 sequencing names K1/K3/K4/K6 as step 2 (after scoping). This change lands exactly that gate.

- **K6 — the compat mount was outside the write gate (security).** `auth.isWritePath` gated `POST /api/v1/memory/banks/{bank}/memories` but not its hindsight-compat alias `POST /v1/default/banks/{bank}/memories`, because the alias lives on the same REST listener under a prefix the gate did not know. With `LEANKG_TOKEN_*` set, a **Viewer could retain** through the alias what Contributor was required to write natively. Fixed by adding both memory prefixes to the write set — and by keying the last-segment rule on the **method**, because the new `GET …/memories` listing shares that path with retain. Pinned by `TestCompatMountWriteRoutesGated` (Viewer 403 on retain; 200 on ensure/recall/stats/list/by-id/reflect) and `TestCompatRetainAllowedForContributor` (the tightening does not lock out a legitimate writer).
- **K4 — `GET /v1/default/banks/{bank}/stats` did not exist.** The client's `/memory stats` calls it and printed **"server unreachable"** for a healthy server. Now answers `{bank, entries, bytes, banks, last_retain, root}` — the root field doubles as the single-writer anchor a deployment needs to debug two servers on one bank.
- **K3 — no read parity: `GET …/memories` (list, `?offset=&limit=`, newest-first, unpaged `total`) and `GET …/memories/{id}`.** `{id}` resolves the wire id first and the client's `document_id` second (exact id wins), and an unknown id is a **404**, not an empty 200 — a read-before-edit seam that answers 200-with-nothing is worse than no seam. Rows render through the same `hindsightRows` builder recall uses, so a read and a recall of the same row are byte-identical.
- **K1 — `serve --memory-global`** keeps the bank root in `~/.leankg/memory` instead of `<project>/.leankg/memory`, so one service can host the harness's memory for several projects. `Memory.Open(dir, global)` already supported it; only the wiring was missing. **Opt-in by design** — an existing deployment's banks never move underneath it — and the root is logged at startup because two servers on one root append to the same JSONL with no cross-process lock.
- **Read surface lives in `internal/memory/read.go`** (`Stats`/`ByID`/`List`/`Banks`), four methods over the JSONL the banks already are. `ponytail:` it re-reads and re-tokenizes per call with no index — the same ceiling `RecallBanks` has (xdev's K7), fine to ~10⁴ rows/bank; past that both want the FTS/L3 tier, not a per-route cache.

**Verified:** `go test ./...` (46 packages ok, 0 fail), `go vet ./internal/... ./cmd/...` clean, `gofmt` clean. New tests: `TestBankReadSurface` (stats/by-id/list/paging/torn line), `TestHindsightCompatReadSurface` (stats → list → by-id → recall row-shape equality → document_id → 404), `TestCompatMountWriteRoutesGated` + `TestCompatRetainAllowedForContributor` (auth). The §5.2 gate walk — `PUT` ensure → `POST` retain → `POST` recall → `POST` reflect → `GET` stats — answers 200 across the board where stats previously 404'd, and read-by-id answers where the route previously did not exist.

**Remaining from the gap list (tracked as FR-ZCP-14):** K2 (first-class `Entry.Tags` — tags still ride in `Metadata`, a client's private key name), K5 (honest `reflect` — still a `- ` digest, and it ignores the `tags`/`budget`/`max_tokens` the client already sends), K7 (recall ranking: no index, no freshness decay, no embedding tier), K8 (retain idempotency — `update_mode:"replace"` is documented as append, so a re-retain duplicates), K9 (`leankg connect|install --target xdev`, which needs a YAML writer beside the omp one).
### v4.12.2-hindsight-compat — omp's native memory can point at LeanKG (#414) (2026-09-14)

- **What landed:** `leankg serve --hindsight-compat` (REST listener, requires `--memory`) mounts the exact wire omp's `memory.backend:"hindsight"` client speaks — `PUT /v1/default/banks/{bank}` (ensure, always 200), `POST .../memories` (`items[]` with `content/timestamp/context/metadata/document_id/tags`), `POST .../memories/recall` → `{results:[{text,…}]}`, `POST .../reflect` → `{text}` digest. One file (`internal/rest/hindsight.go`) as an **additive alias** — the native `/api/v1/memory/*` surface is unchanged, and without the flag the compat routes 404 (pinned by test).
- **Why no upstream PR was needed:** `memory.backend` is a closed enum, but the `hindsight` arm takes `hindsight.apiUrl` — speaking the server side of that wire lights up auto-retain, `<memories>` injection and the memory tools with zero omp changes. The earlier "upstream `memory.backend:\"mcp\"` PR" path stays available but is no longer the only door.
- **Cursor trap (the #406 class, memory side):** the hindsight retain wire has no `retained_through_user_turn`, and `Memory.Retain` gates on `throughUserTurn <= bankCursor` — a cursorless 0 write would have been **silently dropped after the first batch**. New `memory.RetainRaw` shares Retain's id/source/timestamp/importance defaults but appends unconditionally; pinned by `TestRetainRawNoCursor`.
- **Mapping policy:** client tags ride in entry metadata and filter recall (`all`/`all_strict` require every tag, else intersection; page = 8, the OMP recall limit); `update_mode:"replace"` is treated as append (JSONL has no per-document revision); documents/mental-models endpoints are deliberately not mounted — the wiring disables mental models client-side.
- **Verified:** in-process httptest suite (wire walk + 404-without-flag) and a LIVE probe replaying `hindsight/client.ts` call shapes byte-for-byte against a scratch `serve --hindsight-compat` — bank ensure, 2-item retain + second-batch retain, ranked tag-scoped recall, disjoint-tag exclusion, reflect digest: all PASS. Harness wiring: `memory.backend="hindsight"`, `hindsight.apiUrl=<rest>`, `hindsight.mentalModelsEnabled=false`.
- **Shipped as v0.34.0** (release PR #422, run `34871489633`): 4 platform tarballs, `latest` pointer correct, proxy origin resolves the tag, `go install github.com/FreePeak/LeanKG/cmd/leankg@v0.34.0` → `leankg 0.34.0`, zero `go/v0.34*` mirror tags (job retired in #415).

### v4.12.1-container-corpus — the demo image indexed what the `Dockerfile` says it excludes (#419) (2026-09-14)

**Trigger:** building the image to check the module-root cut for real — CI has no `docker` step, so `docker build` is the only gate the Render deploy has. #415 retargeted the build stage correctly (`COPY cmd/ ./cmd/`), but the demo stage became `COPY cmd/ internal/ /demo/engine/`, and BuildKit expands a trailing-slash source's **contents** into the destination: the two trees merged flat into `/demo/engine/{annot,astgrep,…,tstree,leankg}` instead of `/demo/engine/{cmd,internal}`.

| Consequence | Evidence (origin/main image vs. fixed) |
|---|---|
| `RUN rm -rf /demo/engine/internal/tstree` matched nothing | `test -d /demo/engine/internal` → absent; the grammars sat at `/demo/engine/tstree` |
| the ~60 MB of vendored tree-sitter grammars the stage comment excludes were shipped **and indexed** into the public demo store | baked graph `elements=4771 files=555` before, `4578/528` after — which is what the comment promises ("~530 files, ~4.6k elements") |
| dashboard clusters stopped reading like repository paths | `/api/graph/clusters`: `engine/annot`, `engine/astgrep`, … → `engine/cmd/leankg` |
| nothing failed | `docker build --target demo` exits 0 either way: a green build with the wrong corpus |

**Fix:** name each destination (`COPY cmd/ /demo/engine/cmd/`, `COPY internal /demo/engine/internal/`) and make the stage self-checking — `test -d /demo/engine/cmd/leankg && test -d /demo/engine/internal/store` ahead of the `rm`, so a re-flatten fails the build instead of shipping a different graph.

**Verified:** `docker build --target demo` passes the layout assertions with `tstree` gone; the full runtime image answers `/health` `{"ok":true}`, `/api/index/status` 4578 elements / 14889 relationships, `POST /api/query {"query":"authenticate"}` → `fresh` hits.


### v4.12.0-module-root — the engine IS the root module; the `go/` directory is gone (#403) (2026-09-14)

- **The one quiet cut landed:** `git mv go/* .` — `cmd/`, `internal/`, `benchmark/`, `testdata/`, `go.mod`/`go.sum` now sit at the repository root, and the module is `github.com/FreePeak/LeanKG` (196 files' imports rewritten). pkg.go.dev therefore serves the module at the canonical name, and the root `vX.Y.Z` release tags version it directly — so the `modtag` mirror job in `release.yml` is retired (REL-SHIP-03 superseded), and `go install github.com/FreePeak/LeanKG/cmd/leankg@latest` resolves from the next root-tagged cut.
- **Repointed in the same commit:** release-please `version-file`/`extra-files`, ci.yml path filters + tidy/build steps, Makefile targets (`bin/` output), Dockerfile stages, `.gitignore` negations (the global-gitignore `leankg` swallow trap keeps its guards: `!cmd/leankg/`, `!internal/rpc/leankg/`), `.gitattributes`, the kilo-ab harness, and the agent-context docs. `go/README.md` was folded into the root README — with go.mod at the root, that one readme serves both GitHub and pkg.go.dev.
- **Generated-code gotcha (pinned here so nobody rediscovers it):** the ConnectRPC `leankg.pb.go` embeds the module path inside the raw descriptor seed as a length-prefixed string. Textually rewriting the import path shrank that string by 3 bytes while the `/go` era lengths stayed — protobuf init then panicked (`slice bounds out of range [-1:]`) in every consumer. The cut fixes the go_package to the new path with corrected length prefixes. The `.proto` is not committed; regenerating it properly is follow-up work if the RPC surface ever changes again.
- **Shipped:** merged as #415 (squash `a7a02c6`); the first root-tagged release **v0.33.0** was cut deliberately via a `Release-As: 0.33.0` trailer commit (#417) because the squashed `refactor!` commit is filtered as non-user-facing by the simple release strategy. Verified after the cut: 4 platform tarballs on the release, **no** `go/v0.33.0` mirror tag (job retired), proxy `@v/list` carries `v0.33.0`, `go install github.com/FreePeak/LeanKG/cmd/leankg@v0.33.0` → `leankg 0.33.0`, pkg.go.dev serves the root module page, and a pristine clone of main builds clean (`cmd/leankg/VERSION` present — global-gitignore trap holds).

### v4.11.4-vector-reclaim — `gc` now reclaims vectors too (#411 closed); the loop's fixes shipped as v0.32.0 (2026-09-14)

- **#411 fixed:** `DeleteByFile` owns elements + edges, so vectors of deleted elements were permanent residue (`Orphans: 11` on this repo, forever). New `store.DeleteOrphanVectors` (both engines — PG walks the per-model stamped collections, skipping vanished tables; watermark bumps only when rows go, a no-op never touches it) wired into `leankg gc` as one repair printing both counts. Store regression test pins the setup→bug→reclaim sequence, live-row preservation, and the idempotence clauses. Live on this repo: `gc` reclaimed exactly the 11; the next `leankg-embed run` reports **`Orphans: 0`, Coverage 1.0**; `freshness: fresh` throughout.
- **The loop shipped:** release-please cut **v0.32.0** (waves #401→#410) with all four platform tarballs verified on the release — FR-SELF-04's "engine fixes ship via release-please" clause demonstrated end to end.

### v4.11.3-doctor-truth — the loop audits its own auditor (#406 closed)

**Trigger:** the live self-host's `doctor --deep` output itself — three permanently-wrong classifications (the #406 pair, plus the same T0 lie found on the portfolio **query** path while verifying the fix) and two inventory-refresh carry-ins of a class v4.10.0 had already fixed everywhere else (the `gc` verb from #401, and the writer daemon's flush path), caught by running the loop's own checks back against the fixes.

| Finding | Consequence | Fix |
|---|---|---|
| `index-freshness` derived the indexed side from `code_elements` (`IndexedFiles`) | a supported file that legitimately yields **zero elements** (ui-v2 configs, build-tag stubs, `calc.h` — 24 on this repo) reads as permanently "missing": a WARN no amount of re-indexing clears, training users to ignore the report | compare the disk walk against the **bookkeeping** table (`RecordedFiles`) — the source of truth for "the indexer has seen this file"; pinned by a zero-element regression case, verified live (PASS 808/808) and E2E on a throwaway project |
| fleet leg opened a **T0 manifest parent's** (never-indexed by design — children carry the stores) store and reported `UNREADABLE` | every portfolio parent registered via `register-project` dragged doctor --deep to WARN + exit 1 | the registry's own never-indexed shape (`LastIndexed==nil, elements=0, files=0`) classifies as `MANIFEST` — informational like MISSING; genuinely broken stores still WARN (both paths tested) |
| the portfolio **fan-out** opened each hot child and marked store-open failures `error` — a T0 manifest parent can never succeed | `portfolio` queries reported `games: error` + `projects_failed`, the same design-read-as-fault #406-2 fixed in doctor but on the answering surface | FanOut pre-classifies the registry's never-indexed-zero-counts shape as `not_indexed` with a `reason` and a distinct `projects_not_indexed` count; a registration claiming counts but missing its store stays a genuine `error` (the existing test pins that half) |
| `leankg gc` bumped the watermark without refreshing the inventory snapshot (#401 carry-in — the exact class v4.10.0 fixed for index/pull/summarize) | a just-gc'd project read `possibly_stale` until some later unrelated write | `cmdGC` calls `store.RefreshInventory` when rows went; E2E: index → delete callee → re-index → `gc` purges 1 → `status` **fresh** |
| the writer daemon's debounced flush ran `index.IndexDirWith` (bumping the watermark) with no inventory refresh | same class as the gc row above, but worse: the project the loop exists to keep current — this repo, under a live writer — read `possibly_stale` on every fleet sample, so the freshness WARN was structurally un-clearable | `flushIndex` helper: one index pass then `store.RefreshInventory`, the contract every other writer holds. Proven by `TestFlushRefreshesInventorySnapshot` and live: after the fixed writer + one stamp, `fleet: leankg current, FRESH`, check PASS |


No new capability; the point of M10 is that every surface — including the diagnostics surface — gets re-proved against this repo's real data, and "0-fail exit 1" noise is a defect class of its own. Live on this repo (wave binary + fixed writer restarted): `doctor --deep` = **9 pass, 2 warn, 0 fail**; `index-freshness` PASS (the 24 phantom "missing" → 0), `fleet` PASS rendering `games: MANIFEST` (not `UNREADABLE`) and `leankg: current, fresh`. The two remaining WARNs are **true statements**: `embedding-coverage` counts the honest over-context remainder (9025/9027 — items no shrink makes fit, reported not hidden), and `leankg-dir` names the live writer's held `watch.lock` with its PID — the designed single-flight report, correct exactly while a writer runs. Before this wave, both fleet staleness and the missing-file delta were lies no amount of re-indexing could clear.

### v4.11.2-selfhost-validated — the self-host ran on itself; every defect it found is fixed (2026-09-14)

**Trigger:** M10/S1 executed for real on this repository. The dynamic HTTP server came up over this checkout (`leankg serve --http :9699 --rest :9700 --ui :9701 --memory --embed-provider local`) with a pinned local embedder (`llama-server` serving `bge-small-en-v1.5` GGUF, 384-d). All five S1 gates passed; the run (and the first S3 step) then surfaced six real engine defects, each fixed with a failing-first test.

**S1 self-host — verified live, not inferred:**
- **Index:** this repo reconciled into sqlite (`9,025 elements`, `19,662 relationships`, `807 files`); `doctor --deep` → `orphaned-relationships PASS`, `duplicate-names PASS`, `migrations 14/14`.
- **Embed:** 8,989 vectors from the pinned catalog model; `ChunkerVersion 2`; the `Failed`/`Truncations`/`Coverage` report surfaced honestly (first pass: `Coverage 0.9984`, 14 over-context items counted, not fatal — after this wave's shrink-retry they embedded too: **Coverage 1.0**). Stamp `bge-small-en-v1.5-384|local|384|cosine|ea104dace…|2` written to `emb_stamp`.
- **Ladder:** L1 (`exact identifier match`), L2 (`FTS5 keyword match`), **L3 (`vector similarity (cosine)`)** all answered over REST `POST /api/v1/query` with `retrieval` provenance on the repo's own embeddings.
- **Memory:** `session/retain` → `written=2`; `session/recall` round-trips; **survives a full server restart** (read back after re-launch).
- **Health:** `/health` → `{"ok":true}` on MCP (9699), REST (9700), and UI (9701).

**Dogfood defects found and fixed (each pinned failing-first):**

| Finding | Consequence before the fix | Fix |
|---|---|---|
| `Run` clipped local-provider text at the general 8000-rune cap, but a 512-token GGUF embedder answers HTTP **500** (not truncate) beyond its context | One over-context element (`leankg-embed full` hit it immediately on this repo: an 845-token markdown/doc node) **aborted the whole pass** — a mass of good vectors lost to one bad item | local family embeds under `maxLocalTextChars = 1000` (~500 tokens at code density, measured 2.7 chars/token on real Go); `ChunkerVersion → 2` |
| A PROVIDER-level batch failure returned fatal (`embedFiles`), distinct from a validation failure | a single poison element killed the run even after the budget, because llama.cpp rejects >512 tokens rather than truncating | `embedFiles` retries a provider-failed batch **item by item**: the rejected item is counted in `Report.Failed` and left dirty, its siblings still commit; only an **all-failed** run aborts (dead provider is infra, not data) |
| `doctor --deep` orphan hint told users to purge leftovers with `leankg gc` — **a verb that did not exist** (it named the deliberately-dropped Rust `gc.rs`) | a mass deletion (v4.10.1) leaves dangling edges that incremental index never revisits, with no repair path | implemented `leankg gc` over a new `store.DeleteOrphanRelationships` (both engines; watermark bumps only when rows go) — purged 5,810 edges live, orphan check FAIL→PASS |
| `checkLeankgDir` warned on any `*.lock` file's mere **existence**; `embed.lock`/`watch.lock` are persistent kernel-released flocks | every successful embed left a permanent "stray lock file" WARN, and the hint to `cat <lock>` for a PID was a lie (the PID was never written) | check now probes **HELD** via `LOCK_EX|LOCK_NB` (idle token → PASS); embed/watch stamp the owning PID so the WARN names it |
| `TestSidecarExitsDuringStartup` hardcoded port `18080` | a dev ssh tunnel answering `/health` there made "the sidecar died" a silent **false pass** | use `freeTCPPort()` (the rest of the file already did); `TestLeankgDir` likewise rewritten to assert idle→PASS / held→WARN / released→PASS |
| Dense content still exceeded the 1000-rune budget (real Godot/TS: **523 tokens in 1000 runes**) — every item of the first portfolio child was rejected, and the all-failed guard misread that as "provider down" | `leankg-embed run games/the-survival` died 14/14 with status `failed` | `embedFiles`' per-item path now **halves and retries** (≤3 shrinks, successes counted in `Truncations`); the abort is reserved for a provider that fails at any size. Live: 132/132 embedded, Coverage 1.0 (64 via shrink); the repo's own 14 poison items embedded too → repo Coverage 1.0 |
| `leankg-embed <verb> <dir>` **silently ignored** the positional dir (Go's flag package stops at the first non-flag arg) and embedded the **cwd** store — the #370 index-root footgun reborn in the other binary | `run /abs/child` quietly wrote vectors into the wrong project | `bindPositional` binds the first positional to `--project` for all five verbs (explicit `--project` wins; >1 positional is a hard error); `cmd/leankg-embed/main_test.go` pins it |

**S3 first increment — verified on the loop's second real parent:** `games/` (6 nested repos, ~1.2k code files) registered as T0 with zero eager indexing (`elements=0`, store `missing`, portfolio answers `n/a` honestly); one child (`the-survival`) promoted to its own indexed+embedded store; a `portfolio` query over the live server attributed its hits to `the-survival` per-child with `freshness: fresh` — no restart at any step. Vanished fixture registrations pruned with `leankg projects --forget`. The relocation of the module to the repo root (pkg.go.dev root URL) is scoped and held as **#403** behind the parallel refactor waves.

**Test-layer policy (owner decision, codified in `AGENTS.md` + §6):** unit tests stay in-process and fast (no real provider/network/sleeps — `go test ./...` is the gate); anything making a real embed/LLM/PG call is an integration/e2e test gated on env (`LEANKG_TEST_PG_URL`, live sidecar) or its own job (`ttfv`); **benchmarks never run in CI**. The live self-host run above is the integration tier, executed by hand and recorded here, not a CI unit test.

### v4.11.1-ship-surface — the public surfaces: brand mark, README, the published module, the live deploy (2026-09-14)

**Trigger:** explicit user direction (redesign the favicon, update the README, publish the Go package, restore the tag badges, fix the Render deploy). This is the wave FR-SELF-01 named as its own gate — the pending deploy work is now landed, so S1's only remaining step is running the self-host.

**Brand mark** — `assets/icon.svg` and both favicons (`ui-v2/public/favicon.svg`, and the embedded copy `internal/web/embed/favicon.svg`) are redesigned. The old mark (dashed hexagon + solid hexagon + triangle + three nodes) collapsed into noise at 16 px. The new mark is a **"K" drawn as a resolved graph**: a stem and two arms (three edges) meeting at a hub node, terminals capped with nodes, on the dark-tile/cyan/amber palette the banner already uses. The favicon cut drops the hub disc and the edge gradient — a zero-width bounding box (`M22 14V50`, a pure vertical) painted with an `objectBoundingBox` gradient renders **nothing at all**, and that silent failure, not the composition, is what killed the first attempts; the shipping cut uses `gradientUnits="userSpaceOnUse"` (brand) and flat high-contrast strokes (favicon), rasterized and checked at 16/32/128 px.

**Published Go module** — a module in a subdirectory is versioned by a directory-prefixed tag, and only `vX.Y.Z` existed, so `github.com/FreePeak/LeanKG` had no release: `proxy.golang.org/…/@v/list` was empty and `pkg.go.dev/github.com/FreePeak/LeanKG/go` was a 404 (only a pseudo-version resolved). `release.yml` gains a `modtag` job that mirrors each release commit as `go/vX.Y.Z` (refusing to move a tag that already exists), and `go/v0.31.3` was cut by hand for the current release. Verified live: the proxy lists `v0.31.3` as `@latest`, pkg.go.dev serves the module page, and `go install github.com/FreePeak/LeanKG/cmd/leankg@latest` → `leankg 0.31.3`. The README's install section now leads with that command.

**Container + Render deploy** — the Render service (`srv-d75ncl7fte5s73e6ro40`, runtime `docker`, `./Dockerfile`) has been building against a Dockerfile that PR #377 deleted with the Rust tree, which is why the live demo answered `/health` and `/mcp` from a stale August image and 404'd everything else — including the dashboard the README advertises. Restored: a three-stage CGO-free [Dockerfile](../Dockerfile) (engine binaries → **demo graph baked at build time** from a slice of this repo → unprivileged runtime serving it `--read-only`), plus a `.dockerignore`. `serve --ui` gains a JSON `/health` route on the dashboard listener (`TestDashboardListenerServesHealthAndShell`) — the SPA fallback would have answered a health probe with `200 index.html` forever, so a check that only wants a 200 could not tell a serving dashboard from a broken one. Two container-only defects surfaced while verifying and are fixed: SQLite cannot open a **WAL** database read-only unless it may create the `-shm` sidecar (so the store directory must be owned by the runtime user — `COPY --chown`, otherwise every query fails `attempt to write a readonly database (1544)`), and the "dashboard bound to" warning printed an empty host for the `:PORT` bind shape a PaaS uses.

**README** — the tag badges PR #326 dropped are restored as rows for platform (macOS · Linux · Docker · Render) and wired clients (Claude Code · Cursor · Codex · Gemini CLI · OpenCode · omp — exactly `leankg connect`'s targets), joined by release / pkg.go.dev / CI / license and a Go / SQLite / Postgres / MCP-surface row. Every badge URL was fetched and checked for text, not assumed. Stale claims corrected: releases **do** ship (v0.31.1–0.31.3 with all four tarballs — the file the README named, `release-go.yml`, no longer exists), 40 language profiles (not 13 — `internal/langs.Default`), ontology procedural workflows + req↔code traceability implemented in the Go engine (the "not implemented yet" line predated the parity waves), and the previously dangling `Live Demo · Docs ·` link row now points at pkg.go.dev and the changelog.

**Verification:** new dashboard-listener test green (`go test ./cmd/leankg/ -run TestDashboardListenerServes…`), `gofmt` + `go vet` clean · container run as the non-root user serving the baked store: `/health` → `{"ok":true}`, `/` → embedded shell, `/favicon.svg` → 200, `/api/search` + `/api/graph/clusters` returning real elements (4,641 elements / 14,727 relationships baked in 2.3 s) · module proxy + pkg.go.dev + `go install` checks above · Render deploy verified live at <https://leankg.onrender.com>.

**Verified live after merge (PR #392 → `main` 4bb0246):** the Render service rebuilt from the restored Dockerfile and reports `live` — `dep-dajp5slg1s2s73cln2o0`, the first successful build after two consecutive `build_failed` deploys (the missing-Dockerfile state, caught by reading the deploy history rather than guessing). At <https://leankg.onrender.com>: `/` → 200 `text/html` embedded shell, `/health` → `{"ok":true}`, `/favicon.svg` → the new mark, `/assets/index-*.js` 1.08 MB + `.css` 23.7 KB → 200, and `/api/index/status` reporting the baked store (`/var/lib/leankg/demo`, ~4.6k elements / ~14.7k relationships — the corpus is this repo, so the exact counts move with every image build) with `/api/search` and `/api/graph/clusters` answering real elements. The dashboard's own data path is therefore proven over HTTP, not just in a container.

**Module landing page (PR #396):** `go/README.md` — the page pkg.go.dev renders for the module — was still the W1 slice note: PostgreSQL and ConnectRPC listed as deferred (both shipped), `internal/ports/` in a layout where no such package exists, "7 packages" against the tree's 54, a link to a document #377 moved to `docs/archive/analysis/`, and the Rust line still described as "released and in maintenance" two waves after it was deleted. Rewritten to the current surface, layout and ceilings, with each claim read back against the code and both `go install` lines run for real.

**Open, on the record:** `ConnectRPC` (`--rpc`) still has no `/health`, so FR-SELF-01's "`/health` on every listener" AC is met on the MCP, REST and dashboard listeners only. `docs/mcp-tool-contract.md` is still generated from the deleted `src/mcp/tools.rs` and documents the Rust-era `set`/`get` pair rather than `import`/`query`.

### v4.11.0-selfhost-dogfood-loop — the plan, anchored: LeanKG serves itself first (2026-09-14)

**Trigger:** explicit user direction. The rewrite, the parity waves and the release pipeline are done (v4.10.x); the next move is not a feature but an **operating mode**: run the dynamic HTTP server as a persistent self-host over *this repository*, and use it to build LeanKG before scaling to the portfolio.

**Anchored as §3.10 (FR-SELF-01..04) and milestone M10:**

1. **S1 — bootstrap.** Gate: the in-flight refactor wave lands on `main` (the uncommitted Docker/Render deploy service, the `go/vX.Y.Z` module-tag mirror job, the `serve` `/health` liveness route). Then `leankg serve --http :9699 --rest :8080 --ui :8081 --memory` runs long-lived against this checkout; the repo is indexed, embedded with the pinned `LEANKG_EMBED_*` identity (ModelStamp-guarded, so L3 answers rather than degrades), and the memory layer is live (markdown + `session_retain`/`session_recall`).
2. **S2 — dogfood.** All further LeanKG development flows through the self-host: impact/callers before touching an exported symbol, tested-by/traceability before closing, recall when reopening a session. Every wrong, stale or empty answer the loop produces is an engine defect — fixed failing-test-first, then reindex-and-re-ask. "LeanKG first" means the tool earns its claims on its own codebase before anyone else's.
3. **S3 — scale.** When the loop is smooth, add **small parent directories with nested repos** to the same server (registry + `LEANKG_PROJECT_DIRS`, T0 manifest / T1 hot-set, cap 8, never eager). Measured guardrail stays in force: never bulk-index the `freepeak` portfolio root (99,574 files → multi-GB store); scale = repos added to the registry one at a time.
4. **S4 — keep building, fixing.** The loop is the end state, not a phase to exit: each indexing/embedding/memory wave on real code surfaces defects; the PRD status tracks the cycle.

No code changes ride this revision — it records the anchor and the gates.

### v4.10.1-hygiene-and-release — the post-cutover sweep, a smaller Go tree, and releases that actually ship binaries (2026-09-14)

**Trigger:** the Rust→Go cutover (f7624143, #370) left three kinds of debt behind: tracked build output, Rust-era scaffolding the rewrite no longer uses, and a release path that had never run.

**Repository hygiene** (~150 tracked files removed; `git ls-files` 1,131 → 913):

- **Tracked binaries/artifacts:** `bin/leankg-embed` (21 MB), `tmp-langs/` (29.8 MB Mach-O probe + fixtures), the 0-byte root `leankg`, `leankg-ui.png`, `test_minimal.html`, five `.DS_Store`, committed agent-session state (`.mimosa/`, `.pi/`).
- **Local config that was never meant to be shared:** `.config/opencode.json` (absolute host paths, a private LLM-proxy base URL, personal provider list), `leankg.yaml` (self-index config pointing `root: ./src` at the deleted Rust tree), `.devcontainer/` (rust image), `render.yaml`/`.dockerignore`/`entrypoint.sh` for a Dockerfile that no longer exists.
- **Superseded surfaces:** the old Sigma.js dashboard `ui/` and its `e2e/` spec (the engine embeds `ui-v2`, proven by `embed/ui-build.json`), Rust-era `benchmark/` results, `benchmarks/alamofire-30q/`, one-off scripts (`ship*`, `run_kilo_ab_*` later restored, `watch-leankg-build.sh`, 13 more), 12 generated RCA docs.
- **Kept deliberately:** `examples/` (20 dirs — 19 pinned by `extract_langexp2_test.go`, `go-api-service` is the CI TTFV fixture), `docs/archive/` (the historical record), and the vendored tree-sitter grammars. (`gitleaks.toml` was removed in the same sweep and no CI job runs a secret scanner — worth reinstating, since `.config/opencode.json` had committed absolute host paths and a private LLM-proxy base URL.)
- **Moved, not deleted:** `docs/go-rewrite-analysis.md` → `docs/archive/analysis/`, per the one-live-PRD convention.
- **Language statistics fixed:** GitHub reported the repo as C (94%) with Go at 4% because three generated tree-sitter `parser.c` files are 62 MB against 3.1 MB of Go. `.gitattributes` now marks `internal/tstree/**`, the embedded dashboard build and the indexing fixtures as generated/vendored, so linguist excludes them.
- **Git objects:** a leftover `refs/original/refs/heads/fix/p0-embed-resume-deadlock` backup ref from an unfinished `filter-branch` was still anchoring 5,154 Rust-era `target-linux/**` blobs (1.9 GB) as *reachable* in the local clone: `.git` was 613 MB against a 44 MB remote. Deleting that ref (its 5 only-there commits saved as patches first), expiring reflogs and `git gc --prune=now` took it to **48 MB with no history rewrite**. `origin/main`'s published history (branches + tags) now reaches **no** build binaries: a fresh clone is an 11.44 MiB pack, ~34% generated tree-sitter grammars and ~29% docs. The 48 MB of Mach-O/Go binaries survive only under `refs/pull/*`, which no force-push can rewrite — so a further purge of the ~0.72 MB of remaining historical residue (old `graph.json`, committed `.leankg` indexes) is measured as not worth rewriting public history for.
- **Git objects (second pass, 2026-09-14):** the residue above was then removed by a real history rewrite at the maintainer's request (`graph.json`, six `.leankg` index blobs, `.leankg_test`, `graph-test.html`, `graph-debug.html`, plus `tmp-langs/`, `bin/`, `src/embed/`, `ui/dist/` defensively). Outcome, measured rather than asserted: the 10 blobs were 8.31 MB raw / ~0.77 MB packed, and the delivered clone pack went 11.52 MiB -> 11.61 MiB (delta-compression noise swamped the removal) while GitHub's reported repo size stayed 11 MB. Cost: every SHA on `main` and all 172 tags changed, ~2 commits became empty and were dropped, and every clone/worktree had to re-sync. Recorded because the earlier entry in this section argued against exactly this rewrite on the same numbers, and the numbers held: a history purge is only worth running when the blobs are large relative to the pack, which these were not.

**Go restructure** (`go build`/`vet`/`test ./...` green, both tag sets):

- **Dead code deleted:** `internal/budget`'s `BudgetGuard` half — 256 lines of wall-clock/RSS/iteration aborts with zero call sites anywhere in the engine (the live `TokenBudget` half stays; MCP enforces it) — plus `go/CONTRACTS.md` (W1 fan-out scratch) and two never-called `setupcfg` exports (`MergeProjectRoots`, and the time-based `FreshnessLabel` that the seq-based derivation superseded) with their tests.
- **Duplicated logic collapsed:** index-freshness was derived twice (byte-identical in `core.freshness` and `doctor.fleetFreshness`) — now one `store.Freshness`; the savings percentage lived in four places inside `internal/compress` (three string-based `EstimateSavings` methods, two of them recomputing the heuristic with a literal `/ 4`, plus a `CompressionStats` variant that shadowed the shared function with a local of the same name) — now one `percentSaved` over the package's `CharsPerToken`, reached through `savingsPercent`; the Postgres DSN ladder was re-derived in two packages — now `projectcfg.PGURL`; and all **six** hand-rolled `sortedKeys` helpers (`convo`, `prdindex`, `export`, `store_metrics`, `budget`, `benchmark/ab`) became `slices.Sorted(maps.Keys(...))`, taking `budget`'s private insertion sort with them. Counters on the merged tree: `func sortedKeys` 0, `/ 4` in `compress` 0, `staleAfter`/`freshnessForEntry` 0 — the per-key staleness helpers went with the dead `setupcfg.FreshnessLabel`, leaving `store.Freshness` as the only freshness derivation in the engine.
- **Deliberately kept:** the three `truncateRunes` copies in `cmd/leankg`, `convo` and `embed` are rune-bounded and are *not* the same contract as `store.ClipUTF8`, which is byte-bounded — unifying them would change behavior, so only `embed`'s copy was made panic-safe (its `[]rune(s)[:n]` form relied on a guard two lines above at the single call site). `writeJSON` stays per-package: three near-identical 8-line HTTP helpers do not justify a new shared package.
- **Broken build tooling repaired:** `make go-ui-assets` copied from the deleted `src/embed` (Rust path) and is now a real `ui-v2` → `embed/` sync; `make go-build-tstree` / `go-test-tstree` were advertised in `help`/`.PHONY` but had no rules; the `go-bench` harness's `run_kilo_ab_*.sh` clients are restored.
- **Stale agent docs rewritten:** `CLAUDE.md` and `GEMINI.md` carried 340 lines of `cargo run -- init`, `src/lib.rs` and `src/mcp/tools.rs` instructions under a "historical" banner; both are now short, accurate pointers to `AGENTS.md`, which gained the release-pipeline facts and the `.gitignore`-traps note.

**Release pipeline** (`fix(ci)`, see §Release in `AGENTS.md`): `release-go.yml` was `workflow_dispatch`-only and had run **zero** times, so nothing had been released since v0.30.0. It is replaced by `.github/workflows/release.yml`: release-please cuts `vX.Y.Z` on a merged release PR, and the tag, GitHub Release and four `leankg-<goos>-<goarch>.tgz` assets happen in the **same run** — because a tag pushed with `GITHUB_TOKEN` cannot start a second workflow, which is exactly why v0.28.1/v0.29.0/v0.30.0 shipped with no assets (and v0.27.0 stayed a draft, invisible to the `releases/latest` that `leankg update` polls). Two configuration defects were caught by reading release-please's own source: `.release-please-manifest.json` carried a `$schema` key (its parser runs `Version.parse` over **every** value and throws) and keyed the version `"main"` where the lookup is by package path (`"."`); `cmd/leankg/VERSION` is bumped natively via `version-file` instead of a comment marker in a file that has no comment syntax. The release run gained a gate asserting tag == VERSION == the MCP `serverInfo` const, since `ci.yml` skips release-metadata commits.

**Verification (live, not inferred):** `gofmt` clean · `go build ./...`, `CGO_ENABLED=0 go build ./...`, `go build -tags tstree ./...` · `go vet` both tags · `go test ./... -count=1` green — including from a **pristine `git clone`** (the guard against the global-`gitignore` `leankg` pattern that once hid `cmd/leankg` from every pushed commit) · CLI smoke of the refactored paths (`leankg version` → `index` → `status` reporting `"freshness": "fresh"` through the new `store.Freshness` → `doctor` exit 0) · manifest/config resolution replayed against the installed release-please library.

The pipeline then released for real: **v0.31.1 and v0.31.2 were cut automatically by release-please on merges to `main` and each published with all four `leankg-<goos>-<goarch>.tgz` assets** (verified by downloading `leankg-darwin-arm64.tgz`, running it — `leankg 0.31.1`, `leankg-embed 0.31.1` — and matching the published SHA256 against the download). The first automated release, v0.31.0, shipped with **zero** assets, which is how two further defects were found and fixed: the action's outputs are `tag_name`/`version` (read out of the shipped bundle, not the docs), and `inputs.version` resolves empty inside a job-level `if`, so both silent-skipped the whole asset chain while reporting success. `publish` now asserts the four tarballs exist and refuses to move `releases/latest` backwards, and v0.31.0 has been backfilled with its binaries.

**Version math, for the record:** the `feat(go)!` cutover commit did **not** produce v1.0.0 — `bump-minor-pre-major: true` makes release-please treat a breaking change as a minor bump while the major is 0. The first 1.0.0 is therefore still a deliberate decision (`release-as` on a release PR), not an accident of the queue. **Proved the hard way in the #403 cut:** the simple strategy's releasable commit set excludes `chore`/`refactor`/`docs`-type commits, so a merged `refactor!`/`BREAKING CHANGE` squash opens **no** release PR at all ("No user facing commits found" in the version job) — when the cut rides such a commit, declare the version deliberately with an empty `Release-As: X.Y.Z` trailer commit (#417), which forces the release PR on the next push.

### v4.10.0-issue-scope-and-dogfood — the open issues implemented, then hardened against this repo's own data (2026-09-13)

> **Trigger:** PR #370 resolved the rewrite-scope issues (#365/#368/#369/#332); an issue-by-issue audit against the
> code — not the docs — left ~10 open. All of them are implemented here, and then the whole surface was run live
> against this repository's own index (real ONNX embeddings, both engines), because that is where the remaining
> defects were.

**Issues closed by this revision** (each verified on real data, not only by unit tests):

| Issue | Shipped | Measured evidence |
|---|---|---|
| #273 | PG keyword + hybrid retrieval: `fts tsvector` generated column over (name, qualified_name, **content**), GIN indexes, `ts_rank` L2 arm, reciprocal-rank fusion of vector+tsvector+trigram at L3, `orgknowledge` routed to the same arm (migration **12**, PG-only by design) | On this repo's own 11,327-element PG store: L2 answers `tsvector keyword match`; L3 answers `reciprocal-rank fusion rrf(vector+tsvector)` with one hit contributed by the keyword arm alone (`ranks={tsvector:1}`, `similarity=0`) — real fusion, not a label |
| #279 | Pinned model catalog (repo + 40-hex revision + dims + prefixes per row), `chunker_version`/`query_prefix`/`document_prefix` in the collection stamp (migration **15**, both engines), prefix application at the request layer, whole-identity compare on the READ path so chunker or prefix drift degrades instead of mixing vector spaces | Live on 9,250 real vectors: setting `chunker_version` 1→0 in the DB served `stamp mismatch: collection differs from the live provider (chunker_version 0, live revision ea104da…); degraded from L3; run leankg-embed full to rebuild` at L2, never mixed-model hits; restoring it returned cosine L3 |
| #275 | Session-memory adjacency over the 3-tool envelope: `session_retain` (write, with the `retained_through_user_turn` cursor) + `session_recall`/`memories` reads, the scope matrix (`per-project`, `global`, `per-project-tagged`), MCP schemas, REST routes, strict `ParseScope` | Live MCP round trip: retain 2 turns → `written=2`; re-retain at the same cursor → `skipped=2, written=0`; `<memories>` injection block returned |
| #276 | A/B harness hardening (`benchmark/ab/harness.go`): 40-hex pins with refusal-to-measure, ≥3 trials/arm enforced at aggregation, label-erased blind judging with an injectable seam, the 5 zvec-grep pitfalls reported in every run artifact, FR-HEA-01/03 ontology accounting | 14 hermetic harness tests + 5 alias-accounting tests; `abrun score` verified to fail a 2-trial arm by name |
| #280 | Time-to-first-value gate: cold-cache build+index+query budget asserted in `scripts/ttfv_smoke.sh`, `ttfv` CI job with an artifact | Measured: warm 4.9 s, `GOCACHE`-cold 17.8 s, fully cold **21.6 s** against a 300 s budget; the over-budget path exits 1 |
| #61 | Vendored `tree-sitter-perl` v2.0.0 (MIT) regenerated to **ABI 14** (every published release targets ABI 15, which the Go runtime rejects), behind `//go:build tstree` on every `.c`/`.h`; module/method/import kinds with `use_statement` module-field resolution | On 1,398 real Perl files: **99.2 % clean parses vs 42.8 %** before (791 files recovered). Live: `Inventory.pm::stock_count`, `MyApp::Inventory`, Moose/`namespace::autoclean` imports and a v5.38 `sub tally ($self, $items)` all indexed |
| #297 | graft's two-pass LLM-meaning pipeline: `file_summaries` checkpoint (migration **14**, model-pinned resume), pass 1 one-completion-per-file at temperature 0 behind a failure gate (5 consecutive → stop; terminal quota/auth → immediate), pass 2 JSON-validated node/relation synthesis with closed enums, markdown node files that preserve human notes, `leankg summarize`, opt-in post-index hook (default **off**) | 23 tests; the hook's default-off contract pinned so an index can never start spending tokens because a provider happens to be configured |
| #372 | Federation **receiver** (`POST /api/v2/graph/push` + `GET /api/v2/graph`, mounted per project through `rest.Handler`), Contributor+ to write, upsert-by-identity apply that never deletes unmentioned rows and carries local content forward (the wire has no content slot), provenance into metadata; `pull` now fetches **and applies**, degrading to the Rust connectivity probe against a server without the route | Live push→pull round trip between two projects: 2 elements/1 relationship applied, incoming fields won the conflict, local content and unmentioned rows survived, provenance re-stamped |
| #376 (with #277/#278) | Portfolio registry (migration **13**, sqlite `$LEANKG_PORTFOLIO_DB` or its own PG schema), register-on-index, T0 manifest / T1 hot-set fan-out running the **real** L0–L3 ladder inside each project with per-child attribution, `portfolio` query action, `register-project`/`projects` verbs, `doctor --deep` fleet leg with migration-drift vocabulary, `projects --forget` | Live over MCP: one `--action portfolio` call returned the Perl symbol from one child at L1 and this repo's documents at L3 in a single merged answer |
| #73 | `leankg update [--check]`: Releases lookup, semver compare that never treats an unparseable version as equal, bounded download, archive verification (SHA-256 when published, else a content check that says so), atomic sibling-temp replace with `.old` rollback, `PermError` naming the exact `sudo` command, exit 1 when `--check` is behind | 33 tests; live end-to-end install replaced both binaries and the swapped binary ran; live `--check` reports the honest "no darwin-arm64 asset published yet" |

**Plus the earlier mis-scoped closures:** #274/#272 (single-flight already existed; the real remainder was per-file atomic replace, which #279 shipped), #286, #27, #371.

**Dogfood findings — every one a real defect, every one fixed with a failing-first test:**

| Finding | Consequence before the fix | Fix |
|---|---|---|
| `import {action:"repo", path:"."}` resolved **`.` against the server's cwd**, then the store-wide reconcile deleted what the walk missed | Cost this repository's own index **4,525 of 9,250 elements** during the dogfood (recovered from a pre-test copy with vectors intact) | Relative paths anchor at the project dir; a subtree root is **refused** instead of attempted; `index.Result.DeletedFiles` surfaces as `deleted_files` so a routine run cannot hide a sweep — `internal/core/importpath_test.go` shows `3 → 0` and `3 → 1` pre-fix |
| `leankg index . --source <tree>` walked the source against the project's store | Replaced the project's content with the source's **at identical counters** (1 element before, 1 after), so a count-only assertion passes on the bug | `sameIndexRoot` refuses the mismatch with the actionable alternative; `cmd/leankg/indexroot_test.go` asserts surviving **file paths** |
| Content clipped on byte boundaries: `prdindex.truncate` backed off to the first `RuneStart` (which stops on a lone lead byte) and `index.boundContent` did not back off at all | `invalid byte sequence for encoding "UTF8": 0xe2` aborted the **whole PostgreSQL batch** — found only by indexing this repo into PG; every source file was valid UTF-8, so a clip manufactured the byte | `store.ClipUTF8` (bounded valid-prefix loop) at all three sites, plus `store.ValidText` at **both** backends' write boundary so no producer can reintroduce the class and both engines store identical bytes; `internal/store/clip_test.go` sweeps every boundary |
| The PG keyword arm indexed identifiers only while sqlite's FTS5 covers content, and never degraded on an empty-but-successful tsvector result | Multi-word queries returned **0 hits on PG where sqlite returned 10** — silent engine-dependent amnesia | content added to the tsvector (weight B, matching migration 12's own comment), and an empty result now falls through to trigram → ILIKE |
| `portfolio` was missing from the tool-budget table, inheriting the 1000-token default | A two-project fan-out was truncated to its own summary — `children` and `hits` gone, `truncated=true` | `portfolio` gets the widest cap on the table (12000), with the reason recorded in the table |
| The inventory snapshot was refreshed only by the Engine's own write path | `leankg index`, `summarize`, `pull` and `leankg-embed` all left a healthy project reading **`possibly_stale`** until some later unrelated write | `store.RefreshInventory` (one function over `Backend`, both engines) called by every CLI writer — verified `possibly_stale → fresh` at Coverage 1.0 on the real repo |
| `var version = strings.TrimSpace(embeddedVersion)` — a non-constant initializer | `-X main.version=<tag>` was **silently ignored** for `cmd/leankg`: a dispatched v0.32.0 release would have shipped a binary reporting the VERSION file | constant initializer + `Version()` fallback; verified `9.9.9` stamped, `0.31.0` unstamped |
| `checkFleet` classified a **vanished checkout** as `UNREADABLE` | Any long-lived machine accumulates permanent doctor noise — and the test suite was writing the real `$HOME/.leankg/portfolio.db` (14 rows of deleted temp dirs, since pruned) | `Missing` is a distinct informational state (PASS + the `--forget` hint); CLI tests redirect the registry through `TestMain`, so `go test ./...` never touches user state |
| One-shot CLI verbs built their engine with a nil embedder | `leankg query --action semantic` could **never reach L3**, even with a provider running | `openEngine` attaches `embed.FromEnv()` (attach-only, so a CLI run cannot orphan a sidecar); L3 verified from the CLI on real vectors |
| `compress` with `args.cmd` and no payload returned a zero-count envelope | A caller could not distinguish "nothing to compress" from "payload in the wrong field" | accepts the output in `query` **or** `args.response`, and names both when neither is set |

**Migration ledger after this revision:** sqlite 1–11, 13–15; PostgreSQL 1–15. The gap is deliberate — migration 12 is
PG-only because sqlite has had FTS5 since migration 2 — and the store tests pin both ledgers' ordering and uniqueness.
The 6→15 upgrade ran live against this repo's real 53 MB store by starting the server on it.


### v4.9.2-adversarial-audit — silently-wrong results closed (2026-09-12)

> **Trigger:** an adversarial audit of the merged wave found surfaces that were *quietly* wrong rather than red.

**Fixed (each with a test):** flags after positionals (clap semantics) across 47 call sites — `env-conflicts w2 --env production` had reported an empty-service success, `refresh . --full` ran incremental, `query --kind impact foo` looked up the literal `--kind`, and surplus positionals are now rejected with `unexpected argument` (`run` keeps the raw parse deliberately; its child argv must not be re-parsed) · `push`/`pull` were advertised in help but missing from the dispatch switch · `/api/project/switch` refused switching *after* multi-project serving shipped, and now consults an injected **non-opening** resolver (`Router.Resolve` — no store open, no `Migrate`) returning success for a served project or a refusal naming `LEANKG_PROJECT_DIRS` + `?project=` (the old check passed on HTTP 200 because `failEnvelope` is also 200 — the new test asserts the payload) · **a viewer bearer could mint itself an admin token**; the requested role is clamped to the caller's role for non-writers · `refresh --full` labelled its embed step "incremental" · `setup --status` printed nothing on an empty workspace · FR-ZCP-13 gained the persistence test its AC had been claiming.

**Remainders recorded** (§6b "Known remainders and unverified seams"): PG migrations 010/011 unexecuted on Postgres · `gc.rs` as a Go-native substitute (idle-gated embedding dropped) · the `doc_indexer` doc→code join unported · 8/14 error codes unwired · `env_snapshots` cannot env-filter calls/schemas · `team` verb + `/api/teams` unported · embeddings single-flight + per-file atomic replace absent (#279).


### v4.9.1-parity-third-wave — project config + the FR-ZCP-13 first-run contract (2026-09-12)

> **Trigger:** the module audit's last two gaps: Rust's `leankg.yaml` project config (only the LSP block had been ported) and the first-run setup contract FR-ZCP-13 (setup choice, setup pipeline, auto-index gates).

**Delivered**
- **`internal/projectcfg`** — the full `ProjectConfig` shape + defaults (project/steer, indexer, mcp, documentation, microservice, auth, lsp, source, db), parse/absent postures, project-path resolution (nearest-config walk-up, `project.project_path` anchor, canonicalized), the `db:`/`auth:` walk-up readers, and the N1 read-modify-write migration helpers (existing keys — including unmodelled ones — win; missing keys are filled; idempotent).
- **Wired, not just landed**: `resolveProjectDir` consults the anchor; `pgURLFor` applies Rust's precedence (env > `db:` yaml > default) at the store call sites; `index` self-heals a missing `project_path` before deriving the schema; `serve`/`doctor`/`status` resolve their store through `FindProjectRoot`/`ResolveProjectRoot`; `status` publishes the effective config (with `mcp.auth_token` redacted); `doctor --deep` adds the config `project_path` as a schema-identity candidate and gains a `config` check (WARN unreadable, FAIL dangling anchor); each `LEANKG_PROJECT_DIRS` project honors its own anchor.
- **`internal/setupcfg` + `internal/setup` + `internal/indexgate`** — FR-ZCP-13 end to end: the auto/manual choice persisted at `<project>/.leankg/config.json` (unknown keys preserved), `leankg setup [--reset|--clone|--index|--embed|--status]` (repo resolution from `LEANKG_REPOS`/`LEANKG_PROJECT_DIRS`/`LEANKG_WORKSPACE_DIR`, cloning via `internal/sources`, the config template written through the preserving merge), and the auto-index decision table (`ReadOnly|Disabled|Fresh|NoGit|Index` + the `LEANKG_SKIP_FRESHNESS_CHECK` escape hatch) driven by the `mcp.auto_index_*` keys — with the question asked once on `index` (flags > `LEANKG_SETUP_MODE` > stored > TTY prompt > manual).

**Deliberate choices:** `status` keeps the store-watermark `freshness` definition the v4.4.3 contract already publishes (the Rust git-commit variant would be a second, conflicting definition); an explicit `leankg index` always indexes — manual mode governs the automatic paths only; setup stages run in-process through a `Stages` seam rather than re-exec'ing the binary.

### v4.9.0-parity-second-wave — the Rust internals nobody had ported (2026-09-12)

> **Trigger:** after v4.8.0 closed the recorded deferred ledger, a systematic audit of the Rust tree (every `src/*` module and all 73 CLI variants) found capabilities the ledger never listed. All are now ported from `f7624143^`.

**Delivered:** federation client (`push`/`pull`) · conversation mining (Claude/ChatGPT/Slack) · persisted usage metrics (`context_metrics`, `leankg metrics`, H10 buckets behind `leankg dashboard`) · org knowledge (incidents, team notes, env conflicts, service context, team map; store migration 010 + `/api/v2/*`) · PRD indexing (`FR/US/AC` → requirement entities + workflow edges; `leankg prd`, `prd-trace`) · remote sources (git+/GCS/local; `index|refresh --source`) · response token budget (`internal/budget`) · error catalog (`internal/errs`, FR-ZCP-12 T1).

**Fixes found while porting (Rust defects not reproduced):** mining dropped every `decided_about` edge but the last per file; the PRD id regex silently dropped `FR-ZCP-13`/`FR-GE-01`-shaped ids; `pull` was never a data pull and no server ever served the push route (recorded in #372).

**Not applicable / deliberate:** `gc.rs` (Rust-allocator `malloc_trim`/RSS workaround — Go's scavenger owns this); `setup --clone` pipeline (the `sources` package can serve each spec if it is wanted); Rust's `team`/`/api/teams` (the Go engine has membership + ownership in the auth subsystem, no teams table).

**Verification:** `gofmt` clean · build ×3 (default, `CGO_ENABLED=0`, `-tags tstree`) · `go vet` ×2 · `go test ./...` green under both tags · `go mod tidy` no-op · live CLI smokes for the new verbs.


### v4.8.0-full-parity — every deferred item implemented; nothing silently missing (2026-09-12)

> **Trigger:** user direction — "implement all the missing part, I want everything not missing anything", fanned out across subagents with the deleted Rust tree as the contract.

**Delivered (closed the whole v4.6.0/v4.7.0 deferred ledger, each ported from `f7624143^`)**
- **Dashboard API (#371)**: all 11 legacy `/api/*` endpoints on the `-ui` address (index status, search, query, query-graph, file, graph children/expand-service/clusters/report/service-topology, project switch), SPA fallback now JSON-404s `api/*`; verified live on every route.
- **Ontology**: procedural workflows (YAML loader/sync/marker), traceability (`trace`, `feature_flow`, `traceability_matrix`), concept search, safe-discover mega-graph path, ontology-guided downward traversal.
- **Compression**: the full Rust pipeline in `internal/compress` (8 reader modes, cargo-test/git-diff/shell command compressors, 7 response shapers, session cache, entropy, symbol map, LITM), reachable through the 3-tool envelope (`import{action:"read"}`, `query{action:"compress"}`) and the CLI.
- **LSP bridge**: project-configured server catalog + typed call-edge resolution + index-time enrichment, **gated on project config** so default indexing is unchanged; tag→catalog mapping spans every registry language.
- **Android/Gradle/Maven**: 14 specialist extractors with fixtures, wired through the shared walk/doctor gate.
- **Embeddings**: llama.cpp sidecar lifecycle (spawn, bounded health poll, process-group shutdown, attach-or-spawn, actionable failure — never fake vectors) + `leankg-embed` wiring.
- **Languages**: registry 13 → **40** (all 27 remaining examples/ languages), objc/dart tree-sitter grammars vendored with objc message-send call edges and dart ctor/factory/enum elements.
- **Enterprise**: accounts/orgs/memberships/team_members/resource_ownership + `/api/v1/auth/*` (public bootstrap mounted outside the bearer gate); DB-backed tokens with expiry/revocation/scopes/last-used.
- **Multi-project serving**: `LEANKG_PROJECT_DIRS` with per-request `?project=` / MCP `project` routing; single-project behavior byte-identical when unset. **doctor --deep**: 8 fleet checks with Rust exit semantics.
- **Obsidian**: vault init/status, push (store→notes), pull (notes→Note elements, wiki-links, annotations), debounced watcher.
- **CLI parity**: `run`, `detect-clusters`, `report`, `gods`, `ctags`, `cost`, `migrate`, `audit export|verify`, `auth register|token …`, `export`, `pack`, `generate`, `annotate`, `link`, `search-annotations`, `show-annotations`, `register`, `unregister`, `list`, `status-repo`, `tunnels`, `quality`, `reflect`, `refresh`, plus `query --action` passthrough.

**Security deviation from Rust (deliberate):** `/api/v1/auth/register` and `/login` no longer serialize `password_hash` — the Rust port returned the PBKDF2 verifier (salt + KDF params + digest) to the caller, which is credential material leaving the process; the field is now in-process only (`json:"-"`), pinned by a test. The Go engine also enforces the bearer middleware on `/api/v1/status`-class routes and requires a valid token for revoke, where Rust checked presence only.

**Fixed while integrating** (found by the wave, not by tests): `CGO_ENABLED=0 go build ./...` was broken by untagged vendored C sources (CI could not see it — a CGO-free tree build step is now in `ci.yml`); `/api/v1/auth/*` was behind the bearer gate (bootstrap deadlock); the dashboard listener's unauthenticated API now warns when bound beyond loopback; the marker-less language census never looked deeper than two levels (silent zero-file indexing); `leankg impact` double-called its handler (panic); MCP `serverInfo` version drift; the `leankg add`/`mcp-stdio` dead wiring.

**Audited, not assumed**: the per-verb disposition table (§6b) accounts for all 73 Rust CLI variants; the five capabilities that remain unimplemented are *new product milestones*, tracked as #372–#376.


### v4.7.1-merge-truth — CI-green fixes + honest ledger for the Go cutover (2026-09-11)

> **Trigger:** pre-merge audit of PR #370 — the one CI job gating the rewrite was red, and three shipped claims (dashboard done, install paths, tracked corpus) did not survive contact with the branch.

**Fixed**
- **ast-grep probe drops the deprecated `sg` alias** (`internal/astgrep`): `New()` resolves only `ast-grep`. The previous name-only probe of `sg` accepted util-linux's set-group command on Linux, so `query{action:"pattern"}` shelled out to a binary that prints no JSON and returned a hard error instead of the documented L2 degrade, while `internal/langs.astGrepPresent()` (the same name check) advertised a live ast-grep tier in `status`. `sg` is deprecated upstream and validating an alias would have cost a process spawn per probe — dropping it removes the whole impostor class without one. Regression tests: `TestNewIgnoresSgAlias` (rejects `sg` without executing it) + `TestAstGrepPresentIgnoresSgAlias` (tier stays dark).
- **Client wiring spawned a command that does not exist** (`cmd/leankg`): `connect` and `install --target` wrote `["mcp-stdio"]` (± `--project`) as the stdio entry, but the Go binary has no `mcp-stdio` subcommand — the real mode is `serve --stdio`. Every wired client (claude-code, cursor, codex, gemini, opencode, omp) spawned a process that died before answering. The tests pinned the broken string, so the suite could not see it; `TestStdioSpawnServesMCP` now builds the binary and drives the emitted argv over stdio (initialize → initialized → tools/list = `import`/`query`/`status`), so future wiring regressions fail loudly. Stdio entries stay projectless by default (FR-ZCP-04 URL contract); `--project` remains the explicit escape hatch.
- **SessionStart hook ran a verb that does not exist** (`cmd/leankg`): `install --register-cwd` wrote `leankg add $CLAUDE_PROJECT_DIR` into `~/.claude/settings.json`, and the Go binary has no `add` case — Claude Code would fail the hook every session. The Go attach-and-index equivalent is `index`, which is what the hook now runs (creating-or-updating that project's store, incremental after the first run); `TestHookCommandRuns` executes the expanded hook through a shell — with a space in the project path — and requires the store it promises; the hook embeds the resolved exe (`CurrentCommand()`) and quotes the variable, so neither PATH nor spaces can silently no-op it.
- **Dangling gitlink**: `benchmark/corpora/docs` was committed as a submodule reference with no `.gitmodules` entry — `git submodule update --init` and `--recurse-submodules` clones failed (`fatal: No url found for submodule path 'benchmark/corpora/docs'` in CI's own post-job). Removed.
- **Install surface**: `scripts/release.sh` (drove the deleted `Cargo.toml`, referenced nowhere) removed; `scripts/install-go.sh` now clones over HTTPS instead of `git@github.com:` and prints the verified `install --target <client>` next step.
- **Version stamp**: MCP `serverInfo` reported the stale marker `0.31.0-go-w1`; it reports `0.31.0` again, with `internal/mcp/version_test.go` failing if it drifts from `cmd/leankg/VERSION`.

**Corrected claims**
- Parity ledger `web api+ui`: **DONE → PARTIAL**. `internal/web` embeds and serves the ui-v2 build, but the dashboard's `/api/*` contract (11 endpoints: index status, search, query, query-graph, graph clusters/report/children/expand-service/service-topology, file, project switch) is not ported — the SPA fallback answers those calls with `index.html`, so the dashboard renders without data. Port tracked in **#371**.
- README install/CLI/contributing sections rewritten to the Go surface: `cargo install`, the crates.io badge, and the Rust-era verbs (`init`, `update`, `path`, `explain`, `graph-query`, `embed --init`, `mcp-stdio`, `ontology sync`, `serve --port`) described binaries and subcommands this tree no longer contains.

### v4.7.0-lazy-languages — AST tiers + lazy per-codebase language activation (2026-09-11)

> **Trigger:** user direction: support the default language set **go, rust, ts, tsx, js, jsx, py, md, java, kotlin, swift, objective-c, flutter(dart)** via ast-grep + tree-sitter + optional LSP; everything idle/lazy — activate per opened codebase (nested repos each activate their own slice); LSP consulted against the queried directory at query time.

**Delivered:**
- `internal/langs` — 13-language registry: extensions (single-owner + `.h` header rule), repo markers (go.mod/Cargo.toml/package.json/tsconfig/pom.xml/build.gradle*/Package.swift/Podfile/pubspec.yaml/…), aliases (golang, txs, flutter, object-c…), LSP command candidates. `Activate(codebase)` walks bounded depth (nested roots + marker-less census supplement); `Deactivate` returns to idle; `Tiers()` reports per-language LIVE capability: `regex` (always), `tree-sitter` (build tag `tstree` only — default stays CGO-free), `ast-grep` (CLI on PATH), `lsp` (server binary resolves).
- `internal/tstree` — CGO tree-sitter tier behind `tstree`: bundled grammars for go/rust/ts/tsx/js/jsx/py/java/kotlin/swift, **wired into the indexer** (`tstree_seam.go`: tree-sitter first with real block end lines, regex fallback per language; objc/dart/md have no bundled grammar → regex+LSP gap, documented). CI compiles+tests the tag (tstree job runs before the default suite) + tidy guard.
- `internal/lsp` — lazy JSON-RPC/Content-Length client (stdlib framing): per (language, rootDir) pool, 5s startup bound, didOpen+documentSymbol (hierarchical+flat normalized, 1-based lines) + workspace/symbol, idle-TTL eviction.
- `internal/astgrep` — ast-grep CLI wrapper (argv-safe, ctx-bounded, `--json` parsing).
- Indexer: java/kotlin/swift/objc/dart regex extractors (+ per-language testdata fixtures); `IndexDirWith(reg)` routes through the registry (lazy) while `IndexDir` stays all-on compatible.
- Transports: `status`/`query{action:"languages"}` carry active languages + live tiers; `query{action:"lsp"}` runs server-backed workspace/document symbol lookups scoped to the queried directory (server spawned lazily, pooled, idle-evicted); `query{action:"pattern"}` runs ast-grep structural search and DEGRADES to L2 keywords when the CLI is absent.

**Verified:** live serve on a polyglot fixture activated go+dart (markers) and java (census supplement) with correct tier lists; registry tests incl. nested repos, aliases, idle semantics; dual-engine gate green.

### v4.6.0-go-full-parity — Go engine at full surface parity; Rust line removed (2026-09-10)

> **Trigger:** maintainer direction: "keep working until the Go code replaces 100% of the Rust code" — all waves landed, both engines live-verified, then the Rust tree deleted.

**Parity ledger (analysis §7 row-by-row):**

| Rust subsystem | LOC | Fate in Go | Status |
|---|---|---|---|
| db (Cozo+translator+sqlite) | 18.6k | `internal/store` — plain typed SQL, Backend interface, SQLite WAL + PostgreSQL/pgvector (schema-per-project, per-model vector tables + HNSW) | **DONE** (10/10 live-PG tests) |
| mcp transports + envelope | 16.4k | `internal/mcp` (official go-sdk, stdio+streamable HTTP) + `internal/rpc` (ConnectRPC gRPC/gRPC-Web/JSON) + `internal/rest` | **DONE** |
| graph/query traversal | 15.2k | `internal/graph` — Impact/ShortestPath/Callers/Callees/Context/Explain, wired as query actions | **DONE** (connection verbs; ontology-walk provenance = deferred, see below) |
| indexer extractor+call graph | 11k | `internal/index` + `internal/docindex` — **13 default languages** (go, rust, ts, tsx, js, jsx, py, md, java, kotlin, swift, objective-c, flutter/dart) with **lazy per-codebase activation** (`internal/langs`): nested repos activate their own slice; \`IndexDirWith(reg)\` routes extraction through the registry. Extraction tiers: regex (all 13, always), tree-sitter (`tstree` build tag, 10 grammars — objc/dart treesitter absent in the bundled set), ast-grep (\`query{action:"pattern"}\`, degrades to L2 when the CLI is absent), LSP (\`query{action:"lsp"}\`, server pooled per (lang,dir), idle-evicted, consulted at query time). Live-verified indexing all 13 + runtime activation. vs Rust's 43 grammars | **DONE for the 13 defaults; broader language list = future tree-sitter grammar additions** |
| embeddings | 8.2k | `internal/embed` + `cmd/leankg-embed` — Provider port (OpenAI-compatible = API + llama.cpp sidecar), **sidecar lifecycle management** (`embed.StartProvider`: spawn `LEANKG_EMBED_SIDECAR_CMD` (default `llama-server`), bounded `/health` readiness poll, process-group SIGTERM→SIGKILL shutdown on cancel/release; unset `LEANKG_EMBED_SIDECAR_ARGS` now serves the pinned bge-small-en-v1.5 GGUF (`-m ~/.leankg/models/bge-small-en-v1.5-f16.gguf` or `-hf CompendiumLabs/bge-small-en-v1.5-gguf:f16` self-download); absent sidecar fails actionably — never fake vectors), ModelStamp guards, NDJSON offsite | **DONE** |
| web api+ui | 5.8k | `internal/web` (go:embed ui build, SPA fallback) + `APIHandler` (all 11 legacy dashboard endpoints: index status, search, query, query-graph, file, graph children/expand-service/clusters/report/service-topology, project switch) + REST `/api/v1/*` | **DONE** — the `-ui` address mounts `/api/` ahead of the SPA; unknown `api/*` returns a JSON 404; verified live (every route JSON, SPA preserved). Port of `src/web/*` + `src/graph/{query,nl_query,clustering,provenance}.rs`; #371 closed |
| cli/connect/install | 2.2k+1.4k | `cmd/leankg` — serve/index/writer/doctor/connect/install for 6 clients, --register-cwd hooks | **DONE** |
| ontology | 4.0k | `internal/ontology` — concept catalog + element matching + kv persistence + **procedural workflows** (YAML loader/sync/marker), **traceability** (`trace`, `feature_flow`, `traceability_matrix`), concept search, safe-discover for mega-graphs, and the ontology-guided downward traversal; reachable via `import{action:"ontology"}` (dir = workflow sync) and `query{action:"ontology", args:{cmd: matches\|trace\|status\|concept_search\|feature_flow\|traceability}}` over MCP/REST/CLI | **DONE** |
| session offload | 0.9k | `internal/session` — offload/recall bit-for-bit + checksums, canvas, lesson dedup; reachable via `import{action:"session", command:offload|lesson}` + `query{action:"session"}` (canvas/recall) over MCP/REST/CLI, POST /api/v1/session/read; core + mcp + rest tests pin the round-trip | **DONE** |
| compress | 3.5k | `internal/compress` — reader modes (adaptive/full/map/signatures/diff/aggressive/entropy/lines), command compressors (cargo test, git diff, shell), response shapers (impact/call-graph/search/dependencies/context), session cache, symbol map, entropy, LITM; reachable through the envelope as `import{action:"read", args:{mode,lines,fresh}}` and `query{action:"compress", args:{cmd\|tool+response}}`, plus the CLI `query --compress` / `run --compress` paths | **DONE** |
| lsp bridge | 2.6k | `internal/lsp` — bridge over the query-time client: per-project `leankg.yaml` config (servers/workspace_root/timeout_ms), server catalog + capability tiers, typed call-edge resolution (`indexer.typed_resolve`, default off), and the index-time enrichment pass (`lsp.Enrich`) **gated on project config** so a codebase without an LSP block indexes exactly as before; tag→catalog mapping covers all registry languages | **DONE** |
| Android/Gradle/Maven extractors | ~9k | `internal/index` specialists — AndroidManifest (+ permissions/components), resources (string/color/style/dimen/bool/integer/array), Jetpack nav (XML + Kotlin DSL), fragment/leanback nav, Room, Hilt, WorkManager, resource refs/linking, Gradle (deps/module rels), Maven; wired through `SpecialistClaim`/`indexSpecialistFile`/`indexKotlinExtras` (walk + doctor agree on the same gate) | **DONE** |
| benchmark harness | 4.5k | `benchmark/ab` — Go benchmarks + **executed A/B REPORT.md** (fresh Rust 0.30.0 build from pre-removal commit vs v4.6.0: index parity 0.10s/0.10s, L1 53ms/50ms, 12.7× smaller binary; impact marked NOT COMPARABLE — seed granularity differs (Rust=file, Go=element QN) and the two arms disagree on the same chained corpus; in-process Rust cells 'not measured') | **DONE** |
| audit/doctor/auth | ~3.3k | audit ledger (hash-chained, tamper-verified) + RBAC middleware + **enterprise auth** (accounts/orgs/memberships/team_members/resource_ownership + `/api/v1/auth/*` handlers, public by design and mounted outside the bearer gate) + DB-backed tokens (expiry/revocation/scopes/last-used) + `doctor --deep` (8 checks, Rust exit semantics 0/1/2, `--format json`) + CLI `audit export\|verify`, `auth token …`, `migrate` | **DONE** |
| npm wrapper + manifest + release pipeline | — | removed with Rust; Go release engineering is now automatic: `.github/workflows/release.yml` runs release-please on every merge to `main`, and the merge of its release PR cuts `vX.Y.Z`, publishes the GitHub Release and attaches the 4-target `CGO_ENABLED=0` matrix **in that same run** (a `GITHUB_TOKEN`-pushed tag cannot trigger a follow-up workflow — the defect that left v0.28.1–v0.30.0 with zero assets). Version source of truth `cmd/leankg/VERSION`, declared as release-please's `version-file`; `internal/mcp/server.go` keeps the second copy in lockstep under a marker, guarded by `internal/mcp/version_test.go` and a CI gate on the release run itself. npm wrapper intentionally not revived (no Node runtime in the Go engine) | **DONE** (automatic on merge) `scripts/release_workflow_guards.py` runs as a CI step pinning the three invariants that were each broken once (no job may gate on `inputs.*`; publish must assert the asset count; `--latest` must stay conditional), so a re-introduced silent-skip fails CI instead of shipping an empty release. |
| language registry | — | `internal/langs` — **40 languages** (13 defaults + 27 expanded: c/cpp/csharp/php/ruby/scala/perl/lua/haskell/elixir/crystal/cuda/cypher/elm/erlang/fsharp/glsl/hlsl/nim/ocaml/sql+plsql+tsql/powershell/qsharp/solidity/systemverilog/verilog/zig), each with regex extractors + fixtures; every directory under `examples/` extracts non-zero elements | **DONE** |
| tree-sitter grammars | — | `internal/tstree` — bundled grammars incl. **objc (tree-sitter-objc 3.0.2, vendored C, tagged `tstree`) and dart (nielsenko 0.0.4, ABI-14)**; objc message-send call edges + dart constructor/factory/enum elements | **DONE** |
| multi-project serving | — | `internal/projects` — `LEANKG_PROJECT_DIRS` registry, lazy per-project store+engine, routing by dir path or name on REST `?project=`, MCP tool arg `project` (stdio + HTTP), ConnectRPC and the dashboard; unknown selectors are errors, never a silent fallback; single-project behavior byte-identical when unset | **DONE** |
| federation (client) | 0.1k | `internal/federation` — `leankg push`/`pull` ported as Rust had them (POST `/api/v2/graph/push` with `X-LeanKG-Token/-Engineer/-Env`; `pull` is a `/api/v2/status` connectivity probe). **Finding: no server — Rust's own Axum app included — ever served the push route**, so a real federation (receiver + auth + merge policy) remains a design task; tracked in #372 | **DONE** (client parity) |
| conversation mining | 0.7k | `internal/convo` — Claude/ChatGPT/Slack export parsers, classify (decision/preference/milestone/problem), topic + code-target extraction, `decided_about` edges; `leankg mine-conversations --format … --input …`. Fixes a Rust defect where all but the last `decided_about` edge per file path were dropped | **DONE** |
| persisted metrics | — | `context_metrics` ledger (migration 011, both backends) + `internal/metrics` — `leankg metrics` (since/tool/json/session/reset/retention/cleanup/seed) and the H10/FR-PLG-8 usage buckets behind `leankg dashboard`; recorded from the MCP dispatch like Rust (REST never recorded there either) | **DONE** |
| org knowledge | — | `internal/orgknowledge` + store migration 010 (incidents, knowledge_entries, service_metadata, env_snapshots) — incident CRUD/query, team notes/annotations, env-conflict detection, service-context aggregate, team map; CLI `incident\|note\|env-conflicts\|service-context\|team-map` and `/api/v2/{incidents,env/diff,service/context}` | **DONE** |
| PRD indexing | 0.3k | `internal/prdindex` — FR/US/AC extraction from a PRD document into requirement entities + knowledge rows + `implemented_by` edges to ontology workflows; `leankg prd` / `prd-trace` and `import{action:"prd"}` / `query{action:"prd"}`. Additive over Rust: AC lines are captured, and the id regex fix stops silently dropping `FR-ZCP-13`-shaped ids | **DONE** |
| remote sources | 1.3k | `internal/sources` — git+ (shallow branch clone, full-clone+checkout fallback for tags/SHAs, fetch/advance), GCS (paginated listing, size cap), local tree; `index --source` / `refresh --source` now work (the old `--source is not supported` refusal is gone), with `--ref-name` and the `--auth` > `GITLAB_TOKEN` > `GIT_TOKEN` chain | **DONE** |
| response token budget | 0.7k | `internal/budget` — per-action caps with the `_token_budget` marker, one-pass truncation, protected keys; applied on MCP responses. **Trimmed 2026-09-14:** Rust's `BudgetGuard` (wall-clock/RSS/iteration aborts, `getrusage` probes, `LEANKG_MAX_RSS_MB`) was ported but never wired to any call site — deleted rather than kept as a dead 256-line parallel surface (`TokenBudget`, the live half, is what `internal/mcp` enforces) | **DONE** (guard half removed as dead) |
| error catalog (FR-ZCP-12 T1) | 0.4k | `internal/errs` — 14 Rust entries with code + cause + runnable fix + doc anchor, rendered through the CLI/store/MCP/REST/auth paths (the audit test fails on drift) | **DONE** |
| obsidian | 0.7k | `internal/obsidian` — vault init/status, push (store→notes, Rust path/metadata/template parity), pull (notes→`Note` elements + `[[wiki-links]]` + frontmatter links + annotations), bounded debounced watcher; CLI `obsidian init\|push\|pull\|watch\|status` | **DONE** |
| CLI verbs (Rust parity) | — | `run` (+`--compress`), `detect-clusters`, `report`, `gods`, `ctags`, `cost`, `migrate`, `audit export\|verify`, `auth register\|token create/list/revoke`, `export`, `pack`, `generate`, `annotate`, `link`, `search-annotations`, `show-annotations`, `register`, `unregister`, `list`, `status-repo`, `tunnels`, `quality`, `reflect`, `refresh` — all wrapper-level ports of shipped Rust features | **DONE** |
| registry | 0.1k | `internal/registry` — global repo registry at `$HOME/.leankg/registry.json` (Rust schema/version parity, null-field semantics), register/unregister/list/status incl. live element counts | **DONE** |

**Validation:** `gofmt` clean · `go build ./...` + `CGO_ENABLED=0 go build ./...` + `go build -tags tstree ./...` green · `go vet` both tags clean · full `go test ./...` green under BOTH the default and `-tags tstree` builds · `go mod tidy` a no-op. Live-verified this wave: dashboard JSON on every route, multi-project routing (`?project=`), MCP tool-arg routing, `run --compress` exit-code propagation, `audit verify` on a tampered chain, `auth token create/list/revoke` with revocation enforced, `detect-clusters`/`report`/`gods`/`tokens`…, obsidian push/pull idempotency, doctor `--deep` exit codes 0/1/2.

**Remaining Rust-era capabilities, tracked as issues (never silently dropped):** federation push/pull (#372) · conversation mining (#373) · org knowledge surfaces: incidents, team notes, env conflicts (#374) · persisted usage metrics (#375) · FR-ZCP-09/10 portfolio registry + cross-schema queries + fleet doctor (#376). Each is a *new* product milestone rather than a port of live behavior; the per-verb disposition table below records the full audit. Live smoke both engines: SQLite (index→embed→L1/L2/L3→memory→MCP 3-tool registry→RPC) AND PostgreSQL :5433 (index→embed with vectors physically in `leankg_*` schema→L1/L3 pgvector→graph verbs→status backend=postgres). Benchmarks: IndexDir 100 files 0.73s, L1 137µs, L2 375µs, 1k×384 cosine scan 4.4ms (exact scan ceiling vs HNSW documented).

**Cutover state:** Rust source, Cargo config, cargo CI jobs, npm wrapper and manifest removed; Go CI job added; release automation landed 2026-09-14 (`release.yml`: release-please versioning + tag-triggered-equivalent binary publish in one run, replacing the manual-`workflow_dispatch`-only `release-go.yml`).

### v4.5.1-go-rewrite-w1 — Go engine W1 delivered: `go/` greenfield engine + leankg-embed (#368) + full-markdown memory (#369) (2026-09-10)

> **Trigger:** maintainer go-decision on the Rust→Go rewrite (#365 umbrella). This PR lands migration waves **W1 (core)** plus the two issue-scoped slices **#368 (embedding pipeline as an independent binary)** and **#369 (full-markdown memory)** — not full parity.

**Delivered** (`go/` module, ~7 packages, all `go test ./...` green + live-smoked):

| Slice | What landed |
|---|---|
| W1 store | SQLite WAL (`internal/store`): FTS5 L2 rung, float32-BLOB vectors + in-proc cosine (documented ceiling; sqlite-vec upgrade path), DB-resident `write_watermark` freshness (kills the C4 TOCTOU class), real PKs on `code_elements`/`relationships` (documented breaking fix) |
| W1 core | 3-tool envelope (`import`/`query`/`status`, legacy `set`/`get` as tool names rejected over the wire; action aliases live) + L0→L1→L2→L3 ladder with `retrieval{rung,reason}` + `freshness` on every answer |
| W1 indexer | regex extraction (go/rs/ts/tsx/js/jsx/py/md; documented ceiling until W2 tree-sitter), 3-signal change detection (size+mtime → SHA-256 confirm), calls/contains relationships |
| W1 transports | MCP stdio + streamable HTTP via official `modelcontextprotocol/go-sdk` v1.7.0; REST `/health`, `/api/v1/{status,query,import}` (stdlib net/http) |
| #368 | `cmd/leankg-embed` independent binary (`run`/`full`/`export`/`import`/`status`); shared `internal/embed` library; ModelStamp guard on EVERY vector writer — full+mismatch ⇒ clear+rebuild, **incremental+mismatch ⇒ hard fail with `leankg-embed full` directive** (flag-slip must not wipe a collection), first build stamps; NDJSON offsite export/import with dim-guard + resume; serving binary does zero inference (query-time embedding = HTTP client call, provider failure degrades to L2) |
| #369 | Full-markdown memory (`internal/memory`): `MEMORY.md`/`USER.md` bounded 2,200 bytes with **error-not-truncate** overflow + usage-% snapshot header; `topics/*.md` unbounded; Claude-Code file commands (`view/create/str_replace/insert/delete/rename`) with path-traversal + symlink-escape rejection; Hermes `add/replace/remove` with unique-substring semantics (ambiguous ⇒ error); FTS5 `index.db` re-indexed on every write; mnemopi JSONL banks adapter (wyhash64 byte-exact port of the Rust wyhash 0.5 crate, `retained_through_user_turn` cursor) |

**§9 decisions taken:** (1) tool rename `set`/`get` → `import`/`query` (breaking tool-name cutover; action aliases keep the verb namespace); (2) query-time embeddings provider-first — OpenAI-compatible API or llama.cpp sidecar share one HTTP shape; sidecar runtime itself is W6; (3) ui-v2 untouched (REST-compatible); (4) W1 scope = core + #368 + #369, NOT full parity. (5) project resolution: explicit `--project`/cwd first — roots/list stays available as a fallback but is not the only path (deprecated by SEP-2577 in the go-sdk); server-initiated roots/list lands when the HTTP surface needs remote resolution.

**Waves remaining:** W2 (tree-sitter indexer), W4 (pgvector), W5 (ConnectRPC + hindsight memory e2e), W6 (local model runtime), W7 (parity fixtures + cutover). Tracker rows: FR-GO-W1, FR-GO-EMBED, FR-GO-MEM.


### v4.5.0-go-rewrite-analysis — Deep Rust→Go feasibility study (2026-09-10)

> **Trigger:** user direction to evaluate replacing the Rust implementation with a Go engine: a lightweight agent-memory MCP server (just 3 tools — import/query/status), easy coding-tool integration, REST + RPC transports, layered storage (index → embedding), query ladder exact → fuzzy → semantic, SQLite default + PostgreSQL/pgvector, local (default) + API-provider embeddings, real writer/reader separation, and a comprehensive markdown analysis as the deliverable.

**Delivered:** [`docs/go-rewrite-analysis.md`](archive/analysis/go-rewrite-analysis.md) — full-source feasibility study (5 parallel deep-dive passes over mcp/db/indexer+graph/embeddings+memory/ops-CI plus first-hand verification of every load-bearing claim). Contents: measured inventory (168k LOC across 323 files, 3 tools / 83 verbs / 114 CLI verbs / 176 MB binary / 662 MB self-index), root-caused cons (Datalog translator tax, Cozo single-writer + FFI-abort class #286, per-process freshness/L1 cache races, unwired BLAKE3 content hash, sqlite L2 fuzzy asymmetry, sync-PG + block_in_place, surface sprawl), an honest pros list, a shipped-vs-vision gap table (≈90% of the stated target is already shipped and live-verified), and a Go target architecture: plain-SQL dual store (modernc sqlite WAL + pgx/pgvector), DB-resident freshness watermarks replacing TTL caches, MCP + REST + ConnectRPC from one core, providers-first embeddings with llama.cpp-sidecar default, 7-wave migration plan, risks, and 4 open questions for the user.

**Status:** analysis only — no code changes landed. The Rust line (v0.30.0) stays released and in maintenance; the Go rewrite proceeds on user go/no-go and answers to §9 questions. Open questions: (1) sidecar vs in-process local ONNX, (2) rename `set`/`get` → `import`/`query` with aliases, (3) keep ui-v2 as-is, (4) timeline appetite (weeks core vs full parity).

### v4.4.3-dual-engine-verified — CI pipeline healed + Postgres path fixed + verified on both engines (2026-09-09)

> **Trigger:** user report that CI was failing on GitHub, then a dual-engine (sqlite + Postgres) verification sweep.

**CI pipeline healed (3 stacked semantic-release defects):** #336 idempotent bumps (release-PR merge re-entering create-pr treated already-at-target as a fatal throw); #337 event-race fix (merging any PR fired push+pull_request runs sharing one concurrency group — they cancelled each other; groups now per-event, release mode only for `release/*` head branches); #339 semantic-release now bumps the npm wrapper (auto release PRs pass the parity gate). Verified end-to-end: merge → auto release PR → CI green → merge → tag published automatically. **v0.28.0 (#335) and v0.28.1 (#338) shipped this way.**

**Storage platform:** #326 sqlite is the default and only CI-tested engine — Docker/Postgres triggers removed (Dockerfiles, compose files, docker-* scripts, Makefile docker targets, install.sh docker subcommand); PG service containers dropped from all workflows (also fixed the week-long perf-gate red: the scale harness still asserted the pre-#283 one-tool registry). Postgres remains an explicit `LEANKG_DB_ENGINE=postgres` opt-in and was re-verified live on pgvector/pg18.

**Postgres path fixed:** #342 restored `006_audit_log` to the PG MIGRATIONS array (dropped by the v4.4.0 sweep — fresh PG installs had no audit ledger: recording silently disabled, export/verify failing); #341 temporal_query uses `!regex_matches` (the `not` spelling parsed on sqlite but the PG translator has no top-level `not`); #343 integration suites updated to the 3-tool surface + bounded temporal_query API (they did not compile since #331 — CI runs `--lib` only, so this escaped CI).

**Stale-surface cleanup:** no_legacy_terms guard resized (cozo is the live engine; FORBIDDEN = datadog only), hidden dirs + vendor/ + nested node_modules skipped in its walk, Makefile dead docker targets removed, ui-v2/CLAUDE.md/SKILL.md Docker-era guidance rewritten, 10+ RocksDB comments modernized.

**Dual-engine live verification (same binary):** sqlite — get 7 hits, temporal_query 57,473 (bounded), audit chain 2 entries; PG — temporal_query 32ms/9, get green, audit chain intact (1 entry recorded + verified), migration 006 applies on fresh db; full PG integration suite 49/49 suites, 3,222 tests.

### v4.4.2-harden-and-validate — 8 PRs merged, live-validated (2026-09-08)

### v4.4.2-harden-and-validate — 8 PRs merged, live-validated (2026-09-08)

> **Trigger:** fanout review of the 8 open fix PRs (scouts caught 4 blockers pre-merge), then one-by-one merge with post-merge live validation on the sqlite server.

**Merged (each CI 6/6 green, reviewed):** #298 router L1-first (#290) — identifier queries answer from L1 exact before ANN; #301 embedder+rerank per-process cache (#292) — router latency 14-31s → 2.3s warm; #313 token budget post-truncation accounting (#300); #305 panic hook + durable crash log (#286 diagnostics); #312 sqlite audit ledger with review-blocker fixes (#309); #307 fuzzy ranking — test demotion, exact>substring, specificity (#293/#294) + hydration QN single-quote escaping (cozo 0.7.6 rejects \" in double-quoted strings); #304 vendor excludes (#291) — minified/embed assets skipped at collection; #311 stale-element sweep — incremental sync removes elements for files that dropped out of the collection set; #315 embeddings-feature test build green (1527/0) (#302); #314/#316 bench deltas.

**Live-validated on this repo (sqlite, `:9799`):** 578 files, 10,202 elements, 12,984 vectors (vendor-free); `create_hnsw_index` → rung=exact, real symbol; router latency 6.07s cold → 2.31s warm; audit ledger recording enabled.

**Still open (all closed since):** #308-followup + #309-followup + #300-followup landed in the v4.4.3 window; #302 closed via #315.

### v4.4.1-three-tools-live — live-tested SQLite server + Datalog repairs (2026-09-07, PR #284)

> **Trigger:** live 3-tool SQLite MCP server testing on this repo (index + embed + router queries on `:9799`) surfaced five Datalog/dispatch gaps between the PG-shaped code paths and raw Cozo.

**Fixes (PR #284, `fix/sqlite-l3-hydration`):**

| Bug | Fix |
|-----|-----|
| rmcp dispatch arm still resolved the one-tool envelope — `set`/`get`/`status` refused on stdio while JSON-RPC worked | single `resolve_3tool` resolver on both arms; legacy `leankg_context` envelope kept as back-compat; `action`/`verb` aliases |
| `import_relations` wrapped rows in an extra bracket layer → every import failed `Fixed rule head arity mismatch` | single bracket layer (`?[] <- [rows]`) |
| Cozo `:insert` rejects duplicate keys — full re-embeds replaying existing QNs died | upsert via `:put rel {cols}` (parity with PG `INSERT .. ON CONFLICT`); regression test `import_relations_upserts_duplicate_keys` |
| `fuzzy_find_elements` script had no relation reference (unparseable) + case-sensitive pattern | proper `:= *code_elements{...}` + lowercased pattern |
| short-positional rule application `*rel[col]` invalid on raw Cozo for multi-column relations | attribute form `*rel{col}` (doctor probes, embed counts) |
| L3 ANN hydration (`elements_by_qualified_names` / `find_element_by_key` / `find_element_by_name_col`) was PG-only | Datalog ports on `SqliteBackend` |
| `embed_control` sub-command (`on|off|status`) collided with the routing `action` key — `set {action:"embed"}` silently returned status | routing priority: control capabilities keep their payload `action`; friendly `embed` alias arms the builder |

**Open bugs filed:** #286 (server exit code 1, no panic trace, during concurrent embed+queries — suspected FFI abort), #287 (fuzzy pattern not regex-escaped), #288 (audit remaining short-positional rule applications).

**Live validation (this repo, sqlite):** index 581 files; full embed 9522/9522 vectors (`total_vectors` 9522, storage_engine sqlite); router ladder L1 exact / L2 fuzzy / L3 `hnsw+ontology-traverse` runs end-to-end with graceful below-confidence-floor degradation; ANN distance sanity 0.25 (relevant) vs 0.43 (random).

### v4.4.0-three-tools-dual-backend — 3-tool surface + SQLite dual-backend (2026-09-06)

> **Trigger:** user decision evolving the one-tool envelope into a **3-tool surface** — `set` (import repo / nested dir of repos), `get` (query with multiple layers via the L0–L3 ladder), `status` (health/inventory) — plus **dual-backend storage**: PostgreSQL *and* SQLite (vector-capable), with **SQLite as the session default**.

> **Design (D-2026-09-06-1):** the verb namespace survives as the *action* namespace inside each tool — `set {action}`, `get {action?}` (omitted = NL router), `status`. Legacy verb names remain valid as actions (zero-loss migration). Envelope resolution order is preserved: (tool, action) → effective capability resolved **before** read-only gate / write-lock / RBAC / audit.
>
> **Design (D-2026-09-06-2):** SQLite lands by resurrecting the pre-v0.20 CozoDB layer on the `storage-sqlite` engine (`cozo = 0.7.6`, feature-gated). All 216 Datalog call sites run natively on cozo-sqlite — zero translation — and HNSW vectors come from CozoDB's own `::hnsw` (cosine, dim 384). One SQLite file per project (`<project>/.leankg/leankg.db`) preserves per-project isolation. Selection: `LEANKG_DB_ENGINE=sqlite|postgres`, **default sqlite**; `LEANKG_PG_URL` opts back into PG.

**Product actions this revision:**

| # | ID | Focus | Intent | Status |
|--:|----|-------|--------|--------|
| 1 | `FR-3T-01` | **P0** | Registry: exactly 3 tools (`set`/`get`/`status`); per-tool action namespaces; legacy verbs as actions (aliases) | **IN_PROGRESS** |
| 2 | `FR-3T-02` | **P0** | SQLite backend: cozo-sqlite engine, per-project `.leankg/leankg.db`, HNSW vectors, audit ledger, migrations | **IN_PROGRESS** |
| 3 | `FR-3T-03` | **P0** | Session default: SQLite engaged when `LEANKG_DB_ENGINE=sqlite` or `LEANKG_PG_URL` unset; `leankg migrate`/`index`/serve run on SQLite | **IN_PROGRESS** |
| 4 | `FR-3T-04` | **P1** | Live validation: this repo indexed into SQLite, 3-tool smoke (search/status/router), persistence across restart | **NOT_DONE** |

### v4.3.1-one-tool-envelope — Hard one-tool cutover (2026-09-05)

> **Trigger:** user decision superseding the T3 budget path — hard-delete every registered tool except `leankg_context`; all capabilities ride it as verbs. Envelope = `{verb: "<capability>", ...args}`; omitting `verb` uses the natural-language router. The verb namespace IS the legacy tool namespace (all docs/hints naming a tool remain valid as verb references). Envelope resolution happens **before** read-only gate / write-lock serialization / audit recording, so verb-scoped security decisions cannot be bypassed by hiding a write verb inside a read-named envelope (audit records the effective capability).

**Product actions this revision:**

| # | ID | Focus | Intent | Status |
|--:|----|-------|--------|--------|
| 1 | `FR-ZCP-03` | **P0** | End-state enforced: registry = exactly 1 tool (`leankg_context`); `resolve_envelope` unwraps/hard-refuses; legacy names are gone, not deprecated (catalog error names the verb mechanism) | **DONE** |
| 2 | `FR-ZCP-12` | **P1** | T3 re-scoped: "≤ 12 + full opt-in" replaced by the CI-enforced one-tool invariant (`list_tools().len() == 1` + verb-catalog membership lint); T2 unchanged | folded |

### v4.3.0-one-tool-ladder — Single router + capability degradation ladder + first-run setup contract (2026-09-04)

> **Trigger:** user direction (2026-09-04): "turn LeanKG into 1 tool that fits every scenario — if there are no embedding vectors, use exact or fuzzy search," plus first-run setup requirements (auto init/index/embed choice, simple repo add). Two-scout ground-truth audit (search handlers + DB capabilities): a 4-tier retrieval ladder already exists inside separate tools — exact/regex (`search_code`, `translate.rs` regex/LIKE), ontology keyword (`safe_discover.rs:104-211`), pgvector ANN + rerank + traverse (`retrieval/pipeline.rs:272-293`), graph BFS — but only `semantic_search` degrades silently (`handler.rs:1918-2001`); `kg_semantic_context` hard-errors without vectors (`:3975-4036`); no tool exposes machine-readable capabilities; and the `QueryOrchestrator` (intent parser + hot-path cache, `orchestrator/mod.rs:26-46`) is **built but never registered as an MCP tool** (zero references under `src/mcp`) — FR-ZCP-03 has a seed implementation waiting for registration. Schema has zero FTS primitives (no tsvector/GIN/pg_trgm, `schema.sql`); pgvector supports exact-distance fallback (plain `ORDER BY vec <-> $1`, `translate.rs:2133-2168`).

> **Decision (D-2026-09-04-4):** one tool, many rungs. `leankg_context` (FR-ZCP-03) probes per-project capabilities once per request (< 10 ms: `state.has_any` limit-1 vector probe, `::relations` HNSW check, `index_inventory.total_vectors`) and routes each query down the best rung the data supports: **L3** vectors → ANN + rerank + traverse; **L2** no vectors → FTS/trigram fuzzy + ontology concepts; **L1** no index → exact identifier/regex + suggestions; **L0** cold → guidance + auto-index kick (FR-ZCP-02). Every response carries `retrieval: {rung, reason}` + `freshness`; the ladder never errors — capability loss degrades ranking, not availability. The attach side gets the same treatment (FR-ZCP-13): one auto/manual setup question, `leankg add` for coverage growth.

**Product actions this revision:**

| # | ID | Focus | Intent | Status |
|--:|----|-------|--------|--------|
| 1 | `FR-ZCP-03` | **P0** | Rewritten as the ladder router: capability probe + L0–L3 rungs + `retrieval` provenance block; registers the unregistered `orchestrate` parser | **NOT_DONE** |
| 2 | `FR-ZCP-13` | **P1** | First-run setup contract: one auto/manual question (init + index + embed vs manual) persisted in `.leankg/config.json`; one-command repo registration (Go verb: `leankg index <path>`; the designed Rust name `leankg add` is superseded); embeddings are a preference, never a prerequisite | **NOT_DONE** |
| 3 | `FR-ZCP-05` | folded | Bridge tier spec: `pg_trgm` GIN + `text_pattern_ops` prefixes as the L2 fuzzy baseline before FTS lands | folded |

### v4.2.0-simplicity-first — Measured-simplicity contract for a young product (2026-09-04)

> **Trigger:** three-track research sprint (2026-09-04, three parallel scouts). **(1) Repo friction audit (file:line):** **76/73 MCP tools** exact-count CI-pinned (`src/mcp/tools.rs:1156-1160`, already pruned 87→76) vs the winning 1–2-tool norm (zg = 1 default + 6 full; context7 = 2; serena ≈ 48); **103 CLI verbs** (61 top-level + 42 nested, `src/cli/mod.rs`); **116 distinct `LEANKG_*` env-var names** (88 runtime in `src/`, 28 script-only, 1 docs-only; only ~5 first-run relevant); a **10-step / 8-decision** first-value walkthrough (README Get Started) vs Supabase's published "under 2 minutes"; error copy without remediation (`Unauthorized` — which env sets the token? `server.rs:3372-3374`; `Unknown tool` with no nearest-match hint, `handler.rs:299`); README claims "85+ tools" (README.md:170,179) vs the code-verified 76. **(2) Competitor mechanics (live-fetched URLs):** zg's 1-tool default + try-it tour + docs that match shipped behavior; context7's OAuth one-liner; Desktop Commander's fuzzy-match error feedback; gitleaks' zero-config default rules. **(3) Onboarding playbooks (URLs verified):** Supabase/Convex publish wall-clock TTFV numbers; Stripe's error objects carry `code` + `doc_url` (~200-code catalog); clig.dev: suggest the next command, never dead-end, <100 ms first feedback; Vercel's "zero configuration" is a script-verifiable per-framework claim; Stack Overflow 2025: 46% of developers actively distrust AI-tool accuracy — LeanKG's consumers are verification-hungry agents.

> **Decision (D-2026-09-04-3):** simplicity is a **measured contract, not a vibe** — every simplicity claim must be a number a CI job can verify (tool-count budget, TTFV wall-clock, error-catalog coverage, claim-to-script mapping). Tiered cheap-first: **T1** error/config/claim honesty (docs-and-strings cheap — ship immediately), **T2** published CI-timed TTFV (the number itself wins mindshare — zg publishes none), **T3** CI-pinned default-tool budget riding FR-ZCP-03's router. The audit's *unclaimed* hotspots (env-var surface, error-copy contract, docs split-brain, semantic-path default-off UX) are absorbed here; already-claimed ones stay in their FRs.

**Product actions this revision:**

| # | ID | Focus | Intent | Status |
|--:|----|-------|--------|--------|
| 1 | `FR-ZCP-12` | **P1** | Measured-simplicity contract, three tiers: T1 error catalog (stable code + cause + runnable fix + doc anchor, 100% CI-linted) + single copy-paste config block + claim hygiene; T2 CI-timed published TTFV ≤ 5 min; T3 CI-pinned default-tool budget (≤ 12 default, tier-tagged) riding FR-ZCP-03 | **NOT_DONE** |
| 2 | — | folded | §1.1 zg row extended; §2.6 new problem line (unclaimed friction, quantified); §3.4 AC additions; §4/§5/§6 anchored on audit numbers | folded |

### v4.1.1-omp-embed-ground-truth — OMP memory + zvec-grep embedding audits (2026-09-04)

> **Trigger:** two file:line audits (2026-09-04). **(1) OMP memory backend** — installed packages with full TS source (`@oh-my-pi/pi-coding-agent`, `@oh-my-pi/pi-mnemopi` v18.0.7): `memory.backend` is a **closed 4-value enum** (`off|local|hindsight|mnemopi`, settings-schema.ts:2932-2952, resolve.ts:16-25) with **no pluggable MCP/URL backend**; hindsight is the remote-HTTP precedent (`hindsight.apiUrl` → `POST /v1/default/banks/{bank_id}/memories` + `/recall` + `/reflect`, hindsight/client.ts:274-340). Mnemopi bank = `<basename(cwd)>-<wyhash36(abs cwd)>` ≤64 chars, derived from **cwd only, never git root** (stability contract #2412, mnemopi/config.ts:168-186); scoping `global|per-project|per-project-tagged` (default per-project; tagged = project write bank + [project, shared] recall); retain every N user turns (default 4) on `agent_end` with a `retained_through_user_turn` integer cursor — **not prefix-hash**; recall injected once on first turn as a `<memories>` block in developer instructions (state.ts:472-490, 914-922; injectionTokenLimit 5000). OMP's MCP client answers server-to-client `roots/list` with `file://<cwd>` (mcp/client.ts:59-64) — the standards-based cwd channel. **(2) zvec-grep v0.2.1 (main@d756cc7)**: every model catalog entry carries HF repo + **40-hex commit revision** (catalog.ts); the embedding schema `{provider, model, dimension, metric}` lives in the workspace manifest with a **hard mixed-model guard** (mismatch → `EMBEDDING_SCHEMA_CHANGE_REQUIRES_REBUILD`, service/zvec-grep.ts:1622+); positional chunk ids `sha256(fileId \0 chunkIndex)`; size+mtime fast-path → SHA-256 content-hash diff; per-hit query-time `fresh|possibly_stale`; hourly reconciliation; single-flight per root (JobScheduler + `{pid, hostname, instanceToken}` lease file).


### v4.1.0-portfolio-scale — Ground-truth storage audit + portfolio scale (2026-09-04)

> **Trigger:** the empty-glass → 1 repo → parent-of-3 → parent-of-100 stress test (2026-09-04 session) plus a file:line ground-truth audit of the storage/resolution model. Headline corrections to v4.0.0 assumptions: storage is **already one PG database with schema-per-project** (`leankg_p_<hex(canonical project root)>` via `project_identity_keys_in`, `src/db/backend.rs:2613-2675`; per-connection `search_path` pinning `:904-908`; all 6 migrations run **per schema** with per-schema ledgers, `src/db/pg/migrations.rs:36-57`/`:83-116`; HNSW per schema) — **not** DB-per-project; no one-DB+`project_id` consolidation is needed. The real 100-repo gaps: **no project registry** (project list is implicit — `.leankg` walks + `LEANKG_PROJECT_DIRS`, `src/mcp/server.rs:1062-1116`) and **zero cross-schema query capability** (only `current_schema()`-scoped UNION ALL, `translate.rs:3217-3224`). Further verified corrections: no `X-LeanKG-Project` header exists in code (v4.0.0 §3.1 wrongly listed it); unresolved projects **silently fall back to the server-default schema** (`server.rs:2987-2989`) — wrong-project data with no error; `LEANKG_AUTO_ATTACH` does not exist; `ensure_project_indexed` runs **awaited inline in the request path** (`server.rs:2579-2694` — blocking, errors swallowed, no freshness flag); the HTTP `initialize` handler returns a static result and never reads client params (`server.rs:3542-3552`); the watcher is single-project-per-process (`server.rs:1585-1596`/`:2015-2026`); recall/diary memory is **JSONL files under `<project>/.leankg/`**, not PG (only `knowledge_entries` is per-schema PG).

> **Decision (D-2026-09-04-1):** the repo is the unit of scope; **a portfolio (a directory of repos) is a scope, never a project**. Growth path: empty DB = registry (attach = one catalog INSERT); 1 repo = nearest-marker resolution (keep the `find_leankg_for_path` walk); parent-of-N cwd with no repo marker = **portfolio scope** with T0 manifest inventory (zero eager indexing, per-child freshness); parent-of-100 = hot-set cap + LRU detach-to-cold, one indexer slot, T0/T1/T2 tiers, cross-schema portfolio queries over the registry, federated portfolio memory. Project identity stays **canonical-path-derived**; any future re-key (e.g. git-remote) MUST follow the `schema_candidates_for_path` preferred+legacy adoption pattern (`backend.rs:2528-2540`, `:2928-2948`) — no dual-write, no row migration.

**Product actions this revision:**

| # | ID | Focus | Intent | Status |
|--:|----|-------|--------|--------|
| 1 | `FR-ZCP-11` | **P1** | Embedding correctness ported from zvec-grep: pinned model catalog (commit revision + query/document prefixes), model-stamped vectors + hard rebuild guard, chunker-version coupling, 3-signal change detection, per-file atomic replace + truncation accounting, watcher reconciliation, single-flight indexing | **IN_PROGRESS** — parts 1+2 DONE (#351: pinned revisions + per-collection stamp + embed-path guard; #353: query-side degrade guard in semantic_search + entry-point coverage in run/build_index_parallel; AC amended: stamp mismatch degrades to L2 with reason instead of erroring). 3-signal change detection: chunker_version coupled into content_hash_for (CHUNKER_VERSION folded into every hash — a bump invalidates all hashes → full re-embed; #355). part-2 compile fixes under the embeddings feature: #355) |
| 2 | `FR-ZCP-01` | **P0** | §3.1 HTTP resolution corrected: server-initiated MCP `roots/list` (answers `file://<cwd>`) replaces the dropped `clientInfo.workingDirectory` proposal — no harness sends the latter (OMP initialize params verified) | folded |
| 3 | `FR-ZCP-07` | **P1** | §3.5 rewritten on installed-source ground truth: mnemopi-compatible bank naming/scoping/`retained_through_user_turn` cursor/recall contract + hindsight-shaped HTTP memory API as upstream-evidence artifact | folded |

**v4.1.0's action #3 said "`initialize` must read `workingDirectory`" — superseded:** no harness transmits `workingDirectory` on initialize; the standards-based cwd channel is the server-to-client `roots/list` request.

**Product actions this revision:**

| # | ID | Focus | Intent | Status |
|--:|----|-------|--------|--------|
| 1 | `FR-ZCP-09` | **P1** | Project registry + portfolio scope (T0 manifest inventory, per-child freshness) + cross-schema portfolio queries + federated portfolio memory; one indexer slot, hot-set cap, LRU detach-to-cold | **NOT_DONE** |
| 2 | `FR-ZCP-10` | **P2** | Per-schema migration fleet reconciliation + `doctor --deep` drift check across all project schemas | **NOT_DONE** |
| 3 | — | correction | §2/§3.1/§3.2/§4 rewritten on verified file:line ground truth (escape hatch = `?project=` only; silent fallback killed in FR-ZCP-02; background-only indexing; `initialize` must read `workingDirectory`) | folded |

**Portfolio stages this revision answers:**

| Stage | Behavior |
|---|---|
| Empty DB | Registry, not an error: first connection with auto-attach inserts one catalog row keyed by canonical project root, `freshness: cold`, serves empty results, background index |
| 1 repo | Nearest repo marker (`.leankg`/`.git`) wins; existing pipeline unchanged |
| Parent of 3 | cwd has no repo marker → portfolio scope: depth-limited manifest scan (seconds, no tree-sitter); cross-repo questions answered from manifests + `service_calls` + env configs; full graph per child only on first touching query |
| Parent of 100 | Never eager-index: attach = one row; hot-set cap (~8 fully-indexed children) + LRU detach-to-cold (archive, never destroy); single indexer slot; ambiguous portfolio queries degrade to candidate repos + per-child freshness, never block |

### v4.0.0-zero-config-projects — Unified doc set + zero-config project resolution (2026-09-03)

> **Trigger:** Two 2026-09-03 sources: the [zvec-grep audit](archive/analysis/zvec-grep-vs-leankg-2026-09-03.md) (search-layer discipline: 1 default tool, freshness contract, one-command install) and the [OMP memory-integration draft](archive/planning/2026-09-03-leankg-omp-memory-integration-draft.md) (LeanKG wired into OMP via MCP; per-project `?project=` routing is the top ergonomic failure). Cross-checked against the mnemopi / Mnemosyne memory backend (oh-my-pi): **banks derived automatically from the working directory** — `per-project` scoping derives a project bank from the cwd basename + stable hash of the absolute path; `per-project-tagged` adds a shared global bank. No URL parameters, no path pinning, no init step in the agent's workflow.

> **Decision (D-2026-09-03-1):** Kill the explicit `?project=` contract. LeanKG resolves the project **from the request context automatically** — exactly how mnemopi derives banks and how zg accepts a bare workspace `root`. The agent never passes a project path; the server maps connection → project via (1) the OMP/OpenCode harness cwd (stdio: process cwd; HTTP: registered harness sessions), (2) a client-declared working directory on the MCP `initialize` handshake, (3) first-touch auto-attach of the nearest `.leankg`/repo root with **lazy indexing** on first query, (4) explicit URL `?project=` retained only as an escape hatch for remote/multi-tenant deployments.

**Product actions this revision:**

| # | ID | Focus | Intent | Status |
|--:|----|-------|--------|--------|
| 1 | `FR-ZCP-01` | **P0** | Contextual project resolution: connection→project mapping (cwd / initialize workingDirectory / registered session), zero URL params | **NOT_DONE** |
| 2 | `FR-ZCP-02` | **P0** | Lazy auto-attach + auto-index: first query in an unindexed repo attaches and indexes in background; queries serve stale-or-empty with freshness flag instead of failing "not initialized" | **NOT_DONE** |
| 3 | `FR-ZCP-03` | **P0** | Default toolset: one intent-expressing router tool; full catalog behind `full` opt-in (merges `FR-ZG-01`) | **NOT_DONE** |
| 4 | `FR-ZCP-04` | **P1** | `leankg install --target` agent wiring incl. URL **without** `?project=` (merges `FR-ZG-04`) | **DONE** (opencode + omp writers, projectless URL contract — no ?project= by default; --register-cwd hook; six-client integration suite) |
| 5 | `FR-ZCP-05` | **P1** | Postgres FTS ranking + RRF fusion (merges `FR-ZG-02`) | **NOT_DONE** |
| 6 | `FR-ZCP-06` | **P1** | Freshness contract in every index-backed response (merges `FR-ZG-03`) | **DONE** (#347) |
| 7 | `FR-ZCP-07` | **P1** | OMP memory-backend adjacency: recall/retain MCP surface (`session_retain`, auto-recall injection) so LeanKG can act as harness memory alongside code-graph memory (extends `FR-SMA-04`) | **IN_PROGRESS** — slice 1 DONE (#357: mnemopi-compatible bank naming via wyhash36 + canonicalize, scoping matrix, session_retain/session_recall with retained_through_user_turn cursor, memory_get/update/forget/invalidate mirrors, file-backed JSONL banks; outstanding: hindsight-shaped HTTP API, OMP end-to-end injection AC) |
| 8 | `FR-ZCP-08` | **P2** | Cross-tool harness hardening (merges `FR-ZG-05`) | **DONE** (pinned 40-hex corpus SHAs + repos.lock.yaml, prompt_version + prompt SHA-256 per row, >=3-trials gate, judge-blind 0-6 rubric scorer `score.py` with shuffled labels, zg pitfalls checklist computed in the report: leakage/like-for-like/stochasticity/tool-access smoke) |

> **Doc restructure this revision:** all prior docs (66 entries: analyses, reports, plans, PRD v3.8.x history, design/ERD, benchmarks) moved to [`docs/archive/`](archive/). This document is the **one** comprehension document; [`docs/prd-task-tracker.md`](prd-task-tracker.md) is the **one** tracker (done / in-progress / todo). Section numbering below is fresh and self-contained.

---

## 1. Mission

**Stop Burning Tokens. Start Coding Lean.** LeanKG is the **persistent code-graph + org-memory substrate** for AI coding agents: semantic search + structural graph (impact, traceability, incidents) + session memory, exposed over MCP — with **zero configuration at the point of use**: an agent opens a repository and the memory is simply there.

**Positioning (2026-09-03, harness-era):** do not compete with harness-native Glob/Grep/LSP on raw search; zg (1.4k★) validates that flat hybrid retrieval is becoming commoditized. LeanKG's durable moat is the graph and the memory: impact radius, FR→workflow→code traceability, incidents, env conflicts, service graphs, cross-session lessons. Steal competitors' *discipline* (surface minimalism, freshness honesty, eval rigor, zero-config attach), not their *product*.

### 1.1 What we steal, and from whom

| Source | What they teach | LeanKG adoption |
|---|---|---|
| [zvec-grep](https://github.com/zvec-ai/zvec-grep) | One default MCP tool with intent-expressing params; `fresh`/`possibly_stale` on every response; `zg install --target`; `root`-based workspace addressing; every error carries a stable code + doc URL + runnable fix; try-it-yourself tour and a short numbered CLI (8 commands) whose docs verifiably match shipped behavior | `FR-ZCP-03..06`, `FR-ZCP-08`, `FR-ZCP-12` |
| [OMP mnemopi + Hindsight backends](https://github.com/can1357/oh-my-pi) (installed-source audit, 2026-09-04) | Closed `memory.backend` enum (`off\|local\|hindsight\|mnemopi`, no pluggable MCP backend); hindsight = remote HTTP banks (`POST /banks/{id}/memories` + `/recall`, `hindsight.apiUrl`); mnemopi banks = `<basename(cwd)>-<wyhash36(cwd)>` ≤64 chars, **cwd only, never git root** (stability contract #2412); 3-mode scoping (`per-project` default; tagged = project write bank + [project, shared] recall); retain every N user turns (default 4) with a `retained_through_user_turn` cursor — **not prefix-hash**; recall injected once on first turn as a `<memories>` block (injectionTokenLimit 5000); OMP's MCP client answers `roots/list` with `file://<cwd>` | `FR-ZCP-01` (roots/list channel), `FR-ZCP-07` (bank/scoping/cursor/injection contract) |
| Harness-native primitives | Glob/Grep/LSP win raw search; don't fight them | positioning §1 |

### 1.2 Explicit non-goals

- Competing on raw file-chunk search speed with harness-native tools or zg.
- Becoming a chat-persona memory (Mem0/Tencent style); code-graph memory + org memory only.
- Forking OMP's closed `memory.backend` enum — integration is via MCP, never a fork (OMP draft §3).
- Multimodal/PDF/image ingest; managed-rg reimplementation; desktop GUI.

---

## 2. Problem Statement

1. **Context blindness** — agents re-read the same files every session; no memory of impact radius, traceability, or prior insights.
2. **Configuration friction (this revision's P0)** — LeanKG today requires the agent (or its config) to pass an explicit `?project=` path that must match an **initialized** index (`src/mcp/server.rs:636` `resolve_project_db_path`; `find_leankg_for_path` at `:588`). Mismatch → "not initialized" → the agent gives up and greps. Every harness integration draft spends its hardest section on this. mnemopi and zg both demonstrate the alternative: **the working directory is the identity.** Ground truth (2026-09-04 audit) makes it worse: an unrecognized project does **not** fail — it **silently falls back to the server-default schema** (`server.rs:2987-2989`), serving wrong-project data with no error; `LEANKG_AUTO_ATTACH` does not exist, so a repo without `.leankg` is never attached; and resolution re-walks the filesystem on every request with no connection cache.
3. **First-use latency** — `leankg index ./src` takes minutes on large repos; requiring it before first query is a dead-end for lazy adoption.
4. **Tool sprawl** — ~76 MCP tools inflate agent triage; v3.8.5 live audit found 50% failing.
5. **No freshness honesty** — responses carry no staleness signal; agents cannot distinguish current from drifted data.
6. **Simplicity debt beyond config (this revision)** — what the friction audit quantified: **76/73 MCP tools** (exact-count CI-pinned, `src/mcp/tools.rs:1156-1160`) vs the 1–2-tool norm (zg=1, context7=2); **103 CLI verbs**; **116 `LEANKG_*` env-var names** with only ~5 first-run relevant; a 10-step/8-decision first-value path vs Supabase's published "under 2 minutes"; error copy that fails "what went wrong + how do I fix it" (`Unauthorized` `server.rs:3372-3374`; `Unknown tool` `handler.rs:299`); README claims ("85+ tools") diverging from the code-verified count. FR-ZCP-12 turns these into measured contracts.

---

## 3. Functional Requirements

### 3.1 Zero-Config Project Resolution (FR-ZCP-01/02/13) — **P0, this revision's core**

**Narrative.** Like mnemopi's banks and zg's `root`: the project is derived from context, never typed by the user. The connection IS the scope.

**FR-ZCP-01 — Contextual project resolution (Must Have, P0)**

- Resolution order (first match wins):
  0. **Nearest repo marker wins**: walk up from the request cwd to the nearest `.leankg`/repo root (existing `find_leankg_for_path`, `server.rs:588-605`); that root IS the project. A cwd **outside** any repo marker (a container/workspace parent) resolves to the **portfolio scope**, never to a project (FR-ZCP-09).
  1. **stdio MCP**: process cwd → clause 0.
  2. **HTTP MCP**: harness-registered session mapping (see below) → server-initiated **`roots/list`** (standard MCP server-to-client request — OMP's client answers `file://<cwd>`, `pi-coding-agent/src/mcp/client.ts:59-64` and `manager.ts:#getRoots`; OpenCode does the same; LeanKG MUST ask once at initialize and re-ask on cwd-change capability) → legacy `?project=` (compat, deprecated) → loopback client IP + recent attach table. ~~clientInfo.workingDirectory~~ — **dropped in v4.1.1**: no harness sends it (OMP's initialize params carry only `protocolVersion`/`capabilities`/`clientInfo.name`, client.ts:99-105); `roots/list` is the standards-based equivalent.
- Project identity = **canonical project root** (existing `project_identity_keys_in`, `src/db/backend.rs:2613-2675`; schema `leankg_p_<hex>`, `:2543-2560`) — never a raw cwd hash, so opening a repo from any subdirectory, or after opening its parent portfolio, reuses the same schema. A future re-key (e.g. git-remote identity) MUST use the `schema_candidates_for_path` preferred+legacy adoption pattern (`:2528-2540`, `:2928-2948`) — no dual-write, no row migration.
- Harness session mapping: server keeps a registration table (`cwd → project`) populated by (a) `leankg install --target` writing per-repo MCP config that includes a one-time `register` call, or (b) OMP/OpenCode `session_start` hook calling `leankg_register(cwd)`. **The user never edits URLs.**
- Resolution is **cached per connection** (today there is none — every request re-walks the FS, `:637-661`); invalidation on cwd-change notification (stdio) or re-initialize.
- AC: a fresh agent session with zero config in a new repo gets correct KG answers for that repo; a second repo in the same server gets its own scope; a repo opened after its parent portfolio was opened reuses the existing schema (no re-index); no URL editing anywhere in the flow.

**FR-ZCP-02 — Lazy auto-attach + background first index (Must Have, P0)**

- First query against an unindexed repo: attach immediately (= one registry row once FR-ZCP-09 lands; today a de-facto `.leankg` init), answer from what exists (empty/stale + `freshness: cold`), and kick off **background** indexing (existing watcher + incremental indexer; see FR-ZCP-06).
- **Kill the silent fallback**: an unresolved project MUST error with "unknown project" (or auto-attach per `LEANKG_AUTO_ATTACH`), never route to the server-default schema (`server.rs:2987-2989` does this today — wrong-project data, no error).
- **Never block a query on indexing**: today `ensure_project_indexed` runs awaited inline in the request (`server.rs:2579-2694`, errors swallowed, no flag); move it fully background and surface state via `freshness`.
- Never fail a query with "not initialized" — degrade gracefully (`freshness: cold|possibly_stale|fresh`) and serve zero-verbosity results rather than errors.
- Indexing status surfaces via `mcp_status` and the router tool's preamble (no polling tool needed by default).
- Respect `LEANKG_AUTO_ATTACH=0` opt-out (indexing nothing by default in read-only/shared deployments); default ON for local single-user. This flag does not exist yet — it is introduced by this FR.
- Watcher lifecycle: the watcher is single-project-per-process today (`server.rs:1585-1596`/`:2015-2026`); multi-project attach requires per-project watcher tasks bounded by the same one-indexer-slot budget as FR-ZCP-09.
- AC: `rm -rf .leankg && query "where is auth handled?"` → immediate non-error response + background index completes within existing SLA; second query hits the graph; a query naming a never-seen repo never returns another repo's data.

**FR-ZCP-13 — First-run setup contract (Should Have, P1; registration verb `leankg add` superseded by `leankg index` in the Go engine)**

- **One question, asked once.** The first time a user touches a repo with no `.leankg` config (install wizard, first `leankg` CLI call, or the router's L0 response), LeanKG asks exactly one question: **auto or manual?** Auto = init + index + embed (catalog default model) proceed unattended (index/embed always background); manual = `init` only, with `index`/`embed` as explicit commands. The choice persists in `.leankg/config.json` (`{"setup": "auto"|"manual", "embed": bool}`) and governs every later attach; `--auto`/`--manual` flags and `LEANKG_SETUP_MODE` override per invocation for scripts/CI. No silent re-prompting; `leankg setup --reset` re-asks.
- **Embeddings are a preference, not a prerequisite.** Choosing auto-with-embed on a non-`--features` build (or a machine without the model cache) stores the preference and serves L2/L1 results (the ladder, FR-ZCP-03) while `embed` is pending or unavailable — the answer changes what is *eventually* indexed, never whether queries work.
- **Registration verb (Go engine: `leankg index <path>`; the designed-but-unbuilt Rust name `leankg add <path> [--embed]` is superseded)** — the one-command way to grow coverage: registers the repo (registry row once FR-ZCP-09 lands; store creation today), applies the persisted setup choice (or the flag), returns immediately with status-shaped per-project output. `leankg index .` inside a portfolio parent registers children without indexing them (T0 manifests, FR-ZCP-09). `leankg status` lists everything added with freshness + rung.
- **Zero dead ends**: `install --target` (FR-ZCP-04) prints the `add`/`index` commands in its output; the L0 router response names the exact next command; error strings carry runnable fixes (FR-ZCP-12 T1).
- AC: fresh machine + fresh repo → one auto/manual answer → queries work during indexing (`freshness: cold`, L0/L1) → embeddings arrive later with no further user action; `leankg index ../other-repo` from an indexed repo returns < 2 s and other-repo appears in `leankg status` with its own schema; manual-mode users are never auto-indexed.

### 3.2 Default Toolset (FR-ZCP-03) — **P0**

- One default tool (`leankg_context` — router) whose parameters express intent (`semantic`, `lexical`, `impact`, `graph`, `files`). The router classifies intent **itself** — the existing `QueryOrchestrator` (`src/orchestrator/mod.rs:26-110`, never wired to MCP) covers only 5 file-centric intents (context/impact/dependencies/search/doc) and is demoted to one rung's executor, not the ladder's brain.
- Portfolio-aware (rides FR-ZCP-09): a query resolved to portfolio scope routes to a child when unambiguous, else answers from T0 manifests with per-child freshness — the router is the single surface for both project and portfolio answers.
- **Capability probe + degradation ladder (router = the ladder executor):** the rungs already exist as per-tool honest hints — `embeddings_index_available` gating (`handler.rs:4417-4421`, applied `:1934-1935`), `vectors_missing_hint` pointing at `search_code` (`:4800-4811`), low-confidence empty-page fallback with `rejected_reason` (`:4866-4886`) — but no single tool executes them server-side. The router consolidates them: it probes per-project capabilities in < 10 ms (`state.has_any` limit-1 probe `src/embeddings/state.rs:373-381`; HNSW presence via `::relations` `src/db/pg/translate.rs:3209-3227`; `index_inventory.total_vectors`) and runs the best rung the data supports:
  - **L3 — vector rung** (vectors present): pgvector ANN (`ORDER BY vec <-> $1`) + cross-encoder rerank + BFS traversal — today's `semantic_search` dual path.
  - **L2 — keyword rung** (no vectors): FTS + trigram/prefix fuzzy (FR-ZCP-05 bridge tier) fused with ontology concepts (`safe_discover.rs:104-211`) — never a bare `ILIKE` dead end.
  - **L1 — exact rung** (no/cold index): exact identifier + regex over existing graph remnants + nearest-match suggestions.
  - **L0 — cold rung** (nothing indexed): guidance + background index kick (FR-ZCP-02), `freshness: cold`, non-error.
  - Every response carries `retrieval: {rung, reason}` beside the `freshness` flag (FR-ZCP-06); capability loss downgrades **ranking, never availability** — the `kg_semantic_context` hard error today (`handler.rs:3975-4036`) is the pattern to delete.
- **Single source of recommendations:** tool-hint copy moves into the router — today `safe_discover.rs:105-113` still recommends the **pruned** `find_function`/`query_file` (claim rot exactly of the kind FR-ZCP-12 T1 lints), and `search_code`'s stale `recommended_tools` copy duplicates fallback logic. After FR-ZCP-03, exactly one component owns "what to try next"; every other tool references it.
- **Hard one-tool envelope (v4.3.1 end-state, DONE):** the registry exposes exactly one tool; every capability rides `{verb}` (legacy tool names are the verb namespace — existing hints stay valid); envelope unwrapped before the read-only gate, write-lock, and audit; unknown tool/verb hard-refuses with a catalog error naming the verb mechanism.
- AC: fresh-session probes resolve via the router with ≤1 tool call for intent + ≤1 follow-up for detail.
- AC (ladder): deleting a project's vectors and re-asking the same query returns L2-ranked results with `retrieval: {rung: "keyword"}` — never an error; with no index at all the response is L0 guidance + a started background index, still non-error.

### 3.3 Search Discipline (FR-ZCP-05/06) — **P1**

**FR-ZCP-05 — Postgres FTS + RRF fusion (Should Have, P1)**

- `tsvector` + GIN on `code_elements(name, qualified_name)` + `knowledge_entries(title, content)`; `websearch_to_tsquery`; RRF-fused with vector scores in `semantic_search`'s dual path (single parameterized `k`); substring/`ILIKE` only as exact escape hatch.
- **Bridge tier (no FTS required)**: `pg_trgm` GIN on `code_elements(name, qualified_name)` + b-tree `text_pattern_ops` prefixes give L2 fuzzy/prefix matching before FTS ships; trigram similarity powers "did you mean" suggestions; `websearch_to_tsquery` upgrades L2 to ranked FTS when FR-ZCP-05 lands — the ladder (FR-ZCP-03) targets the best tier available per schema.
- AC: lexical anchor queries rank real identifiers above noise; no F1 regression on the cross-tool suite.

**FR-ZCP-06 — Freshness contract (Should Have, P1)**

- Every index-backed response carries `freshness: fresh|possibly_stale|cold`; `cold` = attached but not yet indexed (FR-ZCP-02 state).
- Background reconciliation (watcher-maintained; burst-limit fix already in `src/mcp/watcher.rs`) flips the flag; **heavy work never shares the request transaction** (lesson of the pre-PG v3.8.4 LOCK-poison incident).
- AC: forced drift → next response says `possibly_stale` and self-heals without blocking the query.

### 3.4 Agent Onboarding (FR-ZCP-04) — **P1**

**Narrative.** Onboarding is one command per harness, writing that harness's own config format, with **no project path in the emitted URL** — FR-ZCP-01's contextual resolution (stdio process cwd; HTTP `roots/list`) makes `?project=` unnecessary in the happy path, and shipping it by default is the dead-end this FR exists to kill. Implementation extends the existing `connect` writers (`src/connect/`) with the two missing targets; `install --target` is the global-config surface, `connect <client>` stays as its alias.

**Command.** `leankg install --target claude-code|cursor|codex|gemini|opencode|omp [--http --url URL] [--project PATH] [--register-cwd]` (Go flags; the Rust `--remote`/`--remove` do not exist)

**Per-target config writers** (entry name `leankg`; merge-or-replace that key only, atomic tmp+rename write, never clobber siblings; parse errors abort with the file path — existing `connect` semantics):

| Target | File | Entry shape | Status |
|--------|------|-------------|--------|
| `claude-code` | `~/.claude.json` | `mcpServers.leankg` — stdio `{command,args}` (no `type` key); http `{type:"http",url}` | exists |
| `cursor` | `~/.cursor/mcp.json` | `mcpServers.leankg` — `{command,args}` | exists |
| `codex` | `~/.codex/config.toml` | `[mcp_servers.leankg]` table via `toml_edit` (comments/order preserved) | exists |
| `gemini` | `~/.gemini/settings.json` | `mcpServers.leankg` | exists |
| `opencode` | `~/.config/opencode/opencode.json` | `mcp.leankg` — `{type:"local",command:[…],enabled:true}` / `{type:"remote",url,enabled:true}` | **new writer** |
| `omp` | `~/.omp/agent/mcp.json` | `mcpServers.leankg` — `{type:"stdio",command,args,enabled:true}` / `{type:"http",url,enabled:true}` | **new writer** |

**URL contract.** Default stdio entry: `<current exe> serve --stdio` — **no `--project` flag** (server resolves from process cwd, FR-ZCP-01 clause 1). `--http --url URL` emits the bare URL (e.g. `http://localhost:9699/mcp`) — no `?project=` suffix (server-initiated `roots/list` resolution; the Go `--remote` and `--docker` flags do not exist — Docker was a Rust-line exception with a container-mount table, and the Go engine has no Docker mode). `--project PATH` remains the explicit escape hatch and is the only way a `--project` flag gets emitted.

**`--register-cwd`.** Writes a Claude Code session-start hook running `<exe> index "$CLAUDE_PROJECT_DIR"` (exe resolved via `CurrentCommand()`; the variable quoted for the shell) — real effect: attaches-and-indexes the project (`index` creates-or-updates that project's store, incremental after the first run). The Rust-era `leankg add` verb has no Go case. It does **not** write a cwd→project table: the persistent session-registration table is FR-ZCP-01 clause 3, explicitly out of scope here. Clients with no hook mechanism get a printed note naming the manual command (zero dead ends).

**Zero dead ends.** `install` output always prints the next step: if the cwd project is unindexed, print `leankg index <cwd>`; always print "restart the client". The `import` tool's repo action is the tool-form of the same registration.

**Env hygiene (FR-ZCP-12 T1).** The `LEANKG_*` inventory is documented in one table, generated from source and CI-pinned — the table itself is the single source of truth for the count (a hand-typed total here would be exactly the unverifiable claim this AC polices; the last manual figure, "116 = 88+28+1", summed to 117 and matched no derivation). The happy path requires **zero** env vars beyond the one hard prerequisite (`LEANKG_PG_URL`). Every "zero-config"/"no-setup" sentence in README/docs names the script or CI job that executes it literally.

**Config-block parity.** `install`/`connect` emit **exactly one JSON (or TOML) block** per client (the Rust `mcp_install` verb is not in the Go toolset), byte-identical to the docs snippet — snapshot-tested per target.

- AC: fresh clone → `leankg install --target omp` → open omp in a repo → tools work, correct project, zero manual URL edits; no emitted config contains `?project=` outside the documented Docker exception; re-run is idempotent (entry replaced, siblings byte-identical); `--remove` deletes only the `leankg` key; snapshot tests pin each target's exact block.

### 3.5 Memory-Backend Adjacency (FR-ZCP-07) — **P1**

LeanKG as harness memory **via MCP** (no fork of OMP's closed `memory.backend` enum — verified v4.1.1: the enum `off|local|hindsight|mnemopi` is a closed switch, `pi-coding-agent/src/memory-backend/resolve.ts:16-25`, with no URL/adapter setting). Dual integration target:

1. **Mimic the mnemopi MCP surface** so LeanKG can stand in for `mnemopi mcp` (stdio, 22 tools, per-request `bank` arg falling back to `MNEMOPI_MCP_BANK`, `pi-mnemopi/src/mcp-tools.ts:284-395, 425-427`). LeanKG already serves MCP over HTTP; it exposes a compatible subset and honors the same conventions:
   - **Bank naming**: `sanitize(basename(cwd)) + "-" + wyhash36(abs cwd)`, ≤64 chars, `[A-Za-z0-9_-]` (`mnemopi/config.ts:176-186, 253-263`) — **derived from cwd only, never the git root** (upstream bug #2412: git-root resolution fragmented banks when a `.git` appears/disappears). LeanKG's canonical-root project identity (FR-ZCP-01) is the stable superset; the bank alias is computed for compatibility.
   - **Scoping matrix** (`computeMnemopiBankScope`, `mnemopi/config.ts:128-161`): `global` (write+read shared) / `per-project` default (write+read project) / `per-project-tagged` (write project, read [project, shared] merged + deduped). Cross-project recall is never implicit. Store `cwd` in memory metadata — it is load-bearing for OMP's legacy-bank rescue scan.
   - **Retain contract**: incremental transcript text in `[role: user]\n…\n[user:end]` framing (only user/assistant plain-text turns), one row per batch with `source="coding-agent-transcript"`, importance 0.65, metadata `{session_id, source_id: "<sessionId>-<ms>", message_count, retained_through_user_turn, cwd}` — LeanKG MUST persist and honor the **`retained_through_user_turn` integer cursor** (idempotency: re-retain with the same cursor is a no-op; sessions resume without re-retaining). Retain cadence belongs to the harness (default every 4 user turns on `agent_end`); LeanKG never re-frames or re-chunks what it is told to retain.
   - **Recall/injection contract**: ranked `{id, content, source, timestamp, score}` list — OMP renders it as the `<memories>` block appended to developer instructions on the first turn, capped by `recallLimit` (8) and `injectionTokenLimit` (5000 tokens), query composed from the prompt + last 3 turns truncated to 4000 chars (`mnemopi/state.ts:472-490, 914-922`). LeanKG's `session_retain` (FR-SMA-04) + `get_overview_context(recall=true)` + ranked-lesson read path (FR-SMA-01..03) are the implementation; the AC below is the mnemopi-parity gate.
   - Tool names LeanKG exposes for stand-in use: `session_retain`, `session_recall` (mnemopi-shaped args: `query`, `limit`, `bank`), plus id-stable `memory_get`/`memory_update`/`memory_forget`/`memory_invalidate` mirrors of `mnemopi_get/update/forget/invalidate`.
2. **Hindsight-shaped HTTP memory API** (evidence base for an upstream OMP proposal): hindsight proves the harness can use a **remote HTTP memory** (`hindsight.apiUrl` → `POST /v1/default/banks/{bank_id}/memories` + `/memories/recall` + `/reflect`, `hindsight/client.ts:274-340`; real `project:<name>` retain/recall tags in `per-project-tagged`, `hindsight/bank.ts:30, 95-103`). LeanKG exposes `POST /api/v1/memory/{bank}/retain|recall|reflect` on its existing axum server with mnemopi-identical payload semantics; with that artifact, propose upstream `memory.backend: "mcp"` + URL setting (the `MemoryBackend` interface is already backend-agnostic and non-throwing, `memory-backend/types.ts:80-166`) — upstream PR, not a fork (§1.2 non-goal). **SHIPPED 2026-09-14 (#414):** `leankg serve --hindsight-compat` mounts the client's byte-exact wire (`PUT /v1/default/banks/{bank}`; `.../memories` accepting `items[]` with `content/timestamp/context/metadata/document_id/tags`; `.../memories/recall` → `{results:[{text,…}]}`; `.../reflect` → `{text}` digest), so native memory points at LeanKG **without the upstream PR**: `memory.backend="hindsight"` + `hindsight.apiUrl` + `mentalModelsEnabled=false`. Retain bypasses the user-turn cursor via `memory.RetainRaw` (the hindsight wire has no cursor — `Retain(…,0)` would silently drop every write after the first); tags round-trip through entry metadata with `all`/`any` filtering; `update_mode:"replace"` is treated as append. Verified live against the shipped omp client call shapes.

**Service surface (v4.13.0, FR-ZCP-14).** The compat mount is a *service* only once it is readable and gated, not just writable. `GET /v1/default/banks/{bank}/stats` answers `{bank, entries, bytes, banks, last_retain, root}` (its absence made the client print "server unreachable" for a healthy server); `GET .../memories?offset=&limit=` lists newest-first with the unpaged `total`; `GET .../memories/{id}` resolves the wire id then the client's `document_id` and **404s** on an unknown id — a read-before-edit seam that answers 200-with-nothing is worse than no seam. Rows render through the same builder recall uses, so a read and a recall of one row are byte-identical. `serve --memory-global` hosts the bank root in `~/.leankg/memory`, making one process the memory service for several projects (opt-in: moving the root on a live deployment leaves its banks behind). Both memory prefixes now sit inside `auth`'s write gate — the compat alias previously let a Viewer retain what Contributor was required to write natively — with the last-segment rule keyed on the method, since `GET …/memories` (list) shares that path with `POST …/memories` (retain). `ponytail:` the read surface re-scans the bank JSONL per call, the same unindexed ceiling recall has (K7); past ~10⁴ rows/bank both want the FTS/L3 tier, not a per-route cache.

- AC (mnemopi parity): an OMP session configured with LeanKG's memory endpoint retains at the same cadence boundaries and recalls the lesson on the next session's first turn inside a `<memories>`-equivalent envelope, with the turn cursor preventing duplicate retention after resume; deleting the project bank never leaves the harness's cursor claiming rows that no longer exist (cursor + rows agree).
- AC (regression): retain → new session → recall injects the lesson (currently injects nothing — the v3.8.8 audit finding).

### 3.6 Benchmark Rigor (FR-ZCP-08) — **P2**

- Harden `benchmarks/cross_tool/` (existing 7-repo WITH/WITHOUT harness): pinned repo SHAs + prompt versions, ≥3 trials/arm with variance, judge-blind scorer; adopt the zg pitfalls checklist (leakage / like-for-like / stochasticity / tool-access smoke).

### 3.7 Portfolio Scale (FR-ZCP-09/10) — **P1/P2, org-scale moat**

**Narrative.** The empty glass → 1 repo → parent of 3 → parent of 100 progression (2026-09-04 stress test). Storage is already one PG database with schema-per-project — the missing pieces are a registry, a portfolio scope, and cross-schema reads.

**FR-ZCP-09 — Project registry + portfolio scope + cross-schema queries (Should Have, P1)**

- **Registry table** (`public.leankg_projects`): one row per attached project (canonical root, schema name, freshness tier, last-indexed, indexer state). Attach = INSERT; detach = archive. Today the project list is implicit (`.leankg` walks + `LEANKG_PROJECT_DIRS`, `server.rs:1062-1116`) — the registry becomes the project SoT.
- **Portfolio scope**: a cwd with no repo marker resolves to the portfolio, never a project. Portfolio behavior: depth-limited manifest scan (T0 — seconds, no tree-sitter), per-child freshness from the registry, cross-repo questions answered from manifests + `service_calls` + env configs. Zero eager indexing of children; a full index is strictly earned by the first touching query.
- **Index tiers**: T0 manifest inventory / T1 declarations-only shallow graph / T2 full graph + embeddings. Budget: **one background indexer slot** (same bounded-jobs discipline as the repo build rules), queue ordered by recency; hot-set cap (~8 fully-indexed children) with **LRU detach-to-cold** — detach archives graph data and keeps the registry row; an earned index is never silently destroyed.
- **Cross-schema portfolio queries**: schema-qualified UNION ALL / dynamic SQL over the registry on a catalog connection (no cross-schema capability exists today — only `current_schema()`-scoped UNION ALL, `translate.rs:3217-3224`). Cap fan-out (`max_repos_per_query`); ambiguous portfolio queries degrade to candidate repos + per-child freshness, never block.
- **Memory federation**: per-repo recall/diary banks (JSONL files under each `<project>/.leankg/` — file-based, verified) + one portfolio bank; recall merges both, writes never mix scopes (mirrors mnemopi `per-project-tagged`; folds in FR-SMA-05's git-common-dir worktree sharing).
- **Search-path hardening**: unqualified tables currently fall through to `public` (cross-tenant leak vector if DDL is ever shared); portfolio/catalog connections must qualify schemas explicitly.

- AC: attach 100 repos → registry has 100 rows, zero indexing started; one touching query indexes exactly one child in the background; a portfolio query returns candidates with per-child freshness < 500 ms; detach keeps cold data restorable.

**FR-ZCP-10 — Migration fleet reconciliation (Could Have, P2)**

- All 6 migrations run **per project-schema** with per-schema ledgers (`src/db/pg/migrations.rs:36-57`, `:83-116`) — nothing checks fleet-wide drift. Add a reconciliation pass + `doctor --deep` check: every registered schema at the latest migration version, per-schema HNSW/collection state consistent (`reconcile_vector_dim` semantics, `migrations.rs:124-159`), orphan schemas (registry row without schema or vice versa) reported.
- AC: `doctor --deep --format json` lists per-schema migration versions and flags any drift; a drifted schema is repairable with one command.

### 3.8 Embedding Correctness (FR-ZCP-11) — **P1, ported from zvec-grep**

**Narrative.** zvec-grep (v0.2.1, main@d756cc7) runs local embedding models while keeping vector data **provably correct** — the property LeanKG's embed pipeline currently lacks a guard for: nothing records which model produced a given vector set, so a model upgrade silently poisons the HNSW space. Port zg's machinery onto LeanKG's existing per-model collections (`src/embeddings/registry.rs:4` — collections already split per model; `state.rs:1-21` — content-hash staleness already exists).

**FR-ZCP-11 — Local-embedding vector correctness (Should Have, P1)**

- **Pinned model catalog** (zg: every entry = HF repo + 40-hex commit `revision` + dims + dtype + pooling + normalize + query/document prefixes, `src/engine/models/catalog.ts`): LeanKG's `EmbeddingModelEntry` gains `revision` (commit pin, not semver) and `query_prefix`/`document_prefix` (E5/nomic-style models need paired prefixes; one-sided prefixing silently degrades recall). Same reference ⇒ same vectors for a given release. Unknown id = hard error (today's `set_embed_model` already refuses unknown ids — keep).
- **Model schema stored with the vectors + hard mixed-model guard** (zg: `{provider, model, dimension, metric}` in the workspace manifest; mismatch → `EMBEDDING_SCHEMA_CHANGE_REQUIRES_REBUILD`, `service/zvec-grep.ts:1622+`; dimension-only checks are insufficient): persist the full stamp `{model_id, revision, dimensions, distance, provider}` per schema (a per-schema `leankg_meta` row beside the collections) and **refuse query/append on mismatch** with an explicit `leankg embed --rebuild --model <id>` remediation hint. Current `set_embed_model` runtime switch persists only `model_id` (`.leankg/embed-model.json`) — extend to validate the stored stamp before any embed/search against that collection.
- **Positional chunk keys, chunker-version coupling** (zg: `makeEntityId = sha256(fileId + "\0" + chunkIndex)` — content-independent keys, so content edits change vectors, never keys; chunker change is treated like model change: explicit rebuild): LeanKG's `embedding_state` is keyed by `qualified_name` (already positional/stable — keep); add a `chunker_version` to the model stamp; re-extraction schema change ⇒ mark collection `requires_rebuild` like a model change, never mix chunk generations under one ANN index.
- **Three-signal file change detection** (zg: size+mtime fast-path skip → SHA-256 content-hash confirm; per-hit query-time `fresh|possibly_stale` = `indexedTime >= mtime` OR rehash matches): the indexer's staleness marking (`mark_stale_if_changed`, `state.rs:218+`) gains the hash-confirm fallback so git-checkout mtime churn does not spuriously re-embed, and touches do not fake freshness (rides FR-ZCP-06's per-response flag).
- **Per-file atomic replace + truncation accounting + batch validation** (zg: mark dirty → delete-by-file → batch upsert → single optimize; `truncated_fragment_count` per file surfaced in status; every embedding batch validated for count/dimension/finiteness at the model boundary): `embed` processes per file; over-long inputs are counted and surfaced via `mcp_status` instead of silently truncated; imported vectors (`embed --import`) are validated against the active model's dims before entering the collection.
- **Watcher-miss insurance** (zg: hourly full reconciliation + sleep/wake resume checks + watcher-burst compaction with forced full reconcile beyond a path budget; `.gitignore` edit triggers directory rescan): schedule a periodic reconcile probe on the FR-ZCP-02 background indexer; watcher-burst handling reuses the existing burst-limit fix (`src/mcp/watcher.rs`).
- **Single-flight indexing** (zg: `JobScheduler.activeByRoot` coalescing + cross-process `{pid, hostname, instanceToken}` lease file with heartbeat/stale-PID adoption): one index/embed job per project root; concurrent MCP queries attach to the running job instead of duplicating it (FR-ZCP-02's background indexer + FR-ZCP-09's one-indexer-slot budget).
- AC: flipping the model stamp in a project's DB without re-embedding → next `semantic_search` errors with the rebuild hint (never mixed-model results); `touch`-ing a file without content change → rehash confirms fresh, zero re-embed; truncation counter appears in `mcp_status` after indexing a file with an over-budget chunk; two concurrent `embed` runs on one project → one runs, one coalesces.

### 3.9 Measured Simplicity (FR-ZCP-12) — **P1, young-product adoption contract**

**Narrative.** LeanKG is young; adoption is won by products that are simple *and measurably* simple. The friction audit (2026-09-04) put numbers on the debt: **76/73 MCP tools** (exact-count CI-pinned, `src/mcp/tools.rs:1156-1160` — two pruning rounds already went 87→76, so the momentum exists), **103 CLI verbs** (`src/cli/mod.rs`), **116 `LEANKG_*` env names** (~5 first-run relevant), a **10-step / 8-decision** first-value walkthrough, error copy that fails the two-question rule (`Unauthorized`, `Unknown tool`), and a README saying "85+ tools" while the registry pins 76. Competitors set the bar: zg = 1 default tool (6 full) + freshness + install; context7 = 2 tools; Supabase publishes "under 2 minutes"; Stripe ships ~200 error codes each with `code` + `doc_url`; clig.dev: suggest the next command, never dead-end. Per the 2025 Stack Overflow survey, 46% of developers actively distrust AI-tool accuracy — LeanKG's consumers are verification-hungry agents that reward deterministic, low-friction surfaces.

**FR-ZCP-12 — Measured-simplicity contract (Should Have, P1)** — three tiers, ordered cheap-first:

- **T1 — Error & config honesty (cheap; ship first).** Every user-facing CLI + MCP error carries a **stable code, a human cause clause, and a runnable fix** naming the concrete command/flag/env var, plus a doc anchor — Stripe's `code`+`doc_url` model; clig.dev's "what went wrong + how do I fix it". Immediate victims from the audit: `Unauthorized` (say which env/flag sets the token), `Unknown tool` (suggest the nearest match + the `full` catalog hint), low-confidence empty pages (link the fallback tool). CI lints: error variants enumerated from source vs a catalog (100% coverage); fix-clause lint on every error string (allowlist shrinks, never grows). Config surface: one copy-paste JSON block per client (FR-ZCP-04), docs snippet byte-identical to generated (snapshot test); every "zero-config" claim maps to a named script/CI job that runs it literally (Vercel pattern) — unverified claims get deleted, not footnoted.
- **T2 — Published, CI-timed time-to-first-value.** The happy path (`install → leankg init → leankg mcp-http → one JSON config block → first useful query`) is measured in CI on a fresh cold environment and the number is **published** in README + quickstart. Target: **first useful MCP query ≤ 5 minutes** (embeddings stay out of the promise; Supabase's "under 2 minutes" and Convex's 7-step one-command path are the reference bar; zg publishes no TTFV number at all — publishing one wins mindshare). If measurement says worse, the PRD number changes, not the test.
- **T3 — CI-pinned one-tool invariant (v4.3.1 re-scope; the ≤ 12 budget + `full` opt-in is superseded by the hard cutover).** CI enforces `ToolRegistry::list_tools().len() == 1` and verb-catalog membership for every capability; the single tool description documents the verb mechanism. Audit/read-only/write-lock decisions resolve the effective capability from the envelope **before** any gate.
- AC-T1: 100% of error variants resolve to a catalog entry with doc anchor; every error string contains cause + runnable fix; docs config block == generated block (CI snapshot); zero unverifiable zero-config claims.
- AC-T2: CI times the cold happy path end-to-end; README publishes the measured number; a regression above 5 min fails the gate.
- AC-T3 (v4.3.1): registry length == 1 (CI-enforced); every legacy capability resolvable as a verb (catalog lint); unknown names refused with `LEANKG_ERROR_UNKNOWN_TOOL` naming the nearest verb; audit records the effective capability.


### 3.10 Self-host dogfood loop (FR-SELF-01..04) — **P0, the operating plan (v4.11.0)**

**Narrative.** Everything shipped so far was validated against throwaway fixtures and ephemeral dogfood runs. From this revision the operating mode inverts: LeanKG's own repository is served by a long-lived dynamic HTTP front door — one binary, MCP streamable HTTP (`/mcp`) + REST (`/api/*`) + the embedded dashboard, with per-connection project resolution (`?project=` / nearest-`.leankg` walk / `LEANKG_PROJECT_DIRS`) — continuously indexed, embedded with a pinned provider, and memorizing. That self-host then becomes *the* tool used to build LeanKG. The stages are a ladder: each gates on the previous one reporting clean.

**FR-SELF-01 — Bootstrap the self-host (P0; gate **satisfied 2026-09-14 by v4.11.1**)**
- The deploy wave this gate waited on has landed: [Dockerfile](../Dockerfile) + the Render service (rebuilt from the Go image), the `go/vX.Y.Z` module-tag mirror in `release.yml`, and the `serve` `/health` liveness route on the dashboard listener. Run: `leankg serve --http :9699 --rest :8080 --ui :8081 --memory` against this checkout (sqlite store in `.leankg/`).
- Index this repo (`leankg index .`), embed with the pinned identity (`leankg-embed run`; `LEANKG_EMBED_*` ModelStamp-guarded — L3 must answer with `retrieval` provenance, not degrade), and turn memory on: the markdown layer + `session_retain`/`session_recall`.
- AC: `/health` on every listener; `status` shows `fresh` coverage for this repo; L1/L2/L3 verified live on its own elements; retain→recall round-trip survives a server restart; `doctor --deep` clean.

**FR-SELF-02 — Build LeanKG with LeanKG (P0, continuous once S1 is up)**
- Every subsequent change to this repo is informed by the self-host *before* it is made: `impact`/`callers` blast radius before touching an exported symbol, `get_tested_by`/traceability before closing an issue, `session_recall` when reopening a session, `explain`/path queries during RCA.
- Every wrong, stale or empty answer the loop produces is an engine defect: filed as an issue, fixed failing-test-first against this repo's real data, then re-indexed and re-asked ("LeanKG first").
- AC: PR descriptions cite graph evidence (impact/callers output) for exported-symbol changes; dogfood findings enter the tracker with the same rigor as unit-test failures; no regressions attributed to missing graph context.

**FR-SELF-03 — Scale to nested-repo parents (P1; gate: S1 + S2 running smoothly)**
- Add small parent directories with nested repos to the same server, one at a time: single-repo leaves first (measured per-project stores: 0.6–17 MB), then a 2–3-repo parent via the portfolio registry (register-on-index, T0 manifest, T1 hot-set fan-out, `LEANKG_PORTFOLIO_MAX_REPOS` = 8, zero eager indexing). Scale up only while `doctor --deep` and per-child freshness stay honest.
- **Hard guard (measured 2026-09-11):** never bulk-index the `freepeak` portfolio root (99,574 files — a multi-GB, 30–60 min job); scaling means repos entering the registry one by one, never one recursive walk.
- AC: portfolio queries answer with per-child attribution and honest freshness; adding a repo costs one `register-project`/`index`, no restart; memory banks stay per-project scoped.

**FR-SELF-04 — Keep building, keep fixing (P0, the end state)**
- The steady state after S1–S3: every indexing/embedding/memory wave on real code surfaces defects; defects are fixed in the engine, never worked around per-caller; release-please keeps shipping; this PRD's status tracks the cycle.
- AC: dogfood-found issue rate stays > 0 while the fix rate keeps pace; no milestone regresses; the self-host is up whenever development happens.

---

## 4. Architecture (HLD Summary)

| Layer | Today | Zero-config delta |
|---|---|---|
| Transport | axum HTTP MCP (`/mcp`) + stdio; Bearer + DB token store; `?project=` routing | Resolution layer **in front of** routing (FR-ZCP-01); `?project=` demoted to escape hatch |
| Storage | PostgreSQL + pgvector, **one database, schema-per-project** (`leankg_p_<hex(canonical root)>`; per-connection `search_path` pin, per-schema migrations + HNSW) | Registry table + portfolio scope + cross-schema portfolio queries (FR-ZCP-09); fleet reconciliation (FR-ZCP-10) |
| Indexing/embeddings | tree-sitter graph + optional embeddings (`--features embeddings`), incremental watcher — single-project-per-process, inline `ensure_project_indexed`; per-model collections exist but **no model stamp/guard** on the vectors | Lazy auto-attach + **background** first index (FR-ZCP-02); tiers T0/T1/T2 + one indexer slot + hot-set LRU (FR-ZCP-09); pinned catalog + model-stamped vectors + rebuild guard + single-flight (FR-ZCP-11) |
| Tools | Was 76/73 raw tools exact-count CI-pinned; v4.3.1 hard cutover → **1 tool** (`leankg_context`) with ~76 capabilities as verbs | One-tool envelope (FR-ZCP-03 end-state); published TTFV (FR-ZCP-12 T2); error code+fix catalog (FR-ZCP-12 T1, DONE); portfolio-scoped answers from T0 manifests |
| Memory | RecallStore = **JSONL files** under `<project>/.leankg/` (read path complete; write path dead — v3.8.8 audit); `knowledge_entries` per-schema PG | `session_retain` + auto-recall live (FR-ZCP-07, rides FR-SMA-01..04); portfolio memory federation (FR-ZCP-09) |

**Trust boundaries:** loopback-only HTTP by default; Bearer auth independent of embedding egress — but note (v4.5.0 correction): **remote embedding now exists** (`LEANKG_EMBED_PROVIDER=openai`, `src/embeddings/provider.rs` `OpenAiCompatibleProvider`, usable without the `embeddings` feature; catalog: Qwen3-Embedding-4B / jina-v3 / gemini-embedding-2|001). Setting it ships embedding input blobs (element names + doc lines, not raw source bodies — `text_blob.rs`) to a third-party API; it is an explicit opt-in and the stale "no remote embedding" claim is hereby corrected.

---

## 5. Milestones

| Milestone | Scope | Gate |
|---|---|---|
| **M1 — Zero-config attach** | FR-ZCP-01, FR-ZCP-02, FR-ZCP-13 | New repo, zero config → correct answers; no "not initialized" failures; one setup question (auto/manual) honored everywhere; the registration verb (Go: `leankg index`) returns instantly with status |
| **M2 — One-tool surface** | FR-ZCP-03 (+04) | Default set = 1 router tool; ladder degrades L3→L0 with `retrieval` provenance and zero hard errors; v3.8.5 probe suite passes; `install --target` writes project-less URLs |
| **M3 — Honest search** | FR-ZCP-05, FR-ZCP-06 | FTS ranking + freshness in every response; no F1 regression |
| **M4 — Harness memory** | FR-ZCP-07 (rides FR-SMA-01..04) | retain → recall round-trip works in OMP + OpenCode sessions; mnemopi-compatible bank/cursor contract verified against OMP session resume |
| **M5 — Defensible evidence** | FR-ZCP-08 | Pinned, ≥3-trial, judge-blind cross-tool report published |
| **M6 — Org-scale portfolio** | FR-ZCP-09, FR-ZCP-10 | 100-repo parent: registry rows ≠ indexed repos; portfolio queries answer from manifests; no eager indexing; `doctor --deep` reports fleet drift |
| **M7 — Embedding correctness** | FR-ZCP-11 | Model-stamp mismatch → explicit rebuild error, never mixed-model results; rehash-confirms-fresh; truncation accounting in `mcp_status` |
| **M8 — Measured simplicity** | FR-ZCP-12 (T1 immediately; T2/T3 after M1/M2) | Error catalog 100% + fix clauses CI-linted; README publishes the CI-timed TTFV; default-tool budget CI-enforced |
| **M9 — Three tools + dual backend** | FR-3T-01..04 | 3-tool registry (`import`/`query`/`status`) + SQLite default with PG opt-in; this repo indexed live and L1/L2/L3 answered over MCP HTTP (v4.6.0–v4.10.x waves) |
| **M10 — Self-host dogfood loop** | FR-SELF-01..04 | Persistent dynamic HTTP server (MCP + REST + dashboard) over **this repo**, index+embed+memory green; development flows through the self-host; then small nested-repo parents added one at a time (never the portfolio root); the loop is the steady state |

Order: M1 → M2 → M3 → M4 → M5 → M6 → M7 → M8, with M8's T1 tier (error/config honesty) pulled forward immediately — it is docs-and-strings-cheap and multiplies every later milestone's adoption. M1 and M2 are the adoption blockers; M3/M4 are quality gates; M5 is evidence; M6 is the org-scale moat; M7 hardens the embedding layer that M6's tiers depend on; M8 keeps the young product honest about its friction. M9 (3-tool surface + dual backend) closed by the Go rewrite itself; **M10 is the current milestone** — every feature claim from M1–M9 is re-proved against the live self-host before anything scales past this repository.

### 5.1 M10 runbook (the anchored next actions, in order)

1. ~~Wait for the refactor waves to land~~ — **done in v4.11.1** (Docker/Render deploy service, `go/vX.Y.Z` module-tag mirror, `serve --ui` `/health`). Pull rebase-clean `main` before touching anything.
2. **S1 — bootstrap the self-host** (this checkout, sqlite engine) — **DONE v4.11.2; the corrected commands as actually run:**
   ```bash
   # one-time: brew install llama.cpp (the pinned GGUF self-downloads on first embed — no manual fetch)
   llama-server -m ~/.leankg/models/bge-small-en-v1.5-f16.gguf --embeddings --host 127.0.0.1 --port 8085 -c 8192   # or: leankg-embed spawns this itself when LEANKG_EMBED_SIDECAR_ARGS is unset
   go build ./... && ./bin/leankg index . --auto     # this repo -> .leankg/leankg.db
   env LEANKG_EMBED_PROVIDER=local LEANKG_EMBED_BASE_URL=http://127.0.0.1:8085/v1 \
       LEANKG_EMBED_MODEL=bge-small-en-v1.5-384 bin/leankg-embed full .
   bin/leankg serve --http :9699 --rest :9700 --ui :9701 --memory --embed-provider local --hindsight-compat --project .
   ```
   Ports 9700/9701, not 8080/8081: the live onegw gateway owns 8080 (pre-flight `lsof -iTCP -sTCP:LISTEN` first) — including for the sidecar: `--embed-provider local` **without** `LEANKG_EMBED_BASE_URL` spawns the pinned-GGUF llama-server on :8080 (default `LEANKG_EMBED_SIDECAR_ARGS`; v0.35.0-dev+ — before that it spawned a model-less `llama-server` and crash-looped) and serve crash-loops when the port is taken. With it, serve ATTACHes the running sidecar — a serve process never orphans a spawned one.
   `--hindsight-compat` (#414, v0.34.0+) additionally serves omp's Hindsight wire on the REST listener, so the harness's native agent memory points here: `memory.backend: hindsight` + `hindsight.apiUrl: http://127.0.0.1:9700` + `hindsight.mentalModelsEnabled: false` in `~/.omp/agent/config.yml` (e2e-proven 2026-09-14: a real `omp -p` session retained `zebra-cantaloupe-42` through the mount into bank `omp`, and a fresh session reproduced it from the recall injection).
   `--memory-global` (v4.13.0+) keeps that bank root in `~/.leankg/memory` instead of `<project>/.leankg/memory`, so one serve process is the memory service for several projects — opt-in, because moving the root on an existing deployment leaves its banks behind. The compat read surface is `GET .../stats` (entry/byte/bank counts, last retain, root), `GET .../memories?offset=&limit=` (newest-first, unpaged `total`), and `GET .../memories/{id}` (wire id, then `document_id`; unknown id is a 404). Two servers on one root append to the same JSONL with no cross-process lock — the startup line names the root for exactly that reason.
   Verify (all ✅ 2026-09-14): `/health` → `{"ok":true}` ×3; L1/L2/L3 with `retrieval` provenance (L3 = cosine on 8,989 real vectors); retain→recall survives restart; `doctor --deep` 0-fail after the wave's gc run.
3. **S2 — work through it**: impact/callers before exported-symbol edits, tested-by before closes, recall at session open; wrong answers become engine defects (failing test first), fixed in the root module, then re-index and re-ask.
4. **S3 — scale carefully**: add small nested-repo parents one at a time via the registry / `LEANKG_PROJECT_DIRS` (hot-set cap 8, zero eager indexing). **Never** index the `freepeak` root (99,574 files).
5. **S4 — loop**: build, fix, release via release-please; keep this PRD + tracker as the status ledger each cycle.

---

## 6. Non-Functional Requirements

| Metric | Target |
|---|---|
| Project resolution overhead | < 5 ms per connection (cached) |
| First-query-on-unindexed-repo | Non-error response < 500 ms; background index per existing SLA |
| Router capability probe | < 10 ms per request (limit-1 probe + catalog reads; cached per connection between cwd changes) |
| Ladder degradation | Same query returns non-error results at every rung L0–L3; `retrieval: {rung, reason}` on 100% of index-backed responses; zero "not initialized"/no-vector hard errors in the default toolset |
| Default toolset surface | **1 tool** (`leankg_context`) — hard cutover, CI-enforced; capabilities ride the `{verb}` envelope |
| One-tool envelope (T3, v4.3.1) | `list_tools().len() == 1` CI-enforced; envelope resolved before RO-gate/write-lock/audit; unknown verb → catalog refusal with nearest-verb fix |
| Storage | PostgreSQL + pgvector only — one database, schema-per-project; the registry is the project SoT |
| MCP HTTP | loopback by default; Bearer + DB-backed access-token store |
| Portfolio attach (T0) | One registry INSERT + depth-limited manifest scan; zero eager indexing |
| Portfolio query (unindexed children) | Candidate repos + per-child freshness < 500 ms; fan-out capped (`max_repos_per_query`) |
| Model-stamp integrity | 100% of vector collections carry `{model_id, revision, dimensions, distance, provider}`; mismatch → explicit rebuild error on query/append, never mixed-model results |
| Embedding batch validation | 100% of embed/import batches validated (count, dimension, finiteness); truncations counted and surfaced via `mcp_status` |
| Single-flight indexing | ≤1 active index/embed job per project root; concurrent requests coalesce |
| Error contract (T1) | 100% of CLI+MCP error variants: stable code + cause clause + runnable fix + doc anchor; CI lints every string; allowlisted exceptions shrink, never grow |
| Time-to-first-value (T2) | Cold happy path (install → init → serve → one JSON config block → first useful query) ≤ 5 min, CI-timed on a fresh environment, number published in README |
| Claim hygiene (T1) | Every "zero-config"-class README/docs claim maps to a named script or CI job that executes it literally; claims without a passing script are deleted |
| Setup friction (FR-ZCP-13) | Exactly one auto/manual question per user, persisted; the registration verb (Go: `leankg index`) returns < 2 s; manual mode never auto-indexes |
| Test layering (v4.11.2 contract) | Unit tests in-process + fast (no real provider/network/sleeps; `go test ./...` is the whole gate, 300 s CI budget); real embed/LLM/PG calls live in env-gated integration/e2e (`LEANKG_TEST_PG_URL`, live sidecar, `ttfv` job); **benchmarks never run in CI** |

## 6b. Rust CLI parity — per-verb disposition

Audit of all 73 variants of the Rust `CLICommand` enum against the Go surface. `C` = covered by an existing Go surface · `I` = implemented in this wave · `D` = deliberate drop (with rationale).

| Rust verb | Disposition |
|---|---|
| Version, Status, Serve, Web, Dashboard, ApiServe, McpStdio, McpHttp, Index, Add, Init, Impact, Embed, IndexDocs, Refresh(C+I), Query, SemanticContext, Ontology, Trace, FindByDomain, CheckConsistency, LspResolve, Doctor, Status, Watch, Setup(register half) | **C/I** — `version`, `status`, `serve`(-stdio/-http/-rest/-ui/-rpc), `index`, `impact`, `run`, `leankg-embed`, `query` (+`--action` passthrough for path/explain/callers/callees/context/pattern/lsp/compress/read), ontology actions (matches/trace/status/concept_search/feature_flow/traceability), `doctor [--deep]`, `writer`, `refresh` |
| GraphQuery, Path, Explain, Gods, Report, DetectClusters, Tunnels, Quality, Reflect, Ctags, Cost, Audit, Migrate, Auth, ApiKey, Export, Pack, Generate, Annotate, Link, SearchAnnotations, ShowAnnotations, Register, Unregister, List, StatusRepo, Obsidian, Run | **I** — this wave (see ledger rows above) |
| LspInstall, LspList | **D** — install hints deliberately unported (documented in `internal/lsp/registry.go`); resolution/query-time LSP is live |
| SmokeTest, Benchmark, ToolBench, AbTest, BenchmarkUnified | **D** — harnesses live in Go tests + `benchmark/ab` + `scripts/` |
| Metrics, Dashboard(usage half) | **I** — `context_metrics` ledger (migration 011) + `internal/metrics`: `leankg metrics` (since/tool/json/session/reset/retention/cleanup/seed) and the H10 usage buckets behind `leankg dashboard`; recorded from the MCP dispatch as Rust did |
| Update | **D** — self-update ships again: `leankg update` resolves `releases/latest`, verifies the `leankg-<goos>-<goarch>.tgz` asset and installs both binaries; `.github/workflows/release.yml` publishes them automatically |
| Proc | **D** — dev process management (Leankg/Vite), out of the engine's scope |
| MineConversations | **I** — `internal/convo` + `leankg mine-conversations` (Claude/ChatGPT/Slack; fixes the Rust edge-loss defect) |
| Incident, Note, EnvConflicts, Team | **I** — `internal/orgknowledge` + `incident\|note\|env-conflicts\|service-context\|team-map` verbs and `/api/v2/*`; `team` = the team-map read (membership/ownership live in the auth subsystem — no teams table by design) |
| Push, Pull | **I** (client parity) — `internal/federation`; note Rust's `pull` was only a `/api/v2/status` probe and **no server ever served `/api/v2/graph/push`**, so the receiver/merge policy is still a design task: #372 |
| Prs | **D** — PR-impact analysis is a separate product backlog item, never part of the engine rewrite |
| gc.rs | **D** — Rust-allocator workaround (`malloc_trim`, RSS polling for glibc); Go's scavenger returns memory itself, so there is nothing to port |
| budget.rs, mcp/token_budget.rs, errors.rs | **I** — `internal/budget` (per-action caps, `_token_budget` marker, RSS guard) and `internal/errs` (14-entry catalog, FR-ZCP-12 T1) wired through MCP/CLI/REST/auth |
| cost_estimate / ctags_export / prd_indexer / sources / setup(clone) | **I/C** — `leankg cost`, `leankg ctags`, `internal/prdindex` (+ `prd\|prd-trace`), `internal/sources` (`index\|refresh --source`); the `setup --clone` pipeline itself is **D** (its per-repo clone spec is servable by `internal/sources` if wanted) |

### Known remainders and unverified seams (truthful record)

Everything below is *known*, with its consequence stated — none of it is a silent gap.

| Item | State / consequence |
|---|---|
| PG migrations 008 (tokens), 009 (enterprise auth) | **Executed** on throwaway PostgreSQL clusters during the wave (their agents' reports). |
| PG migrations 008–011 (tokens, enterprise auth, org knowledge, context metrics) | **Executed on PostgreSQL 18.6** (throwaway local cluster): all 17 gated PG tests green — round-trips for tokens (expiry/revocation/lifecycle), accounts/orgs/memberships/ownership, incidents/knowledge_entries/service_metadata/env_snapshots (arrays, nullable BIGINTs, jsonb metadata), metrics ledger, plus `Migrate` idempotency and schema isolation. Re-run anywhere with a PG server: `LEANKG_TEST_PG_URL=… go test ./internal/store/ ./internal/auth/ ./internal/orgknowledge/ ./internal/metrics/` |
| `gc.rs` (Rust `MemoryGuard`) | **Go-native substitute, not a port**: Rust polled RSS and called `malloc_trim` per MCP request plus an idle-triggered vacuum scheduler; Go's runtime GC returns memory itself and the store has no vacuum step. The *idle-gated embedding* behaviour (`embeddings/control.rs`) has no Go counterpart — recorded as dropped, not forgotten. |
| doc→code join (`doc_indexer` references/`documented_by` edges, `paths.go`, dir elements, 512K cap) | **Unported**: `internal/docindex` covers the doc walk and sections only, so import-hygiene questions that relied on doc→code edges return empty. |
| Error catalog wiring | 6 of 14 catalog entries are wired into call sites; the rest have no Go emission point yet (the drift-audit test logs the uncovered set rather than hiding it). |
| `env_snapshots` | Go stand-in for Rust's env-scoped `code_elements` (Go elements are keyed on `qualified_name`, single-env). Consequence: `calls`/`called_by`/`schemas` cannot be env-filtered. |
| `leankg team` verb + `/api/teams` routes | Unported (membership/ownership live in the auth subsystem; the `team-map` read exists). |
| Agent notes authority | `knowledge_entries` (migration 010) carries **scoped org facts** (per element/feature/environment, queryable); the markdown memory layer (#369: `MEMORY.md`/`USER.md`/`topics/`) remains the **agent-facing** substrate. Two substrates by design, one purpose each — not interchangeable. |
| Embeddings single-flight | Still absent: concurrent `leankg-embed` runs are not serialized (a second writer interleaves with the first). Per-file atomic replace and truncation accounting **did** land with #279 (writes are grouped per file in one transaction, so a crash cannot leave half a file's vectors). |
| `tmp-langs/` (29 MB probe dir, committed by an earlier session in `c4f55a5f`) | Left in place deliberately (not this wave's artifact): a cleanup candidate for the maintainer. |

### Environment inventory (build/runtime)

| Variable | Default | Meaning |
|---|---|---|
| `LEANKG_DB_ENGINE` | `sqlite` | storage engine (`sqlite` \| `postgres`) |
| `LEANKG_DB_PATH` | — | SQLite store file path (default `<project>/.leankg/leankg.db`); set to make the project directory optional (FR-P2) |
| `LEANKG_PG_URL` | — | PostgreSQL DSN (libpq params incl. `sslmode`/`sslrootcert` ride the URL) |
| `LEANKG_PROJECT_DIRS` | — | comma-separated project dirs to serve (multi-project routing) |
| `LEANKG_PROJECT` | cwd | project dir for `query`/`impact` when `--project` is absent |
| `LEANKG_TOKEN_ADMIN` / `_CONTRIBUTOR` / `_VIEWER` | — | static bearer fallback (DB tokens take precedence) |
| `LEANKG_EMBED_PROVIDER` | `local` | `local` \| `openai` \| `deterministic` |
| `LEANKG_EMBED_BASE_URL` | — | attach to a running OpenAI-compatible endpoint (with `local`, skips the sidecar) |
| `LEANKG_EMBED_API_KEY` | — | bearer for the embedding provider |
| `LEANKG_EMBED_MODEL` / `_DIMS` / `_REVISION` | `local` / `384` / `local:<model>` | ModelStamp identity; a revision change invalidates the collection |
| `LEANKG_EMBED_SIDECAR_CMD` / `_ARGS` / `_PORT` / `_READY_SECS` | `llama-server` / pinned bge-small GGUF + `--embeddings` / `8080` / `120` | sidecar spawn + bounded readiness; unset `_ARGS` serves `-m ~/.leankg/models/bge-small-en-v1.5-f16.gguf` when present, else `-hf CompendiumLabs/bge-small-en-v1.5-gguf:f16` (self-downloads) |
| `LEANKG_CACHE_MAX_TOKENS` | `500000` | compression session-cache budget |
| `LEANKG_MAX_CACHE_ELEMENTS` | `50000` | mega-graph threshold (ontology-first discovery path) |
| `LEANKG_PORTFOLIO_DB` | `$HOME/.leankg/portfolio.db` | fleet registry location (sqlite); the suite redirects it so tests never write user state |
| `LEANKG_PORTFOLIO_MAX_REPOS` | `8` | hot-set cap for a portfolio fan-out; re-read per fan-out, so it is a serving knob |
| `LEANKG_LLM_BASE_URL` / `_API_KEY` / `_MODEL` | — | the #297 summarize pass (OpenAI-compatible `/chat/completions`, temperature pinned 0) |
| `LEANKG_JUDGE_URL` / `_API_KEY` / `_MODEL` / `_TIMEOUT_SECS` | — / — / `jev-1.13.0` / `30` | `internal/judge` Server backend (System One provider API: Jev or Jev-compatible Laya endpoint); unset = no provider calls |
| `LEANKG_JUDGE_SIDECAR_URL` | — | `internal/judge` Local backend attach point (Laya `laya-serve` sidecar); neither URL set = nil judge, native rules, zero calls |
| `LEANKG_SUMMARIZE_AFTER_INDEX` | off | opt-in post-index LLM pass; default off so indexing can never spend tokens unprompted |
| `LEANKG_UPDATE_MAX_MB` | `128` | `leankg update` download ceiling; over-cap is an error, never a truncation |
| `GITHUB_TOKEN` | — | raises the `leankg update` API rate limit; absence only warns (anonymous calls work) |
| `PATH` | — | ast-grep / LSP server / sidecar discovery |

### FR-DSH-01 — Optional DSH usage dashboard (default off)

Dogfood observability for whether coding sessions call LeanKG MCP correctly. Not part of the default binary: rebuild with `-tags dshusage` / `make go-build-dshusage`. Laya corroboration stays off unless `LAYA_URL` / `LEANKG_JUDGE_SIDECAR_URL` / `--laya-url` is set (same nil-judge pattern as FR-TYPE-02). Product-side fixes that always ship: multi-project MCP rejects omitted `project` (fail-closed) and streamable HTTP runs `Stateless: true` so sticky client session IDs survive process restart. See [`dsh-root-causes.md`](dsh-root-causes.md), [`laya-shared-service.md`](laya-shared-service.md), UC-9 in [`judge-use-cases.md`](judge-use-cases.md).

### FR-P2 — Project directory optional (DB from config/env)

- The project directory is **no longer required** for the server to run. When configured, the server opens a standalone SQLite store whose path comes from configuration, not from `<cwd>/.leankg/leankg.db`.
- **Precedence** (db path, high → low):
  1. `LEANKG_DB_PATH` environment variable.
  2. `db.standalone_db_path` key in `leankg.yaml` (see `DBConfig` in `internal/projectcfg`).
  3. Default: `<project>/.leankg/leankg.db` (project-scoped mode, existing behavior).
- **Serving**: `leankg serve --db <path>` (CLI flag, equivalent to env). When the flag/env is set, project-resolution sidecars (auto-index, setup pipeline, leankg.yaml anchor resolution) are skipped — the server has no checkout in this path (FR-P2-3).
- **Importing**: users import data themselves via `leankg import ./` (indexes cwd with full absolute path as an indexable/embeddable target; embedding only fires if an embedding provider is configured — `LEANKG_EMBED_PROVIDER` / sidecar). The MCP `import` tool description guides agents to use `action=dir` with `path="."` to scope import to the current directory.

---

## 7. Historical Record

All superseded material is preserved and linked, not deleted:

- **PRD history v2.0 → v3.8.9** (competitive analyses: Graphify, CBM, TencentDB, MemPalace, Codez, zvec-grep; harness-era repositioning; session-memory audits): [`archive/prd.md`](archive/prd.md)
- **Task-tracker history (560+ items, waves 0–4, release gates):** [`archive/prd-task-tracker.md`](archive/prd-task-tracker.md)
- **zvec-grep audit (2026-09-03):** [`archive/analysis/zvec-grep-vs-leankg-2026-09-03.md`](archive/analysis/zvec-grep-vs-leankg-2026-09-03.md)
- **OMP memory-integration draft (2026-09-03):** [`archive/planning/2026-09-03-leankg-omp-memory-integration-draft.md`](archive/planning/2026-09-03-leankg-omp-memory-integration-draft.md)
- **Design/ERD/architecture docs:** [`archive/design/`](archive/design/), [`archive/erd.md`](archive/erd.md), [`archive/architecture.md`](archive/architecture.md)
- **OMP memory-backend + zvec-grep embedding audits (2026-09-04, installed-source @ `node_modules/@oh-my-pi/*`, zvec-grep main@d756cc7):** findings folded into this PRD (§3.1, §3.5, §3.8); full citations inline
- **Simplicity research sprint (2026-09-04, three parallel scouts):** repo friction audit (file:line — 76/73 tools, 103 CLI verbs, 116 env names, 10-step walkthrough, error-copy gaps); competitor mechanics (zg, context7, serena, Desktop Commander, gitleaks — live-fetched URLs); onboarding playbooks (Supabase/Convex TTFV, Stripe error codes, clig.dev, Vercel, Stack Overflow 2025) → findings folded into §2.6, §3.9 (FR-ZCP-12), §5 M8, §6
- **One-tool ladder + setup-contract design (2026-09-04, two scouts):** retrieval-engine inventory (exact/regex, ontology keyword, pgvector ANN+rerank, graph BFS) with capability probes (`state.has_any`, `::relations`, `index_inventory`), the unregistered `orchestrate` parser, and the zero-FTS schema audit → folded into §3.1 (FR-ZCP-13), §3.2 (ladder), §3.3 (bridge tier)
- **Rust→Go rewrite feasibility study (2026-09-10):** [archive/analysis/go-rewrite-analysis.md](archive/analysis/go-rewrite-analysis.md) — 168k-LOC audit with pros/cons, shipped-vs-vision gap table (target ≈90% already live), Go target architecture (WAL sqlite + PG/pgvector, watermark freshness, MCP/REST/ConnectRPC from one core, provider-first embeddings), 7-wave migration plan, evidence index

- **FR-TYPE-02 (2026-09-21):** `internal/judge` abstraction (Server + Local backends over the Jev-compatible state+questions wire, `FromEnv` selection, unavailable-never-fatal) + one LIVE call site (convo `ClassifyWithJudge`: keyword-first, judge only on the KindGeneral branch, confidence-gated) + [`judge-use-cases.md`](judge-use-cases.md) (Laya deep-dive from the HF source, function_calling cookbook patterns, 8 use cases: UC-3 LIVE, UC-1/2/4/6/7 CANDIDATE, UC-5/8 likely never) + interactive diagram [`diagrams/laya-judge.html`](diagrams/laya-judge.html) (archify showcase 9/9, three guided views: backbone / judge branch / never-judges). Laya (`convaiinnovations/laya`, Apache 2.0) replaces the rejected Jev provider path with a local-first option: same three primitives (choice/score/noul), ~33 ms single-forward-pass batching, $0 self-hosted. **DONE** via #434 + #435.
*Last updated: 2026-09-23 (FR-DSH-01 optional dsh-usage. Prior: 2026-09-21 (FR-TYPE-02 DONE via #434 + #435: abstraction + convo path + use-case doc + interactive diagram; tracker row closed. Prior: 2026-09-19 FR-P2.)*
