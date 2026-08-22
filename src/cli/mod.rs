use clap::{Subcommand, ValueEnum};

pub mod mcp;
pub mod reexec;
pub mod shell_runner;
pub mod worker;

/// Output format for `leankg tags` (currently only `ctags`).
#[derive(ValueEnum, Clone, Copy, Debug, PartialEq, Eq)]
pub enum TagsFormat {
    /// readtags-compatible `tags` file.
    Ctags,
}

/// Output format for `leankg cost`.
#[derive(ValueEnum, Clone, Copy, Debug, PartialEq, Eq)]
pub enum CostFormat {
    /// Human-readable text.
    Text,
    /// JSON.
    Json,
}

#[derive(Subcommand, Debug)]
pub enum CLICommand {
    /// Show LeanKG version
    Version,
    /// Initialize a new LeanKG project
    Init {
        #[arg(long, default_value = ".leankg")]
        path: String,
        /// FR-LSP-B / REL-039: write prefab `lsp:` servers + typed_resolve=go,ts
        #[arg(long, default_value_t = false)]
        with_lsp: bool,
    },
    /// Index the codebase
    Index {
        /// Path to index
        path: Option<String>,
        #[arg(long, short)]
        incremental: bool,
        /// Filter by language (e.g., go,ts,py)
        #[arg(long, short)]
        lang: Option<String>,
        /// Exclude patterns (comma-separated)
        #[arg(long)]
        exclude: Option<String>,
        /// Verbose output
        #[arg(long, short)]
        verbose: bool,
        /// Target environment (local, staging, production)
        #[arg(long, default_value = "local")]
        env: String,
        /// Service name for this index
        #[arg(long)]
        service_name: Option<String>,
        /// Version tag for this index (semver or git sha)
        #[arg(long)]
        version: Option<String>,
        /// Source URI for remote indexing (gs://bucket, git+https://..., etc.)
        /// When provided, content is synced to a local staging directory before indexing.
        /// Without this flag, indexes the local filesystem path as before.
        #[arg(long)]
        source: Option<String>,
        /// Git branch/tag/commit to check out (used with --source git+...)
        #[arg(long)]
        ref_name: Option<String>,
        /// Auth credential for remote sources.
        /// GCS: OAuth access token; prefer GCS_ACCESS_TOKEN env var.
        /// Git: GitLab/GitHub PAT; prefer GITLAB_TOKEN or GIT_TOKEN env vars.
        /// Note: GCS service-account JSON is not supported yet.
        #[arg(long)]
        auth: Option<String>,
        /// Run live A/B benchmark before and after indexing.
        #[arg(long)]
        benchmark: bool,
    },
    /// Query the knowledge graph
    Query {
        /// Query string
        query: String,
        /// Query type: name, type, rel, pattern, content, or subgraph
        /// (content does case-insensitive substring match across name, qualified_name, and file_path;
        /// subgraph runs US-GF-03 NL scoped subgraph query — same as `graph-query`)
        #[arg(long, default_value = "name")]
        kind: String,
        /// Find elements in a specific file path (substring match)
        #[arg(long)]
        file: Option<String>,
        /// Find functions by name (substring match)
        #[arg(long)]
        function: Option<String>,
        /// US-GF-03: token budget when `--kind subgraph` (default 2000)
        #[arg(long)]
        token_budget: Option<usize>,
        /// US-GF-03: BFS depth when `--kind subgraph` (default 2)
        #[arg(long)]
        max_depth: Option<usize>,
    },
    /// US-GF-03: Natural-language scoped subgraph query (seed → expand → budget trim)
    GraphQuery {
        /// Natural-language connection question
        question: String,
        /// Approximate token budget for the response (default 2000)
        #[arg(long, default_value = "2000")]
        token_budget: usize,
        /// BFS expansion depth from seeds (default 2)
        #[arg(long, default_value = "2")]
        max_depth: usize,
    },
    /// Generate documentation
    Generate {
        #[arg(long, short)]
        template: Option<String>,
    },
    /// Start web UI server (deprecated - use 'web' command instead)
    Serve {
        /// Port to listen on (default: from PORT env var or 8080)
        #[arg(long)]
        port: Option<u16>,
        /// Project root to open (default: cwd / find_project_root). Use container
        /// mounts like `/workspace` in Docker — do not inherit MCP's multi-repo cwd.
        #[arg(long)]
        project: Option<String>,
    },
    /// Start the embedded web UI server
    Web {
        /// Port to listen on (default: from PORT env var or 8080)
        #[arg(long)]
        port: Option<u16>,
        /// Project root to open (default: cwd / find_project_root)
        #[arg(long)]
        project: Option<String>,
    },
    /// Start MCP server with stdio transport (for opencode integration)
    McpStdio {
        /// Enable auto-indexing with file watcher
        #[arg(long)]
        watch: bool,
        /// Open the database in read-only mode (reject all write tools).
        /// Useful for query-only replicas that should never mutate state.
        #[arg(long, default_value_t = false)]
        read_only: bool,
    },
    /// Apply pending PostgreSQL schema migrations (LEANKG_PG_URL, default localhost:5433)
    Migrate {},
    /// Start MCP server with HTTP transport (for remote clients)
    McpHttp {
        /// Port to listen on (default: 9699)
        #[arg(long)]
        port: Option<u16>,
        /// Bearer token for authentication (optional)
        #[arg(long)]
        auth: Option<String>,
        /// Enable auto-indexing with file watcher
        #[arg(long)]
        watch: bool,
        /// Reuse existing server if already running (don't wait/start new)
        #[arg(long)]
        reuse: bool,
        /// Project root directory (default: auto-detect from cwd)
        #[arg(long)]
        project: Option<String>,
        /// Open the database in read-only mode (reject all write tools).
        /// Useful for query-only replicas that should never mutate state.
        #[arg(long, default_value_t = false)]
        read_only: bool,
    },
    /// Calculate impact radius
    Impact {
        /// File to analyze
        file: String,
        /// Depth of analysis
        #[arg(long, default_value = "3")]
        depth: u32,
        /// Maximum affected elements to return (default 10000).
        /// Bounded to keep memory + output size predictable on big monorepos.
        #[arg(long, default_value = "10000")]
        max_affected: usize,
    },
    /// US-GF-01: Find shortest path between two symbols in the graph
    Path {
        /// Source symbol (qualified_name, name, or fuzzy suffix)
        source: String,
        /// Target symbol (qualified_name, name, or fuzzy suffix)
        target: String,
        /// Maximum number of hops (1-10)
        #[arg(long, default_value = "6")]
        max_hops: usize,
    },
    /// US-GF-02: Explain a node (definition, cluster, degree, neighbors)
    Explain {
        /// Symbol qualified_name, exact name, or fuzzy suffix
        name: String,
    },
    /// US-GF-05: List god nodes (most-connected symbols)
    Gods {
        /// Limit number of results
        #[arg(long, default_value = "20")]
        limit: usize,
        /// Exclude top-N% super-hubs (0-100)
        #[arg(long)]
        exclude_hubs_percentile: Option<u8>,
    },
    /// US-GF-06: Generate GRAPH_REPORT.md (god nodes, confidence, suggested questions)
    Report {
        /// Project display name (default: directory name)
        #[arg(long)]
        project_name: Option<String>,
        /// Output file path (default: .leankg/GRAPH_REPORT.md)
        #[arg(long)]
        out: Option<String>,
    },
    /// US-MP-03 / FR-MP-09..13: Mine conversation exports (Claude / ChatGPT
    /// / Slack) into decisions, preferences, milestones, and problems, then
    /// persist them into the project graph as typed elements with
    /// `decided_about` edges.
    MineConversations {
        /// Export format: claude | chatgpt | slack
        #[arg(long, value_parser = ["claude", "chatgpt", "slack"])]
        format: String,
        /// Project root whose `.leankg` graph receives the mined nodes
        #[arg(long, default_value = ".")]
        project: String,
        /// Input file or directory of export JSON files
        #[arg(long)]
        input: String,
    },
    /// US-MP-05: Check graph for broken / stale links
    CheckConsistency {
        /// Filter by severity: BROKEN | STALE | CURRENT
        #[arg(long)]
        severity: Option<String>,
        /// Limit findings shown (default 50). Use 0 for unlimited,
        /// but be ready for big output on a large graph.
        #[arg(long, default_value = "50")]
        limit: usize,
    },
    /// US-CBM-B1: Resolve a symbol via the configured LSP server
    LspResolve {
        /// Source language (go, typescript, python, ...)
        /// If omitted, the bridge auto-detects from the file extension.
        #[arg(long)]
        language: Option<String>,
        /// File containing the symbol
        file_path: String,
        /// 0-indexed line
        #[arg(long, default_value = "0")]
        line: u32,
        /// 0-indexed character (column)
        #[arg(long, default_value = "0")]
        character: u32,
        /// LSP request kind
        #[arg(long, default_value = "definition", value_parser = ["definition", "references", "hover"])]
        request: String,
        /// Project root (where leankg.yaml lives)
        #[arg(long, default_value = ".")]
        project: String,
    },
    /// US-CBM-B1: Install the LSP server for a language (or "all").
    /// Runs the best install method we know for the host OS.
    LspInstall {
        /// Language id or "all" to install every known server.
        language: String,
        /// Project root (where leankg.yaml lives)
        #[arg(long, default_value = ".")]
        project: String,
        /// Print commands instead of running them.
        #[arg(long)]
        dry_run: bool,
    },
    /// US-CBM-B1: List every language the LSP registry knows about.
    LspList,
    /// US-MP-06: List cross-domain tunnels (cross-cluster relationships)
    Tunnels {
        /// Limit
        #[arg(long, default_value = "50")]
        limit: usize,
    },
    /// US-GF-09: Record a query outcome lesson (useful | dead_end | corrected)
    Reflect {
        /// Original question
        question: String,
        /// Outcome classification
        outcome: String,
        /// Optional comma-separated qualified_names that were returned
        #[arg(long)]
        nodes: Option<String>,
        /// Optional free-form note
        #[arg(long)]
        note: Option<String>,
    },
    /// US-GF-08: PR impact dashboard (severity + touched clusters)
    Prs {
        /// Environment scope (default: local)
        #[arg(long, default_value = "local")]
        env: String,
        /// Comma-separated changed file paths (overrides git diff auto-detect)
        #[arg(long)]
        files: Option<String>,
    },
    /// Auto-install MCP config
    Install,
    /// FR-PLG-1: One-command MCP client setup — write (or remove) the
    /// LeanKG server entry in an AI client's config file so agents can use
    /// LeanKG without hand-editing JSON/TOML. Idempotent; preserves every
    /// sibling key, comment, and unknown field.
    Connect {
        /// Target AI client: claude-code | cursor | codex | gemini
        #[arg(value_enum)]
        client: crate::connect::Client,
        /// Point the client at a remote HTTP MCP endpoint (e.g.
        /// http://localhost:9699) instead of spawning local stdio.
        #[arg(long)]
        remote: Option<String>,
        /// Remove only the leankg entry from the client config
        /// (succeeds even when absent).
        #[arg(long, conflicts_with = "remote")]
        remove: bool,
        /// Project root passed as `mcp-stdio --project` (default: cwd)
        #[arg(long)]
        project: Option<String>,
    },
    /// Diagnose stale leankg processes, mmap'd DB files, and current
    /// RSS. Prints `leankg daemon kill` to clean them up. Safe to run
    /// at any time.
    Doctor {
        /// Also kill stale leankg processes (default: report only).
        /// Refuses to kill the current process and the caller's parent.
        #[arg(long)]
        kill: bool,
    },
    /// Show index status
    Status,
    /// Start file watcher for incremental re-indexing.
    /// Supports local filesystem watching OR remote source polling.
    /// For remote sources use --source URI --interval SECONDS.
    Watch {
        /// Path to watch (default: project root).
        /// Mutually exclusive with --source.
        #[arg(long)]
        path: Option<String>,
        /// Remote source URI (git+https:// or gs://).
        /// When set, polls the remote at --interval instead of watching
        /// the local filesystem.
        #[arg(long)]
        source: Option<String>,
        /// Ref name for git sources (default: main). Ignored for GCS.
        #[arg(long)]
        ref_name: Option<String>,
        /// Poll interval in seconds (default: 60). Only used with --source.
        #[arg(long, default_value = "60")]
        interval: u64,
        /// Auth credential for the remote source (access token).
        /// Prefer GITLAB_TOKEN/GIT_TOKEN or GCS_ACCESS_TOKEN env vars.
        #[arg(long)]
        auth: Option<String>,
        /// Also run embed after each detected change.
        #[arg(long)]
        embed: bool,
    },
    /// Find oversized functions
    Quality {
        /// Minimum line count (default: 50)
        #[arg(long, default_value = "50")]
        min_lines: u32,
        /// Filter by language
        #[arg(long)]
        lang: Option<String>,
    },
    /// Build or refresh the embedding index (requires --features embeddings).
    /// Default mode is incremental: only re-embed nodes touched since the
    /// last `embed` run, plus newly-added nodes. Orphans (state rows whose
    /// qualified_name no longer exists) are reaped from usearch + state.
    ///
    /// By default the command spawns a detached background process and
    /// returns in <1s, leaving a PID + progress file under
    /// `<project>/.leankg/embed_status.json`. Pass `--wait` to run in the
    /// foreground (the legacy behavior). Use `--status` / `--cancel` to
    /// inspect or stop a running background embed.
    #[cfg(feature = "embeddings")]
    Embed {
        /// Download the embedding + reranker models to the cache and exit.
        /// No index is built. Recommended first step on a fresh install.
        #[arg(long)]
        init: bool,
        /// Ignore embedding_state freshness and re-embed every node from
        /// scratch. Use after a model swap or index corruption.
        #[arg(long)]
        full: bool,
        /// Override the embedding batch size (default 32). Auto-capped by
        /// `LEANKG_EMBED_MAX_MB` (default 2048 on macOS). Lower further on
        /// memory-constrained hosts.
        #[arg(long, default_value = "32")]
        batch_size: usize,
        /// Project root (defaults to current working directory).
        #[arg(long, default_value = ".")]
        project: String,
        /// Wait for the embed to complete in the foreground (legacy
        /// behavior). Default: spawn a detached background process and
        /// return immediately.
        #[arg(long)]
        wait: bool,
        /// Print progress for an in-flight background embed and exit.
        #[arg(long)]
        status: bool,
        /// Cancel an in-flight background embed (SIGTERM) and exit.
        #[arg(long)]
        cancel: bool,
        /// Internal: set by the parent process when re-spawning itself as
        /// a background worker. End-users should not pass this.
        #[arg(long, hide = true)]
        background: bool,
        /// Number of parallel ONNX inference workers (default 2). Each
        /// worker holds its own ONNX session (~300–400 MB). Auto-capped by
        /// `LEANKG_EMBED_MAX_MB`. Set to 1 on low-RAM hosts.
        #[arg(long, default_value = "2")]
        workers: usize,
        /// Comma-separated list of element types to embed (e.g. `function,method`).
        /// Defaults to "all" for small graphs (<50k elements) and
        /// `function,method` for mega-graphs to keep cold embed under 5 min.
        /// Pass `--types all` to embed every embeddable type regardless of size.
        #[arg(long, default_value = "")]
        types: String,
        /// Run live A/B benchmark measuring semantic search quality before and after embedding.
        #[arg(long)]
        benchmark: bool,
        /// Do NOT write embedding vectors to the Postgres vector store.
        /// Runs inference only (useful for benchmarking/smoke tests without
        /// touching PG). Equivalent to `LEANKG_EMBED_WRITE_VECTORS=0`.
        #[arg(long)]
        no_vectors: bool,
        /// FR-EMBED-SUMMARY: GraphRAG-style summary-primary embedding. When
        /// enabled, per-function vectors are skipped for files above
        /// `--summary-primary-cap` lines (default 500) — the file-summary
        /// node carries the signal via its `contains` edges instead. Cuts
        /// inference ~3–8× on large codebases. Values: `on` | `off` | `auto`
        /// (auto = enabled when the graph exceeds 50k elements). Default
        /// `auto`. Env: `LEANKG_EMBED_SUMMARY_PRIMARY`.
        #[arg(long, default_value = "auto")]
        summary_primary: String,
        /// Source-line cap above which a file is summary-only under
        /// `--summary-primary`. Default 500. Env:
        /// `LEANKG_EMBED_SUMMARY_PRIMARY_CAP`.
        #[arg(long)]
        summary_primary_cap: Option<u32>,
        /// FR-EMBED-SUMMARY-ONLY: embed only file + module summary nodes —
        /// no function/method/constructor vectors at all. Functions are
        /// discovered purely via ontology traversal at query time
        /// (`semantic_search` walks down from file/module summary seeds via
        /// `contains` edges). This is the strictest GraphRAG-style mode:
        /// smallest vector count, every function reached by traversal.
        /// Combine with `--full` to drop existing function vectors after a
        /// mode switch. Values: `on` | `off`. Default `off`. Env:
        /// `LEANKG_EMBED_SUMMARY_ONLY`.
        #[arg(long, default_value = "off")]
        summary_only: String,
        /// Offsite embedding — collect embed queries exactly as a normal run
        /// would, then write them to a file (one row per query, NDJSON)
        /// instead of calling the embedder. Non-mutating: leaves
        /// `embedding_vectors` and `embedding_state` untouched. Batch-embed
        /// the file elsewhere (e.g. Colab T4 GPU via
        /// `scripts/embed_batch.py`), then load results with `--import`.
        /// Default file: `<project>/.leankg/embed_export.jsonl`.
        #[arg(long)]
        dry_run: bool,
        /// Output path for `--dry-run`. Default
        /// `<project>/.leankg/embed_export.jsonl`.
        #[arg(long)]
        export_file: Option<String>,
        /// Import mode — read vectors produced from a `--dry-run` export
        /// file (typically by `scripts/embed_batch.py`) and upsert them into
        /// the DB. Resumable: rows already fresh with a matching
        /// `content_hash` are skipped, so re-running after an interruption
        /// picks up where it left off.
        #[arg(long)]
        import: Option<String>,
        /// Skip the graph-drift content_hash check on `--import` (faster).
        /// By default `--import` rebuilds each row's current hash from the
        /// live graph and skips rows whose element changed or vanished since
        /// the export (so stale vectors are never written). Only pass
        /// `--no-verify` when the graph has not changed since the export.
        #[arg(long)]
        no_verify: bool,
    },
    /// One-shot embedding retrieval for CLI testing (requires
    /// --features embeddings). Useful for validating the retrieve→rerank→
    /// traverse pipeline without standing up the MCP server.
    #[cfg(feature = "embeddings")]
    SemanticContext {
        /// Natural language query.
        query: String,
        /// Environment filter.
        #[arg(long, default_value = "local")]
        env: String,
        /// ANN retrieve depth. Defaults to adaptive based on index size
        /// (50 for ≤10k vectors, scaling up to 300 for >1M).
        #[arg(long)]
        top_k: Option<usize>,
        /// Final seed count after rerank.
        #[arg(long, default_value = "10")]
        rerank_top_n: usize,
        /// Disable Stage 4 graph enrichment.
        #[arg(long)]
        no_traverse: bool,
        /// Include paths under .worktrees/ / .claude/worktrees/ /
        /// .opencode/worktrees/ (filtered by default).
        #[arg(long)]
        include_worktrees: bool,
        /// Include workflow_step / playbook_step / decision_point /
        /// failure_mode candidates even when the query doesn't mention
        /// them (filtered by default).
        #[arg(long)]
        include_ontology_steps: bool,
        /// Print diagnostics: candidate counts, latency, reranker status.
        #[arg(long)]
        debug: bool,
        /// Project root (defaults to current working directory).
        #[arg(long, default_value = ".")]
        project: String,
    },
    /// Run canonical semantic-context queries with structural assertions.
    /// Catches regressions in the retrieve→rerank→traverse pipeline.
    #[cfg(feature = "embeddings")]
    SmokeTest {
        /// Project root (defaults to current working directory).
        #[arg(long, default_value = ".")]
        project: String,
    },
    /// Index documentation files (markdown, etc.) into the graph.
    IndexDocs {
        /// Path to documentation directory.
        #[arg(long)]
        path: Option<String>,
        /// Project root (defaults to current working directory).
        #[arg(long, default_value = ".")]
        project: String,
    },
    /// Full refresh: index code + docs + embed in one command.
    #[cfg(feature = "embeddings")]
    Refresh {
        /// Path to index (default: project root).
        path: Option<String>,
        /// Docs path (default: project_root/docs/).
        #[arg(long)]
        docs: Option<String>,
        /// Source URI for remote indexing.
        #[arg(long)]
        source: Option<String>,
        /// Git ref name.
        #[arg(long)]
        ref_name: Option<String>,
        /// Auth credential.
        #[arg(long)]
        auth: Option<String>,
        /// Full re-embed.
        #[arg(long)]
        full: bool,
        /// Project root.
        #[arg(long, default_value = ".")]
        project: String,
    },
    /// Export knowledge graph
    Export {
        /// Output file path
        #[arg(long, default_value = "graph.json")]
        output: String,
        /// Export format: json, dot, mermaid, or html
        #[arg(long, default_value = "json")]
        format: String,
        /// Scope export to a specific file's subgraph
        #[arg(long)]
        file: Option<String>,
        /// Max depth for subgraph traversal (used with --file)
        #[arg(long, default_value = "3")]
        depth: u32,
        /// Scope export to a path prefix (e.g. "src")
        #[arg(long)]
        path: Option<String>,
        /// Scope export to a community/cluster id
        #[arg(long)]
        community: Option<String>,
        /// Maximum nodes to include (default 5000). On unbound mega-graphs
        /// without scope, the export refuses.
        #[arg(long, default_value = "5000")]
        max_nodes: usize,
    },
    /// Export a ctags-compatible `tags` file from the indexed graph
    /// (strategy: ctags/GNU Global fast edge layer, Tier 1 item 9).
    Tags {
        /// Output path (default: `tags` in project root)
        #[arg(long, default_value = "tags")]
        output: String,
        /// Export format
        #[arg(long, value_enum, default_value_t = TagsFormat::Ctags)]
        format: TagsFormat,
        /// Project root (default: cwd / find_project_root)
        #[arg(long)]
        project: Option<String>,
    },
    /// Estimate token cost of an impact radius or a file set
    /// (strategy §18.4: `kg_cost estimate` — "rewriting this would cost N in / M out").
    Cost {
        /// File to compute impact radius from. When omitted, `--files` is used.
        #[arg(long)]
        file: Option<String>,
        /// Impact depth (used with --file).
        #[arg(long, default_value = "3")]
        depth: u32,
        /// Max affected elements to price (used with --file).
        #[arg(long, default_value = "200")]
        max_affected: usize,
        /// Comma-separated file paths to price directly (no impact scan).
        #[arg(long)]
        files: Option<String>,
        /// Output format
        #[arg(long, value_enum, default_value_t = CostFormat::Text)]
        format: CostFormat,
        /// Project root (default: cwd / find_project_root).
        #[arg(long)]
        project: Option<String>,
    },
    /// Export a portable context pack (deterministic graph slice + manifest)
    /// (strategy §8.5 / §17 Tier 6 item 36).
    Pack {
        /// Output directory (default: ./leankg-pack)
        #[arg(long, default_value = "leankg-pack")]
        output: String,
        /// Scope to a path prefix (e.g. "src").
        #[arg(long)]
        path: Option<String>,
        /// Max elements (default 5000; pack refuses to truncate silently).
        #[arg(long, default_value = "5000")]
        max_nodes: usize,
        /// Source revision to record in the manifest (git sha / tag).
        #[arg(long)]
        revision: Option<String>,
        /// Project root (default: cwd / find_project_root).
        #[arg(long)]
        project: Option<String>,
    },
    /// Annotate code element with business logic description
    Annotate {
        /// Element qualified name (e.g., src/main.rs::main)
        element: String,
        /// Business logic description
        #[arg(long, short)]
        description: String,
        /// User story ID (optional)
        #[arg(long)]
        user_story: Option<String>,
        /// Feature ID (optional)
        #[arg(long)]
        feature: Option<String>,
    },
    /// Link code element to user story or feature
    Link {
        /// Element qualified name
        element: String,
        /// User story or feature ID
        id: String,
        /// Link type: story or feature
        #[arg(long, default_value = "story")]
        kind: String,
    },
    /// Search business logic annotations
    SearchAnnotations {
        /// Search query
        query: String,
    },
    /// Show annotations for an element
    ShowAnnotations {
        /// Element qualified name
        element: String,
    },
    /// Show feature-to-code traceability
    Trace {
        /// Feature ID to trace
        #[arg(long)]
        feature: Option<String>,
        /// User story ID to trace
        #[arg(long)]
        user_story: Option<String>,
        /// Show all traceabilities
        #[arg(long, short)]
        all: bool,
    },
    /// Find code elements by business domain
    FindByDomain {
        /// Business domain (e.g., authentication, validation)
        domain: String,
    },
    /// Run benchmark comparison
    Benchmark {
        /// Specific category to run (optional)
        #[arg(long)]
        category: Option<String>,
        /// CLI tool to use: opencode, gemini, kilo, or claude (default: kilo)
        #[arg(long, default_value = "kilo")]
        cli: String,
    },
    /// Run direct tool performance benchmarks (ontology/search/find)
    ToolBench {
        /// Project path (default: auto-detect from cwd)
        #[arg(long)]
        project: Option<String>,
    },
    /// Run A/B test: LeanKG tools vs manual grep/find equivalents
    AbTest {
        /// Project path (default: auto-detect from cwd)
        #[arg(long)]
        project: Option<String>,
    },
    /// Run unified A/B benchmark (all tools, simple->complex, auto-export markdown)
    BenchmarkUnified {
        /// Project path (default: auto-detect from cwd)
        #[arg(long)]
        project: Option<String>,
    },
    /// Register current directory in global registry
    Register {
        /// Name for the repository
        name: String,
    },
    /// Unregister a repository from global registry
    Unregister {
        /// Name of the repository to unregister
        name: String,
    },
    /// List all registered repositories
    List,
    /// Show status for a registered repository
    StatusRepo {
        /// Name of the repository
        name: String,
    },
    /// Global setup: clone repos -> index -> embed (server-side pipeline),
    /// or legacy client-side setup (register MCP + Claude hooks) when no
    /// pipeline flags are given.
    Setup {
        /// Clone the repo list (LEANKG_REPOS) into LEANKG_CLONE_ROOT before indexing.
        #[arg(long)]
        clone: bool,
        /// Run the full index per repo dir.
        #[arg(long)]
        index: bool,
        /// Run the embedding build (embed --wait) per repo dir.
        #[arg(long)]
        embed: bool,
        /// Print resolved repo list + registry state without running.
        #[arg(long)]
        status: bool,
    },
    /// Run a shell command with optional RTK-style compression
    Run {
        /// Command to run (e.g., "git status", "cargo test")
        command: Vec<String>,
        /// Enable compression (RTK-style)
        #[arg(long)]
        compress: bool,
    },
    /// Run community detection to identify code clusters
    DetectClusters {
        /// Path to the project (default: current directory)
        #[arg(long)]
        path: Option<String>,
        /// Minimum edges for a node to be considered a hub
        #[arg(long, default_value = "5")]
        min_hub_edges: usize,
    },
    /// Start the REST API server
    ApiServe {
        /// Port to listen on (default: 8081)
        #[arg(long, default_value = "8081")]
        port: u16,
        /// Require API key authentication
        #[arg(long)]
        auth: bool,
    },
    /// Manage API keys for REST API access
    ApiKey {
        #[command(subcommand)]
        command: ApiKeyCommand,
    },
    /// OAuth2-style access-token auth management (accounts, tokens)
    Auth {
        #[command(subcommand)]
        command: AuthCommand,
    },
    /// Obsidian vault sync commands
    Obsidian {
        #[command(subcommand)]
        command: ObsidianCommand,
    },
    /// Show context metrics (token savings, usage stats)
    Metrics {
        /// Show metrics from the last N days (e.g., 7d, 30d)
        #[arg(long)]
        since: Option<String>,
        /// Filter by tool name (e.g., search_code, get_context)
        #[arg(long)]
        tool: Option<String>,
        /// Output in JSON format
        #[arg(long, short)]
        json: bool,
        /// Show metrics for current session only
        #[arg(long)]
        session: bool,
        /// Reset all metrics
        #[arg(long)]
        reset: bool,
        /// Set retention period in days (for cleanup)
        #[arg(long)]
        retention: Option<i32>,
        /// Run cleanup to remove old metrics
        #[arg(long)]
        cleanup: bool,
        /// Seed test metrics data
        #[arg(long)]
        seed: bool,
    },
    /// Update LeanKG to the latest version from GitHub releases
    Update,
    /// Manage LeanKG and Vite processes
    Proc {
        #[command(subcommand)]
        command: ProcCommand,
    },
    /// Manage incidents in the knowledge graph
    Incident {
        #[command(subcommand)]
        command: IncidentCommand,
    },
    /// Add a team note to a service or element
    Note {
        /// Target service or element qualified name
        #[arg(long)]
        target: String,
        /// Note content
        #[arg(long)]
        content: String,
        /// Environment
        #[arg(long, default_value = "local")]
        env: String,
    },
    /// Add a known risky pattern annotation
    Pattern {
        /// Pattern title
        #[arg(long)]
        title: String,
        /// Pattern context (code/config pattern description)
        #[arg(long)]
        context: String,
        /// Solution or prevention
        #[arg(long)]
        solution: String,
        /// Environment
        #[arg(long, default_value = "local")]
        env: String,
    },
    /// Show environment conflicts for a service
    EnvConflicts {
        /// Service name
        #[arg(long)]
        service: String,
    },
    /// Push local graph deltas to a shared LeanKG server
    Push {
        /// Remote server URL (e.g., https://leankg.internal)
        #[arg(long)]
        remote: String,
        /// Team token
        #[arg(long)]
        token: String,
        /// Environment
        #[arg(long, default_value = "local")]
        env: String,
    },
    /// Pull latest graph state from a shared LeanKG server
    Pull {
        /// Remote server URL
        #[arg(long)]
        remote: String,
        /// Team token
        #[arg(long)]
        token: String,
        /// Environment to pull
        #[arg(long, default_value = "production")]
        env: String,
    },
    /// Team management commands
    Team {
        #[command(subcommand)]
        command: TeamCommand,
    },
    /// Ontology management commands (semantic search layer)
    Ontology {
        #[command(subcommand)]
        command: OntologyCommand,
    },
}

