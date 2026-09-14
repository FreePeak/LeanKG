# LeanKG — Go engine (v4.6.0; the Rust tree was removed)
# Default target
help:
	@echo "LeanKG Makefile (Go engine)"
	@echo ""
	@echo "Targets:"
	@echo "  go-build        Build the Go engine binaries into go/bin/ (leankg, leankg-embed)"
	@echo "  go-test         Run the Go engine test suite"
	@echo "  go-bench        Run the Go A/B benchmark suite"
	@echo "  go-vet          Vet the Go engine"
	@echo "  go-build-tstree Build with the tree-sitter tier (CGO)"
	@echo "  go-test-tstree  Test the tree-sitter tier"
	@echo "  go-ui-assets    Sync the checked-in ui build into the Go embed dir"
	@echo "  dual-engine     Run the dual-engine (sqlite + postgres) acceptance gate"
	@echo "  clean           Remove Go build artifacts"
	@echo "  install-go      Build-from-source installer (scripts/install-go.sh)"

.PHONY: help go-build go-test go-bench go-vet go-ui-assets dual-engine clean install-go go-build-tstree go-test-tstree

go-build:
	cd go && CGO_ENABLED=0 go build -o bin/ ./cmd/leankg ./cmd/leankg-embed

go-test:
	cd go && go test ./... -count=1

go-bench:
	cd go && go test ./benchmark/ab/ -bench=. -benchmem -run='^$$'

go-vet:
	cd go && go vet ./...

# Sync a fresh ui-v2 production build into the Go dashboard embed dir
# (`go/internal/web/embed`, consumed by //go:embed all:embed in web.go). The
# Rust-era source path `src/embed` is gone; ui-v2's vite `dist/` is the only
# producer. ui-build.json is a hand-maintained provenance marker, so it is
# kept across the sync.
go-ui-assets:
	npm --prefix ui-v2 run build
	@test -f ui-v2/dist/index.html || { echo "go-ui-assets: ui-v2 build produced no dist/"; exit 1; }
	find go/internal/web/embed -mindepth 1 ! -name ui-build.json -delete
	cp -R ui-v2/dist/. go/internal/web/embed/

dual-engine:
	bash scripts/test-dual-engine.sh

clean:
	rm -rf go/bin

install-go:
	bash scripts/install-go.sh
