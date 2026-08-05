# LeanKG — lean Postgres-only image: Rust binary + HTTP MCP server.
# No UI build, no source indexing bake. Postgres (pgvector) is the only
# storage engine (D4); the container connects via LEANKG_PG_URL.
FROM rust:1-bookworm AS builder
WORKDIR /app

# Starter Render build pipeline: 2 CPU, 8 GB RAM (docs.render.com/build-pipeline).
# embeddings → fastembed → hf-hub → native-tls → openssl-sys needs libssl-dev.
ENV CARGO_BUILD_JOBS=1 \
    CARGO_PROFILE_RELEASE_LTO=false \
    CARGO_INCREMENTAL=0 \
    RUSTFLAGS="-C debuginfo=0" \
    CARGO_TERM_COLOR=always

# Deb.debian.org is unreachable from some build networks (IPv6 routing);
# ftp.debian.org is a working mirror. Pin apt to it.
RUN printf 'Types: deb\nURIs: http://ftp.debian.org/debian\nSuites: bookworm bookworm-updates\nComponents: main\nSigned-By: /usr/share/keyrings/debian-archive-keyring.gpg\n' > /etc/apt/sources.list.d/debian.sources \
    && apt-get update \
    && apt-get install -y --no-install-recommends \
        clang \
        libclang-dev \
        pkg-config \
        libssl-dev \
    && rm -rf /var/lib/apt/lists/*

COPY Cargo.toml Cargo.lock ./
COPY src ./src
# benches/ required — Cargo.toml [[bench]] targets fail manifest parse if missing.
COPY benches ./benches
# examples/ required — Cargo.toml [[example]] targets (embeddings feature) fail
# manifest parse if missing.
COPY examples ./examples
COPY ontology/ ./ontology/
COPY leankg.yaml ./leankg.yaml

RUN cargo build --release \
    && strip target/release/leankg \
    && cp target/release/leankg /usr/local/bin/leankg

FROM debian:bookworm-slim AS runtime

COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt

RUN printf 'Types: deb\nURIs: http://ftp.debian.org/debian\nSuites: bookworm bookworm-updates\nComponents: main\nSigned-By: /usr/share/keyrings/debian-archive-keyring.gpg\n' > /etc/apt/sources.list.d/debian.sources \
    && apt-get update \
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
