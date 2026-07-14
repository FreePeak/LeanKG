use clap::Subcommand;

pub mod shell_runner;

#[derive(Subcommand, Debug)]
pub enum CLICommand {
    /// Show LeanKG version
    Version,
    /// Initialize a new LeanKG project
    Init {
        #[arg(long, default_value = ".leankg")]
        path: String,
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
    },
    /// Query the knowledge graph
    Query {
        /// Query string
        query: String,
        /// Query type: name, type, rel, pattern, or content
        /// (content does case-insensitive substring match across name, qualified_name, and file_path)
        #[arg(long, default_value = "name")]
        kind: String,
        /// Find elements in a specific file path (substring match)
        #[arg(long)]
        file: Option<String>,
        /// Find functions by name (substring match)
        #[arg(long)]
        function: Option<String>,
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
    },
    /// Start the embedded web UI server
    Web {
        /// Port to listen on (default: from PORT env var or 8080)
        #[arg(long)]
        port: Option<u16>,
    },
    /// Start MCP server with stdio transport (for opencode integration)
    McpStdio {
        /// Enable auto-indexing with file watcher
        #[arg(long)]
        watch: bool,
    },
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
    },
    /// Calculate impact radius
    Impact {
        /// File to analyze
        file: String,
        /// Depth of analysis
        #[arg(long, default_value = "3")]
        depth: u32,
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
    /// US-MP-05: Check graph for broken / stale links
    CheckConsistency {
        /// Filter by severity: BROKEN | STALE | CURRENT
        #[arg(long)]
        severity: Option<String>,
    },
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
    /// US-CBM-B7: Find near-duplicate functions / methods
    Clones {
        /// Similarity threshold (0.0 - 1.0)
        #[arg(long, default_value = "0.6")]
        threshold: f64,
        /// Limit
        #[arg(long, default_value = "50")]
        limit: usize,
    },
    /// Auto-install MCP config
    Install,
    /// Show index status
    Status,
    /// Start file watcher for incremental re-indexing
    Watch {
        /// Path to watch (default: project root)
        #[arg(long)]
        path: Option<String>,
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
        /// Override the embedding batch size (default 32). Lower this on
        /// memory-constrained hosts.
        #[arg(long, default_value = "32")]
        batch_size: usize,
        /// Project root (defaults to current working directory).
        #[arg(long, default_value = ".")]
        project: String,
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
    /// Export knowledge graph
    Export {
        /// Output file path
        #[arg(long, default_value = "graph.json")]
        output: String,
        /// Export format: json, dot, or mermaid
        #[arg(long, default_value = "json")]
        format: String,
        /// Scope export to a specific file's subgraph
        #[arg(long)]
        file: Option<String>,
        /// Max depth for subgraph traversal (used with --file)
        #[arg(long, default_value = "3")]
        depth: u32,
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
        /// CLI tool to use: opencode, gemini, or kilo (default: kilo)
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
    /// Global setup: configure MCP for all registered repos at once, install Claude hooks and register plugin
    Setup {},
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
