//! H11: git-committable Markdown graph docs (`leankg export --markdown`).
//!
//! Produces a deterministic Markdown document describing the indexed graph:
//!
//! 1. Front matter (`title`, `project`, `generated_at`) — `generated_at` is
//!    the ONLY timestamp; every other byte is a pure function of DB state.
//! 2. `## Overview` — element/relationship totals and counts by type.
//! 3. `## Top Clusters` — id, label, member count.
//! 4. `## God Nodes (top 10 by degree)` — degree + source-linked symbols.
//! 5. `## Architecture Tree` — directories → files → elements (depth cap 4).
//! 6. `## Cluster Details` — SKILL-style blocks per cluster.
//!
//! Determinism contract: same DB state ⇒ byte-identical document once the
//! single `generated_at:` front-matter line is ignored. Every collection is
//! explicitly sorted; HashMap iteration order never reaches the output.

use crate::db::models::{CodeElement, Relationship};
use crate::graph::query::GraphEngine;
use std::collections::BTreeMap;

/// Max clusters rendered in the Top Clusters table + detail blocks.
const MAX_CLUSTERS: usize = 20;
/// Max god nodes rendered (spec: top-10 by degree).
const MAX_GOD_NODES: usize = 10;
/// Max directory nesting levels rendered in the architecture tree.
const MAX_TREE_DEPTH: usize = 4;
/// Max member symbols listed per cluster detail block.
const MAX_CLUSTER_MEMBERS_LISTED: usize = 10;

/// Everything the exporter needs, collected once from the graph engine.
pub struct MarkdownDoc {
    pub project: String,
    pub generated_at_utc: String,
    pub elements: Vec<CodeElement>,
    pub relationships: Vec<Relationship>,
    /// Top god nodes by degree (already limited upstream; re-sorted on render).
    pub god_nodes: Vec<crate::graph::query::GodNode>,
    pub clusters: Vec<crate::graph::clustering::Cluster>,
}

impl MarkdownDoc {
    /// Collect inputs from an open engine; wall-clock UTC front matter.
    pub fn collect(
        engine: &GraphEngine,
        project: &str,
    ) -> Result<Self, Box<dyn std::error::Error>> {
        Self::collect_with_timestamp(engine, project, &now_rfc3339_utc())
    }

    /// Determinism seam: same engine data + explicit timestamp.
    pub fn collect_with_timestamp(
        engine: &GraphEngine,
        project: &str,
        generated_at_utc: &str,
    ) -> Result<Self, Box<dyn std::error::Error>> {
        let elements = engine.all_elements()?;
        let relationships = engine.all_relationships()?;
        let god_nodes = engine.get_god_nodes(MAX_GOD_NODES, None)?;
        let clusters = collect_clusters(engine, &elements)?;

        Ok(Self {
            project: project.to_string(),
            generated_at_utc: generated_at_utc.to_string(),
            elements,
            relationships,
            god_nodes,
            clusters,
        })
    }
}

/// Clusters for the docs: prefer DB-precomputed `cluster_id` rows (stable),
/// else fall back to a deterministic folder grouping. Live Louvain assigns
/// `cluster_N` ids in HashMap iteration order — not stable across processes,
/// so it is unusable for a byte-deterministic artifact.
fn collect_clusters(
    engine: &GraphEngine,
    elements: &[CodeElement],
) -> Result<Vec<crate::graph::clustering::Cluster>, Box<dyn std::error::Error>> {
    if let Ok((clusters, _)) =
        crate::graph::clustering::load_precomputed_clusters(engine, MAX_CLUSTERS)
    {
        if !clusters.is_empty() {
            return Ok(clusters);
        }
    }
    Ok(folder_clusters(elements))
}

/// Deterministic folder-based clustering: group elements by parent directory.
/// IDs derive from the folder path itself, so they are stable forever.
fn folder_clusters(elements: &[CodeElement]) -> Vec<crate::graph::clustering::Cluster> {
    use crate::graph::clustering::Cluster;

    let mut by_folder: BTreeMap<String, Vec<String>> = BTreeMap::new();
    for e in elements {
        let folder = match e.file_path.rfind('/') {
            Some(i) => e.file_path[..i].to_string(),
            None => "root".to_string(),
        };
        by_folder
            .entry(folder)
            .or_default()
            .push(e.qualified_name.clone());
    }

    by_folder
        .into_iter()
        .map(|(folder, mut members)| {
            members.sort();
            let label = folder.rsplit('/').next().unwrap_or(&folder).to_string();
            Cluster {
                id: format!("dir:{folder}"),
                label,
                members,
                representative_files: vec![folder.clone()],
            }
        })
        .collect()
}

