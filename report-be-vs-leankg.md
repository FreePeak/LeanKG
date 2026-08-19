# be-knowledge-graph vs LeanKG `feat/multi-model-embed-db` — full diff report

**Date:** 2026-08-11
**Compared:**
- `be-knowledge-graph` @ `e58a2fbb` (branch `release`, GitLab `git.begroup.team:platform-saas/be-knowledge-graph.git`)
- LeanKG @ `32cba000` (branch `feat/multi-model-embed-db`, GitHub `FreePeak/LeanKG`)

**Method:** Fetched be's `release` into the LeanKG object store and diffed the two HEAD trees (`git diff <mine-tree> <be-tree>`). The repos share no common ancestor, so this is a pure tree-to-tree diff: **223 files, ~105,582 insertions, ~6,496 deletions**. Direction: changes below are **be's tree relative to your branch**.

---

## 1. Bottom line

**Be is a fork that evolved independently for a very different deployment** — a single-org (VEEP/FOOD) GitLab + k8s deployment over ~19 food services, rather than the open-source multi-model embed engine you've been building. It carries a large body of be-specific work you don't have (Confluence, GitLab CI, baked PAT config, `LEANKG_PATH_REWRITE`, a pgcat-safe bulk write path) **and deliberately reverted/simplified a chunk of your recent work** (auth, gemini, `--no-vectors`, per-model HNSW DDL, project-schema scoping).

Net: **it is not your branch + extra commits.** It has diverged so far that merging would be a rewrite, not a rebase.

---

## 2. Removed from be (code you have that be dropped)

### 2.1 Auth — entirely stripped (`004_auth`, `src/auth/*`, `src/api/auth_handlers.rs`)
- Deleted `src/auth/{accounts,tokens,mod}.rs` (~821 lines) and `src/api/auth_handlers.rs` (322 lines).
- Deleted migration `src/db/pg/migrations/004_auth.sql`.
- Removed `AuthCommand` CLI subcommands (`register`, `token`, `list-tokens`, `revoke`) and the `/api/v1/auth/*` routes.
- MCP auth falls back to `AuthManager::with_default_token()` — the leankg.yaml `auth:` block wiring is gone.
- PG translator dropped auth-table PKs (`accounts`, `orgs`, `access_tokens`, `org_memberships`, `team_members` composite keys) and the JSONB `scopes` column.
- `is_read_only()` on `DbBackend` removed.

### 2.2 Gemini — removed (`003_gemini_embed`, provider entries, live test)
- Deleted migration `src/db/pg/migrations/003_gemini_embed.sql`.
- Removed `gemini-embedding-2-3072` / `gemini-embedding-001-3072` entries from `registry.rs`, plus the `gemini_entries_resolve_with_3072_and_table_names` test.
- Deleted `tests/gemini_live_test.rs`.

### 2.3 `LEANKG_EMBED_WRITE_VECTORS` / `embed --no-vectors` — removed
- `BuildOptions.write_vectors`, `write_vectors_enabled()`, CLI `--no-vectors`, and all inference-only branches removed. Be **always writes vectors**.

