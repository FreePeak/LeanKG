use clap::Subcommand;

#[derive(Subcommand, Debug)]
pub enum CLICommand {
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
    Serve,
    /// Start the embedded web UI server
    Web,
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
    /// Start file watcher
    Watch,
    /// Find oversized functions
    Quality {
        /// Minimum line count (default: 50)
        #[arg(long, default_value = "50")]
        min_lines: u32,
        /// Filter by language
        #[arg(long)]
        lang: Option<String>,
    },
    /// Export graph as HTML
    Export {
        #[arg(long, default_value = "graph.html")]
        output: String,
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
