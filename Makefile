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
	@echo "  go-ui-assets    Sync the checked-in ui build into the Go embed dir"
	@echo "  dual-engine     Run the dual-engine (sqlite + postgres) acceptance gate"
	@echo "  clean           Remove Go build artifacts"
	@echo "  install-go      Build-from-source installer (scripts/install-go.sh)"

.PHONY: help go-build go-test go-bench go-vet go-ui-assets dual-engine clean install-go

go-build:
	cd go && CGO_ENABLED=0 go build -o bin/ ./cmd/leankg ./cmd/leankg-embed

go-test:
	cd go && go test ./... -count=1

go-bench:
	cd go && go test ./benchmark/ab/ -bench=. -benchmem -run='^$$'

go-vet:
	cd go && go vet ./...

go-ui-assets:
	rm -rf go/internal/web/embed && cp -r src/embed go/internal/web/embed

dual-engine:
	bash scripts/test-dual-engine.sh

clean:
	rm -rf go/bin

install-go:
	bash scripts/install-go.sh
