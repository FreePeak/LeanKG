# service_calls breadth (#187) live evidence — 2026-08-02

## Environment
- commit: 8c77b22b | binary: target/release/leankg 0.19.31 (local) | project: /tmp/leankg-live-fixture

## Steps
1. Added `config/services.yaml` with `payments_address: dns:///payments-be.default.svc.cluster.local.:8081`, `notify_address: dns:///notify-be...`, `orders_address: http://orders-be.default.svc.cluster.local.:8080`
2. `index config` (microservice extractor scans `config/` YAML)

## Results
- `query "service_calls" --kind rel` → 2 edges:
  - `unknown-service -> payments-be (service_calls)` — PASS (gRPC dns:/// extracted)
  - `unknown-service -> notify-be (service_calls)` — PASS
- `orders_address` (http://...default.svc.cluster.local) — NOT extracted as edge: the `_http_pattern` path requires the k8s-service regex match AND the extractor's http branch (config.go YAML or direct http match); plain `http://` without `.default.svc.cluster.local` not matched. Partial.
- Source service = `unknown-service` (fixture has no service name in leankg.yaml microservice section) — expected for unconfigured project.

## Tracker
- service_calls breadth (#187): PASS for gRPC `dns:///` + `*_address` YAML keys. HTTP k8s-style addresses: partial (needs `.default.svc.cluster.local` form + http branch). Note: `http.Get`/`client.Get` in code are extracted as `http_calls` (route_extractor) not service_calls.
