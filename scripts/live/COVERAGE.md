# Live-harness coverage matrix: issues #586 to #592 and the index bugs

Maps each fix to the scenario and assertions that check it. Sources: the scenario
headers and assertion labels under `scripts/live/scenarios/`, and the wave2-final
run (`results.tsv`, 347 PASS, 0 FAIL, 3 SKIP) compared with the baseline run
(`baseline-final2`, 88 FAIL, all 88 on `[fixed]`/regression assertions that pass on
wave2). Scenarios 08 to 12 carry no `[fixed]`/`[guard]` tag in their assertion names:
the label column says so, and the baseline column shows whether the assertion
fails on the pre-fix binary.

Label meanings: `[fixed]` must FAIL on the baseline and PASS on the fix; `[guard]`
must give the same result on both; `[unit-gate]` runs a Go test and asserts its
PASS line. "LIVE" means the assertion drives the real binary (CLI, HTTP, MCP or
signal). "UNIT-ONLY" means no live assertion reaches the fix; the named unit test
is the evidence.

## Matrix

| Issue / item | Scenario | Assertion names (representative) | Label | Baseline (fail count in scenario) | Evidence |
|---|---|---|---|---|---|
| #586 login burst and token list | 08 | "login burst: exactly 5 generic invalid-credentials responses", "login burst: exactly 15 rate-limited responses", "viewer bearer sees an empty token list", "viewer bearer gets 403 on PUT bank" | untagged | 4 of 8 fail | LIVE |
| #586 duplicate registration (generic error, no email echo) | 15 (part 1) | "[fixed] duplicate registration error is the generic text", "[fixed] duplicate registration does not echo the email", "[fixed] duplicate registration in upper case gets the same generic text"; "[guard] short-password refusal keeps its specific text" | [fixed] / [guard] | 3 fail | LIVE |
| #587 embed run ledger (SIGINT, cancelled row) | 10 | "SIGINT during an embed run exits 130", "newest embed_runs row after a mid-embed SIGINT is cancelled", "a run stopped before its ledger row still leaves a cancelled row" | untagged | 1 fails | LIVE |
| #587 dense recall deadline | 17 | "[fixed] dense recall with a hung embedding answers within 25 s", "[fixed] the recall answer carries the keyword row", "[fixed] all four concurrent recalls embed their query (no caller skips the dense arm)" | [fixed] | 4 fail | LIVE |
| #587 stop coinciding with a store error records cancelled | 17 | "[unit-gate] stop coinciding with a store error records cancelled" (TestRunStopCoincidingWithStoreErrorRecordsCancelled) | [unit-gate] | n/a | UNIT-ONLY |
| #587 auto-embed starts the provider once | 17 | "[unit-gate] auto-embed starts the provider once" (TestAutoEmbedStartsProviderOnce) | [unit-gate] | n/a | UNIT-ONLY |
| #588 FTS rowid alignment after update | 16 (a) | "[fixed] index rowids match entry rowids after an update" | [fixed] | 6 fail in scenario 16 (wave2 pass) | LIVE |
| #588 legacy schema-19 store migrates to 20 | 16 (b) | "[fixed] the binary under test opens the legacy store with exit 0 and migrates it to schema 20", "[fixed] legacy index rowids match entry rowids after the upgrade" | [fixed] | (same) | LIVE (needs LIVE_BASELINE_BIN) |
| #588 rowid alignment survives vacuum --full | 16 (d) | "[fixed] index rowids still match entry rowids after leankg vacuum --full" | [fixed] | (same) | LIVE |
| #588 partial-word keyword recall, SQLite | 16 | "[unit-gate] #588 item 1 partial-word keyword recall, SQLite" (TestKnowledgeKeywordSearchSQLite) | [unit-gate] | n/a | UNIT-ONLY |
| #588 pre-migration-19 degraded ranking | 16 | "[unit-gate] #588 item 3 pre-19 read-only store reports degraded ranking" (TestKnowledgeSearchPreMigration19ReportsDegradedRanking) | [unit-gate] | n/a | UNIT-ONLY |
| #588 pgvector iterative-scan version gate | 16 | "[unit-gate] #588 item 4 iterative-scan version gate (pure)" (TestPGVectorHasIterativeScan) | [unit-gate] | n/a | UNIT-ONLY |
| #588 negative K in SQLite SearchVectors | 16 | "[unit-gate] #588 negative K in SQLite SearchVectors does not panic" (TestSearchVectorsNegativeKReturnsEmpty) | [unit-gate] | n/a | UNIT-ONLY |
| #588 PostgreSQL parity (partial-word, iterative-scan follows installed vector, cancelled migration rollback) | 16 | "[unit-gate] #588 item 1 partial-word keyword recall, PostgreSQL parity" (TestPGKnowledgeKeywordSearch), "#588 item 4 iterative_scan follows the installed pgvector" (TestPGIterativeScanFollowsInstalledVector), "#588 item 5 cancelled migration leaves no open transaction" (TestPGMigrateRollsBackWhenCtxCancelled) | [unit-gate] | n/a | UNIT-ONLY (PG-gated, LIVE_PG_URL) |
| #592 read-only store without emb_stamp is healthy | 16 (c) | "[fixed] status of a never-embedded store is healthy", "[fixed] status reports no embeddings_error for an absent emb_stamp" | [fixed] | 2 fail in scenario 16 on baseline | LIVE |
| #592 absent emb_stamp reads empty on PostgreSQL | 16 | "[unit-gate] #592 absent emb_stamp reads empty on PostgreSQL" (TestPGStoreOpensWithoutEmbStamp) | [unit-gate] | n/a | UNIT-ONLY (PG-gated) |
| #592 CLI single usage print | 11 | "delete-annotation with a bad flag prints its usage exactly once", "delete-annotation -h prints its usage exactly once" | untagged | 2 fail | LIVE |
| #592 leankg-embed reads db.url from leankg.yaml | 11 | "leankg-embed does not demand LEANKG_PG_URL when leankg.yaml has db.url", "leankg-embed reports a connection failure for the yaml URL" | untagged | 2 fail | LIVE |
| #592 docs conflict-marker hygiene | 15 (part 2) | "[guard] no conflict markers at line start under docs/" (repo check); "[unit-gate] TestDocsHaveNoConflictMarkers PASS line" | [guard] / [unit-gate] | n/a | LIVE (repo check) plus UNIT-ONLY (archtest) |
| #592 Off-construction telemetry writer | 15 (part 3) | "[unit-gate] telemetry Off-construction (no CLI route): TestRecorderRaisedFromOffCloses exits 0", "...reports PASS" | [unit-gate] | n/a | UNIT-ONLY |
| FR-ZCP-01 roots/list decision: fail-closed routing | 12 | "projectless tools/call is refused with LEANKG_ERROR_MISSING_PARAM" (per tool), "?project=nope answers HTTP 404", "stdio status without project succeeds (not an error)" | untagged (header: regression) | 0 fail (passes on baseline) | LIVE |
| #589 `leankg run` and `update --check` on SIGINT/SIGTERM | 14 | "[cancel INT] leankg run exits 130 on INT (cancelled, not a plain failure)", "[cancel TERM] leankg update --check exits 130 on TERM", "[cancel INT] sleep child is gone within 5 s of INT" | [cancel SIG] (no [fixed] tag) | 10 fail | LIVE |
| #589 grandchild holds stdout; serve with locked ledger | 20 | "[fixed] leankg run exits 130 on SIGINT while a grandchild holds stdout", "[fixed] leankg run exits within 5 s of SIGINT while a grandchild holds stdout", "[fixed] leankg serve exits within 5 s of SIGINT with a locked telemetry ledger", "[fixed] leankg serve names the abandoned ledger flush on stderr" | [fixed] | 8 fail | LIVE |
| #589 summarize, maintain.Pass, gitobj, index walk, doctor fleet, leankg-embed %w | 20 | "[unit-gate] summarize keeps the cached entry of an unreached batch after a cancel", "maintain.Pass returns the cancel when the checkpoint is cut", "gitobj cat-file cancel reports context.Canceled", "index SupportedFiles stops on a cancelled ctx", "doctor fleet deadline is not reported as an unreadable registry", "leankg-embed verbs keep context.Canceled through the %w wrapping" | [unit-gate] | n/a | UNIT-ONLY |
| #590 malformed JSON and token_budget 0 | 13 | "[fixed] REST malformed /api/v1/query body carries code LEANKG_ERROR_INVALID_PARAM", "[fixed] UI malformed /api/query-graph body carries code LEANKG_ERROR_INVALID_PARAM", "[fixed] query-graph token_budget 0 answers 400", "[fixed] import repo of a subdirectory answers 409" | [fixed] | 6 fail | LIVE |
| #590 body limit, store failure, read budget, consent 400s | 19 | "[fixed] REST query body over 1 MiB answers 413", "[fixed] REST status on the store with no code_elements answers 500", "[fixed] UI index status carries code LEANKG_ERROR_INDEX_UNAVAILABLE", "[fixed] import read answer reports _token_budget.max 8000", "[fixed] consent ... carries code LEANKG_ERROR_INVALID_PARAM" | [fixed] | 18 fail | LIVE |
| #590 Postgres connect failure maps to PG_UNREACHABLE | 19 | "[unit-gate] Postgres connect failure maps to PG_UNREACHABLE" (TestFailMapsAnUnreachablePostgresToPGUnreachable) | [unit-gate] | n/a | UNIT-ONLY (serve refuses to start) |
| #591 memory redaction on delete and retain | 09 | "DELETE reports memory_units_deleted 1 for the raw document id", "two retains of one document id leave one row", "raw lkg_ token hex is absent from the readback", "stored token row carries the [REDACTED:key] marker" | untagged | 6 fail | LIVE |
| #591 pre-redaction rows compacted; markdown ownership; tool contract; core-file bound | 18 (A-D) | "[fixed] pair bank holds no raw secret after compaction", "[fixed] solo row carries the [REDACTED:key] marker", "[fixed] docs after index, then index: guide.md holds no docindex rows", "[fixed] import tool contract names the docindex split for action=docs", "[fixed] add whose redacted text passes the 2200-byte bound is refused" | [fixed] | 11 fail | LIVE |
| #591 import status_error, sessionlink bound, specialist rollback, stale vector drop | 18 (E) | "[unit-gate] import repo names a Status failure in status_error" (TestImportIndexNamesStatusFailure), "sessionlink keeps at most 8 transcripts" (TestParsedBoundsKeptTranscripts), "specialist files roll back on a cancelled directory" (TestCancelRollsBackSpecialistFiles), "stale vector of a redacted row" (TestCompactDropsVectorOfRedactedRow) | [unit-gate] | n/a | UNIT-ONLY |
| Index bug A: false call edge (trailing comment inside a span) | 07, 21 | 07: "Alpha does not call AlphaTwo (a.go:4-7 never names it)"; 21: "[fixed] after the upgrade Alpha has no edge to AlphaTwo" | untagged (07), [fixed] (21) | 07: fails; 21: fails | LIVE |
| Index bug B: wrong element span (line_end 9) | 07, 21 | 07: "Alpha line_end is its closing brace (a.go:7)"; 21: "[fixed] after the upgrade Alpha line_end is its closing brace (a.go:7)", "[fixed] the second run keeps Alpha's corrected span (line_end 7)" | untagged (07), [fixed] (21) | 07: fails; 21: fails | LIVE |
| Index bug B: extractor-version bump re-extracts a pre-fix store once | 21 | "[fixed] the version bump re-extracts all 3 fixture files once", "[guard] second incremental run re-extracts no file (files=0)" | [fixed] / [guard] | 4 fail | LIVE (needs LIVE_SEED_BIN) |
| Index bug C: unstable L1 resolution (SQLite) | 07 | "incremental store: plain AlphaTwo resolves to a.go::AlphaTwo", "fresh store: plain AlphaTwo resolves to a.go::AlphaTwo" | untagged | 1 fails (incremental) | LIVE |
| Index bug C: exact-case first, then path ascending (SQLite) | store | TestFindExactRanksDeterministically, TestFindExactTieBreaksByPath (`internal/store/findexact_order_test.go`) | (go test) | n/a | UNIT-ONLY |
| Index bug C: exact-case first, then path ascending (PostgreSQL) | store | TestPGFindExactRanksDeterministically (`internal/store/store_pg_test.go`) | (go test) | n/a | UNIT-ONLY (PG-gated) |

