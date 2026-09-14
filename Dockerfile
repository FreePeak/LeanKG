# LeanKG Go engine — container deploy (Render `leankg.onrender.com`, Docker runtime).
#
# One CGO-free binary, three stages. The ui-v2 dashboard is a checked-in build
# under internal/web/embed consumed by //go:embed, so there is no Node stage
# here: `make go-ui-assets` is what re-syncs that directory, and this image
# ships whatever revision the tree carries (see embed/ui-build.json).

FROM golang:1.25-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
COPY cmd/ ./cmd/
COPY internal/ ./internal/
ENV CGO_ENABLED=0
RUN go build -trimpath -ldflags "-s -w" -o /out/leankg ./cmd/leankg \
 && go build -trimpath -ldflags "-s -w" -o /out/leankg-embed ./cmd/leankg-embed

# Demo graph: a public demo with an empty store shows an empty canvas, so the
# sqlite store is built at image-build time and served `--read-only` at runtime.
# The corpus is a deliberate subset of this repo — the language examples, the
# engine, and the dashboard source (~530 files, ~4.6k elements, ~17 MB store) —
# minus the 60 MB of vendored tree-sitter grammars, which are generated
# fixtures and would only add noise to the graph.
FROM alpine:3.22 AS demo
COPY --from=build /out/leankg /usr/local/bin/leankg
COPY examples/  /demo/examples/
COPY ui-v2/src/ /demo/web-ui/
COPY cmd/ internal/ /demo/engine/
# Indexing the 60 MB of vendored tree-sitter grammars would add noise, not
# signal, to a demo graph, and the top-level testdata is not copied.
# `internal/index/testdata` stays on purpose: it is the java/kotlin coverage
# the demo shows off.
RUN rm -rf /demo/engine/internal/tstree \
 && leankg index /demo --auto

FROM alpine:3.22
RUN adduser -D -u 10001 leankg
COPY --from=build /out/leankg /out/leankg-embed /usr/local/bin/
# --chown is load-bearing, not cosmetics: the store is WAL, and SQLite can only
# open a WAL database when it can create the -shm sidecar, so a root-owned
# project directory makes every query fail with SQLITE_READONLY_DIRECTORY.
COPY --from=demo --chown=leankg:leankg /demo /var/lib/leankg/demo
USER leankg
WORKDIR /var/lib/leankg/demo

# Render routes the single exposed port and sets $PORT (default 10000).
ENV PORT=10000
EXPOSE 10000
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
  CMD wget -qO- "http://127.0.0.1:${PORT}/health" >/dev/null || exit 1

# The dashboard listener also serves the dashboard's /api/* data endpoints, so
# one port is enough for the demo. MCP/REST/ConnectRPC bind their own addresses
# and are not exposed here.
CMD ["sh", "-c", "exec leankg serve --read-only --ui :${PORT} --project /var/lib/leankg/demo"]
