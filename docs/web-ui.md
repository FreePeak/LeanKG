# LeanKG Web UI

LeanKG includes a fully reconstructed, React-based Web UI for visually exploring your dependencies and reading indexed source code directly in the browser!

It utilizes advanced `Sigma.js` + `ForceAtlas2` mechanics combined with unsupervised machine-learning (`Louvain`) to group and separate your services into beautiful floating spheres!

## Start the Web UI

If you installed LeanKG from the release binary or install scripts, the UI is already built in!

```bash
# Start the web server (default port: 8080)
leankg web

# Or specify a custom port
leankg web --port 9000
```
Open **http://localhost:8080** in your browser.

## Features

![LeanKG Graph Visualization](screenshots/graph.png)

- **Force-Directed Graph Physics** -- Watch your codebase dynamically reorganize into stable clusters. Structural hierarchies form heavy planetary cores while lightweight cross-references orbit around them.
- **WebGL Node Selection & Traversal** -- Instant filtering by module or component types.
- **Resizable Source Code Viewer** -- Click any logical component (Method, Function, Class) to instantly side-load the syntax-highlighted source code via a draggable layout. 
- **Louvain Communities** -- LeanKG runs unsupervised community detection directly in your browser out-of-the-box, clustering relevant business logic together mathematically.

## Development & Building from Source

If you cloned LeanKG from GitHub, the `leankg web` rust server expects the frontend assets to be compiled inside `ui/dist`. You must build them using `npm`:

```bash
# 1. First, build the frontend React application
cd ui
npm install
npm run build
cd ..

# 2. Then, run the rust server
cargo run -- web
```

Alternatively, you can just run the Vite Dev Server directly for instant Hot Moduled Reloading while making UI edits:

```bash
# Run backend API server on 8080
cargo run -- serve

# Run Vite dev server in another terminal (defaults to port 5173 and proxies API requests)
cd ui
npm run dev
```

## Troubleshooting

**Empty graph**: Ensure you have successfully run `leankg index ./src` first! The graph relies on the SQLite graph schema.

**Blank White Page**: You cloned from GitHub but didn't run `npm run build` inside the `ui` directory! Follow the Development guide above.
