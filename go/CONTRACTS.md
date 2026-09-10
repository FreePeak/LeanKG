# Go Engine Package Contracts (W1 + #368 + #369)

Module: `github.com/FreePeak/LeanKG/go` — greenfield engine, strangler pattern
(next to the Rust line; docs/go-rewrite-analysis.md is the design source).
Every package implements EXACTLY this contract. Do not add features beyond it.

Shared facts (already implemented — read `internal/store` before coding):

- Store file: `<project>/.leankg/leankg.db` via `store.Open(path, store.RW|store.RO)`,
  `st.Migrate()` required in RW before use.
- Freshness is DB-resident: every write bumps `write_watermark`; readers compare
  `st.Watermark()` against `st.LoadInventory().LastInventorySeq`.
- Element content is stored on `code_elements.content` (bounded 8000 chars).
- Per-element content hash = SHA-256 hex of `Element.Content`.
- Version pins in `go.mod`: modernc.org/sqlite (storage), modelcontextprotocol/go-sdk v1.7.0 (MCP).
- Go 1.25. No new dependencies beyond these two without explicit approval.

## internal/index (indexer)

```go
type Result struct {
    Files, Elements, Relationships, Skipped int
}
func IndexDir(ctx context.Context, st *store.Store, dir string) (Result, error)
```

- Walk `dir`; SKIP path components: `.git`, `target`, `node_modules`, `vendor`,
  `.leankg`, `dist`, `build`, `.worktrees`, `.worktree`, and any dot-prefixed dir.
- Extensions: `.go`, `.rs`, `.ts`, `.tsx`, `.js`, `.jsx`, `.py`, `.md`.
- 3-signal change detection (FR-ZCP-11 port): size+mtime fast path against
  `st.Files()`; on mismatch SHA-256 the file and compare with
  `FileRecord.ContentHash`; identical → `Skipped++`, no writes.
- Changed/new file: `st.DeleteByFile(relPath)` then insert.
- Deleted file (in code_files, absent on disk): `st.DeleteByFile` + delete the
  code_files row (add a `DeleteFile(path string) error` store method in your
  package? NO — store is frozen: use `st.UpsertFiles` is not enough; instead
  call `st.DeleteByFile(relPath)` and add nothing; a stale code_files row for a
  deleted file is acceptable for W1 — record it in the package doc comment).

  UPDATE (owner decision): `store.DeleteFileRecord(path string) error` WILL be
  added by the integrator before merge; call it if present (check with grep).
- Extraction is regex-based (documented ceiling; tree-sitter port is W2):
  - go: `func`, methods, `type X struct|interface`
  - rust: `fn`, `struct`, `enum`, `trait`, `impl`
  - python: `def`, `class` (indentation-bounded bodies)
  - ts/js/jsx/tsx: `function`, `class`, `const x = (...) =>` / `= function`
  - md: ATX headings (`#`..`####`) as `element_type:"doc"`, content = section text
- Element shape: QualifiedName = `<rel/path>::<name>` (methods: `<rel/path>::<Type>.<name>`),
  Language from extension, LineStart/LineEnd real, ParentQualified = nearest
  enclosing element by line-range containment, Content = source lines bounded
  8000 chars, element_type ∈ {function, method, type, class, doc}.
