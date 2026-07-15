FROM rust:1-bookworm
WORKDIR /app

RUN apt-get update && apt-get install -y clang git curl && rm -rf /var/lib/apt/lists/*
RUN curl -fsSL https://deb.nodesource.com/setup_20.x | bash - && apt-get install -y nodejs

COPY ui/ ./ui/
COPY Cargo.toml Cargo.lock ./
WORKDIR /app/ui
RUN npm install && npm run build

WORKDIR /app
COPY src ./src
COPY ontology/ ./ontology/
# US-CBM-C1 / FR-HNSW-C: build with the `embeddings` feature so semantic
# tools work out of the box (HNSW-backed semantic_search, embed, smoke-test).
RUN cargo build --release --features embeddings && strip target/release/leankg

ENV PORT=8080
EXPOSE 8080 9699

CMD ["leankg", "web"]
