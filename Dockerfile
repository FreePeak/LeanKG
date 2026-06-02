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
RUN cargo build --release && strip target/release/leankg

ENV PORT=8080
EXPOSE 8080 9699

CMD ["leankg", "web"]
