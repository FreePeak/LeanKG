use clap::Parser;
use leankg::cli::CLICommand;

#[derive(Parser)]
struct TestArgs {
    #[command(subcommand)]
    command: CLICommand,
}

#[test]
fn test_cli_init_default_path() {
    let args = TestArgs::try_parse_from(["leankg", "init"]).unwrap();
    match args.command {
        CLICommand::Init { path } => assert_eq!(path, ".leankg"),
        _ => panic!("expected Init command"),
    }
}

#[test]
fn test_cli_init_custom_path() {
    let args = TestArgs::try_parse_from(["leankg", "init", "--path", "/custom/path"]).unwrap();
    match args.command {
        CLICommand::Init { path } => assert_eq!(path, "/custom/path"),
        _ => panic!("expected Init command"),
    }
}

#[test]
fn test_cli_index_no_args() {
    let args = TestArgs::try_parse_from(["leankg", "index"]).unwrap();
    match args.command {
        CLICommand::Index {
            path,
            incremental,
            lang,
            exclude,
            verbose,
        } => {
            assert!(path.is_none());
            assert!(!incremental);
            assert!(lang.is_none());
            assert!(exclude.is_none());
            assert!(!verbose);
        }
        _ => panic!("expected Index command"),
    }
}

#[test]
fn test_cli_index_with_path() {
    let args = TestArgs::try_parse_from(["leankg", "index", "./src"]).unwrap();
    match args.command {
        CLICommand::Index {
            path,
            incremental,
            lang,
            exclude,
            verbose,
        } => {
            assert_eq!(path.as_deref(), Some("./src"));
            assert!(!incremental);
            assert!(lang.is_none());
            assert!(exclude.is_none());
            assert!(!verbose);
        }
        _ => panic!("expected Index command"),
    }
}

#[test]
fn test_cli_index_incremental() {
    let args = TestArgs::try_parse_from(["leankg", "index", "--incremental"]).unwrap();
    match args.command {
        CLICommand::Index {
            path,
            incremental,
            lang,
            exclude,
            verbose,
        } => {
            assert!(path.is_none());
            assert!(incremental);
            assert!(lang.is_none());
            assert!(exclude.is_none());
            assert!(!verbose);
        }
        _ => panic!("expected Index command"),
    }
}

#[test]
fn test_cli_index_lang() {
    let args = TestArgs::try_parse_from(["leankg", "index", "--lang", "rust"]).unwrap();
    match args.command {
        CLICommand::Index {
            path,
            incremental,
            lang,
            exclude,
            verbose,
        } => {
            assert!(path.is_none());
            assert!(!incremental);
            assert_eq!(lang.as_deref(), Some("rust"));
            assert!(exclude.is_none());
            assert!(!verbose);
        }
        _ => panic!("expected Index command"),
    }
}

#[test]
fn test_cli_index_exclude() {
    let args = TestArgs::try_parse_from(["leankg", "index", "--exclude", "vendor,target"]).unwrap();
    match args.command {
        CLICommand::Index {
            path,
            incremental,
            lang,
            exclude,
            verbose,
        } => {
            assert!(path.is_none());
            assert!(!incremental);
            assert!(lang.is_none());
            assert_eq!(exclude.as_deref(), Some("vendor,target"));
            assert!(!verbose);
        }
        _ => panic!("expected Index command"),
    }
}

#[test]
fn test_cli_index_verbose() {
    let args = TestArgs::try_parse_from(["leankg", "index", "--verbose"]).unwrap();
    match args.command {
        CLICommand::Index {
            path,
            incremental,
            lang,
            exclude,
            verbose,
        } => {
            assert!(path.is_none());
            assert!(!incremental);
            assert!(lang.is_none());
            assert!(exclude.is_none());
            assert!(verbose);
        }
        _ => panic!("expected Index command"),
    }
}