#[derive(Subcommand, Debug)]
pub enum ApiKeyCommand {
    /// Create a new API key
    Create {
        /// Name for the API key
        #[arg(long)]
        name: String,
    },
    /// List all API keys
    List,
    /// Revoke an API key
    Revoke {
        /// ID of the API key to revoke
        #[arg(long)]
        id: String,
    },
}

#[derive(Subcommand, Debug)]
pub enum AuthCommand {
    /// Register an account (creates a bootstrap org owned by it)
    Register {
        #[arg(long)]
        email: String,
        #[arg(long)]
        password: String,
        #[arg(long)]
        name: String,
    },
    /// Issue an access token for an account
    Token {
        #[arg(long)]
        account_id: String,
        #[arg(long)]
        name: String,
        #[arg(long, default_value = "viewer")]
        role: String,
        #[arg(long)]
        org_id: Option<String>,
    },
    /// List access tokens for an account
    ListTokens {
        #[arg(long)]
        account_id: String,
    },
    /// Revoke an access token by id
    Revoke {
        #[arg(long)]
        token_id: String,
    },
}

#[derive(Subcommand, Debug)]
pub enum ObsidianCommand {
    /// Initialize Obsidian vault structure
    Init {
        /// Custom vault path (default: .leankg/obsidian/vault)
        #[arg(long)]
        vault: Option<String>,
    },
    /// Push LeanKG data to Obsidian notes
    Push {
        /// Custom vault path (default: .leankg/obsidian/vault)
        #[arg(long)]
        vault: Option<String>,
    },
    /// Pull annotation edits from Obsidian to LeanKG
    Pull {
        /// Custom vault path (default: .leankg/obsidian/vault)
        #[arg(long)]
        vault: Option<String>,
    },
    /// Watch Obsidian vault for changes and auto-pull
    Watch {
        /// Custom vault path (default: .leankg/obsidian/vault)
        #[arg(long)]
        vault: Option<String>,
        /// Debounce delay in milliseconds (default: 1000)
        #[arg(long, default_value = "1000")]
        debounce_ms: u64,
    },
    /// Show vault status
    Status {
        /// Custom vault path (default: .leankg/obsidian/vault)
        #[arg(long)]
        vault: Option<String>,
    },
}