/// Outcome of a CLI markdown export run.
pub struct ExportMarkdownResult {
    pub path: std::path::PathBuf,
    pub elements: usize,
    pub relationships: usize,
    pub clusters: usize,
}

/// CLI entry point (`leankg export --markdown`): collect from the project's
/// DB, render, and write the artifact. Relative `out_path`s resolve against
/// `project_root` (mirrors the MCP `resolve_out_path` anchoring rule);
/// the default destination is `<project>/.leankg/graph-docs.md`.
pub fn run_export_markdown(
    project_root: &std::path::Path,
    out_path: Option<&str>,
) -> Result<ExportMarkdownResult, Box<dyn std::error::Error>> {
    let db_path = project_root.join(".leankg");
    if !db_path.exists() {
        return Err("LeanKG not initialized. Run 'leankg init' and 'leankg index' first.".into());
    }
    let db = crate::db::backend::init_db(&db_path)?;
    let engine = GraphEngine::new(db);

    let project_name = project_root
        .file_name()
        .map(|s| s.to_string_lossy().to_string())
        .unwrap_or_else(|| project_root.display().to_string());
    let doc = MarkdownDoc::collect(&engine, &project_name)?;

    let out = match out_path {
        Some(p) => {
            let pb = std::path::PathBuf::from(p);
            if pb.is_absolute() {
                pb
            } else {
                project_root.join(pb)
            }
        }
        None => db_path.join("graph-docs.md"),
    };
    if let Some(parent) = out.parent() {
        if !parent.as_os_str().is_empty() {
            std::fs::create_dir_all(parent)?;
        }
    }

    std::fs::write(&out, MarkdownExporter::new().generate(&doc))?;
    Ok(ExportMarkdownResult {
        path: out,
        elements: doc.elements.len(),
        relationships: doc.relationships.len(),
        clusters: doc.clusters.len(),
    })
}

pub struct MarkdownExporter;

impl Default for MarkdownExporter {
    fn default() -> Self {
        Self::new()
    }
}

impl MarkdownExporter {
    pub fn new() -> Self {
        Self
    }

    /// Render the deterministic Markdown document.
    pub fn generate(&self, doc: &MarkdownDoc) -> String {
        let mut out = String::new();
        self.render_front_matter(&mut out, doc);
        self.render_overview(&mut out, doc);
        self.render_top_clusters(&mut out, doc);
        self.render_god_nodes(&mut out, doc);
        self.render_architecture_tree(&mut out, doc);
        self.render_cluster_details(&mut out, doc);
        out
    }

    // ------------------------------------------------------------------
    // Section renderers — every byte below front matter is a pure function
    // of `doc`'s graph state (sorted collections only).
    // ------------------------------------------------------------------

    fn render_front_matter(&self, out: &mut String, doc: &MarkdownDoc) {
        out.push_str("---\n");
        out.push_str("title: LeanKG Graph Docs\n");
        out.push_str(&format!("project: {}\n", escape_inline(&doc.project)));
        out.push_str(&format!("generated_at: {}\n", doc.generated_at_utc));
        out.push_str("---\n\n");
        out.push_str("# LeanKG Graph Docs\n\n");
    }

