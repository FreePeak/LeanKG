# LeanKG — Go engine (v4.6.0; the Rust tree was removed)
# Default target
help:
	@echo "LeanKG Makefile (Go engine)"
	@echo ""
	@echo "Targets:"
	@echo "  go-build        Build the Go engine binaries into bin/ (leankg, leankg-embed)"
	@echo "  go-test         Run the Go engine test suite"
	@echo "  go-bench        Run the Go A/B benchmark suite"
	@echo "  go-vet          Vet the Go engine"
	@echo "  go-build-tstree Build with the tree-sitter tier (CGO)"
	@echo "  go-test-tstree  Test the tree-sitter tier"
	@echo "  go-ui-assets    Sync the checked-in ui build into the Go embed dir"
	@echo "  dual-engine     Run the dual-engine (sqlite + postgres) acceptance gate"
	@echo "  clean           Remove Go build artifacts"
	@echo "  install-go      Build-from-source installer (scripts/install-go.sh)"

.PHONY: help go-build go-test go-bench go-vet go-ui-assets go-ui-dashboard go-ui-dashboard-check dual-engine clean install-go go-build-tstree go-test-tstree

go-build:
	CGO_ENABLED=0 go build -o bin/ ./cmd/leankg ./cmd/leankg-embed

go-test:
	go test ./... -count=1

go-bench:
	go test ./benchmark/ab/ -bench=. -benchmem -run='^$$'

go-vet:
	go vet ./...

# The tree-sitter tier is opt-in: `tstree` guards the CGO grammar bindings, so
# the default build stays CGO_ENABLED=0 (CI asserts both shapes).
go-build-tstree:
	go build -tags tstree ./...

go-test-tstree:
	go test -tags tstree ./... -count=1

# Sync a fresh ui-v2 production build into the Go dashboard embed dir
# (`internal/web/embed`, consumed by //go:embed all:embed in web.go). The
# Rust-era source path `src/embed` is gone; ui-v2's vite `dist/` is the only
# producer. ui-build.json is a hand-maintained provenance marker, so it is
# kept across the sync.
go-ui-assets:
	npm --prefix ui-v2 run build
	@test -f ui-v2/dist/index.html || { echo "go-ui-assets: ui-v2 build produced no dist/"; exit 1; }
	find internal/web/embed -mindepth 1 ! -name ui-build.json -delete
	cp -R ui-v2/dist/. internal/web/embed/

# Build the ui-dashboard SPA (DS-22/DS-25) and sync it into the dashboard
# embed dir (`internal/dashboard/embed`). ui-build.json records a content
# hash of the ui-dashboard sources (not a commit id, so uncommitted edits count
# and source + embed can land in one commit); go-ui-dashboard-check fails when
# the stamp no longer matches the sources (CI gate).
UI_DASHBOARD_HASH = find ui-dashboard -type f -not -path '*/node_modules/*' -not -path '*/dist/*' -not -name '.DS_Store' -not -name '*.tsbuildinfo' | LC_ALL=C sort | xargs shasum -a 256 | shasum -a 256 | cut -d' ' -f1

go-ui-dashboard:
	npm --prefix ui-dashboard ci
	npm --prefix ui-dashboard run build
	@test -f ui-dashboard/dist/index.html || { echo "go-ui-dashboard: ui-dashboard build produced no dist/"; exit 1; }
	find internal/dashboard/embed -mindepth 1 ! -name ui-build.json -delete
	cp -R ui-dashboard/dist/. internal/dashboard/embed/
	@printf '{"source_sha256":"%s"}\n' "$$($(UI_DASHBOARD_HASH))" > internal/dashboard/embed/ui-build.json

go-ui-dashboard-check:
	@cur=$$($(UI_DASHBOARD_HASH)); \
	got=$$(sed -n 's/.*"source_sha256":"\([^"]*\)".*/\1/p' internal/dashboard/embed/ui-build.json); \
	test -n "$$got" && test "$$cur" = "$$got" || { echo "go-ui-dashboard-check: internal/dashboard/embed is stale (embed=$$got, sources=$$cur); run make go-ui-dashboard"; exit 1; }
	@echo "go-ui-dashboard-check: embed matches ui-dashboard sources"

dual-engine:
	bash scripts/test-dual-engine.sh

clean:
	rm -rf bin

install-go:
	bash scripts/install-go.sh
