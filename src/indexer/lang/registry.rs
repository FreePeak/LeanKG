//! Single source of truth for language support: extension ↔ canonical name ↔
//! tree-sitter grammar ↔ node-kind patterns. Replaces the five parallel
//! fixed-size dispatch tables that previously lived in mod.rs / parser.rs /
//! extractor.rs / call_graph.rs.

use tree_sitter::{Language, Parser};

// MARKER: tree-sitter grammar imports below for LANG_SPECS static.

#[allow(unused_imports)]
use tree_sitter_bash;
#[allow(unused_imports)]
use tree_sitter_c;
#[allow(unused_imports)]
use tree_sitter_c_sharp;
#[allow(unused_imports)]
use tree_sitter_cpp;
#[allow(unused_imports)]
use tree_sitter_crystal;
#[allow(unused_imports)]
use tree_sitter_css;
#[allow(unused_imports)]
use tree_sitter_elixir;
#[allow(unused_imports)]
use tree_sitter_elm;
#[allow(unused_imports)]
use tree_sitter_erlang;
#[allow(unused_imports)]
use tree_sitter_fsharp;
#[allow(unused_imports)]
use tree_sitter_graphql;
#[allow(unused_imports)]
use tree_sitter_haskell;
#[allow(unused_imports)]
use tree_sitter_html;
#[allow(unused_imports)]
use tree_sitter_json;
#[allow(unused_imports)]
use tree_sitter_lua;
#[allow(unused_imports)]
use tree_sitter_nim;
#[allow(unused_imports)]
use tree_sitter_ocaml;
#[allow(unused_imports)]
use tree_sitter_perl;
#[allow(unused_imports)]
use tree_sitter_php;
#[allow(unused_imports)]
use tree_sitter_powershell;
#[allow(unused_imports)]
use tree_sitter_proto;
#[allow(unused_imports)]
use tree_sitter_r;
#[allow(unused_imports)]
use tree_sitter_ruby;
#[allow(unused_imports)]
use tree_sitter_scala;
#[allow(unused_imports)]
use tree_sitter_solidity;
#[allow(unused_imports)]
use tree_sitter_yaml;
#[allow(unused_imports)]
use tree_sitter_zig;