## Known unit-only items

- **Archtest static checks (#592, Off writer, docs markers).** Static analysis; no
  runtime path. The Off-construction writer bug has no CLI, HTTP or SIGHUP route.
- **Store unit gates (#588, SQLite).** The header of scenario 16 states that no
  CLI, HTTP or MCP path reaches these fixes. Partial-word recall, pre-migration-19
  ranking, the iterative-scan version gate (a pure function) and negative K in
  SearchVectors are checked only by their unit tests.
- **PostgreSQL gates (#588 items 1, 4, 5; #592 emb_stamp; index bug C on PG).** The
  harness cannot reach PostgreSQL unless `LIVE_PG_URL` is set. Without it they SKIP.
  They passed in this session against a private pgvector container (scenario 16 run
  with `LIVE_PG_URL`: 35 PASS, 0 FAIL, 0 SKIP).
- **Embed stop and auto-embed (#587).** A store error cannot be placed inside a
  stop deterministically from a CLI or HTTP path. The sidecar adopts a running
  endpoint, so a second provider start is not visible as a second process.
- **Cancellation internals (#589).** Summarize, maintain.Pass checkpoint, gitobj
  cat-file, index walk, doctor fleet deadline and the `%w` wrapping on the embed
  verbs each have no deterministic CLI path (scenario 20 header).
- **Postgres connect failure (#590).** `serve` refuses to start on an unreachable
  Postgres, so the `PG_UNREACHABLE` mapping is reachable only through its unit test.
- **Memory internals (#591).** Session-link transcript bound (memory is not
  observable), specialist rollback (needs a cancel at a precise point), stale vector
  drop (vectors need an embedding provider), import `status_error` (no input isolates it).
