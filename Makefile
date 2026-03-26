.PHONY: build build-release build-wasm publish publish-wasm test clean lint fmt check release

CARGO = cargo
WASM_PACK = wasm-pack

build:
	$(CARGO) build --release

build-release:
	$(CARGO) build --release

install-release: build-release
	install -m 755 target/release/leankg $(HOME)/.local/bin/leankg

test:
	$(CARGO) test

lint:
	$(CARGO) clippy -- -D warnings

fmt:
	$(CARGO) fmt

check:
	$(CARGO) check

publish: build-release
	$(CARGO) publish

build-wasm:
	$(WASM_PACK) build --target web --out-dir pkg

publish-wasm: build-wasm
	$(WASM_PACK) publish

release:
	./scripts/release.sh $(VERSION)

clean:
	$(CARGO) clean
	rm -rf pkg