    fn render_overview(&self, out: &mut String, doc: &MarkdownDoc) {
        let mut by_type: BTreeMap<&str, usize> = BTreeMap::new();
        for e in &doc.elements {
            *by_type.entry(e.element_type.as_str()).or_default() += 1;
        }
        let mut rel_by_type: BTreeMap<&str, usize> = BTreeMap::new();
        for r in &doc.relationships {
            *rel_by_type.entry(r.rel_type.as_str()).or_default() += 1;
        }

        out.push_str("## Overview\n\n");
        out.push_str(&format!(
            "- Project: `{}`\n- Elements: {}\n- Relationships: {}\n- Clusters: {}\n\n",
            escape_inline(&doc.project),
            doc.elements.len(),
            doc.relationships.len(),
            doc.clusters.len(),
        ));

        out.push_str("### Elements by type\n\n");
        out.push_str("| Type | Count |\n|---|---|\n");
        for (t, c) in &by_type {
            out.push_str(&format!("| {t} | {c} |\n"));
        }
        out.push('\n');

        out.push_str("### Relationships by type\n\n");
        out.push_str("| Type | Count |\n|---|---|\n");
        for (t, c) in &rel_by_type {
            out.push_str(&format!("| {t} | {c} |\n"));
        }
        out.push('\n');
    }

    fn render_top_clusters(&self, out: &mut String, doc: &MarkdownDoc) {
        let ranked = ranked_clusters(doc);
        out.push_str("## Top Clusters\n\n");
        out.push_str("| ID | Label | Members |\n|---|---|---|\n");
        for c in ranked.iter().take(MAX_CLUSTERS) {
            out.push_str(&format!(
                "| `{}` | {} | {} |\n",
                c.id,
                escape_inline(&c.label),
                c.members.len()
            ));
        }
        if ranked.len() > MAX_CLUSTERS {
            out.push_str(&format!(
                "\n… and {} more clusters.\n",
                ranked.len() - MAX_CLUSTERS
            ));
        }
        out.push('\n');
    }

    fn render_god_nodes(&self, out: &mut String, doc: &MarkdownDoc) {
        let by_qn: BTreeMap<&str, (&str, u32)> = doc
            .elements
            .iter()
            .map(|e| {
                (
                    e.qualified_name.as_str(),
                    (e.file_path.as_str(), e.line_start),
                )
            })
            .collect();

        let mut nodes: Vec<&crate::graph::query::GodNode> = doc.god_nodes.iter().collect();
        nodes.sort_by(|a, b| {
            b.degree
                .cmp(&a.degree)
                .then_with(|| a.qualified_name.cmp(&b.qualified_name))
        });
        nodes.truncate(MAX_GOD_NODES);

        out.push_str(&format!("## God Nodes (top {MAX_GOD_NODES} by degree)\n\n"));
        out.push_str("| Degree | Qualified name | Type |\n|---|---|---|\n");
        for n in &nodes {
            let anchor = match by_qn.get(n.qualified_name.as_str()) {
                Some((file, line)) => format!("{}#L{line}", file.replace(' ', "%20")),
                None => String::new(),
            };
            let symbol = if anchor.is_empty() {
                format!("`{}`", n.qualified_name)
            } else {
                format!("[`{}`]({})", n.qualified_name, anchor)
            };
            out.push_str(&format!(
                "| {} | {} | {} |\n",
                n.degree, symbol, n.element_type
            ));
        }
        out.push('\n');
    }

    fn render_architecture_tree(&self, out: &mut String, doc: &MarkdownDoc) {
        out.push_str("## Architecture Tree\n\n");
        if doc.elements.is_empty() {
            out.push_str("_No indexed elements._\n\n");
            return;
        }

        // file_path -> sorted elements
        let mut files: BTreeMap<&str, Vec<&CodeElement>> = BTreeMap::new();
        for e in &doc.elements {
            files.entry(e.file_path.as_str()).or_default().push(e);
        }
        for els in files.values_mut() {
            els.sort_by(|a, b| a.qualified_name.cmp(&b.qualified_name));
        }
        // dir trie keyed by path segment; BTreeMap keeps child order stable.
        let mut root = TrieNode::default();
        for (path, els) in &files {
            let parts: Vec<&str> = path.split('/').collect();
            let mut node = &mut root;
            for seg in &parts[..parts.len() - 1] {
                node = node.dirs.entry((*seg).to_string()).or_default();
            }
            node.files.push(FileEntry {
                name: parts[parts.len() - 1],
                elements: els.clone(),
            });
        }
        root.sort();

        let mut lines: Vec<String> = Vec::new();
        render_trie(&root, 0, &mut lines);
        for l in &lines {
            out.push_str(l);
            out.push('\n');
        }
        out.push('\n');
    }