#[derive(Subcommand, Debug)]
pub enum ProcCommand {
    /// Show running LeanKG and Vite processes
    Status,
    /// Kill all LeanKG and Vite processes
    Kill,
}

#[derive(Subcommand, Debug)]
pub enum IncidentCommand {
    /// Add a new incident
    Add {
        /// Incident title
        #[arg(long)]
        title: String,
        /// Severity: P0, P1, P2, P3
        #[arg(long)]
        severity: String,
        /// Affected service(s), comma-separated
        #[arg(long)]
        affected: String,
        /// Root cause description
        #[arg(long)]
        root_cause: String,
        /// Resolution description
        #[arg(long)]
        resolution: String,
        /// Prevention advice
        #[arg(long)]
        prevention: Option<String>,
        /// Environment
        #[arg(long, default_value = "production")]
        env: String,
        /// Linked ticket ID
        #[arg(long)]
        ticket: Option<String>,
    },
    /// List incidents for a service
    List {
        /// Service name
        #[arg(long)]
        service: String,
        /// Environment
        #[arg(long, default_value = "production")]
        env: String,
        /// Search pattern
        #[arg(long)]
        pattern: Option<String>,
        /// Limit results
        #[arg(long, default_value = "10")]
        limit: usize,
    },
    /// Show a single incident
    Show {
        /// Incident ID
        id: String,
    },
}

