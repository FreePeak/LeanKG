FROM rust:1-bookworm
WORKDIR /app

RUN apt-get update && apt-get install -y clang git curl && rm -rf /var/lib/apt/lists/*
RUN curl -fsSL https://deb.nodesource.com/setup_20.x | bash - && apt-get install -y nodejs

# UI v2 → src/embed (rust_embed). Legacy `ui/` is not shipped.
COPY ui-v2/ ./ui-v2/
WORKDIR /app/ui-v2
RUN npm ci && npm run build

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

CMD ["leankg", "web"]