    fn render_cluster_details(&self, out: &mut String, doc: &MarkdownDoc) {
        let ranked = ranked_clusters(doc);
        if ranked.is_empty() {
            return;
        }
        out.push_str("## Cluster Details\n\n");
        let by_qn: BTreeMap<&str, &CodeElement> = doc
            .elements
            .iter()
            .map(|e| (e.qualified_name.as_str(), e))
            .collect();

        for c in ranked.into_iter().take(MAX_CLUSTERS) {
            out.push_str(&format!("### {} (`{}`)\n\n", escape_inline(&c.label), c.id));
            out.push_str(&format!(
                "{} members across {} files.\n\n",
                c.members.len(),
                c.representative_files.len()
            ));
            if !c.representative_files.is_empty() {
                out.push_str("Member files:\n\n");
                for f in &c.representative_files {
                    out.push_str(&format!("- `{}`\n", f));
                }
                out.push('\n');
            }
            out.push_str("Key symbols:\n\n");
            for qn in c.members.iter().take(MAX_CLUSTER_MEMBERS_LISTED) {
                match by_qn.get(qn.as_str()) {
                    Some(e) => {
                        out.push_str(&format!("- [`{qn}`]({}#L{})\n", e.file_path, e.line_start))
                    }
                    None => out.push_str(&format!("- `{qn}`\n")),
                }
            }
            out.push('\n');
        }
    }
}

/// One file row of the architecture trie: display name + its elements.
struct FileEntry<'a> {
    name: &'a str,
    elements: Vec<&'a CodeElement>,
}

/// Directory trie keyed by path segment; BTreeMap keeps child order stable.
#[derive(Default)]
struct TrieNode<'a> {
    dirs: BTreeMap<String, TrieNode<'a>>,
    files: Vec<FileEntry<'a>>,
}

impl<'a> TrieNode<'a> {
    fn sort(&mut self) {
        self.files.sort_by(|a, b| a.name.cmp(b.name));
        for d in self.dirs.values_mut() {
            d.sort();
        }
    }
}

const INDENT: &str = "  ";

/// Render the trie depth-first. `level` is the indentation level of this
/// node's children (root's children print unindented). At `MAX_TREE_DEPTH`
/// remaining subtrees collapse into one deterministic count line.
fn render_trie(node: &TrieNode, level: usize, lines: &mut Vec<String>) {
    let pad = INDENT.repeat(level);
    if level >= MAX_TREE_DEPTH {
        let hidden = node.dirs.len() + node.files.len();
        if hidden > 0 {
            lines.push(format!("{pad}- … ({hidden} more)"));
        }
        return;
    }
    for (name, dir) in &node.dirs {
        lines.push(format!("{pad}- {name}/"));
        render_trie(dir, level + 1, lines);
    }
    for f in &node.files {
        lines.push(format!("{pad}- {}", f.name));
        let el_pad = INDENT.repeat(level + 1);
        for e in &f.elements {
            lines.push(format!("{el_pad}- {} ({})", e.name, e.element_type));
        }
    }
}

/// Canonical cluster ordering: size desc, then label asc, then id asc —
/// deterministic regardless of how the detector numbered them.
pub(crate) fn ranked_clusters(doc: &MarkdownDoc) -> Vec<crate::graph::clustering::Cluster> {
    use crate::graph::clustering::Cluster;
    let mut clusters: Vec<Cluster> = doc.clusters.clone();
    for c in &mut clusters {
        c.members.sort();
        c.representative_files.sort();
        c.representative_files.dedup();
    }
    clusters.sort_by(|a, b| {
        b.members
            .len()
            .cmp(&a.members.len())
            .then_with(|| a.label.cmp(&b.label))
            .then_with(|| a.id.cmp(&b.id))
    });
    clusters
}

/// Escape characters that would break inline Markdown contexts (tables).
pub(crate) fn escape_inline(s: &str) -> String {
    s.replace('|', "\\|").replace('\n', " ")
}

/// Current time as RFC3339 UTC (`YYYY-MM-DDTHH:MM:SSZ`). Dependency-free.
pub(crate) fn now_rfc3339_utc() -> String {
    let secs = std::time::SystemTime::now()
        .duration_since(std::time::UNIX_EPOCH)
        .map(|d| d.as_secs() as i64)
        .unwrap_or(0);
    unix_secs_to_rfc3339(secs)
}

