# LeanKG UI v2 — README

GitNexus-shell graph explorer adapted for LeanKG (`leankg serve` REST).

## Dev

```bash
# Terminal A
cargo run --release -- serve

# Terminal B
cd ui-v2 && npm install && npm run dev
```

Open http://localhost:5173 — Vite proxies `/api` → `:8080`.

## Tests

```bash
npm test              # Vitest unit
npm run test:e2e      # Playwright (skips if servers down unless E2E=1)
```

## Phase 1 scope

Force / Tree / Circles, file tree, filters, code panel, search, QueryFAB, mega-graph skip.  
**Not included:** browser LLM agent, analyze/upload, embed cutover (`src/embed` still uses legacy `ui/`).
