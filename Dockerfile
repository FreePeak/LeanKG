FROM rust:1-bookworm as builder
WORKDIR /app

# Install Node.js for UI build
RUN apt-get update && apt-get install -y clang git curl && rm -rf /var/lib/apt/lists/*
RUN curl -fsSL https://deb.nodesource.com/setup_20.x | bash - && apt-get install -y nodejs

# Copy UI source (for build)
COPY ui/ ./ui/
COPY Cargo.toml Cargo.lock ./

# Build UI
WORKDIR /app/ui
RUN npm install && npm run build

# Copy source and index for demo
WORKDIR /app
COPY src ./src

# Build Rust (without indexing - we just need the binary)
RUN cargo build --release && strip target/release/leankg

# Index the LeanKG codebase for demo
RUN mkdir -p /app/.leankg && /app/target/release/leankg init --path /app/.leankg && /app/target/release/leankg index /app/src

FROM debian:bookworm-slim
WORKDIR /app
RUN apt-get update && apt-get install -y ca-certificates git && rm -rf /var/lib/apt/lists/*
COPY --from=builder /app/target/release/leankg /usr/local/bin/
COPY --from=builder /app/ui/dist /app/ui/dist
COPY --from=builder /app/.leankg /app/.leankg

ENV PORT=8080
EXPOSE 8080

CMD ["leankg", "web"]