#[test]
fn test_cli_index_all_options() {
    let args = TestArgs::try_parse_from([
        "leankg",
        "index",
        "./src",
        "--incremental",
        "--lang",
        "go",
        "--exclude",
        "vendor",
        "--verbose",
    ])
    .unwrap();
    match args.command {
        CLICommand::Index {
            path,
            incremental,
            lang,
            exclude,
            verbose,
        } => {
            assert_eq!(path.as_deref(), Some("./src"));
            assert!(incremental);
            assert_eq!(lang.as_deref(), Some("go"));
            assert_eq!(exclude.as_deref(), Some("vendor"));
            assert!(verbose);
        }
        _ => panic!("expected Index command"),
    }
}

#[test]
fn test_cli_query_basic() {
    let args = TestArgs::try_parse_from(["leankg", "query", "find_foo"]).unwrap();
    match args.command {
        CLICommand::Query { query, kind } => {
            assert_eq!(query, "find_foo");
            assert_eq!(kind, "name");
        }
        _ => panic!("expected Query command"),
    }
}

#[test]
fn test_cli_query_with_kind() {
    let args = TestArgs::try_parse_from(["leankg", "query", "find_foo", "--kind", "type"]).unwrap();
    match args.command {
        CLICommand::Query { query, kind } => {
            assert_eq!(query, "find_foo");
            assert_eq!(kind, "type");
        }
        _ => panic!("expected Query command"),
    }
}

#[test]
fn test_cli_generate_no_template() {
    let args = TestArgs::try_parse_from(["leankg", "generate"]).unwrap();
    match args.command {
        CLICommand::Generate { template } => {
            assert!(template.is_none());
        }
        _ => panic!("expected Generate command"),
    }
}

#[test]
fn test_cli_generate_with_template() {
    let args = TestArgs::try_parse_from(["leankg", "generate", "--template", "api"]).unwrap();
    match args.command {
        CLICommand::Generate { template } => {
            assert_eq!(template.as_deref(), Some("api"));
        }
        _ => panic!("expected Generate command"),
    }
}

#[test]
fn test_cli_serve() {
    let args = TestArgs::try_parse_from(["leankg", "serve"]).unwrap();
    match args.command {
        CLICommand::Serve { .. } => {}
        _ => panic!("expected Serve command"),
    }
}

#[test]
fn test_cli_impact_basic() {
    let args = TestArgs::try_parse_from(["leankg", "impact", "src/main.rs"]).unwrap();
    match args.command {
        CLICommand::Impact { file, depth } => {
            assert_eq!(file, "src/main.rs");
            assert_eq!(depth, 3);
        }
        _ => panic!("expected Impact command"),
    }
}

#[test]
fn test_cli_impact_custom_depth() {
    let args =
        TestArgs::try_parse_from(["leankg", "impact", "src/main.rs", "--depth", "5"]).unwrap();
    match args.command {
        CLICommand::Impact { file, depth } => {
            assert_eq!(file, "src/main.rs");
            assert_eq!(depth, 5);
        }
        _ => panic!("expected Impact command"),
    }
}

#[test]
fn test_cli_quality_defaults() {
    let args = TestArgs::try_parse_from(["leankg", "quality"]).unwrap();
    match args.command {
        CLICommand::Quality { min_lines, lang } => {
            assert_eq!(min_lines, 50);
            assert!(lang.is_none());
        }
        _ => panic!("expected Quality command"),
    }
}

#[test]
fn test_cli_quality_custom_min_lines() {
    let args = TestArgs::try_parse_from(["leankg", "quality", "--min-lines", "100"]).unwrap();
    match args.command {
        CLICommand::Quality { min_lines, lang } => {
            assert_eq!(min_lines, 100);
            assert!(lang.is_none());
        }
        _ => panic!("expected Quality command"),
    }
}

