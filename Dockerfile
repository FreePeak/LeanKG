# Render / onrender production image (leankg.onrender.com).
# Bakes UI v2 into rust_embed (`src/embed/`). Legacy `ui/` is not shipped.
FROM rust:1-bookworm
WORKDIR /app

# Cache-bust: bump when OnRender sticks on a stale UI layer.
ARG UI_EMBED_REV=2026-07-21-onrender-rca2

RUN apt-get update && apt-get install -y clang git curl && rm -rf /var/lib/apt/lists/*
RUN curl -fsSL https://deb.nodesource.com/setup_20.x | bash - && apt-get install -y nodejs

# Layer-cache npm deps separately from sources (faster Render rebuilds on UI-only changes).
COPY ui-v2/package.json ui-v2/package-lock.json ./ui-v2/
WORKDIR /app/ui-v2
RUN npm ci
COPY ui-v2/ ./
RUN echo "UI_EMBED_REV=${UI_EMBED_REV}" && npm run build

WORKDIR /app
COPY Cargo.toml Cargo.lock ./
COPY src ./src
# benches/ required — Cargo.toml [[bench]] targets fail manifest parse if missing.
COPY benches ./benches
COPY ontology/ ./ontology/
# Always overwrite committed embed with freshly built ui-v2; refuse legacy title "ui".
RUN rm -rf src/embed/* && cp -r ui-v2/dist/* src/embed/ \
    && test -f src/embed/index.html \
    && grep -q '<title>LeanKG</title>' src/embed/index.html \
    && printf '{"ui":"ui-v2","rev":"%s","source":"Dockerfile"}\n' "${UI_EMBED_REV}" > src/embed/ui-build.json

# US-CBM-C1 / FR-HNSW-C: build with the `embeddings` feature so semantic
# tools work out of the box (HNSW-backed semantic_search, embed, smoke-test).
RUN cargo build --release --features embeddings && strip target/release/leankg \
    && cp target/release/leankg /usr/local/bin/leankg

ENV PORT=8080
EXPOSE 8080 9699

# Render health checks hit this path (index status is always available).
HEALTHCHECK --interval=30s --timeout=5s --start-period=90s --retries=3 \
    CMD curl -fsS "http://127.0.0.1:${PORT}/api/index/status" || exit 1

CMD ["leankg", "web"]
