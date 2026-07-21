# Render / onrender production image (leankg.onrender.com).
# Multi-stage: UI (node) → Rust builder (+ demo index) → slim runtime.
# Starter Render pipeline = 2 CPU / 8 GB RAM — single-stage + parallel rustc OOMs.
FROM node:20-bookworm AS ui
WORKDIR /ui
COPY ui-v2/package.json ui-v2/package-lock.json ./
RUN npm ci
COPY ui-v2/ ./
ARG UI_EMBED_REV=2026-07-21-onrender-rca4
RUN echo "UI_EMBED_REV=${UI_EMBED_REV}" && npm run build

FROM rust:1-bookworm AS builder
WORKDIR /app

# Render Starter build pipeline: 2 CPU, 8 GB RAM (docs.render.com/build-pipeline).
# ort + rocksdb + thin LTO can exceed 8 GB with default parallel codegen.
ENV CARGO_BUILD_JOBS=1 \
    CARGO_PROFILE_RELEASE_LTO=false \
    CARGO_TERM_COLOR=always

RUN apt-get update \
    && apt-get install -y --no-install-recommends clang libclang-dev \
    && rm -rf /var/lib/apt/lists/*

COPY Cargo.toml Cargo.lock ./
COPY src ./src
# benches/ required — Cargo.toml [[bench]] targets fail manifest parse if missing.
COPY benches ./benches
COPY ontology/ ./ontology/
COPY --from=ui /ui/dist/ ./src/embed/
ARG UI_EMBED_REV=2026-07-21-onrender-rca4
RUN test -f src/embed/index.html \
    && grep -q '<title>LeanKG</title>' src/embed/index.html \
    && printf '{"ui":"ui-v2","rev":"%s","source":"Dockerfile"}\n' "${UI_EMBED_REV}" > src/embed/ui-build.json

# US-CBM-C1 / FR-HNSW-C: embeddings feature for semantic_search on hosted demo.
RUN cargo build --release --features embeddings \
    && strip target/release/leankg \
    && cp target/release/leankg /usr/local/bin/leankg

# Bake LeanKG source graph for leankg.onrender.com (regression: multi-stage shipped binary-only).
RUN leankg init --path .leankg \
    && leankg index src \
    && test -f .leankg/leankg.db

FROM debian:bookworm-slim AS runtime

COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt

RUN apt-get update \
    && apt-get install -y --no-install-recommends \
        ca-certificates \
        curl \
        libstdc++6 \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /app

COPY --from=builder /usr/local/bin/leankg /usr/local/bin/leankg
COPY --from=builder /app/src ./src
COPY --from=builder /app/ontology ./ontology
COPY --from=builder /app/.leankg ./.leankg
COPY --from=builder /app/leankg.yaml ./leankg.yaml

ENV LEANKG_SERVE_PROJECT=/app \
    PORT=8080
EXPOSE 8080 9699

# Render health checks hit this path (index status is always available).
HEALTHCHECK --interval=30s --timeout=5s --start-period=90s --retries=3 \
    CMD curl -fsS "http://127.0.0.1:${PORT}/api/index/status" || exit 1

CMD ["leankg", "web"]
