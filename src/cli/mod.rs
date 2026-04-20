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
    },
    /// Query the knowledge graph
    Query {
        /// Query string
        query: String,
        /// Query type: name, type, rel, pattern
        #[arg(long, default_value = "name")]
        kind: String,
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
    /// Calculate impact radius
    Impact {
        /// File to analyze
        file: String,
        /// Depth of analysis
        #[arg(long, default_value = "3")]
        depth: u32,
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
    /// Global setup: configure MCP for all registered repos at once
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