#[test]
fn test_cli_quality_with_lang() {
    let args = TestArgs::try_parse_from(["leankg", "quality", "--lang", "rust"]).unwrap();
    match args.command {
        CLICommand::Quality { min_lines, lang } => {
            assert_eq!(min_lines, 50);
            assert_eq!(lang.as_deref(), Some("rust"));
        }
        _ => panic!("expected Quality command"),
    }
}

#[test]
fn test_cli_quality_all_options() {
    let args = TestArgs::try_parse_from(["leankg", "quality", "--min-lines", "75", "--lang", "go"])
        .unwrap();
    match args.command {
        CLICommand::Quality { min_lines, lang } => {
            assert_eq!(min_lines, 75);
            assert_eq!(lang.as_deref(), Some("go"));
        }
        _ => panic!("expected Quality command"),
    }
}

#[test]
fn test_cli_annotate_required_args() {
    let args = TestArgs::try_parse_from([
        "leankg",
        "annotate",
        "src/main.rs::main",
        "-d",
        "Entry point",
    ])
    .unwrap();
    match args.command {
        CLICommand::Annotate {
            element,
            description,
            user_story,
            feature,
        } => {
            assert_eq!(element, "src/main.rs::main");
            assert_eq!(description, "Entry point");
            assert!(user_story.is_none());
            assert!(feature.is_none());
        }
        _ => panic!("expected Annotate command"),
    }
}

#[test]
fn test_cli_annotate_with_user_story() {
    let args = TestArgs::try_parse_from([
        "leankg",
        "annotate",
        "src/main.rs::main",
        "-d",
        "Entry point",
        "--user-story",
        "US-001",
    ])
    .unwrap();
    match args.command {
        CLICommand::Annotate {
            element,
            description,
            user_story,
            feature,
        } => {
            assert_eq!(element, "src/main.rs::main");
            assert_eq!(description, "Entry point");
            assert_eq!(user_story.as_deref(), Some("US-001"));
            assert!(feature.is_none());
        }
        _ => panic!("expected Annotate command"),
    }
}

#[test]
fn test_cli_annotate_with_feature() {
    let args = TestArgs::try_parse_from([
        "leankg",
        "annotate",
        "src/main.rs::main",
        "-d",
        "Entry point",
        "--feature",
        "F-001",
    ])
    .unwrap();
    match args.command {
        CLICommand::Annotate {
            element,
            description,
            user_story,
            feature,
        } => {
            assert_eq!(element, "src/main.rs::main");
            assert_eq!(description, "Entry point");
            assert!(user_story.is_none());
            assert_eq!(feature.as_deref(), Some("F-001"));
        }
        _ => panic!("expected Annotate command"),
    }
}

#[test]
fn test_cli_annotate_all_args() {
    let args = TestArgs::try_parse_from([
        "leankg",
        "annotate",
        "src/main.rs::main",
        "-d",
        "Entry point",
        "--user-story",
        "US-001",
        "--feature",
        "F-001",
    ])
    .unwrap();
    match args.command {
        CLICommand::Annotate {
            element,
            description,
            user_story,
            feature,
        } => {
            assert_eq!(element, "src/main.rs::main");
            assert_eq!(description, "Entry point");
            assert_eq!(user_story.as_deref(), Some("US-001"));
            assert_eq!(feature.as_deref(), Some("F-001"));
        }
        _ => panic!("expected Annotate command"),
    }
}

#[test]
fn test_cli_link_defaults() {
    let args = TestArgs::try_parse_from(["leankg", "link", "src/main.rs::main", "US-001"]).unwrap();
    match args.command {
        CLICommand::Link { element, id, kind } => {
            assert_eq!(element, "src/main.rs::main");
            assert_eq!(id, "US-001");
            assert_eq!(kind, "story");
        }
        _ => panic!("expected Link command"),
    }
}

#[test]
fn test_cli_link_custom_kind() {
    let args = TestArgs::try_parse_from([
        "leankg",
        "link",
        "src/main.rs::main",
        "F-001",
        "--kind",
        "feature",
    ])
    .unwrap();
    match args.command {
        CLICommand::Link { element, id, kind } => {
            assert_eq!(element, "src/main.rs::main");
            assert_eq!(id, "F-001");
            assert_eq!(kind, "feature");
        }
        _ => panic!("expected Link command"),
    }
}

