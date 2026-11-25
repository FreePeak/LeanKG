# Language support

LeanKG registers **40 language profiles**. Every profile gets the regex
extractor. tree-sitter, ast-grep and language-server tiers apply where they
are available.

The registry is [`internal/langs/langs.go`](../internal/langs/langs.go). A
profile declares extensions, repo markers (files or directory suffixes that
declare the language at a repository root), optional header extensions and an
LSP command list with root markers.

## Profiles

Go, Rust, TypeScript, TSX, JavaScript, JSX, Python, Markdown, Java, Kotlin,
Swift, Objective-C, Dart (Flutter), C, C++, C#, PHP, Ruby, Scala, Perl, Lua,
Haskell, Elixir, Crystal, CUDA, Cypher, Elm, Erlang, F#, GLSL, HLSL, Nim,
OCaml, SQL (PostgreSQL, T-SQL and PL/SQL dialects), PowerShell, Q#, Solidity,
SystemVerilog, Verilog, Zig.

## Extraction tiers

| Tier | Availability | What it adds |
| --- | --- | --- |
| Regex | Always | Symbols, imports, calls and references for every profile |
| tree-sitter | Build tag `tstree` (CGO) | Real parse for grammars that need it |
| ast-grep | `ast-grep` CLI on `PATH` | Pattern-structural extraction |
| Language server | Configured LSP command on `PATH` | Query-time symbol resolution, started lazily and stopped after an idle TTL |

Build and test the tree-sitter tier with:

```bash
make go-build-tstree
make go-test-tstree
```

## Activation

A profile is active for a repository when a repo marker matches or when a file
with one of its extensions is walked. Extensions that are never activated do
not route files, so an over-broad profile is benign. The `.h` extension is
owned by C, C++ and Objective-C in a fixed order, because header files are
ambiguous.

## Limits

- Language servers are an optional query-time tier, not an indexing
  requirement. A missing server degrades to the lower tiers.
- Call edges are resolved by name within a directory. There is no import or
  type resolution, so cross-package dispatch is best-effort
  ([`internal/index/relations.go`](../internal/index/relations.go)).
- The registry describes extraction and activation, not complete semantic
  understanding of every language.