#[derive(Subcommand, Debug)]
pub enum TeamCommand {
    /// Create a new team
    Create {
        /// Team name
        #[arg(long)]
        name: String,
        /// Team description
        #[arg(long)]
        description: String,
        /// Owner user ID
        #[arg(long)]
        owner: String,
    },
    /// List all teams
    List,
    /// Show team details
    Show {
        /// Team ID
        id: String,
    },
    /// Update team information
    Update {
        /// Team ID
        #[arg(long)]
        id: String,
        /// New name (optional)
        #[arg(long)]
        name: Option<String>,
        /// New description (optional)
        #[arg(long)]
        description: Option<String>,
    },
    /// Delete a team
    Delete {
        /// Team ID
        #[arg(long)]
        id: String,
    },
    /// Add member to team
    AddMember {
        /// Team ID
        #[arg(long)]
        team: String,
        /// User ID to add
        #[arg(long)]
        user: String,
        /// Role: admin, contributor, viewer
        #[arg(long, default_value = "viewer")]
        role: String,
    },
    /// Remove member from team
    RemoveMember {
        /// Team ID
        #[arg(long)]
        team: String,
        /// User ID to remove
        #[arg(long)]
        user: String,
    },
    /// Generate invite link for team
    Invite {
        /// Team ID
        #[arg(long)]
        team: String,
        /// Role for invitee
        #[arg(long, default_value = "viewer")]
        role: String,
        /// Email for invitee (optional)
        #[arg(long)]
        email: Option<String>,
        /// Invite expiration in hours (default: 48)
        #[arg(long, default_value = "48")]
        expires_hours: u64,
    },
    /// Accept team invite
    Accept {
        /// Invite token
        #[arg(long)]
        token: String,
        /// User ID accepting invite
        #[arg(long)]
        user: String,
    },
    /// List pending invites for team
    Invites {
        /// Team ID
        #[arg(long)]
        team: String,
    },
    /// Revoke team invite
    RevokeInvite {
        /// Invite token
        #[arg(long)]
        token: String,
    },
    /// Set graph read permissions for team
    SetReadUsers {
        /// Team ID
        #[arg(long)]
        team: String,
        /// Comma-separated list of user IDs
        #[arg(long)]
        users: String,
    },
    /// Set graph write permissions for team
    SetWriteUsers {
        /// Team ID
        #[arg(long)]
        team: String,
        /// Comma-separated list of user IDs
        #[arg(long)]
        users: String,
    },
    /// Check if user has permission
    CheckPermission {
        /// Team ID
        #[arg(long)]
        team: String,
        /// User ID to check
        #[arg(long)]
        user: String,
        /// Require write permission
        #[arg(long)]
        write: bool,
    },
}

