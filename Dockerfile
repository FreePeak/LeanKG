# Render / onrender production image (leankg.onrender.com).
# Bakes UI v2 into rust_embed (`src/embed/`). Legacy `ui/` is not shipped.
FROM rust:1-bookworm
WORKDIR /app

RUN apt-get update && apt-get install -y clang git curl && rm -rf /var/lib/apt/lists/*
RUN curl -fsSL https://deb.nodesource.com/setup_20.x | bash - && apt-get install -y nodejs

# Layer-cache npm deps separately from sources (faster Render rebuilds on UI-only changes).
COPY ui-v2/package.json ui-v2/package-lock.json ./ui-v2/
WORKDIR /app/ui-v2
RUN npm ci
COPY ui-v2/ ./
RUN npm run build

WORKDIR /app
COPY Cargo.toml Cargo.lock ./
COPY src ./src
COPY ontology/ ./ontology/
RUN rm -rf src/embed/* && cp -r ui-v2/dist/* src/embed/

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
