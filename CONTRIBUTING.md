# Contributing to LeanKG

First off, thank you for considering contributing to LeanKG! It’s people like you who make LeanKG a powerful tool for the AI-assisted development ecosystem.

As a project focused on **Lightweight Knowledge Graphs for AI**, we value contributions that improve indexing accuracy, reduce token overhead, and expand MCP capabilities.

## 🛠 Tech Stack
- **Language:** Rust (Latest Stable)
- **Database:** CozoDB (Graph engine)
- **Parsers:** tree-sitter (for Go, Rust, TS, Python, etc.)
- **Protocol:** Model Context Protocol (MCP)

---

## 🚀 How to Get Started

### 1. Setup Your Environment
Clone the repository and ensure you have the Rust toolchain installed:
```bash
git clone https://github.com/FreePeak/LeanKG.git
cd LeanKG
cargo build
```

### 2. Local Development & Testing
We use a `Makefile` to simplify common development tasks:
- **Run tests:** `cargo test`
- **Build release:** `cargo build --release`
- **Local MCP Testing:** Use the `mcp-stdio` command to test changes with your local AI tools (Cursor, Claude Code, etc.).

### 3. Project Structure
- `/src`: Core logic, graph schema, and indexing engine.
- `/npm-package`: Wrappers for distribution.
- `/examples`: Sample codebases used for benchmarking.
- `/instructions`: Agent-specific instructions (`CLAUDE.md`, `AGENTS.md`).

---

## 📈 Contribution Areas

### Adding Language Support
LeanKG uses `tree-sitter` for parsing. If you want to add a new language:
1. Add the corresponding tree-sitter dependency in `Cargo.toml`.
2. Implement the parser logic in `src/indexer/`.
3. Define how code elements (functions, classes, imports) map to the graph schema.

### Improving MCP Tools
We are constantly expanding the tools available to AI agents. If you have an idea for a new tool (e.g., `get_complexity_score` or `find_dead_code`):
1. Define the tool in the MCP server module.
2. Ensure the output is **token-optimized** (we aim for high signal-to-noise ratios).

### Benchmarking
Performance is a core feature. If you contribute a feature, please run the benchmarks in the `benchmark/` folder to ensure no significant regression in indexing speed or token usage.

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