#[derive(Subcommand, Debug)]
pub enum OntologyCommand {
    /// Validate ontology YAML files
    Validate,
    /// Sync ontology from YAML files into the graph
    Sync {
        /// Path to ontology directory (default: ./ontology)
        #[arg(long)]
        path: Option<String>,
    },
    /// Show ontology status and coverage
    Status,
    /// Get ontology context for a semantic query
    Context {
        /// Query string
        query: String,
        /// Environment
        #[arg(long, default_value = "local")]
        env: String,
        /// Expansion depth
        #[arg(long, default_value = "2")]
        depth: u32,
    },
    /// Get concept map for a domain or service
    ConceptMap {
        /// Concept or service name
        query: String,
        /// Environment
        #[arg(long, default_value = "local")]
        env: String,
    },
    /// Trace a workflow's ordered steps
    TraceWorkflow {
        /// Workflow name or ID
        workflow_id_or_query: String,
        /// Environment
        #[arg(long, default_value = "local")]
        env: String,
    },
    /// Concept-gated search: extract keywords -> scan concept ontology ->
    /// load concept -> query the LeanKG DB for the actual code.
    ConceptSearch {
        /// Raw natural-language or concept query (e.g. "feature flag", "gorm store")
        query: String,
        /// Environment scope for the ontology scan
        #[arg(long, default_value = "local")]
        env: String,
        /// Maximum number of concepts / code results
        #[arg(long, default_value_t = 20)]
        limit: usize,
    },
}
