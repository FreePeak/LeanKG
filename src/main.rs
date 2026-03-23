use clap::Parser;

#[derive(Parser, Debug)]
#[command(name = "leankg")]
#[command(about = "Lightweight knowledge graph for AI-assisted development")]
struct Args {
    #[command(subcommand)]
    command: Command,
}

#[derive(Parser, Debug)]
enum Command {
    Init,
    Index { path: Option<String> },
    Query { query: String },
    Serve,
}

fn main() {
    println!("LeanKG v0.1.0");
}
