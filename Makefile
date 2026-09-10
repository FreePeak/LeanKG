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

# Run tests
test:

# Run linter
lint:

# Run LeanKG compat binary (stdio mode for local dev)
run:

# Clean build artifacts
clean:

# Kill all leankg MCP processes (HTTP and stdio)
kill:
	pkill -9 -f "leankg.*mcp" 2>/dev/null || true
	pkill -9 -f "leankg-mcp" 2>/dev/null || true
	@echo "All leankg MCP processes killed"

# === Split binaries ===

# Query-only MCP HTTP (read-only; no auto-index / bulk embed)
leankg-mcp:

# Pipeline worker. Examples:
#   make leankg-worker WORKER_CMD="index $(PWD)"
#   make leankg-worker WORKER_CMD="embed --wait --project $(PWD)"
#   make leankg-worker WORKER_CMD=status
WORKER_CMD ?= status
leankg-worker:

# === MCP Stdio Mode (query-only via leankg-mcp) ===

mcp-stdio:

mcp-stdio-watch:

# === MCP HTTP Mode (query-only via leankg-mcp) ===

mcp-http:

mcp-http-auth:

mcp-http-watch:

# Start on custom port
mcp-http-port:
	@read -p "Enter port: " port; \

# === Development ===

dev:

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
	launchctl start com.leankg.mcp-http 2>/dev/null || true

# ---- Go engine (go/) ----

.PHONY: go-build go-test go-bench go-ui-assets go-vet

go-build: ## Build the Go engine binaries into go/bin/
	cd go && go build -o bin/ ./cmd/leankg ./cmd/leankg-embed

go-test: ## Run the Go engine test suite
	cd go && go test ./... -count=1

go-bench: ## Run the Go A/B benchmark suite
	cd go && go test ./benchmark/ab/ -bench=. -benchmem -run='^$$'

go-ui-assets: ## Sync the checked-in ui build into the Go embed dir
	rm -rf go/internal/web/embed && cp -r src/embed go/internal/web/embed

go-vet: ## Vet the Go engine
	cd go && go vet ./...