### 2.4 Per-model `:put` / HNSW table handling — reverted to single-table
This is the biggest loss of your multi-model work in the PG translation layer:
- `translate.rs`: reverted the `explicit_table` resolution in `put_from_literal` / `put_from_batch`, so `:put` falls back to column-signature inference → per-model tables (`embedding_vectors_<model_id>`) again resolve to legacy `embedding_vectors`. Dropped `pk_for_table` entries for `embedding_state_*` / `embedding_vectors_*` and composite PK quoting.
- `backend.rs`: reverted vector/state-table prefix detection back to exact `embedding_vectors` / `embedding_state`.
- `translate.rs` HNSW DDL: reverted the per-model table extraction in `::hnsw create <table>:vec_idx`, and dropped the fallback for the legacy `embedding_vectors` name.
- Migration `002`: dropped the Qwen 2560-d HNSW index (with a note that pgvector 0.8.x caps at 2000d and it's never queried locally — remote provider), and changed `usearch_key` to `BIGINT NOT NULL` (no `DEFAULT 0`).

### 2.5 Project-schema scoping — removed
- `PostgresBackend.schema`, `with_schema()`, and the `inject_search_path` search_path wiring are gone. Backend is back to single shared `public` schema.
- `main.rs`: `leankg index <path>` no longer prefers the positional arg; always `find_project_root()`. The advisory lock changed from a fixed key to per-`(env, path)`.

---

## 3. Added in be (be-specific work you don't have)

### 3.1 Confluence integration — new `src/confluence/*` module (~1,826 lines)
- `client.rs` (828), `config.rs` (261), `convert.rs` (179), `write.rs` (306), `mod.rs` (252).
- Fetches Confluence spaces (default VEEPFOOD) into `{CLONE_ROOT}/confluence/veepfood/`, converts to markdown, feeds the doc indexer.
- New CLI: `leankg setup --confluence`; `run_setup` gains a `do_confluence` arg; auto-armed in `LEANKG_SETUP=1` when `JIRA_API_TOKEN` set / `LEANKG_CONFLUENCE!=0`.
- New test `tests/confluence_live_atlassian_test.rs` (173).
- Docs: `docs/confluence-setup.md`.

### 3.2 GitLab + k8s deployment (replaces GitHub Actions / Render)
- `.github/actions/semver-release/*` + `semantic-release.yml` deleted → replaced with Release Please (`.github/workflows/release-please.yml`, `release-please-config.json`, `manifest.json`).
- New `.gitlab-ci.yml`, GitLab `checkDockerfilePolicy` whitelist (only `asia.gcr.io/docker-veep/` base images allowed).
- Dockerfile rewritten: Alpine 3.21 musl base + rustup (rustc 1.95.0), musl-native system onnxruntime via pkg-config (no `-crt-static`), Kaniko layer caching, lang-extras trimmed out.
- `entrypoint.sh` deleted (logic folded into setup pipeline); `Dockerfile.embed-worker` deleted (embedding in-process).
- `docker-compose.embed.yml` / `docker-compose.yml` reworked; new override files.

### 3.3 Be-specific runtime/ops code
- **`LEANKG_PATH_REWRITE=FROM=TO`** (`indexer/mod.rs`) — remaps stored paths so Mac-host indexes read as in-container `/app` paths.
- **Baked PAT config** — `_LKG_GIT_PAT_A/B/C` env parts reassembled in `setup/mod.rs` (`scripts/with-baked-defaults.sh`).
- **Food-services repo table** — `FOOD_REPOS` const in `setup/mod.rs` + `scripts/food-repo-map.txt`, `food-services.txt`; GitLab clone root defaults to `/app`; default ref `release`.
- **`LEANKG_WORKSPACE_DIR`** — `discover_git_repos()` walks a monorepo for nested `.git` dirs and indexes each as its own project.
- **`upsert_values`** — multi-row VALUES upsert as the default bulk path, replacing COPY for keyed tables (COPY deadlocks through the pgcat pooler; comment documents a ~9-min block + poisoned pool). `use_copy_path` keeps COPY only for big vector payloads.
- **`LEANKG_HNSW_EF_CONST` no longer emitted as a GUC** — `embedding_gucs_for` now returns empty (it's a DDL-time param, not a runtime GUC; emitting it aborts the write tx on pgvector 0.8.x).
- **Always-live HNSW** — `should_use_incremental_hnsw_puts` no longer drops/rebuilds by dirty-set threshold; always live `:put` unless `LEANKG_EMBED_COPY=1` (CREATE INDEX needs table ownership, which BE app roles lack).
- **SQL logging** — `log_pg_run_script` / `log_pg_import` tracing on `leankg::pg_sql` target + `format_named_params_for_log`.
- **`resolve_leankg_bin()`** — setup subprocess invokes the binary next to the current executable (distinct `leankg-internal` vs the freepeak-opensource `leankg` sharing the sccache dir) instead of a PATH `leankg`.
- **`malloc_trim` gated on `target_env = "gnu"`** — musl (Alpine) has no malloc_trim.
- Doc indexer: `rewrite_path_for_storage` applied to stored `file_path`.
- `main.rs` `download_file`: removed non-2xx / empty-body checks (be binary isn't distributed as a GitHub release asset).

### 3.4 Registry retained
The multi-model **registry itself survives** (`registry.rs`, `embedding_vectors_<model_id>` table-per-model, active-model collection switch, `LEANKG_EMBED_ACTIVE_MODEL`). What got reverted is the PG *translator's* per-model table resolution, not the registry concept.

---

## 4. Test files

- **Deleted (yours):** `tests/multi_model_embed_tests.rs` (499), `tests/multi_model_smoke_live.rs` (280), `tests/gemini_live_test.rs` (89), plus `.DS_Store` files.
- **Added:** `tests/confluence_live_atlassian_test.rs` (173), `.gitlab-ci.yml`.
- **Modified:** `tests/embeddings_state_e2e.rs`, `embedding_state_unit_tests.rs` (rewritten around the new always-live HNSW + test_env lock), `pg_*` tests (schema/translate/regression), `readonly_mode_test.rs`.

---

## 5. Docs / config

- New: `docs/2026-08-11-workspace-indexing-session.md`, `docs/reports/mcp-be-knowledge-graph-gateway-validation-2026-08-11.md`, `docs/validation/2026-08-10-be-knowledge-graph-mcp-gateway-validation.md`, `graph-*.html`, `leankg.yaml.md`, `PR_DESCRIPTION.md`.
- Deleted: `docs/embed-multi-model.md`, `docs/planning/2026-08-07-mcp-query-worker-split.md`, `docs/analysis/root-cause-leankg-mcp-not-working-be-monorepo-2026-08-05.md`, `entrypoint.sh`, `.dockerfile.example`.
- Changed: `CLAUDE.md`, `AGENTS.md`, `docs/prd.md`, `docs/mcp-tools.md`, `leankg.yaml`, `CHANGELOG.md`, `Cargo.lock`/`Cargo.toml`, plus lots of scratch artifacts (`.playwright-mcp/*.yml`, `.mcp.json.test`) committed to be.

---

## 6. Cargo / deps
`Cargo.toml` + `Cargo.lock` diverge. Be removes your gemini/auth-related deps and adds Confluence (reqwest-based HTTP + Atlassian auth) and whatever the GitLab/k8s image build pulls in. `Cargo.lock` shows a large reshuffle.

---

## 7. If you want to unify — options

Your branch and be are effectively two products now. Decide what the *upstream* LeanKG is:

1. **Treat be as a downstream fork, keep upstream open-source.** Cherry-pick only the clean, portable wins into your branch (ordered by value):
   - `upsert_values` pgcat-safe bulk path
   - `embedding_gucs_for` fix (ef_construction is not a GUC)
   - always-live HNSW + `LEANKG_EMBED_COPY` opt-in (but note: this *conflicts* with your dirty-set threshold logic)
   - `LEANKG_PATH_REWRITE`
   - `malloc_trim` gnu-gate
   - SQL logging
   - `resolve_leankg_bin` reexec pattern
2. **Fold upstream into be's line** only if you own be too. Then you'd be re-adding auth/gemini/per-model-table work on top of be's PG changes — both are on the `feat/multi-model-embed-db` theme, so the multi-model *registry* is portable, but the translator revert makes it a conflict, not a merge.
3. **Do nothing.** They're separate codebases; keep them that way and share only lessons/docs.

The single highest-value line item to reconcile is **2.4 (per-model `:put`/HNSW table resolution)**: be's translate/backend changes silently undo your multi-model PG write path. If be must keep multi-model, that revert has to be un-reverted. If be is deliberately single-model, then `feat/multi-model-embed-db` and be are incompatible by design.

---

*Generated with Claude Code from a `git diff` between the two HEAD trees (no common ancestor).*
