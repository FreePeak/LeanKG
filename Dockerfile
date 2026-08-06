# LeanKG — lean Postgres-only image: Rust binary + HTTP MCP server.
# No UI build, no source indexing bake. Postgres (pgvector) is the only
# storage engine (D4); the container connects via LEANKG_PG_URL.
#
# Embeddings are opt-in via --build-arg LEANKG_FEATURES=embeddings (the
# fastembed/ONNX stack adds ~hundreds of MB and several minutes to the build).
# The MCP HTTP server's in-process background embed + embed_control tool only
# exist when this feature is compiled in — build with it if you want live
# embedding without a separate worker container.
FROM rust:1-bookworm AS builder
WORKDIR /app

ARG LEANKG_FEATURES=""

# Starter Render build pipeline: 2 CPU, 8 GB RAM (docs.render.com/build-pipeline).
# Default build has no embeddings → no fastembed/openssl in the dep graph, so
# clang/libclang-dev/libssl-dev are unnecessary (they were for an old bindgen
# path). pkg-config is still needed by some tree-sitter C build scripts.
ENV CARGO_BUILD_JOBS=1 \
    CARGO_PROFILE_RELEASE_LTO=false \
    CARGO_INCREMENTAL=0 \
    RUSTFLAGS="-C debuginfo=0" \
    CARGO_TERM_COLOR=always

# HTTPS apt mirror. Plain HTTP mirrors (ftp.us.debian.org) are unreachable
# from sandboxed build networks; deb.debian.org (Fastly CDN) over HTTPS works.
# Sources file in its own RUN (heredoc must end the instruction).
RUN cat > /etc/apt/sources.list.d/debian.sources <<'EOF'
Types: deb
URIs: https://deb.debian.org/debian
Suites: bookworm bookworm-updates
Components: main
Signed-By: /usr/share/keyrings/debian-archive-keyring.gpg
EOF
RUN apt-get update \
    && apt-get install -y --no-install-recommends \
        pkg-config \
        libssl-dev \
    && rm -rf /var/lib/apt/lists/*

COPY Cargo.toml Cargo.lock ./
COPY src ./src
# examples/ + benches/ are not copied: their [[example]]/[[bench]] manifest
# targets were removed, so Cargo no longer needs the source files present.
COPY ontology/ ./ontology/
COPY leankg.yaml ./leankg.yaml

# --no-default-features: build only core languages (go/ts/py/rust/java/kotlin/
# bash/ruby/php/perl/r/elixir/swift/c/cpp/objc/dart). The lang-extras grammars
# (scala, csharp, cuda, …) are compiled out, cutting build time substantially.
# Embeddings opt-in via --build-arg LEANKG_FEATURES=embeddings (fastembed/ONNX).
RUN cargo build --release --no-default-features --features "$LEANKG_FEATURES" \
    && strip target/release/leankg \
    && cp target/release/leankg /usr/local/bin/leankg

FROM debian:bookworm-slim AS runtime

COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt

RUN cat > /etc/apt/sources.list.d/debian.sources <<'EOF'
Types: deb
URIs: https://deb.debian.org/debian
Suites: bookworm bookworm-updates
Components: main
Signed-By: /usr/share/keyrings/debian-archive-keyring.gpg
EOF
RUN apt-get update \
    && apt-get install -y --no-install-recommends \
        ca-certificates \
        curl \
        libstdc++6 \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /app

COPY --from=builder /usr/local/bin/leankg /usr/local/bin/leankg
COPY --from=builder /app/ontology ./ontology
COPY --from=builder /app/leankg.yaml ./leankg.yaml

ENV LEANKG_PG_URL="" \
    MCP_HTTP_PORT=9699 \
    LEANKG_EMBED_AUTO_ARM=0
EXPOSE 9699

# Health: the HTTP MCP server exposes /health.
HEALTHCHECK --interval=30s --timeout=5s --start-period=15s --retries=3 \
    CMD curl -fsS "http://127.0.0.1:${MCP_HTTP_PORT}/health" || exit 1

CMD ["leankg", "mcp-http"]
