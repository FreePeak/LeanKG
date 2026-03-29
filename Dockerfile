FROM rust:1-slim as builder
WORKDIR /app

COPY Cargo.toml Cargo.lock ./
COPY src ./src

RUN apt-get update && apt-get install -y clang && rm -rf /var/lib/apt/lists/*

RUN cargo build --release && strip target/release/leankg

FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y ca-certificates && rm -rf /var/lib/apt/lists/*
COPY --from=builder /app/target/release/leankg /usr/local/bin/

ENV PORT=8080
EXPOSE 8080

CMD ["leankg", "web"]
