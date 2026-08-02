# God nodes (#182) live evidence — 2026-08-02

## Environment
- commit: 8c77b22b | binary: Docker :9699 (0.19.31) | project: /workspace-be (662k elements)

## Steps
1. `get_god_nodes limit=5` via MCP
2. `get_architecture max_items=3` hotspots

## Results
- `get_god_nodes` → ranked by degree: `len (124088)`, `uint64 (93436)`, `Errorf ./platform-transport/be-trip/pkg/core/logger.go::Errorf (84610)`, `uint (40724)`, `int (28649)` — PASS: rank_score/degree persisted + ranked list.
- `get_architecture` hotspots top-3: `food_promotion_message.pb.go (5470 functions)`, `generic.pb.go (4535)`, `be_questing_message.pb.validate.go (4214)` — PASS: hotspots top-10; hub files = be's generated protobuf code (the busiest nodes in a real Go monorepo).
- Entry points also returned (python nuclei_domain_scan.py::main, go client main) — PASS.

## Tracker
- God nodes (#182): PASS. Hub node = be's protobuf-generated files (correct for this codebase). rank_score ordering confirmed.
