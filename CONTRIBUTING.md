# Contributing to LeanKG

> **v4.6.0:** the implementation is 100% Go (`go/`); Rust commands below are historical. Build: `make go-build` · Test: `make go-test` · See `AGENTS.md` for the current workflow.


First off, thank you for considering contributing to LeanKG! It’s people like you who make LeanKG a powerful tool for the AI-assisted development ecosystem.

As a project focused on **Lightweight Knowledge Graphs for AI**, we value contributions that improve indexing accuracy, reduce token overhead, and expand MCP capabilities.

## 🛠 Tech Stack
- **Language:** Go 1.25 (`go/`)
- **Database:** SQLite (WAL + FTS5, default) and PostgreSQL + pgvector (schema-per-project)
- **Parsers:** tiered — regex, ast-grep, tree-sitter (optional CGO build tag), LSP
- **Protocol:** Model Context Protocol (MCP), plus REST and ConnectRPC

---

## 🚀 How to Get Started

### 1. Setup Your Environment
Clone the repository and ensure you have [Go 1.25+](https://go.dev/dl/) installed:
```bash
git clone https://github.com/FreePeak/LeanKG.git
cd LeanKG/go
go build ./...
```

### 2. Local Development & Testing
The Go module lives under `go/`; CI runs the same gates:
- **Run tests:** `cd go && go test ./...` (add `-tags tstree` to exercise the CGO tree-sitter tier)
- **Vet:** `cd go && go vet ./...`
- **Local MCP Testing:** `go run ./cmd/leankg serve --stdio` from any project directory, and wire a client with `go run ./cmd/leankg install --target cursor` (claude-code | cursor | codex | gemini | opencode | omp).

### 3. Project Structure
- `/go`: The engine — `cmd/leankg` (server + CLI), `cmd/leankg-embed` (embedding pipeline), `internal/*` (store, core, index, mcp, rest, rpc, graph, langs, embed, memory, session, web), `proto/` (ConnectRPC contract).
- `/ui-v2`: Dashboard SPA (served embedded by the engine).
- `/examples`: Sample codebases used for benchmarking.
- `/instructions`: Agent-specific instructions (`CLAUDE.md`, `AGENTS.md`).

---

## 📈 Contribution Areas

### Adding Language Support
LeanKG uses `tree-sitter` for parsing. If you want to add a new language:
1. Register the language in `go/internal/langs` (extensions, repo markers, extraction tier).
2. Implement the extractor in `go/internal/index` (regex baseline; tree-sitter grammar under the `tstree` tag when a bundled one exists).
3. Add testdata fixtures and define how code elements (functions, classes, imports) map to the graph schema.

### Improving MCP Tools
The agent-facing registry is pinned at exactly three tools — `import`, `query`, `status` — with capabilities as actions/verbs inside that envelope. To extend it:
1. Add the action in `go/internal/core` and wire it through the MCP/REST/CLI transports.
2. Ensure the output is **token-optimized** (we aim for high signal-to-noise ratios).

### Benchmarking
Performance is a core feature. If you contribute a feature, please run the Go benchmarks in [`go/benchmark/ab`](go/benchmark/ab) (see its REPORT.md) to ensure no significant regression in indexing speed or token usage.

---

## 📋 Pull Request Process

1. **Check Issues:** Look for existing issues or create a new one to discuss your idea.
2. **Branching:** Create a feature branch (`feat/your-feature` or `fix/your-fix`).
3. **Commit Messages:** We follow [Conventional Commits](https://www.conventionalcommits.org/) (e.g., `feat: add support for Ruby`, `fix: handle circular dependencies`).
4. **Documentation:** If you add a new CLI command or MCP tool, update the `README.md` and the relevant agent instruction files in `/instructions`.
5. **Review:** Once submitted, a maintainer will review your code. We prioritize performance, code safety (it is Rust, after all!), and documentation.

---

## 🤖 AI-Assisted Contributions
Since LeanKG is built for AI agents:
- Feel free to use LeanKG itself while developing!
- If you find that an AI agent (like Claude or Cursor) struggles to understand a part of this repo, please submit a PR to improve our `CLAUDE.md` or `AGENTS.md` instructions.

## ⚖️ License
By contributing, you agree that your contributions will be licensed under the **MIT License**.

---

### Tips for success:
* **Keep it Lean:** Every byte of data sent via MCP costs tokens. Always look for ways to compress the graph context.
* **Stay Local-First:** We avoid cloud dependencies. Any new feature should work entirely on the user's local machine.
