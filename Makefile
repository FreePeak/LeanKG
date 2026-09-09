# LeanKG Makefile



# Default target
help:
	@echo "LeanKG Makefile"
	@echo ""
	@echo "Targets:"
	@echo "  build           Build release binaries (leankg, leankg-mcp, leankg-worker)"
	@echo "  test            Run tests"
	@echo "  lint            Run linter"
	@echo "  run             Run compat leankg (stdio mode)"
	@echo "  clean           Clean build artifacts"
	@echo "  kill            Kill all leankg MCP processes"
	@echo ""
	@echo "Split binaries:"
	@echo "  leankg-mcp      Query-only MCP HTTP (:9699, read-only)"
	@echo "  leankg-worker   Pipeline: WORKER_CMD=index|embed|watch|status (default: status)"
	@echo ""
	@echo ""
	@echo "MCP Server targets (HTTP mode; prefer leankg-mcp for RO query-only):"
	@echo "  mcp-http        Start query-only MCP HTTP on port 9699"
	@echo "  mcp-http-auth   Start MCP HTTP server with auth"
	@echo "  mcp-http-watch  Start MCP HTTP with file watcher (compat; discouraged for RO)"
	@echo ""
	@echo "MCP Server targets (Stdio mode):"
	@echo "  mcp-stdio       Start query-only MCP stdio server"
	@echo "  mcp-stdio-watch Start MCP stdio with file watcher (compat; discouraged for RO)"

# Build release binaries (leankg + leankg-mcp + leankg-worker)
build:
	cargo build --release --bins

# Run tests
test:
	cargo test

# Run linter
lint:
	cargo clippy --all-targets --all-features -- -D warnings

# Run LeanKG compat binary (stdio mode for local dev)
run:
	cargo run --release --bin leankg

# Clean build artifacts
clean:
	cargo clean

# Kill all leankg MCP processes (HTTP and stdio)
kill:
	pkill -9 -f "leankg.*mcp" 2>/dev/null || true
	pkill -9 -f "leankg-mcp" 2>/dev/null || true
	@echo "All leankg MCP processes killed"

# === Split binaries ===

# Query-only MCP HTTP (read-only; no auto-index / bulk embed)
leankg-mcp:
	cargo run --release --bin leankg-mcp -- mcp-http --port 9699

# Pipeline worker. Examples:
#   make leankg-worker WORKER_CMD="index $(PWD)"
#   make leankg-worker WORKER_CMD="embed --wait --project $(PWD)"
#   make leankg-worker WORKER_CMD=status
WORKER_CMD ?= status
leankg-worker:
	cargo run --release --bin leankg-worker -- $(WORKER_CMD)

# === MCP Stdio Mode (query-only via leankg-mcp) ===

mcp-stdio:
	cargo run --release --bin leankg-mcp -- mcp-stdio

mcp-stdio-watch:
	cargo run --release --bin leankg -- mcp-stdio --watch

# === MCP HTTP Mode (query-only via leankg-mcp) ===

mcp-http:
	cargo run --release --bin leankg-mcp -- mcp-http

mcp-http-auth:
	cargo run --release --bin leankg-mcp -- mcp-http --auth "$(shell uuidgen 2>/dev/null || echo 'secret-token')"

mcp-http-watch:
	cargo run --release --bin leankg -- mcp-http --watch

# Start on custom port
mcp-http-port:
	@read -p "Enter port: " port; \
	cargo run --release --bin leankg-mcp -- mcp-http --port $$port

# === Development ===

dev:
	RUST_LOG=debug cargo run --release --bin leankg-mcp -- mcp-stdio

# === Installation ===

install: build
	sudo cp target/release/leankg target/release/leankg-mcp target/release/leankg-worker /usr/local/bin/

# === macOS LaunchAgent (auto-start on login) ===

mcp-http-launchd:
	./scripts/install-leankg-mcp-launchd.sh

mcp-http-launchd-unload:
	launchctl unload ~/Library/LaunchAgents/com.leankg.mcp-http.plist 2>/dev/null || true
	rm ~/Library/LaunchAgents/com.leankg.mcp-http.plist 2>/dev/null || true
	echo "LaunchAgent removed"

# === Auto-restart on rebuild ===

# Watch for binary changes and restart LaunchAgent service
# Run this in a separate terminal while developing
watch-build:
	./scripts/watch-leankg-build.sh

# Build and auto-reload (single command)
dev-watch: build
	./scripts/watch-and-reload.sh

# Kill and rebuild on next make
rebuild-mcp-http:
	launchctl stop com.leankg.mcp-http 2>/dev/null || true
	cargo build --release
	launchctl start com.leankg.mcp-http 2>/dev/null || true