- Relationships: `calls` (identifier in element content matches another
  element's Name; confidence 0.5; self-calls dropped), `contains`
  (parent→child; confidence 1.0).
- Write via `st.UpsertElements`, `st.UpsertRelationships`, `st.UpsertFiles`
  (relPath keys). Batch per file, not per element.
- Tests (table-driven): go/rust/py/ts fixture files with known elements; change
  detection (touch mtime with same content → skipped; edit → re-extracted);
  delete-by-file removes FTS rows.

## internal/memory (#369 — full-markdown memory)

```go
const CoreFileBytes = 2200 // Hermes bound for MEMORY.md and USER.md
type Memory struct{ /* root, fts db */ }
func Open(projectDir string, global bool) (*Memory, error)
func (m *Memory) Root() string
func (m *Memory) Snapshot() (string, error)          // session-start frozen core
func (m *Memory) View(path string, offsetLine int) (string, error)
func (m *Memory) Create(path, content string) error
func (m *Memory) StrReplace(path, old, new string) error
func (m *Memory) Insert(path, content string, insertLine int) error // 0 = prepend
func (m *Memory) Delete(path string) error
func (m *Memory) Rename(oldPath, newPath string) error
func (m *Memory) Add(file, text string) error        // Hermes §-append sugar
func (m *Memory) Replace(file, old, new string) error
func (m *Memory) Remove(file, substr string) error   // unique match else error
func (m *Memory) Search(query string, limit int) ([]Hit, error)
type Hit struct { Path, Snippet string; Score float64 }
```

- Root: `<project>/.leankg/memory` (global: `~/.leankg/memory`); create dirs +
  empty MEMORY.md/USER.md + `topics/` on Open.
- Path safety: reject absolute paths, `..` components, and any path resolving
  (via EvalSymlinks) outside Root — `ErrOutsideRoot`. Valid targets: `MEMORY.md`,
  `USER.md`, `topics/<name>.md`. Reject everything else: `ErrInvalidPath`.
- Bounds: writes to MEMORY.md/USER.md over CoreFileBytes bytes → structured
  `ErrOverflow{File, Used, Limit}` (error-not-truncate, Hermes semantics);
  `topics/*` unbounded.
- StrReplace/Replace/Remove: substring must occur exactly once, else
  `ErrAmbiguousMatch` (Hermes semantics). Remove keeps the rest of the line.
- Snapshot: usage header + full MEMORY.md + USER.md. Header first line:
  `<!-- leankg-memory usage: MEMORY.md 34% (748/2200) USER.md 12% (264/2200) -->`
  then the file contents (empty file → `# MEMORY.md` then nothing).
- FTS5 index `root/index.db` (own sqlite file, not the project store):
  re-index a file on every successful write (delete old rows for that path +
  insert lines/snippets); Search matches across all memory files, returns
  path + matching snippet + score.
- Mnemopi-compat JSONL adapter (same package, `banks.go`):
  - `BankName(cwd string) string` = sanitize(basename(cwd)) + "-" +
    base36(wyhash64(abs_cwd, seed 0)), ≤64 chars. Implement wyhash64
    (standard final variant, seed 0). Note in a comment: cross-runtime parity
    with the Rust `wyhash` crate is unverified (same caveat exists on the Rust
    side).
  - `Retain(bank string, entries []Entry, throughUserTurn int) error` — JSONL
    append under `root/banks/<bank>.jsonl`; metadata carries
    `retained_through_user_turn` INTEGER cursor; re-retain at/below cursor skips.
  - `Recall(bank, query string, limit int) ([]Entry, error)` — token-overlap
    scored; zero-match entries never surface.
- Tests: path traversal (incl. symlink escape), bounds overflow, ambiguous
  replace, snapshot format, FTS search, bank cursor resume.

## internal/embed (#368 — leankg-embed pipeline library)

```go
type TextKind int
const ( Document TextKind = iota; Query )
type Provider interface {
    ModelID() string; Revision() string; Dimensions() int
    Distance() string; Provider() string
    Embed(ctx context.Context, kind TextKind, texts []string) ([][]float32, error)
}
func OpenAICompatible(baseURL, apiKey, model string, dims int, revision string) Provider
func Deterministic(dims int) Provider
func FromEnv() (Provider, error)
type Report struct { Mode string; Dirty, Embedded, Skipped, Failed, Truncations, Orphans int; Coverage float64; Duration time.Duration }
func Run(ctx context.Context, st *store.Store, p Provider, mode string) (Report, error) // mode "incremental"|"full"
func ExportNDJSON(ctx context.Context, st *store.Store, modelID string, w io.Writer) error
func ImportNDJSON(ctx context.Context, st *store.Store, modelID, revision, distance string, dims int, r io.Reader) (Report, error)
```

- `OpenAICompatible` POSTs `{baseURL}/embeddings` with `{model, input: texts}`
  (OpenAI shape — covers API providers AND the llama.cpp `llama-server`
  sidecar, which speaks the same shape). API key header only when non-empty.
- `Deterministic` — hash-seeded unit vectors; ModelID `deterministic-<dims>`,
  Revision `test:deterministic-v1`, Provider `deterministic`. For tests and
  offline smoke ONLY; never claim real semantics.
- `FromEnv` — `LEANKG_EMBED_PROVIDER` ∈ local|openai|deterministic;
  `LEANKG_EMBED_BASE_URL`, `LEANKG_EMBED_API_KEY`, `LEANKG_EMBED_MODEL`,
  `LEANKG_EMBED_DIMS`. local+sidecar ⇒ same OpenAICompatible with the base URL
  (default `http://127.0.0.1:8080/v1`).
- Stamp guard (lives HERE per issue #368 — shared by every vector writer):
  `full` run compares `st.Stamp(p.ModelID())` with the provider's stamp;
  mismatch ⇒ `st.ClearVectors(modelID)` then rebuild (never mixed-model
  results). Write `st.WriteStamp` on first build.
- Plan: dirty QNs = elements whose current content SHA-256 differs from
  `st.EmbeddingStateMap(modelID)[qn]` (incremental) or all QNs (full).
- Invoke: batches of 32 texts; validate count, dimensions, finiteness
  (NaN/Inf ⇒ that batch failed, counted in Report.Failed, run continues).
- Write: `st.UpsertVectors` per batch (crash-consistent; rerun resumes),
  `st.SetEmbeddingStates`, then `st.StartEmbedRun`/`FinishEmbedRun` bookkeeping
  (start before plan, finish with counters + status ok|partial|failed).
- Truncations: content over 8000 chars counts one truncation (text truncated
  for the embedding call).
- Orphans: vectors whose QN no longer exists (compare vectors vs elements).
- Single-flight: caller's job (cmd binary holds an flock); the library is
  stateless beyond the store.
- ExportNDJSON lines: `{"qualified_name":..., "content_hash":..., "text":...}`.
  ImportNDJSON lines: `{"qualified_name":..., "vec":[floats]}` — dim-guard
  against `dims`, writes via store with the given stamp; resume = skip QNs
  whose stored content_hash already equals the export's.
- Tests: stamp mismatch triggers rebuild (vectors cleared, single stamp),
  incremental skips unchanged, batch validation failure counted not fatal,
  NDJSON export/import round-trip, provider failure surfaces as error from Run.

## internal/core + internal/mcp + internal/rest + cmd/* (integrator-owned)

Not part of your task. Do not create these directories' files.

## Ground rules for all tasks

- Work ONLY inside `~/work/harvey/freepeak/leankg/.worktrees/go-rewrite/go/internal/<yourpkg>/`.
- `go.mod`/`go.sum` are frozen — if you need a new module version, note it;
  do NOT edit go.mod (deps: modernc.org/sqlite is already required; your
  package may import it).
- Validate with `go build ./... && go test ./internal/<yourpkg>/ -count=1`
  from the `go/` dir ONLY (store tests may fail transiently while others edit
  other packages — filter for your package). Actually: run
  `go test ./internal/<yourpkg>/ ./internal/store/ -count=1`.
- No formatters/linters beyond gofmt on your own files.