#[test]
fn test_cli_search_annotations() {
    let args =
        TestArgs::try_parse_from(["leankg", "search-annotations", "authentication"]).unwrap();
    match args.command {
        CLICommand::SearchAnnotations { query } => {
            assert_eq!(query, "authentication");
        }
        _ => panic!("expected SearchAnnotations command"),
    }
}

#[test]
fn test_cli_show_annotations() {
    let args =
        TestArgs::try_parse_from(["leankg", "show-annotations", "src/auth.rs::login"]).unwrap();
    match args.command {
        CLICommand::ShowAnnotations { element } => {
            assert_eq!(element, "src/auth.rs::login");
        }
        _ => panic!("expected ShowAnnotations command"),
    }
}

#[test]
fn test_cli_trace_all() {
    let args = TestArgs::try_parse_from(["leankg", "trace", "--all"]).unwrap();
    match args.command {
        CLICommand::Trace {
            feature,
            user_story,
            all,
        } => {
            assert!(feature.is_none());
            assert!(user_story.is_none());
            assert!(all);
        }
        _ => panic!("expected Trace command"),
    }
}

#[test]
fn test_cli_trace_by_feature() {
    let args = TestArgs::try_parse_from(["leankg", "trace", "--feature", "F-001"]).unwrap();
    match args.command {
        CLICommand::Trace {
            feature,
            user_story,
            all,
        } => {
            assert_eq!(feature.as_deref(), Some("F-001"));
            assert!(user_story.is_none());
            assert!(!all);
        }
        _ => panic!("expected Trace command"),
    }
}

#[test]
fn test_cli_trace_by_user_story() {
    let args = TestArgs::try_parse_from(["leankg", "trace", "--user-story", "US-001"]).unwrap();
    match args.command {
        CLICommand::Trace {
            feature,
            user_story,
            all,
        } => {
            assert!(feature.is_none());
            assert_eq!(user_story.as_deref(), Some("US-001"));
            assert!(!all);
        }
        _ => panic!("expected Trace command"),
    }
}

#[test]
fn test_cli_find_by_domain() {
    let args = TestArgs::try_parse_from(["leankg", "find-by-domain", "authentication"]).unwrap();
    match args.command {
        CLICommand::FindByDomain { domain } => {
            assert_eq!(domain, "authentication");
        }
        _ => panic!("expected FindByDomain command"),
    }
}

#[test]
fn test_cli_install() {
    let args = TestArgs::try_parse_from(["leankg", "install"]).unwrap();
    match args.command {
        CLICommand::Install => {}
        _ => panic!("expected Install command"),
    }
}

#[test]
fn test_cli_status() {
    let args = TestArgs::try_parse_from(["leankg", "status"]).unwrap();
    match args.command {
        CLICommand::Status => {}
        _ => panic!("expected Status command"),
    }
}

#[test]
fn test_cli_watch() {
    let args = TestArgs::try_parse_from(["leankg", "watch"]).unwrap();
    match args.command {
        CLICommand::Watch { .. } => {}
        _ => panic!("expected Watch command"),
    }
}

#[test]
fn test_cli_export_defaults() {
    let args = TestArgs::try_parse_from(["leankg", "export"]).unwrap();
    match args.command {
        CLICommand::Export { output, .. } => {
            assert_eq!(output, "graph.html");
        }
        _ => panic!("expected Export command"),
    }
}

#[test]
fn test_cli_export_custom_output() {
    let args = TestArgs::try_parse_from(["leankg", "export", "--output", "custom.html"]).unwrap();
    match args.command {
        CLICommand::Export { output, .. } => {
            assert_eq!(output, "custom.html");
        }
        _ => panic!("expected Export command"),
    }
}
