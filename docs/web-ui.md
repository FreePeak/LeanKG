# Web UI

LeanKG includes a built-in web UI for visualizing and querying your knowledge graph.

## Start the Web UI

```bash
# Start the web server (default port: 8080)
leankg web

# Or specify a custom port
leankg web --port 9000
```

Open **http://localhost:8080** in your browser.

## Features

- **Graph Visualization** -- Interactive force-directed graph of code elements and relationships
- **Code Browse** -- Navigate files, functions, and classes in your codebase
- **Documentation** -- View and manage code documentation
- **Annotations** -- Add business logic annotations to code elements
- **Quality Metrics** -- View code quality metrics and oversized functions
- **Export** -- Export graph data in various formats
- **Settings** -- Configure LeanKG behavior
- **Query API** -- Execute custom Datalog queries against the knowledge graph

## Prerequisites

Ensure you have indexed your codebase first:

```bash
leankg init
leankg index ./src
```

## Troubleshooting

**Empty graph**: Run `leankg index ./src` to populate the database first.

**Connection refused**: Ensure `leankg web` is running on port 8080.
