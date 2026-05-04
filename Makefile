# LeanKG Makefile

.PHONY: help build test lint run clean mcp-stdio mcp-http mcp-http-auth mcp-http-watch kill

# Default target
help:
	@echo "LeanKG Makefile"
	@echo ""
	@echo "Targets:"
	@echo "  build           Build release binary"
	@echo "  test            Run tests"
	@echo "  lint            Run linter"
	@echo "  run             Run dev (stdio mode)"
	@echo "  clean           Clean build artifacts"
	@echo "  kill            Kill all leankg MCP processes"
	@echo ""
	@echo "MCP Server targets (HTTP mode):"
	@echo "  mcp-http        Start MCP HTTP server on port 9699"
	@echo "  mcp-http-auth   Start MCP HTTP server with auth"
	@echo "  mcp-http-watch  Start MCP HTTP server with file watcher"
	@echo ""
	@echo "MCP Server targets (Stdio mode):"
	@echo "  mcp-stdio       Start MCP stdio server"
	@echo "  mcp-stdio-watch Start MCP stdio server with file watcher"

# Build release binary
build:
	cargo build --release

# Run tests
test:
	cargo test

# Run linter
lint:
	cargo clippy --all-targets --all-features -- -D warnings

# Run LeanKG (stdio mode for local dev)
run:
	cargo run --release

# Clean build artifacts
clean:
	cargo clean

# Kill all leankg processes (HTTP and stdio)
kill:
	pkill -9 -f "leankg.*mcp" 2>/dev/null || true
	@echo "All leankg MCP processes killed"

# === MCP Stdio Mode ===

mcp-stdio:
	cargo run --release -- mcp-stdio

mcp-stdio-watch:
	cargo run --release -- mcp-stdio --watch

# === MCP HTTP Mode ===

mcp-http:
	cargo run --release -- mcp-http

mcp-http-auth:
	cargo run --release -- mcp-http --auth "$(shell uuidgen 2>/dev/null || echo 'secret-token')"

mcp-http-watch:
	cargo run --release -- mcp-http --watch

# Start on custom port
mcp-http-port:
	@read -p "Enter port: " port; \
	cargo run --release -- mcp-http --port $$port

# === Development ===

dev:
	RUST_LOG=debug cargo run --release -- mcp-stdio --watch

# === Installation ===

install: build
	sudo cp target/release/leankg /usr/local/bin/

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