/// Convert Unix seconds to `YYYY-MM-DDTHH:MM:SSZ` (Howard Hinnant's
/// `civil_from_days` algorithm).
pub(crate) fn unix_secs_to_rfc3339(secs: i64) -> String {
    let days = secs.div_euclid(86_400);
    let rem = secs.rem_euclid(86_400);
    let (h, m, s) = (rem / 3600, (rem % 3600) / 60, rem % 60);

    // civil_from_days
    let z = days + 719_468;
    let era = z.div_euclid(146_097);
    let doe = z.rem_euclid(146_097); // [0, 146096]
    let yoe = (doe - doe / 1460 + doe / 36_524 - doe / 146_096) / 365; // [0, 399]
    let y = yoe + era * 400;
    let doy = doe - (365 * yoe + yoe / 4 - yoe / 100); // [0, 365]
    let mp = (5 * doy + 2) / 153; // [0, 11]
    let d = doy - (153 * mp + 2) / 5 + 1; // [1, 31]
    let mth = if mp < 10 { mp + 3 } else { mp - 9 }; // [1, 12]
    let y = if mth <= 2 { y + 1 } else { y };

    format!("{y:04}-{mth:02}-{d:02}T{h:02}:{m:02}:{s:02}Z")
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::db::models::{CodeElement, Relationship};
    use crate::graph::clustering::Cluster;
    use crate::graph::query::GodNode;

    const TS: &str = "2026-08-22T00:00:00Z";

    fn el(qn: &str, name: &str, etype: &str, file: &str, ls: u32) -> CodeElement {
        CodeElement {
            qualified_name: qn.to_string(),
            element_type: etype.to_string(),
            name: name.to_string(),
            file_path: file.to_string(),
            line_start: ls,
            line_end: ls + 5,
            language: "rust".to_string(),
            metadata: serde_json::json!({}),
            ..Default::default()
        }
    }

    fn rel(src: &str, tgt: &str, rtype: &str) -> Relationship {
        Relationship {
            source_qualified: src.to_string(),
            target_qualified: tgt.to_string(),
            rel_type: rtype.to_string(),
            confidence: 1.0,
            ..Default::default()
        }
    }

    fn god(qn: &str, degree: usize) -> GodNode {
        GodNode {
            qualified_name: qn.to_string(),
            name: qn.rsplit("::").next().unwrap_or(qn).to_string(),
            element_type: "function".to_string(),
            degree,
        }
    }

    fn cluster(id: &str, label: &str, members: &[&str], files: &[&str]) -> Cluster {
        Cluster {
            id: id.to_string(),
            label: label.to_string(),
            members: members.iter().map(|m| m.to_string()).collect(),
            representative_files: files.iter().map(|f| f.to_string()).collect(),
        }
    }

    fn doc(
        elements: Vec<CodeElement>,
        relationships: Vec<Relationship>,
        god_nodes: Vec<GodNode>,
        clusters: Vec<Cluster>,
    ) -> MarkdownDoc {
        MarkdownDoc {
            project: "demo".to_string(),
            generated_at_utc: TS.to_string(),
            elements,
            relationships,
            god_nodes,
            clusters,
        }
    }

    fn fixture() -> MarkdownDoc {
        doc(
            vec![
                el("src/app.rs::hub", "hub", "function", "src/app.rs", 1),
                el("src/app.rs::leaf", "leaf", "function", "src/app.rs", 10),
                el("src/lib.rs::init", "init", "function", "src/lib.rs", 1),
                el("src/lib.rs", "lib.rs", "file", "src/lib.rs", 0),
            ],
            vec![
                rel("src/app.rs::hub", "src/app.rs::leaf", "calls"),
                rel("src/lib.rs::init", "src/app.rs::leaf", "calls"),
                rel("src/app.rs::hub", "src/lib.rs::init", "imports"),
                rel("x", "y", "references"), // dangling endpoints still counted
            ],
            vec![god("src/app.rs::hub", 3), god("src/lib.rs::init", 1)],
            vec![
                cluster(
                    "dir:src/app",
                    "app",
                    &["src/app.rs::hub", "src/app.rs::leaf"],
                    &["src/app.rs"],
                ),
                cluster("dir:src/lib", "lib", &["src/lib.rs::init"], &["src/lib.rs"]),
            ],
        )
    }

    fn section_range(md: &str, start: &str) -> std::ops::Range<usize> {
        let s = md
            .find(start)
            .unwrap_or_else(|| panic!("missing section {start}"));
        let rest = &md[s + start.len()..];
        let end = match rest.find("\n## ") {
            Some(i) => s + start.len() + i + 1,
            None => md.len(),
        };
        s..end
    }

    #[test]
    fn sections_emitted_in_declared_order() {
        let md = MarkdownExporter::new().generate(&fixture());
        let order = [
            "# LeanKG Graph Docs",
            "## Overview",
            "### Elements by type",
            "### Relationships by type",
            "## Top Clusters",
            "## God Nodes (top 10 by degree)",
            "## Architecture Tree",
            "## Cluster Details",
        ];
        let mut last = 0;
        for h in order {
            let pos = md.find(h).unwrap_or_else(|| panic!("missing {h}"));
            assert!(pos >= last, "section {h} out of order");
            last = pos;
        }
    }

    #[test]
    fn overview_counts_and_types_match_fixture() {
        let md = MarkdownExporter::new().generate(&fixture());
        assert!(md.contains("project: demo\n"), "front matter project");
        assert!(md.contains("- Project: `demo`\n"));
        assert!(md.contains("- Elements: 4\n"), "{md}");
        assert!(md.contains("- Relationships: 4\n"));
        assert!(md.contains("- Clusters: 2\n"));

        let ov = section_range(&md, "### Elements by type");
        let elems_table = &md[ov];
        assert!(elems_table.contains("| function | 3 |\n"));
        assert!(elems_table.contains("| file | 1 |\n"));
        // Sorted by type name: file < function.
        let fpos = elems_table.find("| file |").unwrap();
        let fupos = elems_table.find("| function |").unwrap();
        assert!(fpos < fupos, "type rows must be sorted");

        let rel_sec = section_range(&md, "### Relationships by type");
        let rels_table = &md[rel_sec];
        assert!(rels_table.contains("| calls | 2 |\n"));
        assert!(rels_table.contains("| imports | 1 |\n"));
        assert!(rels_table.contains("| references | 1 |\n"));
    }

    #[test]
    fn empty_graph_produces_valid_doc_with_zero_counts() {
        let md = MarkdownExporter::new().generate(&doc(vec![], vec![], vec![], vec![]));
        assert!(md.starts_with("---\ntitle: LeanKG Graph Docs\n"));
        assert!(md.contains("generated_at: "));
        assert!(md.contains("- Elements: 0\n"));
        assert!(md.contains("- Relationships: 0\n"));
        assert!(md.contains("- Clusters: 0\n"));
        assert!(md.contains("| Type | Count |"));
    }

    #[test]
    fn byte_deterministic_regardless_of_input_order() {
        let exp_a = MarkdownExporter::new().generate(&fixture());

        // Same logical DB state, different row delivery order.
        let mut d = fixture();
        d.elements.reverse();
        d.relationships.reverse();
        d.god_nodes.reverse();
        d.clusters.reverse();
        let exp_b = MarkdownExporter::new().generate(&d);

        assert_eq!(
            exp_a, exp_b,
            "output must be a pure function of graph state"
        );

        // And twice on identical input is byte-equal.
        let again = MarkdownExporter::new().generate(&fixture());
        assert_eq!(exp_a, again);
    }

    #[test]
    fn god_nodes_capped_at_ten_sorted_by_degree_desc() {
        let mut gods: Vec<GodNode> = (0..14)
            .map(|i| god(&format!("src/m.rs::n{i:02}"), i % 3))
            .collect();
        gods.reverse(); // scrambled input order

        let md = MarkdownExporter::new().generate(&doc(
            gods.iter()
                .map(|g| el(&g.qualified_name, &g.name, "function", "src/m.rs", 1))
                .collect(),
            vec![],
            gods,
            vec![],
        ));

        let sec = section_range(&md, "## God Nodes (top 10 by degree)");
        let table = &md[sec];
        let rows: Vec<&str> = table
            .lines()
            .filter(|l| l.starts_with("| ") && !l.contains("Degree"))
            .collect();
        assert_eq!(rows.len(), 10, "exactly top-10 rendered:\n{table}");

        let degrees: Vec<usize> = rows
            .iter()
            .filter_map(|l| l.split('|').nth(1)?.trim().parse().ok())
            .collect();
        assert_eq!(
            degrees,
            // 14 nodes with degree i%3 → four 2s, five 1s, five 0s;
            // cap keeps 4×deg2 + 5×deg1 + the first deg0 (qn asc).
            vec![2, 2, 2, 2, 1, 1, 1, 1, 1, 0],
            "degree desc; ties broken by qualified_name asc"
        );
    }

    #[test]
    fn architecture_tree_depth_capped_at_four() {
        let mut elements = vec![
            el("src/app.rs::hub", "hub", "function", "src/app.rs", 1),
            el(
                "a/b/c/d/e/f.rs::deep_fn",
                "deep_fn",
                "function",
                "a/b/c/d/e/f.rs",
                7,
            ),
        ];
        elements.reverse();

        let md = MarkdownExporter::new().generate(&doc(elements, vec![], vec![], vec![]));

        let sec = section_range(&md, "## Architecture Tree");
        let tree = &md[sec];
        assert!(tree.contains("- src/\n"), "{tree}");
        assert!(tree.contains("  - app.rs\n"));
        assert!(tree.contains("    - hub (function)\n"));

        // Depth-4 cap: a(1)/b(2)/c(3)/d(4) collapses its children into one line.
        assert!(
            tree.contains("- … (1 more)\n"),
            "collapsed subtree line missing:\n{tree}"
        );
        assert!(
            !tree.contains("e/"),
            "must not expand past depth 4:\n{tree}"
        );
        assert!(
            !tree.contains("f.rs"),
            "must not expand past depth 4:\n{tree}"
        );
        assert!(
            !tree.contains("deep_fn"),
            "elements past cap hidden:\n{tree}"
        );
    }

    #[test]
    fn top_clusters_ranked_size_desc_then_label() {
        let md = MarkdownExporter::new().generate(&fixture());
        let sec = section_range(&md, "## Top Clusters");
        let table = &md[sec];
        let app_pos = table
            .find("`dir:src/app`")
            .expect("cluster row dir:src/app");
        let lib_pos = table
            .find("`dir:src/lib`")
            .expect("cluster row dir:src/lib");
        assert!(app_pos < lib_pos, "bigger cluster first:\n{table}");
        assert!(table.contains("| `dir:src/app` | app | 2 |\n"));
    }

    #[test]
    fn cluster_details_list_member_files_and_symbols() {
        let mut d = fixture();
        d.clusters[0].members.push("src/app.rs::aaa".to_string());
        d.elements
            .push(el("src/app.rs::aaa", "aaa", "function", "src/app.rs", 20));

        let md = MarkdownExporter::new().generate(&d);
        let det = section_range(&md, "## Cluster Details");

        assert!(det.start < md.len());
        let block = &md[det.clone()];
        assert!(block.contains("### app (`dir:src/app`)"));
        assert!(block.contains("3 members across 1 files."), "{block}");
        assert!(block.contains("- `src/app.rs`\n"), "{block}");
        // Key symbols sorted: aaa before hub before leaf, linked to source.
        let aaa = block.find("src/app.rs::aaa").unwrap();
        let hub = block.find("src/app.rs::hub").unwrap();
        let leaf = block.find("src/app.rs::leaf").unwrap();
        assert!(
            aaa < hub && hub < leaf,
            "key symbols sorted by qn:\n{block}"
        );
        assert!(block.contains("(src/app.rs#L1)"), "source anchor:\n{block}");
    }

    #[test]
    fn unix_timestamp_formatter_matches_known_instants() {
        assert_eq!(unix_secs_to_rfc3339(0), "1970-01-01T00:00:00Z");
        // Leap-year boundary: 2024-02-29T23:59:59Z = 1709251199
        assert_eq!(unix_secs_to_rfc3339(1_709_251_199), "2024-02-29T23:59:59Z");
        // 2026-08-22T00:00:00Z
        assert_eq!(unix_secs_to_rfc3339(1_787_356_800), TS);
        assert_eq!(unix_secs_to_rfc3339(-1), "1969-12-31T23:59:59Z");
    }
}
