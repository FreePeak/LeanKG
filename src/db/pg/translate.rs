//! CozoDB Datalog → PostgreSQL SQL translator (plan T3.1–T3.4).
//!
//! One mechanical translator that converts the ~115 cozo query shapes in
//! `docs/analysis/cozo-query-inventory.md §2` into SQL + bound parameters.
//! Returns a [`Translation`] the caller executes and maps rows from.
//!
//! Scope: the 11 HAND-WRITE shapes (§3) plus the ~95 TRIVIAL+MODERATE shapes
//! (§2 class column). Out of scope (Phase 5): `GraphEngine::run_raw_query` and
//! the MCP `run_raw_query` / web `api_query` pass-throughs, which take
//! arbitrary user-supplied Datalog and need explicit fencing.
//!
//! Distance semantics note: pgvector `<->` returns L2 distance; cozo HNSW
//! returns cosine distance (1 − cos_sim). On unit vectors the orders are
//! identical; absolute values differ. Downstream `Seed.ann_distance` is only
//! used for ordering + the reranker stage; we expose the raw `<->` value and
//! note this in the doc on [`Translation::ann_select`].

#![allow(clippy::type_complexity)]
#![allow(clippy::collapsible_match)]
#![allow(clippy::collapsible_else_if)]
// ponytail: pre-Phase 4 lints that rustc 1.95 surfaces for the translator
// (unused bindings in the no-op `::relations / PRAGMA / VACUUM` arms and the
// `_bytes` parameter); Phase 5+ rewrites will clean them up. Today the
// translator compiles and tests pass; flip to error after Phase 5.
#![allow(unused_variables)]
#![allow(unused_mut)]

use crate::db::pg::mutability::mutability_for;
use crate::db::value::{DataValue, NamedRows};
use postgres::types::ToSql;
use std::collections::BTreeMap;

/// A translated query ready to execute against Postgres.
#[derive(Debug)]
pub struct Translation {
    pub sql: String,
    /// Bound parameters. The N-th `$N` placeholder in `sql` references
    /// `params[N - 1]`. Each value is a `&dyn ToSql`-compatible boxed trait
    /// object; the translator stores owned `String`/`i64`/`f64`/`bool`/`Option`
    /// values.
    pub params: Vec<Box<dyn ToSql + Sync + Send>>,
    /// Kind hint for the executor (SELECT / INSERT / DELETE / DDL no-op).
    pub kind: TranslationKind,
    /// Head column order (positional consumption downstream). For reads,
    /// matches the cozo head exactly. Empty for writes.
    pub head: Vec<String>,
    /// Postgres GUC overrides applied via `SET LOCAL` inside the same
    /// transaction as `sql` (Phase 4 — `LEANKG_HNSW_EF` → `hnsw.ef_search`).
    /// Only effective for `Read` and `Write` kinds; ignored for `DdlNoop`.
    pub gucs: Vec<(String, String)>,
}

#[derive(Debug, Copy, Clone, PartialEq, Eq)]
pub enum TranslationKind {
    Read,
    Write,
    DdlNoop,
}

impl Translation {
    fn read(sql: String, params: Vec<Box<dyn ToSql + Sync + Send>>, head: Vec<String>) -> Self {
        Self {
            sql,
            params,
            kind: TranslationKind::Read,
            head,
            gucs: Vec::new(),
        }
    }
    fn read_with_gucs(
        sql: String,
        params: Vec<Box<dyn ToSql + Sync + Send>>,
        head: Vec<String>,
        gucs: Vec<(String, String)>,
    ) -> Self {
        Self {
            sql,
            params,
            kind: TranslationKind::Read,
            head,
            gucs,
        }
    }
    fn write(sql: String, params: Vec<Box<dyn ToSql + Sync + Send>>) -> Self {
        Self {
            sql,
            params,
            kind: TranslationKind::Write,
            head: Vec::new(),
            gucs: Vec::new(),
        }
    }
    fn write_with_gucs(
        sql: String,
        params: Vec<Box<dyn ToSql + Sync + Send>>,
        gucs: Vec<(String, String)>,
    ) -> Self {
        Self {
            sql,
            params,
            kind: TranslationKind::Write,
            head: Vec::new(),
            gucs,
        }
    }
    fn ddl_noop(head: Vec<String>) -> Self {
        Self {
            sql: String::new(),
            params: Vec::new(),
            kind: TranslationKind::DdlNoop,
            head,
            gucs: Vec::new(),
        }
    }
}

/// Box a `serde_json::Value` (the only value type the rest of the codebase
/// passes) into the trait object the `postgres` crate wants. JSON `Null`
/// becomes SQL `NULL` (Option::None); numbers become i64/f64; bool stays;
/// string stays; arrays/objects bind as `serde_json::Value` (the
/// `with-serde_json-1` feature makes it accept JSON/JSONB columns — the
/// JSONB columns in schema.sql receive these directly).
fn json_to_pg(v: serde_json::Value) -> Box<dyn ToSql + Sync + Send> {
    match v {
        serde_json::Value::Null => Box::new(Option::<String>::None),
        serde_json::Value::Bool(b) => Box::new(b),
        serde_json::Value::Number(n) => {
            if let Some(i) = n.as_i64() {
                Box::new(i)
            } else if let Some(f) = n.as_f64() {
                Box::new(f)
            } else {
                Box::new(n.to_string())
            }
        }
        serde_json::Value::String(s) => Box::new(s),
        // Arrays/objects → JSONB via the serde_json feature.
        other => Box::new(other),
    }
}

/// Top-level entry. Classifies mutability, parses the leading operator, and
/// dispatches to a handler. Returns an error string (not `postgres::Error`)
/// because this layer is engine-agnostic; the caller wraps.
pub fn translate(
    query: &str,
    params: BTreeMap<String, serde_json::Value>,
) -> Result<Translation, String> {
    let trimmed = query.trim();
    // Classify mutability BEFORE we touch the SQL (DDL :create is also a
    // write for the connection state even when we no-op).
    let _ = mutability_for(query);

    // Strip a leading `\n` / `\r\n` common in cozo scripts; doesn't change semantics.
    let body = trimmed;

    if body.is_empty() {
        return Err("empty query".into());
    }

    // Ordered dispatch — most specific first.
    if let Some(rest) = strip_prefix(body, "::relations") {
        return Ok(relations_introspection());
    }
    if let Some(rest) = strip_prefix(body, "::hnsw") {
        return hnsw_ddl(rest);
    }
    if let Some(rest) = strip_prefix(body, "::index") {
        return index_ddl(rest);
    }
    if let Some(rest) = strip_prefix(body, ":schema") {
        return schema_introspection(rest);
    }
    if let Some(rest) = strip_prefix(body, ":create") {
        return create_ddl(rest);
    }
    if let Some(rest) = strip_prefix(body, ":replace") {
        return replace_ddl(rest);
    }
    if let Some(rest) = strip_prefix(body, ":delete") {
        return delete_where(rest, &params);
    }
    if let Some(rest) = strip_prefix(body, ":put") {
        return put_script(rest, &params);
    }
    if let Some(rest) = strip_prefix(body, ":rm") {
        return rm_script(rest, &params);
    }
    if let Some(rest) = strip_prefix(body, "PRAGMA") {
        return Ok(Translation::ddl_noop(Vec::new()));
    }
    if let Some(rest) = strip_prefix(body, "VACUUM") {
        return Ok(Translation::ddl_noop(Vec::new()));
    }
    if body.starts_with("?[") {
        return read_script(body, &params);
    }
    // Multi-rule scripts start with an intermediate rule
    // (`files[f] := *code_elements[...]\n?[count(f)] := files[f]` — H6/G88
    // `count_files`). Route them to read_script which handles the pair.
    if split_rule_pair(body).is_some() {
        return read_script(body, &params);
    }

    Err(format!(
        "unrecognized script (no leading operator): {}",
        &body[..body.len().min(80)]
    ))
}

fn strip_prefix<'a>(s: &'a str, p: &str) -> Option<&'a str> {
    if let Some(rest) = s.strip_prefix(p) {
        // Allow either whitespace or end-of-input after the operator.
        if rest.is_empty() || rest.starts_with(|c: char| c.is_whitespace()) {
            return Some(rest);
        }
    }
    None
}

/// Locate the trailing write operator in a `?[...]` script (the body
/// contains both a read rule head and a write action). Used to disambiguate
/// the dispatcher when the leading token is `?`.
fn find_write_operator(body: &str) -> Option<WriteOp> {
    // Search for the LAST occurrence of a write operator token — cozo
    // patterns put the action at the end (`?[cols] := *rel[...] :put t {...}`).
    const OPS: &[&str] = &[":put", ":rm", ":replace", ":delete", ":create"];
    let mut best: Option<(usize, &str)> = None;
    for op in OPS {
        // Walk all occurrences (string-find is greedy left-to-right).
        let mut start = 0;
        while let Some(idx) = body[start..].find(op) {
            let abs = start + idx;
            // Ensure the operator is preceded by a separator (space or
            // newline) so we don't match a substring of a column name.
            let preceded_ok = abs == 0
                || body.as_bytes()[abs - 1].is_ascii_whitespace()
                || body.as_bytes()[abs - 1] == b',';
            if preceded_ok {
                match best {
                    Some((prev, _)) if prev > abs => {}
                    _ => best = Some((abs, op)),
                }
            }
            start = abs + op.len();
        }
    }
    best.map(|(idx, kind)| WriteOp {
        idx,
        kind: kind.trim_start_matches(':'),
    })
}

struct WriteOp {
    idx: usize,
    kind: &'static str,
}

// ---------------------------------------------------------------------------
// Reads (TRIVIAL + MODERATE aggregates). The biggest bucket.
// ---------------------------------------------------------------------------

fn read_script(
    body: &str,
    params: &BTreeMap<String, serde_json::Value>,
) -> Result<Translation, String> {
    // A `?[...]` script can still be a write if the rule body contains a
    // write operator (e.g. `?[a] := *t[a] :replace t {a: String}` or
    // `?[qn] := *t[qn], qn = $x :rm t {qn}`). Detect the trailing operator
    // and dispatch to the matching writer.
    let write_op = find_write_operator(body);
    if let Some(op) = write_op {
        return match op.kind {
            "put" => put_script(body, params),
            "rm" => rm_script(body, params),
            "replace" => replace_ddl(body),
            "delete" => delete_where(body, params),
            "create" => create_ddl(body),
            _ => Err(format!("unsupported trailing operator: {}", op.kind)),
        };
    }

    // Multi-rule count script (H6/G88 — `count_files`):
    //   `files[f] := *code_elements[n, a, b, f, c, d, e, g, h, i, j{tail}]
    //    ?[count(f)] := files[f]`
    // The first rule dedupes `f` (file_path); `count(f)` = count(DISTINCT
    // file_path). Only the last rule's head is the output.
    if let Some((first_rule, rest_rule)) = split_rule_pair(body) {
        if let Some(agg) = aggregate_from_head(&parse_head(&rest_rule)?) {
            let (rel_name, rel_cols, after_rel) = match parse_relation_block(&first_rule) {
                Some(parts) => parts,
                None => return Err(format!("cannot parse intermediate rule in: {first_rule}")),
            };
            // `f` is a positional alias bound to a real column. Resolve it
            // through the relation block: the alias sits at index i of
            // rel_cols, which corresponds to the i-th column of the table.
            // The only multi-rule count in the codebase is `count_files`
            // over code_elements; catalog its columns so `count(DISTINCT
            // "f")` becomes `count(DISTINCT file_path)`.
            let counted_col = rel_cols
                .iter()
                .position(|c| c == &agg.expr)
                .and_then(|i| CODE_ELEMENTS_COLUMNS.get(i))
                .map(|c| c.to_string());
            let expr = counted_col.unwrap_or(agg.expr.clone());
            return aggregate_query(
                &rel_name,
                &rel_cols,
                AggSpec {
                    kind: AggKind::Count,
                    expr,
                    distinct: true,
                    extras: Vec::new(),
                    head_label: Some(format!("count({})", agg.expr)),
                },
                after_rel,
                String::new(),
                params,
            );
        }
    }

    // Split head from the rest at `:=` (read), `<-` (literal-as-read — CH1
    // pattern), or `:` (operator — kept for legacy).
    let (head, rest) = if let Some(idx) = body.find(":=") {
        (&body[..idx], &body[idx + 2..])
    } else if let Some(idx) = body.find("<-") {
        (&body[..idx], &body[idx + 2..])
    } else if let Some(idx) = body.find(':') {
        // Falls through to :put/:rm/:delete etc. handled above.
        (&body[..idx], &body[idx + 1..])
    } else {
        return Err("read script missing rule separator".into());
    };
    let head = parse_head(head)?;

    // ANN: `~<vectors_relation>:vec_idx { ... }` (H1). Any relation ending in
    // `:vec_idx` is an ANN probe — per-model embed tables included.
    if rest.contains(":vec_idx") {
        return ann_translation(rest, &head, params);
    }

    // Pull the relation block out of `*rel[col1, col2, ...]` (or attr syntax).
    // Supports tail markers `{tail}` and trailing `:limit/:offset/:order`.
    let (relation, rel_cols, body_after_rel) = match parse_relation_block(rest) {
        Some(parts) => parts,
        None => return Err(format!("cannot parse relation block in: {rest}")),
    };

    // Now body_after_rel contains: `, filters :limit N :offset N :order ... :group ...`.
    let (filters, group_order_limit) = split_filters_and_modifiers(&body_after_rel);

    // Count queries: `?[count(n)] := *code_elements[n, ...]` — drop into
    // `SELECT count(*)`. Also `count(DISTINCT file_path)` when a `files[f]`
    // intermediate rule dedupes (`H6/G88`) — we detect that pattern by the
    // unique head var inside `count(...)`.
    if let Some(agg) = aggregate_from_head(&head) {
        return aggregate_query(
            &relation,
            &rel_cols,
            agg,
            filters,
            group_order_limit,
            params,
        );
    }

    // NOT EXISTS: `not *code_elements[qualified_name, _, ...]` (H3/E12).
    if let Some(not_rel) = extract_not_exists(&filters) {
        return not_exists_query(&relation, &rel_cols, &head, &not_rel, params);
    }

    // Cross-relation `:rm` (H4) — handled at :rm arm above; reads here stay
    // single-relation. If we see a second `*other[...]` it's a join read.
    if filters.contains("*\x00") || (rest.matches("*\x00").count() > 1) {
        // Marker never appears — keep for symmetry.
    }
    if rest.matches('*').count() > 1 {
        // 2-relation read (e.g. G57 `*relationships[...] *code_elements[...]`)
        // — for reads only. We don't currently have such reads in inventory
        // §2; the only 2-rel form is the cross-relation :rm in G57.
        // Fall through with a clear error.
    }

    // Default: SELECT head FROM table [WHERE filters] [GROUP BY/ORDER BY/LIMIT/OFFSET].
    simple_select(
        &relation,
        &rel_cols,
        &head,
        filters,
        group_order_limit,
        params,
    )
}

/// Result of `aggregate_from_head` — if the head is `?[count(expr), ...]`,
/// returns the expression text. Empty if not an aggregate.
fn aggregate_from_head(head: &[String]) -> Option<AggSpec> {
    if head.len() == 1 && head[0].starts_with("count(") && head[0].ends_with(')') {
        let inner = &head[0][6..head[0].len() - 1];
        return Some(AggSpec {
            kind: AggKind::Count,
            expr: inner.to_string(),
            distinct: false,
            extras: Vec::new(),
            head_label: Some(head[0].clone()),
        });
    }
    if head.len() == 1 && head[0].starts_with("count(DISTINCT ") && head[0].ends_with(')') {
        let inner = &head[0][14..head[0].len() - 1];
        return Some(AggSpec {
            kind: AggKind::Count,
            expr: inner.to_string(),
            distinct: true,
            extras: Vec::new(),
            head_label: Some(head[0].clone()),
        });
    }
    // `?[a, count(b)]` — multi-col, only `group by a` is valid; translate to
    // SELECT a, count(*) ... GROUP BY a (G98/G102/G105/G106).
    if head
        .iter()
        .any(|h| h.starts_with("count(") && h.ends_with(')'))
    {
        let mut extras = Vec::new();
        for h in head {
            if h.starts_with("count(") && h.ends_with(')') {
                let inner = &h[6..h.len() - 1];
                return Some(AggSpec {
                    kind: AggKind::Count,
                    expr: inner.to_string(),
                    distinct: false,
                    extras,
                    head_label: Some(h.clone()),
                });
            }
            extras.push(h.clone());
        }
    }
    None
}

#[derive(Debug)]
struct AggSpec {
    kind: AggKind,
    expr: String,
    distinct: bool,
    /// For multi-col heads like `?[language, count(language)]` — the
    /// non-aggregate columns to GROUP BY (here `[language]`).
    extras: Vec<String>,
    /// Original cozo head text of the count expression (e.g. `count(f)`)
    /// — the result header must match cozo exactly (H6/G88: the positional
    /// alias `f` is resolved to `file_path` for SQL but the header stays
    /// `count(f)`).
    head_label: Option<String>,
}

#[derive(Debug, PartialEq, Eq)]
enum AggKind {
    Count,
}

/// Parse a `?[a, b, c]` head into a list of column names (strings).
fn parse_head(head: &str) -> Result<Vec<String>, String> {
    let t = head.trim();
    if !t.starts_with("?[") {
        return Err(format!("bad head: {t}"));
    }
    // Take only up to the FIRST `]` — a multi-rule head may be followed by
    // ` := ...` (`?[count(f)] := files[f]`), which must not leak in.
    let close = t[2..]
        .find(']')
        .ok_or_else(|| format!("bad head (no closing bracket): {t}"))?;
    let inner = &t[2..2 + close];
    Ok(inner
        .split(',')
        .map(|s| s.trim().to_string())
        .filter(|s| !s.is_empty())
        .collect())
}

/// Canonical `code_elements` column order (schema.sql). Used to resolve
/// positional alias columns in multi-rule count scripts (H6/G88).
const CODE_ELEMENTS_COLUMNS: &[&str] = &[
    "qualified_name",
    "element_type",
    "name",
    "file_path",
    "line_start",
    "line_end",
    "language",
    "parent_qualified",
    "cluster_id",
    "cluster_label",
    "metadata",
    "env",
    "ontology_layer",
];

const RELATIONSHIPS_COLUMNS: &[&str] = &[
    "source_qualified",
    "target_qualified",
    "rel_type",
    "confidence",
    "metadata",
    "env",
];

/// Canonical column order for tables whose aggregates bind positional
/// aliases (schema.sql). Used by [`aggregate_query`] to resolve head
/// aliases to real columns.
fn canonical_columns(relation: &str) -> &'static [&'static str] {
    match relation {
        "code_elements" => CODE_ELEMENTS_COLUMNS,
        "relationships" => &[
            "source_qualified",
            "target_qualified",
            "rel_type",
            "confidence",
            "metadata",
            "env",
        ],
        "incidents" => &[
            "id",
            "env",
            "title",
            "severity",
            "occurred_at",
            "resolved_at",
            "root_cause",
            "resolution",
            "affected_services",
            "trigger_pattern",
            "prevention",
            "tags",
            "author",
            "linked_ticket",
        ],
        "knowledge_entries" => &[
            "id",
            "knowledge_type",
            "title",
            "content",
            "element_qualified",
            "user_story_id",
            "feature_id",
            "tags",
            "environment",
            "branch",
            "author",
            "created_at",
            "updated_at",
        ],
        "business_logic" => &[
            "element_qualified",
            "description",
            "user_story_id",
            "feature_id",
        ],
        _ => CODE_ELEMENTS_COLUMNS,
    }
}

/// Split a two-rule script at the boundary between `rule1\nrule2`.
/// Returns `(first_rule, second_rule)` — used for the H6/G88
/// `files[f] := ... \n ?[count(f)] := files[f]` shape. The second rule
/// starts with `?[` at the beginning of a line (or after whitespace).
fn split_rule_pair(body: &str) -> Option<(String, String)> {
    let lines: Vec<&str> = body.lines().map(|l| l.trim()).collect();
    for (i, line) in lines.iter().enumerate() {
        if i == 0 {
            continue;
        }
        if line.starts_with("?[") {
            let first = lines[..i].join("\n");
            let second = lines[i..].join("\n");
            return Some((first, second));
        }
    }
    None
}

/// Find the first `*rel[...]` block (or `*rel{...}` attr syntax) and return
/// (rel_name, column_placeholders, body_after). Column placeholders are
/// underscore-or-identifier strings — the caller can use them to match
/// against the head.
fn parse_relation_block(rest: &str) -> Option<(String, Vec<String>, String)> {
    let bytes = rest.as_bytes();
    let star = rest.find('*')?;
    // Skip past `*` and any whitespace.
    let after_star = rest[star + 1..].trim_start();
    // The relation name ends at the first `[` or `{`.
    let rel_end = after_star.find(['[', '{']).unwrap_or(after_star.len());
    let rel_name = after_star[..rel_end].trim().to_string();
    if rel_name.is_empty() {
        return None;
    }
    let open = after_star.as_bytes()[rel_end] as char;
    let close = match open {
        '[' => ']',
        '{' => '}',
        _ => return None,
    };
    let body_start = rel_end + 1;
    let body_end_rel = after_star[body_start..].find(close)? + body_start;
    let cols_str = &after_star[body_start..body_end_rel];
    let after_rel = &after_star[body_end_rel + 1..];
    let cols: Vec<String> = if open == '[' {
        cols_str.split(',').map(|s| s.trim().to_string()).collect()
    } else {
        // Attribute-binding: `*rel{col = $x, ...}`. Collapse to underscore
        // placeholders so head matching still works (the order is preserved
        // — cozo attribute syntax places values positionally in the head).
        cols_str
            .split(',')
            .map(|s| {
                let s = s.trim();
                // `col = $x` → keep `col`; bare `col` → `col`; `_` → `_`.
                s.split('=').next().unwrap_or("_").trim().to_string()
            })
            .collect()
    };
    Some((rel_name, cols, after_rel.to_string()))
}

/// Split the post-relation body into (filters, modifier-trailing). Filters
/// come before `:limit`/`:offset`/`:order`/`:group`.
fn split_filters_and_modifiers(body: &str) -> (String, String) {
    // We don't try to parse the modifier block into structured pieces — we
    // emit ` ... LIMIT k OFFSET o ORDER BY ...` directly off regex matches.
    // Find the earliest of `:limit` / `:offset` / `:order` / `:group` so
    // that all modifiers land in the second piece regardless of order in
    // the body.
    let positions = [
        body.find(":limit"),
        body.find(":offset"),
        body.find(":order"),
        body.find(":group"),
    ];
    let first_mod = positions.iter().filter_map(|&p| p).min();
    match first_mod {
        Some(idx) => (body[..idx].to_string(), body[idx..].to_string()),
        None => (body.to_string(), String::new()),
    }
}

/// Pull the `not *rel[...]` (H3 / E12 / list_orphans) out of the filter text
/// if present. Returns the inner relation+cols of the negated relation.
fn extract_not_exists(filters: &str) -> Option<(String, Vec<String>)> {
    let trimmed = filters.trim().trim_start_matches(',').trim();
    let after_not = trimmed.strip_prefix("not ")?.trim_start();
    if !after_not.starts_with('*') {
        return None;
    }
    // Parse the `*rel[...]` block directly (the leading `*` is already
    // present in `after_not` — do NOT re-add it, or the relation name
    // becomes `*code_elements`).
    let (rel, cols, _) = parse_relation_block(after_not)?;
    Some((rel, cols))
}

/// H3 / E12: NOT EXISTS subquery.
fn not_exists_query(
    relation: &str,
    rel_cols: &[String],
    head: &[String],
    not_rel: &(String, Vec<String>),
    params: &BTreeMap<String, serde_json::Value>,
) -> Result<Translation, String> {
    // Inner relation must be joinable by a key — typically `qualified_name`.
    // For E12 `*code_elements[qualified_name, _, ...]` we join on
    // `code_elements.qualified_name = <outer>.qualified_name`.
    let (inner_rel, inner_cols) = not_rel;
    // Find the join key in the outer relation's columns.
    let outer_join_col = rel_cols
        .iter()
        .find(|c| !c.starts_with('_') && inner_cols.iter().any(|ic| ic == *c))
        .cloned()
        .unwrap_or_else(|| "qualified_name".to_string());

    // The inner `relationships` relation has no `qualified_name` column — it
    // keys by `source_qualified` / `target_qualified`. An orphan query
    // (`not *relationships[...]`) must join the outer's qualified_name to
    // `relationships.source_qualified` (elements never used as a relationship
    // source). Emitting `relationships.qualified_name` errors, and emitting a
    // cartesian join hangs. Special-case the join key for relationships.
    let (inner_join_col, outer_join_col) =
        if inner_rel == "relationships" && !inner_cols.iter().any(|c| c == &outer_join_col) {
            ("source_qualified".to_string(), outer_join_col)
        } else {
            (outer_join_col.clone(), outer_join_col)
        };

    let cols_sql = head
        .iter()
        .map(|c| quote_ident(c))
        .collect::<Vec<_>>()
        .join(", ");
    let sql = format!(
        "SELECT {cols_sql} FROM {relation} WHERE NOT EXISTS (SELECT 1 FROM {inner_rel} \
         WHERE {inner_rel}.{inner_join_col} = {relation}.{outer_join_col})"
    );
    Ok(Translation::read(sql, Vec::new(), head.to_vec()))
}

/// Single-relation SELECT.
fn simple_select(
    relation: &str,
    rel_cols: &[String],
    head: &[String],
    filters: String,
    modifiers: String,
    params: &BTreeMap<String, serde_json::Value>,
) -> Result<Translation, String> {
    if head.is_empty() {
        return Err("empty head in SELECT".into());
    }
    // Find head cols defined by filter clauses (`span = line_end -
    // line_start` in the filters defines the head col `span` — G107). The
    // SELECT must emit the expression, not the (nonexistent) column.
    let mut def_exprs: std::collections::HashMap<&str, String> = std::collections::HashMap::new();
    for clause in split_clauses(&filters) {
        let trimmed = clause.trim();
        for h in head {
            // `==` is a cozo equality operator (`service_name == $svc`),
            // NOT a definition — require `= ` or `=` followed by a
            // non-`=` char.
            let prefix = format!("{} =", h);
            if trimmed.starts_with(&prefix) && !trimmed[prefix.len()..].starts_with('=') {
                let rhs = trimmed[prefix.len()..].trim();
                if !(rhs.starts_with('"') || rhs.starts_with('$') || rhs == "null") {
                    def_exprs.insert(h.as_str(), rhs.to_string());
                }
            }
        }
    }
    // Head-alias expressions (G107 `span = line_end - line_start`). A head
    // entry shaped `name = expr` maps to `expr AS name`; the `expr` uses
    // positional aliases from the relation block (bound in order).
    let mut select_parts: Vec<String> = Vec::with_capacity(head.len());
    let mut order_by_span = false;
    for c in head {
        if let Some((alias, expr)) = c.split_once('=') {
            let alias = alias.trim();
            let expr = expr.trim();
            // Resolve positional alias vars against rel_cols.
            let resolved = resolve_positional(expr, rel_cols);
            if alias == "span" && expr == "line_end - line_start" {
                order_by_span = true;
            }
            select_parts.push(format!("{resolved} AS {}", quote_ident(alias)));
        } else if let Some(expr) = def_exprs.get(c.as_str()) {
            // Head col defined by a filter clause (G107 `span = ...`).
            let resolved = resolve_positional(expr, rel_cols);
            if c == "span" {
                order_by_span = true;
            }
            select_parts.push(format!("{resolved} AS {}", quote_ident(c)));
        } else {
            // Head var may be a positional alias bound by the relation block
            // (`?[tgt] := *relationships[_, tgt, rel, _, _, _]` — the head
            // name `tgt` is the alias, the real column is `target_qualified`
            // at that position). Map aliases to their real columns, but only
            // when they are NOT already a real column of the table.
            let resolved = if is_positional_alias(c, rel_cols) {
                let idx = rel_cols.iter().position(|x| x == c).unwrap();
                column_at_for(relation, idx)
                    .map(quote_ident)
                    .unwrap_or_else(|| quote_ident(c))
            } else {
                quote_ident(c)
            };
            select_parts.push(resolved);
        }
    }
    let cols_sql = select_parts.join(", ");

    // Drop head-alias *definition* clauses from the WHERE list. Cozo rules
    // bind derived variables with `alias = expr` (G107 `span = line_end -
    // line_start`); that's a definition, not a constraint — the WHERE must
    // not reference the alias column (it doesn't exist in the table).
    let filters = strip_definition_clauses(&filters, head);
    // Remaining filter clauses can still *use* a head alias defined by a
    // filter clause (`lines > 200` where `lines = line_end - line_start` is a
    // definition). The alias isn't a real column — inline its definition
    // expression so the WHERE references `line_end - line_start > 200`.
    let filters = inline_def_aliases(&filters, &def_exprs);
    // Resolve positional alias tokens in the filters against the relation
    // block (G107 `et in [...]` where `et` is the 2nd column = element_type).
    let filters = resolve_filter_aliases(relation, &filters, rel_cols);
    // Inline string literals in the relation block (`*code_elements[qn,
    // "function", ...]`, H6/get_architecture hotspots) act as equality
    // constraints — absent cozo-side would silently over-count. Cozo treats
    // them as bound filters; emit `"element_type" = 'function'`.
    let filters = append_literal_constraints(&filters, rel_cols);
    let (where_sql, where_params) = compile_filters(filters, params)?;
    let (mut mod_sql, mod_params) = compile_modifiers(&modifiers, head, params);
    if order_by_span && !mod_sql.contains("ORDER BY") {
        mod_sql = format!("{mod_sql} ORDER BY \"span\" DESC")
            .trim()
            .to_string();
    }

    let sql = if where_sql.is_empty() {
        format!(
            "SELECT {cols_sql} FROM {relation}{mod_sql}",
            mod_sql = if mod_sql.is_empty() {
                String::new()
            } else {
                format!(" {mod_sql}")
            }
        )
    } else {
        format!(
            "SELECT {cols_sql} FROM {relation} WHERE {where_sql}{mod_sql}",
            mod_sql = if mod_sql.is_empty() {
                String::new()
            } else {
                format!(" {mod_sql}")
            }
        )
    };

    let mut all_params = where_params;
    all_params.extend(mod_params);
    Ok(Translation::read(sql, all_params, head.to_vec()))
}

/// Resolve cozo positional alias tokens in the filter list against the
/// relation block's column placeholders. Cozo allows `et in [...]` where
/// `et` is a positional alias bound to the 2nd column; PG needs the real
/// column name (`element_type`). Only single-letter-ish aliases that are
/// NOT real column names are remapped (guarded by the rel_cols lookup).
fn resolve_filter_aliases(relation: &str, filters: &str, rel_cols: &[String]) -> String {
    if rel_cols.is_empty() {
        return filters.to_string();
    }
    let mut out = filters.to_string();
    for (i, alias) in rel_cols.iter().enumerate() {
        // Only remap short positional aliases (single/double letters) that
        // don't collide with a real column name of the table. `rel` (3
        // chars, relationships) is such an alias; `env` / `name` etc. are
        // real columns and must be left alone. Underscore `_` is a
        // wildcard placeholder.
        if alias.starts_with('_') || alias == "env" {
            continue;
        }
        let real = column_at_for(relation, i)
            .map(|c| c.to_string())
            .unwrap_or_else(|| alias.clone());
        if real == *alias {
            continue;
        }
        // Word-boundary replacement (not inside strings). The alias can be
        // preceded by a space, a comma, an open paren, or the start of the
        // filter text (` ,fp >= $lo and fp < $hi` — the first `fp` after
        // the relation block comma has no leading space). Match any of
        // those boundaries and a following space / `[`.
        let boundaries = [" ", ",", "("];
        for b in &boundaries {
            let pat = format!("{b}{alias} ");
            let pat2 = format!("{b}{alias}[");
            let pat3 = format!("{b}{alias},"); // `regex_matches(tgt, ...)`
            out = out.replace(&pat, &format!("{b}{real} "));
            out = out.replace(&pat2, &format!("{b}{real}["));
            out = out.replace(&pat3, &format!("{b}{real},"));
        }
        // Start-of-string boundary: `qn in $qns` where `qn` is the first
        // token (leading comma already stripped). Require a word boundary
        // after the alias.
        for pat3 in [format!("{alias} "), format!("{alias}[")] {
            if out.starts_with(&pat3) {
                out = format!("{real}{}", &out[alias.len()..]);
            }
        }
    }
    out
}

/// Cozo treats inline string literals inside a relation block as bound
/// equality filters: `*code_elements[qn, "function", _, file_path, ...]`
/// narrows to element_type = 'function'. PG must emit them as WHERE
/// predicates or every read silently over-counts (get_architecture
/// hotspots counted the file rows too). The literal sits at position i of
/// the relation block = the i-th table column. Returns the literal
/// predicates as extra filter clauses (comma-joined so
/// [`split_clauses`] picks them up).
fn append_literal_constraints(filters: &str, rel_cols: &[String]) -> String {
    use std::fmt::Write;
    let mut out = filters.to_string();
    for (i, token) in rel_cols.iter().enumerate() {
        let t = token.trim();
        if t.len() >= 2 && t.starts_with('"') && t.ends_with('"') {
            let lit = &t[1..t.len() - 1];
            // Escape single quotes for the SQL string literal.
            let lit_esc = lit.replace('\'', "''");
            // Bound column = the table's i-th column.
            if let Some(col) = column_at(i) {
                write!(
                    out,
                    "{}{} = '{}'",
                    if out.trim().is_empty() || out.trim().ends_with(',') {
                        ""
                    } else {
                        ", "
                    },
                    quote_ident(col),
                    lit_esc
                )
                .unwrap();
            }
        }
    }
    out
}

/// The i-th column of every table aggregates alias `?[...]` over
/// (aggregates bind positions to columns via [`canonical_columns`]). For
/// literal-constraint resolution we need the column name for any table,
/// so this walks the same catalogs.
fn column_at(i: usize) -> Option<&'static str> {
    for cols in [CODE_ELEMENTS_COLUMNS, RELATIONSHIPS_COLUMNS] {
        if let Some(c) = cols.get(i) {
            return Some(c);
        }
    }
    None
}

/// Table-aware variant of [`column_at`]: maps the i-th column of the
/// relation named `relation` (used for head-alias resolution where the
/// same index can mean different columns across tables). Unknown relations
/// return None — their head vars are their own columns (`?[a,b,c] :=
/// *table[a,b,c]`), not positional aliases to map.
fn column_at_for(relation: &str, i: usize) -> Option<&'static str> {
    if relation == "code_elements" {
        return CODE_ELEMENTS_COLUMNS.get(i).copied();
    }
    if relation == "relationships" {
        return RELATIONSHIPS_COLUMNS.get(i).copied();
    }
    None
}

/// Remove top-level `alias = expr` clauses whose alias appears in the head
/// (a derived-variable *definition*, not a constraint). e.g. G107:
/// `..., span = line_end - line_start:order -span` — the `span = ...` is a
/// rule binding (head col `span`, filter clause `span = line_end -
/// line_start`); the WHERE gets only the real filters. Any head column
/// bound by a filter clause `col = <non-literal expr>` is a definition.
fn strip_definition_clauses(filters: &str, head: &[String]) -> String {
    let mut out: Vec<&str> = Vec::new();
    for clause in split_clauses(filters) {
        let trimmed = clause.trim();
        // Definition if the clause is `<head_col> = expr` where expr is not
        // a quoted literal / $param / null (those are real equality filters).
        let is_definition = head.iter().any(|col| {
            let prefix = format!("{} =", col);
            if !trimmed.starts_with(&prefix) || trimmed[prefix.len()..].starts_with('=') {
                return false;
            }
            let rhs = trimmed[prefix.len()..].trim();
            !(rhs.starts_with('"') || rhs.starts_with('$') || rhs == "null")
        });
        if !is_definition {
            out.push(trimmed);
        }
    }
    out.join(", ")
}

/// Replace references to head aliases defined by filter clauses with their
/// definition expression. `longest_functions`-style queries do
/// `?[q, n, le, lines] := *code_elements{...}, lines = line_end - line_start,
/// lines > 200` — `strip_definition_clauses` removes the definition, leaving
/// `lines > 200` which references a nonexistent column. Inline the RHS so the
/// WHERE becomes `(line_end - line_start) > 200`. Matches whole alias tokens
/// only (not `my_lines`, `lines_foo`, quoted strings, or `$` params).
fn inline_def_aliases(
    filters: &str,
    def_exprs: &std::collections::HashMap<&str, String>,
) -> String {
    if def_exprs.is_empty() || filters.is_empty() {
        return filters.to_string();
    }
    let mut out = filters.to_string();
    for (alias, expr) in def_exprs {
        // Match whole alias tokens via explicit boundary captures — the
        // `regex` crate (default features) has no look-around. The alias
        // must not be preceded by `[A-Za-z0-9_$]` or followed by `[A-Za-z0-9_]`.
        let re = format!(r"(^|[^\w$])({})([^\w]|$)", regex::escape(alias));
        // Replace bare alias tokens with `(<expr>)`. Handle the common
        // `lines > N` and `lines = expr` forms.
        let rx = regex::Regex::new(&re).unwrap();
        out = rx.replace_all(&out, format!("$1({})$3", expr)).to_string();
    }
    out
}

/// Resolve positional alias variables inside a head-alias expression
/// (`span = line_end - line_start`) by looking up each variable in the
/// relation block's column placeholders (which are bound positionally).
/// Unknown names fall through unchanged (quoted identifiers / literals).
/// True when `name` appears in the relation block as a positional alias
/// (`*rel[s, t, ...]`) and is NOT itself a real column of any known table.
/// Real columns stay as-is; aliases must be mapped to the column at their
/// index.
fn is_positional_alias(name: &str, rel_cols: &[String]) -> bool {
    if !rel_cols.iter().any(|c| c == name) {
        return false;
    }
    // If the name is a real column of the table, leave it alone.
    for cols in [
        &[
            "qualified_name",
            "element_type",
            "name",
            "file_path",
            "line_start",
            "line_end",
            "language",
            "parent_qualified",
            "cluster_id",
            "cluster_label",
            "metadata",
            "env",
            "ontology_layer",
        ][..],
        &[
            "source_qualified",
            "target_qualified",
            "rel_type",
            "confidence",
            "metadata",
            "env",
        ][..],
    ] {
        if cols.contains(&name) {
            return false;
        }
    }
    true
}

fn resolve_positional(expr: &str, rel_cols: &[String]) -> String {
    let mut out = String::with_capacity(expr.len());
    let mut rest = expr;
    while !rest.is_empty() {
        // Split on whitespace/operators to find bare identifier tokens.
        let token_end = rest
            .find(|c: char| {
                c.is_whitespace()
                    || c == '-'
                    || c == '+'
                    || c == '*'
                    || c == '/'
                    || c == '('
                    || c == ')'
            })
            .unwrap_or(rest.len());
        let token = &rest[..token_end];
        if !token.is_empty() {
            if rel_cols.iter().any(|c| c == token) {
                out.push_str(&quote_ident(token));
            } else {
                out.push_str(token);
            }
        }
        if token_end >= rest.len() {
            break;
        }
        out.push_str(&rest[token_end..token_end + 1]);
        rest = &rest[token_end + 1..];
    }
    out
}

/// Aggregate query (H5, H6, G82, G87, G98, G102, G105, G106). Handles
/// `count(*)`, `count(DISTINCT col)`, and `count(col)` with optional
/// `GROUP BY` + `ORDER BY` from `:group` / `:order count(n) desc`.
fn aggregate_query(
    relation: &str,
    rel_cols: &[String],
    agg: AggSpec,
    filters: String,
    modifiers: String,
    params: &BTreeMap<String, serde_json::Value>,
) -> Result<Translation, String> {
    // Positional aliases in filters (`et = $et` where `et` is the 2nd
    // relation-block alias → element_type) must resolve before the WHERE
    // compiles — same step the non-aggregate read path takes (G107).
    let filters = resolve_filter_aliases(relation, &filters, rel_cols);
    let filters = append_literal_constraints(&filters, rel_cols);
    let (where_sql, where_params) = compile_filters(filters, params)?;
    let (group_sql, order_sql, _group_cols, mut mod_params) = compile_group_order(&modifiers);

    // Resolve a head binding to its real table column. Cozo heads can alias
    // positions: `?[node, count(node)] := *relationships[node, _, _, _, _, _]`
    // binds `node` to position 0 → `source_qualified`. Emitting the alias
    // name verbatim (`SELECT "node", count("node")`) fails with E42703.
    // `rel_cols` is the relation block's positional alias list; the i-th
    // alias sits at the i-th table column (canonical column order below).
    let table_cols = canonical_columns(relation);
    let resolve_alias = |expr: &str| -> String {
        if let Some(idx) = rel_cols.iter().position(|c| c == expr) {
            if let Some(col) = table_cols.get(idx) {
                return quote_ident(col);
            }
        }
        quote_ident(expr)
    };

    // Resolve `count(expr)` to a SQL expression. `expr` may be a column name
    // (use as-is), `_` (any literal — fall back to `*`), or `DISTINCT col`.
    // Cozo positional aliases in count() heads are single ASCII letters
    // (`n`, `a`, `b`, ...) — these mean "count rows" because every position
    // in the relation block is bound to an alias, but the count is over
    // rows. Render as `count(*)` for these.
    let count_expr = if agg.distinct {
        format!("DISTINCT {}", resolve_alias(&agg.expr))
    } else if agg.expr == "_" || agg.expr.is_empty() {
        "*".to_string()
    } else if agg.expr.len() == 1 && agg.expr.chars().next().unwrap().is_ascii_alphabetic() {
        // Single-letter positional alias → count(*).
        "*".to_string()
    } else if is_column_token(&agg.expr) {
        resolve_alias(&agg.expr)
    } else {
        // Computed expression (e.g. `count(n)` where `n` is a bound alias);
        // fall back to bare expression.
        agg.expr.clone()
    };

    let select_list = if agg.extras.is_empty() {
        format!("count({count_expr})")
    } else {
        let extras = agg
            .extras
            .iter()
            .map(|c| resolve_alias(c))
            .collect::<Vec<_>>()
            .join(", ");
        format!("{extras}, count({count_expr})")
    };

    // Multi-col aggregate heads (`?[element_type, count(element_type)]`)
    // implicitly group by the non-aggregate columns — cozo semantics.
    // `:group` may also be explicit; `compile_group_order` covers that.
    let group_sql = if group_sql.is_empty() && !agg.extras.is_empty() {
        format!(
            " GROUP BY {}",
            agg.extras
                .iter()
                .map(|c| resolve_alias(c))
                .collect::<Vec<_>>()
                .join(", ")
        )
    } else {
        group_sql
    };

    let sql = if where_sql.is_empty() {
        format!(
            "SELECT {select_list} FROM {relation}{group_sql}{order_sql}",
            group_sql = if group_sql.is_empty() {
                String::new()
            } else {
                format!(" {group_sql}")
            },
            order_sql = if order_sql.is_empty() {
                String::new()
            } else {
                format!(" {order_sql}")
            }
        )
    } else {
        format!(
            "SELECT {select_list} FROM {relation} WHERE {where_sql}{group_sql}{order_sql}",
            group_sql = if group_sql.is_empty() {
                String::new()
            } else {
                format!(" {group_sql}")
            },
            order_sql = if order_sql.is_empty() {
                String::new()
            } else {
                format!(" {order_sql}")
            }
        )
    };

    let mut all_params = where_params;
    all_params.append(&mut mod_params);
    let mut head = Vec::with_capacity(1 + agg.extras.len());
    head.extend(agg.extras.iter().cloned());
    head.push(
        agg.head_label
            .clone()
            .unwrap_or_else(|| format!("count({})", agg.expr)),
    );
    Ok(Translation::read(sql, all_params, head))
}

/// Parse `:group [a, b]` and `:order [-]count(n) [desc]`. Returns
/// `(group_clause, order_clause, group_cols, params)`.
fn compile_group_order(
    modifiers: &str,
) -> (
    String,
    String,
    Vec<String>,
    Vec<Box<dyn ToSql + Sync + Send>>,
) {
    let mut group_cols: Vec<String> = Vec::new();
    let mut order_clause = String::new();
    let params: Vec<Box<dyn ToSql + Sync + Send>> = Vec::new();

    // Split on tokens (`:group`, `:order`).
    let mut rest = modifiers;
    while let Some(idx) = rest.find(':') {
        let op = rest[idx + 1..]
            .split(|c: char| c.is_whitespace())
            .next()
            .unwrap_or("");
        let after = &rest[idx + 1 + op.len()..];
        let (consumed, _value) = match op {
            "group" => match extract_bracket_block(after) {
                Some((body, consumed)) => {
                    group_cols = body.split(',').map(|s| s.trim().to_string()).collect();
                    (consumed, ())
                }
                None => (after.len(), ()),
            },
            "order" => {
                // `:order -count(n) desc` → `ORDER BY count(n) DESC`
                let trimmed = after.trim_start();
                // The value ends at the next `:` operator — `:order -count(x)
                // :limit 10` must not fold `:limit 10` into the expression.
                let trimmed = trimmed
                    .split_once(':')
                    .map(|(head, _)| head)
                    .unwrap_or(trimmed)
                    .trim_end();
                // Match `count(...) desc` or `count(...) asc` — consume the trailing dir.
                let (expr, desc) = if let Some(stripped) = trimmed.strip_prefix('-') {
                    (stripped.trim_start(), true)
                } else {
                    (trimmed, false)
                };
                // Split off trailing direction word if present.
                let (expr, trailing_dir) = {
                    let lower = expr.to_ascii_lowercase();
                    if lower.ends_with(" desc") {
                        (expr[..expr.len() - 5].trim_end(), true)
                    } else if lower.ends_with(" asc") {
                        (expr[..expr.len() - 4].trim_end(), false)
                    } else {
                        (expr, desc)
                    }
                };
                let expr_lc = expr.to_ascii_lowercase();
                let order_expr = if expr_lc.starts_with("count(") && expr_lc.ends_with(')') {
                    let inner = &expr[6..expr.len() - 1];
                    if inner == "_" || inner.is_empty() {
                        "count(*)".to_string()
                    } else if inner.len() == 1
                        && inner.chars().next().unwrap().is_ascii_alphabetic()
                    {
                        "*".to_string()
                    } else {
                        format!("count({})", quote_ident(inner))
                    }
                } else if expr_lc.starts_with("count(distinct ") && expr_lc.ends_with(')') {
                    let inner = &expr[15..expr.len() - 1];
                    format!("count(DISTINCT {})", quote_ident(inner))
                } else {
                    quote_ident(expr)
                };
                let dir = if trailing_dir { "DESC" } else { "ASC" };
                order_clause = format!("ORDER BY {order_expr} {dir}");
                // Consume up to the next `:` operator (next :group/:order/:limit)
                // or end of input — we already produced the order_clause,
                // nothing more to parse here. The `:order` value ends at the
                // first `:` (e.g. `:order -count(x) :limit 10` must NOT fold
                // `:limit 10` into the ORDER BY expression).
                let next_colon = after.find(':').unwrap_or(after.len());
                // Skip past the trailing direction word too, so the next
                // iteration resumes at the next operator.
                let after_dir = if trailing_dir {
                    next_colon + 1
                } else {
                    next_colon
                };
                (after_dir, ())
            }
            _ => (op.len(), ()),
        };
        rest = &rest[idx + 1 + consumed..];
    }

    let group_clause = if group_cols.is_empty() {
        String::new()
    } else {
        format!(
            "GROUP BY {}",
            group_cols
                .iter()
                .map(|c| quote_ident(c))
                .collect::<Vec<_>>()
                .join(", ")
        )
    };
    (group_clause, order_clause, group_cols, params)
}

/// Consume a `[...]` block immediately after `start`; return `(body, bytes_consumed)`.
fn extract_bracket_block(start: &str) -> Option<(&str, usize)> {
    let s = start.trim_start();
    let trimmed_len = start.len() - s.len();
    if !s.starts_with('[') {
        return None;
    }
    let close = s.find(']')?;
    Some((&s[1..close], trimmed_len + close + 1))
}

/// Compile the `:limit N :offset N` trailer. Filters are already split off.
fn compile_modifiers(
    modifiers: &str,
    _head: &[String],
    params: &BTreeMap<String, serde_json::Value>,
) -> (String, Vec<Box<dyn ToSql + Sync + Send>>) {
    let mut out = String::new();
    let bound: Vec<Box<dyn ToSql + Sync + Send>> = Vec::new();
    let placeholder_idx = params.len() + 1; // first available $N — but we don't track real indices across split; use anonymous later
    let _ = placeholder_idx;

    // We append :limit/:offset parameters using the existing param map by
    // adding fresh $N placeholders. Simpler: inline integer literals — the
    // inventory's :limit/:offset are always small ints baked into the query
    // string by the caller, not bound. Confirm in §2: yes, every `:limit`
    // token is `format!(":limit {limit}")` with `limit: usize`. Inline.
    let mut rest = modifiers;
    while let Some(idx) = rest.find(':') {
        let op = rest[idx + 1..]
            .split(|c: char| c.is_whitespace() || c == ',')
            .next()
            .unwrap_or("");
        let after = rest[idx + 1 + op.len()..].trim_start();
        match op {
            "limit" => {
                let (num, consumed) = parse_uint(after);
                if !out.is_empty() && !out.ends_with(' ') {
                    out.push(' ');
                }
                out.push_str(&format!("LIMIT {num}"));
                rest = &rest[idx + 1 + op.len() + consumed..];
                continue;
            }
            "offset" => {
                let (num, consumed) = parse_uint(after);
                if !out.is_empty() && !out.ends_with(' ') {
                    out.push(' ');
                }
                out.push_str(&format!("OFFSET {num}"));
                rest = &rest[idx + 1 + op.len() + consumed..];
                continue;
            }
            "order" => {
                // Handled by compile_group_order when it precedes limit/offset;
                // otherwise handle here.
                let trimmed = after.trim_start();
                let (expr, desc) = if let Some(stripped) = trimmed.strip_prefix('-') {
                    (stripped.trim_start(), true)
                } else {
                    (trimmed, false)
                };
                let expr_lc = expr.to_ascii_lowercase();
                let order_expr = if expr_lc.starts_with("count(") && expr_lc.ends_with(')') {
                    let inner = &expr[6..expr.len() - 1];
                    if inner == "_" || inner.is_empty() {
                        "count(*)".to_string()
                    } else {
                        format!("count({})", quote_ident(inner))
                    }
                } else {
                    quote_ident(expr)
                };
                let dir = if desc { "DESC" } else { "ASC" };
                if !out.is_empty() && !out.ends_with(' ') {
                    out.push(' ');
                }
                out.push_str(&format!("ORDER BY {order_expr} {dir}"));
                // Consume until next `:` operator or end.
                let next = after.find(':').unwrap_or(after.len());
                rest = &rest[idx + 1 + op.len() + next..];
                continue;
            }
            "group" => {
                let (body, consumed) = match extract_bracket_block(after) {
                    Some(b) => b,
                    None => ("", after.len()),
                };
                let cols: Vec<String> = body.split(',').map(|s| s.trim().to_string()).collect();
                if !out.is_empty() && !out.ends_with(' ') {
                    out.push(' ');
                }
                out.push_str(&format!(
                    "GROUP BY {}",
                    cols.iter()
                        .map(|c| quote_ident(c))
                        .collect::<Vec<_>>()
                        .join(", ")
                ));
                rest = &rest[idx + 1 + op.len() + consumed..];
                continue;
            }
            _ => {
                // Skip unknown operator.
                let next = rest[idx + 1..].find(':').unwrap_or(rest.len() - idx - 1);
                rest = &rest[idx + 1 + next..];
                continue;
            }
        }
    }
    (out, bound)
}

fn parse_uint(s: &str) -> (u64, usize) {
    let mut end = 0;
    for (i, c) in s.char_indices() {
        if c.is_ascii_digit() {
            end = i + 1;
        } else {
            break;
        }
    }
    let n: u64 = s[..end].parse().unwrap_or(0);
    (n, end)
}

/// Translate the filter list (everything between the relation block and
/// `:limit`/`:offset`/`:order`/`:group`) into a WHERE clause + bound params.
/// Handles: `col = $x`, `col != $x`, `col = "literal"`, `col != null`,
/// `col in [..]`, `col in $arr`, `regex_matches(col, "literal"|$pat)`,
/// `str_includes(col1, col2)`, `str_contains(col, "literal"|$param)`,
/// `starts_with(col, "literal"|$param)`, `(a = $x or a = $y)`, `col >= $x`,
/// `col < $x`, computed `col = col1 - col2`, and conjunctions (comma).
fn compile_filters(
    filters: String,
    params: &BTreeMap<String, serde_json::Value>,
) -> Result<(String, Vec<Box<dyn ToSql + Sync + Send>>), String> {
    let trimmed = filters.trim().trim_start_matches(',').trim().to_string();
    if trimmed.is_empty() {
        return Ok((String::new(), Vec::new()));
    }
    // Split into clauses — cozo uses commas or newlines as AND separators.
    let clauses = split_clauses(&trimmed);
    let mut out_clauses: Vec<String> = Vec::with_capacity(clauses.len());
    let mut out_params: Vec<Box<dyn ToSql + Sync + Send>> = Vec::new();
    let mut next_idx = 1;
    for clause in clauses {
        let (rendered, used, _) = render_clause(clause, params, &mut next_idx)?;
        out_clauses.push(rendered);
        out_params.extend(used);
    }
    Ok((out_clauses.join(" AND "), out_params))
}

/// Split comma/newline separated clauses, respecting `(...)` parentheses.
/// Also splits on top-level ` and ` so `a = $x and b = $y` becomes two
/// AND-joined clauses.
fn split_clauses(s: &str) -> Vec<&str> {
    let mut out = Vec::new();
    let mut depth = 0usize;
    let mut last = 0usize;
    let bytes = s.as_bytes();
    let mut i = 0usize;
    while i < bytes.len() {
        let c = bytes[i] as char;
        match c {
            '(' | '[' => depth += 1,
            ')' | ']' => depth = depth.saturating_sub(1),
            ',' | '\n' if depth == 0 => {
                // `\n or ...` / `, or ...` continues the previous clause
                // (top-level OR chain — search_by_content / search_knowledge
                // write each alternative on its own line). Only split when
                // the next non-whitespace is NOT `or `.
                let rest = s[i + 1..].trim_start();
                if !rest.starts_with("or ") && !rest.starts_with("or\n") && !rest.starts_with("or(")
                {
                    let piece = s[last..i].trim();
                    if !piece.is_empty() {
                        out.push(piece);
                    }
                    last = i + 1;
                }
            }
            'a' if depth == 0
                && i >= 1
                && bytes[i - 1] == b' '
                && i + 3 <= bytes.len()
                && &s[i..i + 3] == "and"
                && (i + 3 == bytes.len() || (bytes[i + 3] as char).is_ascii_whitespace()) =>
            {
                let piece = s[last..(i - 1)].trim();
                if !piece.is_empty() {
                    out.push(piece);
                }
                last = i + 4; // skip "and" + the trailing whitespace
                i += 3;
            }
            _ => {}
        }
        i += 1;
    }
    let tail = s[last..].trim();
    if !tail.is_empty() {
        out.push(tail);
    }
    out
}

/// Split a clause on top-level ` or ` separators (outside parens/brackets).
fn split_top_level_or(s: &str) -> Vec<&str> {
    let mut out = Vec::new();
    let mut depth = 0usize;
    let mut last = 0usize;
    let bytes = s.as_bytes();
    let mut i = 0usize;
    while i < bytes.len() {
        let c = bytes[i] as char;
        match c {
            '(' | '[' => depth += 1,
            ')' | ']' => depth = depth.saturating_sub(1),
            'o' if depth == 0
                && i >= 1
                && bytes[i - 1] == b' '
                && i + 2 <= bytes.len()
                && &s[i..i + 2] == "or"
                && (i + 2 == bytes.len() || (bytes[i + 2] as char).is_ascii_whitespace()) =>
            {
                let piece = s[last..(i - 1)].trim();
                if !piece.is_empty() {
                    out.push(piece);
                }
                // Skip "or" plus any following whitespace (last is the start
                // of the next part).
                last = i + 3;
                i += 1;
            }
            _ => {}
        }
        i += 1;
    }
    let tail = s[last..].trim();
    if !tail.is_empty() {
        out.push(tail);
    }
    out
}

fn find_top_level_or(s: &str) -> Option<usize> {
    let parts = split_top_level_or(s);
    if parts.len() > 1 {
        Some(1)
    } else {
        None
    }
}

fn render_clause<'a>(
    clause: &'a str,
    params: &BTreeMap<String, serde_json::Value>,
    next_idx: &mut usize,
) -> Result<(String, Vec<Box<dyn ToSql + Sync + Send>>, &'a str), String> {
    let trimmed = clause.trim();

    // Top-level OR chain (no enclosing parens):
    //   `str_includes(a, "x") or str_includes(b, "x") or ...`
    // The `search_by_content` / `search_knowledge` queries write their
    // alternatives as separate `or ...` lines; `split_clauses` keeps them
    // attached to the first clause (see the `\n or ` continuation), so a
    // chain lands here whole. Split on top-level ` or ` and OR-join.
    if find_top_level_or(trimmed).is_some() {
        let parts = split_top_level_or(trimmed);
        let mut rendered = Vec::with_capacity(parts.len());
        let mut used: Vec<Box<dyn ToSql + Sync + Send>> = Vec::new();
        for p in parts {
            let (r, u, _) = render_clause(p.trim(), params, next_idx)?;
            rendered.push(r);
            used.extend(u);
        }
        return Ok((format!("({})", rendered.join(" OR ")), used, clause));
    }

    // Parenthesized OR/AND group: `(a = $x or a = $y)`.
    if trimmed.starts_with('(') && trimmed.ends_with(')') {
        let inner = &trimmed[1..trimmed.len() - 1];
        let parts: Vec<&str> = inner.split(" or ").collect();
        if parts.len() > 1 {
            let mut rendered = Vec::with_capacity(parts.len());
            let mut used: Vec<Box<dyn ToSql + Sync + Send>> = Vec::new();
            for p in parts {
                let (r, u, _) = render_clause(p.trim(), params, next_idx)?;
                rendered.push(r);
                used.extend(u);
            }
            return Ok((format!("({})", rendered.join(" OR ")), used, clause));
        }
        let parts: Vec<&str> = inner.split(" and ").collect();
        if parts.len() > 1 {
            let mut rendered = Vec::with_capacity(parts.len());
            let mut used: Vec<Box<dyn ToSql + Sync + Send>> = Vec::new();
            for p in parts {
                let (r, u, _) = render_clause(p.trim(), params, next_idx)?;
                rendered.push(r);
                used.extend(u);
            }
            return Ok((format!("({})", rendered.join(" AND ")), used, clause));
        }
    }

    // Negated regex: `!regex_matches(col, "literal")` → `col !~ pat`.
    if let Some(rest) = trimmed.strip_prefix("!regex_matches(") {
        let body = rest.strip_suffix(')').unwrap_or(rest);
        let mut parts = body.splitn(2, ',');
        let col = parts
            .next()
            .ok_or_else(|| format!("bad !regex_matches: {trimmed}"))?
            .trim();
        let pat = parts
            .next()
            .ok_or_else(|| format!("bad !regex_matches: {trimmed}"))?
            .trim();
        let col_sql = string_op_col(col);
        let (placeholder, used) = match strip_lowercase_wrapper(pat) {
            Some(inner) => {
                let (r, u) = render_value_or_param(inner, params, next_idx)?;
                (format!("lower({r})"), u)
            }
            None => render_value_or_param(pat, params, next_idx)?,
        };
        return Ok((format!("{col_sql} !~ {placeholder}"), used, clause));
    }
    // regex_matches(lowercase(col), "literal") or regex_matches(col, $pat)
    if let Some(rest) = trimmed.strip_prefix("regex_matches(") {
        let body = rest.strip_suffix(')').unwrap_or(rest);
        let mut parts = body.splitn(2, ',');
        let col = parts
            .next()
            .ok_or_else(|| format!("bad regex_matches: {trimmed}"))?
            .trim();
        let pat = parts
            .next()
            .ok_or_else(|| format!("bad regex_matches: {trimmed}"))?
            .trim();
        let col_sql = string_op_col(col);
        // `regex_matches(col, lowercase($pat))` — strip the wrapper so the
        // param binds (`lower($1)`), not interpolated.
        let (placeholder, used) = match strip_lowercase_wrapper(pat) {
            Some(inner) => {
                let (r, u) = render_value_or_param(inner, params, next_idx)?;
                (format!("lower({r})"), u)
            }
            None => render_value_or_param(pat, params, next_idx)?,
        };
        return Ok((format!("{col_sql} ~ {placeholder}"), used, clause));
    }
    // str_includes(lowercase(a), lowercase(b))
    if let Some(rest) = trimmed.strip_prefix("str_includes(") {
        let body = rest.strip_suffix(')').unwrap_or(rest);
        let mut parts = body.splitn(2, ',');
        let hay = parts
            .next()
            .ok_or_else(|| format!("bad str_includes: {trimmed}"))?
            .trim();
        let needle = parts
            .next()
            .ok_or_else(|| format!("bad str_includes: {trimmed}"))?
            .trim();
        let hay_sql = string_op_col(hay);
        // The needle may be `lowercase($pattern)` / `lowercase("lit")` /
        // `$pattern` / `"lit"` — bind via the value-or-param path so
        // params stay bound (PG `lower($1)`), not interpolated.
        let (needle_sql, used) = match strip_lowercase_wrapper(needle) {
            Some(inner) => {
                let (rendered, used) = render_value_or_param(inner, params, next_idx)?;
                (format!("lower({rendered})"), used)
            }
            None => {
                let (rendered, used) = render_value_or_param(needle, params, next_idx)?;
                (rendered, used)
            }
        };
        return Ok((
            format!("{hay_sql} LIKE '%' || {needle_sql} || '%'"),
            used,
            clause,
        ));
    }
    // str_contains(a, "literal"|$param)
    if let Some(rest) = trimmed.strip_prefix("str_contains(") {
        let body = rest.strip_suffix(')').unwrap_or(rest);
        let mut parts = body.splitn(2, ',');
        let hay = parts
            .next()
            .ok_or_else(|| format!("bad str_contains: {trimmed}"))?
            .trim();
        let needle = parts
            .next()
            .ok_or_else(|| format!("bad str_contains: {trimmed}"))?
            .trim();
        let hay_sql = string_op_col(hay);
        let (placeholder, used) = match strip_lowercase_wrapper(needle) {
            Some(inner) => {
                let (r, u) = render_value_or_param(inner, params, next_idx)?;
                (format!("lower({r})"), u)
            }
            None => render_value_or_param(needle, params, next_idx)?,
        };
        return Ok((
            format!("{hay_sql} LIKE '%' || {placeholder} || '%'"),
            used,
            clause,
        ));
    }
    // starts_with(a, "literal"|$param)
    if let Some(rest) = trimmed.strip_prefix("starts_with(") {
        let body = rest.strip_suffix(')').unwrap_or(rest);
        let mut parts = body.splitn(2, ',');
        let hay = parts
            .next()
            .ok_or_else(|| format!("bad starts_with: {trimmed}"))?
            .trim();
        let needle = parts
            .next()
            .ok_or_else(|| format!("bad starts_with: {trimmed}"))?
            .trim();
        let hay_sql = string_op_col(hay);
        let (placeholder, used) = match strip_lowercase_wrapper(needle) {
            Some(inner) => {
                let (r, u) = render_value_or_param(inner, params, next_idx)?;
                (format!("lower({r})"), u)
            }
            None => render_value_or_param(needle, params, next_idx)?,
        };
        return Ok((format!("{hay_sql} LIKE {placeholder} || '%'"), used, clause));
    }

    // Binary predicate: `col = "literal"`, `col = $x`, `col = expr`,
    // `col != null`, `col >= $x`, `col in [...]`, etc. Split on the comparison
    // operator outside any parens.
    // First check `in` (a word operator, not a symbol).
    if let Some(in_idx) = find_word_operator(trimmed, "in") {
        let lhs = trimmed[..in_idx].trim();
        let rhs = trimmed[in_idx + 2..].trim();
        let lhs_sql = scalar_expr(lhs);
        // RHS is either an inline list literal `["a", "b"]` or `$var`.
        if rhs.starts_with('[') && rhs.ends_with(']') {
            // Flat list `["a", "b"]` (IN RHS) or nested `[["a"], ["b"]]`
            // (delete-where style). Detect by trying flat first.
            let strs: Vec<String> = match serde_json::from_str::<Vec<serde_json::Value>>(rhs) {
                Ok(flat) => flat
                    .into_iter()
                    .map(|v| match v {
                        serde_json::Value::String(s) => s,
                        other => other.to_string(),
                    })
                    .collect(),
                Err(_) => {
                    let items = parse_nested_lists(rhs)?;
                    items
                        .into_iter()
                        .filter_map(|mut row| row.pop())
                        .map(|v| match v {
                            serde_json::Value::String(s) => s,
                            other => other.to_string(),
                        })
                        .collect()
                }
            };
            let placeholder = format!("${}", *next_idx);
            *next_idx += 1;
            return Ok((
                format!("{lhs_sql} = ANY({placeholder}::text[])"),
                vec![Box::new(strs)],
                clause,
            ));
        }
        if let Some(name) = rhs.strip_prefix('$') {
            let v = params.get(name).cloned().unwrap_or(serde_json::Value::Null);
            let strs: Vec<String> = match v {
                serde_json::Value::Array(arr) => arr
                    .into_iter()
                    .map(|item| match item {
                        serde_json::Value::String(s) => s,
                        other => other.to_string(),
                    })
                    .collect(),
                _ => Vec::new(),
            };
            let placeholder = format!("${}", *next_idx);
            *next_idx += 1;
            return Ok((
                format!("{lhs_sql} = ANY({placeholder}::text[])"),
                vec![Box::new(strs)],
                clause,
            ));
        }
        return Err(format!("unsupported IN RHS: {rhs}"));
    }

    let op_pos = find_top_level_op(trimmed)?;
    let op_raw = &trimmed[op_pos..op_pos + 2.min(trimmed.len() - op_pos)];
    let (op, op_len) = if matches!(op_raw, "==" | "!=" | ">=" | "<=") {
        // Cozo uses `==` for equality (D39 attr syntax); PG wants `=`.
        if op_raw == "==" {
            ("=", 2) // consume BOTH `=` chars from the RHS slice
        } else {
            (op_raw, 2)
        }
    } else if let Some(c) = trimmed[op_pos..].chars().next() {
        match c {
            '=' | '<' | '>' => (&trimmed[op_pos..op_pos + 1], 1),
            _ => return Err(format!("unknown operator in clause: {trimmed}")),
        }
    } else {
        return Err(format!("unknown operator in clause: {trimmed}"));
    };
    let lhs = trimmed[..op_pos].trim();
    let rhs = trimmed[op_pos + op_len..].trim();

    let lhs_sql = scalar_expr(lhs);

    // Null literals.
    if rhs == "null" {
        let sql = match op {
            "=" => format!("{lhs_sql} IS NULL"),
            "!=" | "<>" => format!("{lhs_sql} IS NOT NULL"),
            _ => return Err(format!("null with non-equality: {trimmed}")),
        };
        return Ok((sql, Vec::new(), clause));
    }

    // Literal string (quoted).
    if rhs.starts_with('"') && rhs.ends_with('"') && rhs.len() >= 2 {
        let inner = &rhs[1..rhs.len() - 1];
        let value = unescape_cozo_string(inner);
        let placeholder = format!("${}", *next_idx);
        *next_idx += 1;
        let used = vec![json_to_pg(serde_json::Value::String(value))];
        return Ok((format!("{lhs_sql} {op} {placeholder}"), used, clause));
    }

    // Bound parameter `$name`.
    if let Some(name) = rhs.strip_prefix('$') {
        let v = params.get(name).cloned().unwrap_or(serde_json::Value::Null);
        let placeholder = format!("${}", *next_idx);
        *next_idx += 1;
        let used = vec![json_to_pg(v)];
        return Ok((format!("{lhs_sql} {op} {placeholder}"), used, clause));
    }

    // Boolean literal `true` / `false` — bind as a bool param. A bare
    // `false` must not fall through to scalar_expr, which would emit
    // `"is_deleted" = false` and Postgres would read `false` as a column
    // name (E42703).
    if rhs == "true" || rhs == "false" {
        let placeholder = format!("${}", *next_idx);
        *next_idx += 1;
        let used = vec![json_to_pg(serde_json::Value::Bool(rhs == "true"))];
        return Ok((format!("{lhs_sql} {op} {placeholder}"), used, clause));
    }

    // Computed RHS expression (e.g. `span = line_end - line_start`,
    // `(line_end - line_start + 1) >= 50`).
    let rhs_sql = scalar_expr(rhs);
    Ok((format!("{lhs_sql} {op} {rhs_sql}"), Vec::new(), clause))
}

/// Locate the first comparison operator (`=`, `==`, `!=`, `<>`, `<`, `>`,
/// `<=`, `>=`) at top level (not inside parens or string literals).
fn find_top_level_op(s: &str) -> Result<usize, String> {
    let mut depth = 0usize;
    let mut in_string = false;
    let bytes = s.as_bytes();
    let mut i = 0usize;
    while i < bytes.len() {
        let c = bytes[i] as char;
        if in_string {
            if c == '"' {
                in_string = false;
            }
            i += 1;
            continue;
        }
        match c {
            '"' => in_string = true,
            '(' => depth += 1,
            ')' => depth = depth.saturating_sub(1),
            '=' | '!' | '<' | '>' if depth == 0 => return Ok(i),
            _ => {}
        }
        i += 1;
    }
    Err(format!("no top-level operator: {s}"))
}

/// Strip a `lowercase(...)` wrapper, returning the inner token.
fn strip_lowercase_wrapper(s: &str) -> Option<&str> {
    let t = s.trim();
    t.strip_prefix("lowercase(")?.strip_suffix(')')
}

/// Locate a top-level word operator (`in`, `and`, `or`) surrounded by
/// whitespace. Returns the byte offset of the operator.
fn find_word_operator(s: &str, word: &str) -> Option<usize> {
    let bytes = s.as_bytes();
    let mut depth = 0usize;
    let mut in_string = false;
    let mut i = 0usize;
    while i < bytes.len() {
        let c = bytes[i] as char;
        if in_string {
            if c == '"' {
                in_string = false;
            }
            i += 1;
            continue;
        }
        match c {
            '"' => {
                in_string = true;
                i += 1;
            }
            '(' => {
                depth += 1;
                i += 1;
            }
            ')' => {
                depth = depth.saturating_sub(1);
                i += 1;
            }
            c if c.is_ascii_alphabetic() && depth == 0 => {
                // Scan to end of word.
                let start = i;
                while i < bytes.len() && (bytes[i] as char).is_ascii_alphabetic() {
                    i += 1;
                }
                let token = &s[start..i];
                if token == word {
                    let before_ok = start == 0
                        || (bytes[start - 1] as char).is_ascii_whitespace()
                        || bytes[start - 1] == b'(';
                    let after_ok = i == bytes.len()
                        || (bytes[i] as char).is_ascii_whitespace()
                        || bytes[i] == b'[';
                    if before_ok && after_ok {
                        return Some(start);
                    }
                }
            }
            _ => {
                i += 1;
            }
        }
    }
    None
}

/// Render an inline scalar expression — column references, function calls
/// wrapping a column (`lowercase(x)`), or arithmetic on column refs.
fn scalar_expr(s: &str) -> String {
    let trimmed = s.trim();
    // Function call wrapping: `lowercase(x)`, `(line_end - line_start + 1)`.
    if trimmed.starts_with('(') && trimmed.ends_with(')') {
        // Pass through; the caller already handled parens.
        return trimmed.to_string();
    }
    if let Some(rest) = trimmed.strip_prefix("lowercase(") {
        let body = rest.strip_suffix(')').unwrap_or(rest);
        return format!("lower({})", scalar_expr(body));
    }
    if let Some(rest) = trimmed.strip_prefix("upper(") {
        let body = rest.strip_suffix(')').unwrap_or(rest);
        return format!("upper({})", scalar_expr(body));
    }
    // Quoted string literal: emit as a bound param.
    if trimmed.starts_with('"') && trimmed.ends_with('"') && trimmed.len() >= 2 {
        return trimmed.to_string(); // pass through; caller usually pre-binds
    }
    // Column identifier.
    if is_column_token(trimmed) {
        return quote_ident(trimmed);
    }
    // Arithmetic on columns/integers: `line_end - line_start + 1`.
    trimmed.to_string()
}

/// JSONB columns in schema.sql (cozo stored these as JSON *strings*, so
/// string ops like `str_contains`/`regex_matches` applied to the raw text).
/// PG needs an explicit `::text` cast for those operators to compile.
const JSONB_COLUMNS: &[&str] = &[
    "metadata",
    "tags",
    "deploy_envs",
    "graph_read_users",
    "graph_write_users",
    "members",
    "affected_services",
    "elements_by_type_json",
    "relationships_by_type_json",
    "vectors_by_type_json",
    // Auth tables (004_auth): access_tokens.scopes is JSONB.
    "scopes",
];

/// Render a column reference for a string operator (`LIKE`/`~`), casting
/// JSONB columns to text so the operator compiles (H5 — `str_contains(
/// metadata, "...")` on code_elements.metadata; PG column is JSONB, cozo
/// stored the JSON as a string).
fn string_op_col(s: &str) -> String {
    let trimmed = s.trim();
    // Handle `lowercase(col)` wrappers.
    if let Some(inner) = trimmed.strip_prefix("lowercase(") {
        let inner = inner.strip_suffix(')').unwrap_or(inner);
        let inner_sql = string_op_col(inner);
        return format!("lower({inner_sql})");
    }
    if JSONB_COLUMNS.contains(&trimmed) {
        return format!("{}::text", scalar_expr(trimmed));
    }
    scalar_expr(trimmed)
}

fn is_column_token(s: &str) -> bool {
    let mut chars = s.chars();
    match chars.next() {
        Some(c) if c.is_ascii_alphabetic() || c == '_' => {}
        _ => return false,
    }
    chars.all(|c| c.is_ascii_alphanumeric() || c == '_')
}

fn unescape_cozo_string(s: &str) -> String {
    // Cozo `\\` and `\"` escape sequences inside `"..."`.
    let mut out = String::with_capacity(s.len());
    let mut chars = s.chars().peekable();
    while let Some(c) = chars.next() {
        if c == '\\' {
            match chars.next() {
                Some('"') => out.push('"'),
                Some('\\') => out.push('\\'),
                Some('n') => out.push('\n'),
                Some('t') => out.push('\t'),
                Some(other) => {
                    out.push('\\');
                    out.push(other);
                }
                None => out.push('\\'),
            }
        } else {
            out.push(c);
        }
    }
    out
}

/// Render a value-or-param token (the RHS of a function-call filter). If it
/// looks like `$name` and the name exists in the params map, bind it.
/// Otherwise if it's a quoted literal, bind it. Otherwise fall back to
/// treating it as a scalar expression.
fn render_value_or_param(
    token: &str,
    params: &BTreeMap<String, serde_json::Value>,
    next_idx: &mut usize,
) -> Result<(String, Vec<Box<dyn ToSql + Sync + Send>>), String> {
    let t = token.trim();
    if let Some(name) = t.strip_prefix('$') {
        let v = params.get(name).cloned().unwrap_or(serde_json::Value::Null);
        let placeholder = format!("${}", *next_idx);
        *next_idx += 1;
        return Ok((placeholder, vec![json_to_pg(v)]));
    }
    if t.starts_with('"') && t.ends_with('"') && t.len() >= 2 {
        let inner = &t[1..t.len() - 1];
        let value = unescape_cozo_string(inner);
        let placeholder = format!("${}", *next_idx);
        *next_idx += 1;
        return Ok((
            placeholder,
            vec![json_to_pg(serde_json::Value::String(value))],
        ));
    }
    Ok((scalar_expr(t), Vec::new()))
}

// ---------------------------------------------------------------------------
// ANN (H1 — `~embedding_vectors:vec_idx`).
// ---------------------------------------------------------------------------

fn ann_vectors_table(rest: &str) -> Result<String, String> {
    let tilde = rest
        .find('~')
        .ok_or_else(|| "ANN query missing ~relation:vec_idx".to_string())?;
    let after = &rest[tilde + 1..];
    let brace = after
        .find('{')
        .ok_or_else(|| "ANN query missing '{' after relation".to_string())?;
    let rel = after[..brace].trim();
    let table = rel
        .strip_suffix(":vec_idx")
        .ok_or_else(|| format!("ANN relation must end with :vec_idx, got {rel}"))?;
    Ok(table.to_string())
}

fn ann_translation(
    rest: &str,
    head: &[String],
    _params: &BTreeMap<String, serde_json::Value>,
) -> Result<Translation, String> {
    // The vec_literal is interpolated INTO the query string by the caller
    // (build.rs / pipeline.rs), not passed via params. Extract the literal
    // and `k` from the body — both are always present.
    // Shape: `? qualified_name | query: vec([0.1, 0.2, ...]), k: 5, ef: 50, bind_distance: dist }`
    // or     `... k: 50, ef: 100, bind_distance: dist }`
    let vec_literal = extract_ann_vec_literal(rest)?;
    let k = extract_ann_int_field(rest, "k").unwrap_or(50);
    // Phase 4: ef becomes `SET LOCAL hnsw.ef_search` inside the same tx as
    // the SELECT — pgvector honours the GUC on each HNSW probe (cozo had it
    // as a per-call field; here we plumb it via the translator so callers
    // stay on the standard run_script path).
    let ef = extract_ann_int_field(rest, "ef");
    let dist_col = head.first().cloned().unwrap_or_else(|| "dist".to_string());
    let qn_col = head
        .get(1)
        .cloned()
        .unwrap_or_else(|| "qualified_name".to_string());
    // Per-model embed tables target the `~<vectors_relation>:vec_idx` ANN —
    // extract the table from the relation instead of hardcoding the legacy
    // `embedding_vectors` name.
    let vectors_table = ann_vectors_table(rest)?;

    // Distance note: cozo HNSW returns cosine distance; pgvector `<->` is L2
    // distance. On unit vectors the orders are identical (both monotone
    // decreasing in cosine similarity). We expose `<->` raw. Callers that
    // need a cosine-distance value should compute `(d*d)/2.0` themselves.
    let sql = format!(
        "SELECT vec <-> $1::text::vector AS {dist_col}, {qn_col} \
         FROM {vectors_table} \
         ORDER BY vec <-> $1::text::vector \
         LIMIT $2::int8",
        dist_col = quote_ident(&dist_col),
        qn_col = quote_ident(&qn_col),
        vectors_table = quote_ident(&vectors_table),
    );
    let used: Vec<Box<dyn ToSql + Sync + Send>> = vec![Box::new(vec_literal), Box::new(k as i64)];
    let gucs = ef
        .map(|n| vec![("hnsw.ef_search".to_string(), n.to_string())])
        .unwrap_or_default();
    Ok(Translation::read_with_gucs(sql, used, head.to_vec(), gucs))
}

fn extract_ann_vec_literal(s: &str) -> Result<String, String> {
    // Look for `vec([...])` and capture the inner, then wrap it in `[...]`
    // so the pgvector `::text::vector` cast accepts it (pgvector requires
    // the outer brackets).
    let lb = s
        .find("vec([")
        .ok_or_else(|| "ANN query missing vec([ literal)".to_string())?;
    let after = &s[lb + 5..];
    let rb = after
        .find("])")
        .ok_or_else(|| "ANN query missing closing ])".to_string())?;
    Ok(format!("[{}]", &after[..rb]))
}

fn extract_ann_int_field(s: &str, field: &str) -> Option<usize> {
    let needle = format!("{field}:");
    let i = s.find(&needle)?;
    let after = &s[i + needle.len()..];
    let trimmed = after.trim_start();
    let mut end = 0;
    for (j, c) in trimmed.char_indices() {
        if c.is_ascii_digit() {
            end = j + 1;
        } else {
            break;
        }
    }
    trimmed[..end].parse().ok()
}

// ---------------------------------------------------------------------------
// Writes — :put, :rm, :replace, :delete, :create, ::index, ::hnsw.
// ---------------------------------------------------------------------------

fn put_script(
    body: &str,
    params: &BTreeMap<String, serde_json::Value>,
) -> Result<Translation, String> {
    // Strip leading `:put ` and split into the data source (everything
    // before `:put`) and the target relation/columns after.
    // Body shape: `?[cols...] <- [data] :put table {cols => pk}` (or `{cols}`).
    // We look at the part after `:put` to find the target. The `translate`
    // dispatcher strips a leading `:put` prefix before calling here, so the
    // body may arrive without it (pure target form `table {cols} <- $args`).
    let (target, source) = match body.find(":put") {
        Some(idx) => (body[idx + 4..].trim(), body[..idx].trim()),
        None => (body.trim(), ""),
    };

    // Parse target — the part after `:put` looks like `table { cols => pk }`
    // or just `{ cols }` (no table name when the relation was already on
    // the left side of an arrow in a follow-up clause).
    // Strip the table-name prefix (everything before the first `{`), and
    // any trailing `<- $args` arrow (CH2 — `:put table {cols} <- $args`).
    // Keep the explicit table name (the prefix before `{`) — per-model
    // embed tables (`embedding_vectors_<model_id>`) must not fall back to
    // column-signature inference, which always resolves to `embedding_vectors`.
    let target = target.split("<-").next().unwrap_or(target).trim();
    let brace_open = target.find('{').unwrap_or(0);
    let explicit_table = target[..brace_open].trim();
    let tail = &target[brace_open..];
    let inner = tail.trim_start_matches('{').trim_end_matches('}').trim();
    let (cols, pk) = parse_put_target(inner)?;

    // Resolve whether the table is keyed (PK). The catalog of keyed tables
    // is small and stable — see `src/db/pg/schema.sql` PRIMARY KEY lines.
    let is_keyed = pk.is_some();

    // Source is a literal row list `[[v1, v2, ...], [v1, v2, ...]]` or a
    // bound variable `$batch_data` (G42/G46). Either way, we emit a single
    // SQL statement with a UNNEST or VALUES list.
    let source = source.trim_start_matches(',').trim();
    if source.starts_with("?[") {
        // `?[cols] <- [literal_rows]`
        let after_arrow = source
            .split_once("<-")
            .map(|(_, r)| r.trim())
            .ok_or_else(|| "missing <- in :put".to_string())?;
        return put_from_literal(
            after_arrow,
            &cols,
            pk.as_deref(),
            is_keyed,
            explicit_table,
            params,
        );
    }
    if let Some(name) = source.strip_prefix('$') {
        // `?[cols] <- $batch_data` — caller passes a Vec<Vec<serde_json::Value>>
        // under that key. We can't represent UNNEST generically here
        // (caller-typed), so fail with a clear message.
        let v = params.get(name).cloned();
        return put_from_batch(name, v, &cols, pk.as_deref(), is_keyed, explicit_table);
    }
    // `:put table {cols} <- $args` — the source is the whole rule body
    // (CH2 — content_hash.rs save_hashes: `:put index_hashes {path, hash}
    // <- $args` with `args = {path, hash}` a JSON object). Cozo binds the
    // object's keys to the target columns. The `translate` dispatcher may
    // have stripped the leading `:put`, so accept both forms.
    if let Some((target_part, arrow_part)) = body.split_once("<-") {
        let target_part = target_part.trim();
        let arrow_part = arrow_part.trim();
        if target_part.starts_with(":put") || source.is_empty() {
            if let Some(name) = arrow_part.strip_prefix('$') {
                let v = params.get(name).cloned().unwrap_or(serde_json::Value::Null);
                // Cozo's `<- $args` binds a NESTED LIST of rows
                // (`[[path, hash]]`); accept that as the primary form and
                // a JSON object as a convenience.
                let rows: Vec<Vec<serde_json::Value>> = match v {
                    serde_json::Value::Array(outer) => outer
                        .into_iter()
                        .filter_map(|r| match r {
                            serde_json::Value::Array(row) => Some(row),
                            _ => None,
                        })
                        .collect(),
                    serde_json::Value::Object(obj) => vec![cols
                        .iter()
                        .map(|c| obj.get(c).cloned().unwrap_or(serde_json::Value::Null))
                        .collect()],
                    _ => Vec::new(),
                };
                if rows.is_empty() || rows.iter().any(|r| r.len() != cols.len()) {
                    return Ok(Translation::write(
                        "SELECT 1 WHERE false".to_string(),
                        Vec::new(),
                    ));
                }
                let table = infer_table(&cols, pk.as_deref());
                // index_hashes is keyed by `path` even though the CH2 put
                // omits the `=>` marker (the relation is auto-created keyed
                // in cozo; schema.sql has PRIMARY KEY on path).
                let pk = pk.or_else(|| {
                    if table == "index_hashes" {
                        Some("path".to_string())
                    } else {
                        None
                    }
                });
                let keyed = is_keyed || pk.is_some();
                return build_insert(&table, &cols, pk.as_deref(), &rows, keyed);
            }
        }
    }
    Err(format!("unrecognized :put source: {source}"))
}

fn parse_put_target(inner: &str) -> Result<(Vec<String>, Option<String>), String> {
    // `{a, b => c, d}` — comma-separated, with `=>` marking the PK.
    let mut cols = Vec::new();
    let mut pk = None;
    for part in inner.split(',') {
        let p = part.trim();
        if p.is_empty() {
            continue;
        }
        if let Some((left, right)) = p.split_once("=>") {
            let left = left.trim();
            let right = right.trim();
            // The PK column is the one to the left of `=>`; the column to
            // the right is also included in the column list (Cozo treats the
            // PK side and the value side as both written).
            cols.push(left.to_string());
            pk = Some(left.to_string());
            cols.push(right.to_string());
        } else {
            cols.push(p.to_string());
        }
    }
    if cols.is_empty() {
        return Err("empty :put target".into());
    }
    Ok((cols, pk))
}

/// Inventory-derived list of keyed tables (those with `PRIMARY KEY` in
/// schema.sql). Used to choose plain INSERT vs INSERT ON CONFLICT.
#[allow(dead_code)]
fn is_keyed_table(_cols: &[String]) -> bool {
    // The keyed-ness signal is now the `=>` PK marker (see `parse_put_target`).
    // This helper is kept as a hook for callers that want to assert a
    // specific table's keying.
    false
}

fn put_from_literal(
    literal: &str,
    cols: &[String],
    pk: Option<&str>,
    is_keyed: bool,
    explicit_table: &str,
    params: &BTreeMap<String, serde_json::Value>,
) -> Result<Translation, String> {
    // Parse `[[a, b, c], [a, b, c], ...]`. We accept either a Rust-style
    // literal list (cozo callers use this) or a JSON array literal. Cozo
    // literals may reference bound params (`[[ $eq, $desc, ... ]]` — D1
    // business_logic writes); substitute them from the params map first.
    let literal = substitute_params(literal, params);
    let rows = parse_nested_lists(&literal)?;
    if rows.is_empty() {
        // No data → no-op write (preserves cozo's empty-result behaviour).
        return Ok(Translation::write(
            "SELECT 1 WHERE false".to_string(),
            Vec::new(),
        ));
    }
    // Validate arity.
    let n_cols = cols.len();
    for (i, r) in rows.iter().enumerate() {
        if r.len() != n_cols {
            return Err(format!(
                ":put row {i} has {} cols, expected {n_cols}",
                r.len()
            ));
        }
    }
    // Explicit table name wins over column-signature inference — `qualified_name`
    // is the PK of both embedding_state and embedding_vectors, and per-model
    // embed tables (`embedding_vectors_<model_id>`) must not resolve to the
    // legacy table.
    let table = if !explicit_table.is_empty() {
        explicit_table.to_string()
    } else {
        infer_table(cols, pk)
    };
    // Resolve the effective PK from the known table catalog when the caller
    // omitted the `=>` marker (Cozo allows `:put t {cols}` on a keyed table
    // — the PK is implied by the table). Without this, a re-`put` of an
    // existing row hits `embedding_state_pkey` (duplicate key).
    let (pk, keyed) = resolve_effective_pk(&table, pk, is_keyed);
    build_insert(&table, cols, pk.as_deref(), &rows, keyed)
}

/// Known primary-key columns per table (schema.sql). Used to turn a
/// `:put t {cols}` (no `=>`) on a keyed table into an `INSERT ... ON
/// CONFLICT (pk) DO UPDATE` instead of a plain INSERT.
fn pk_for_table(table: &str) -> Option<&'static str> {
    match table {
        "embedding_state" => Some("qualified_name"),
        "embedding_vectors" => Some("qualified_name"),
        "index_inventory" => Some("key"),
        "index_hashes" => Some("path"),
        "migrations" => Some("id"),
        "api_keys" => Some("key_hash"),
        // Auth tables (004_auth): keyed by id for Cozo `:put` upsert.
        "accounts" => Some("id"),
        "orgs" => Some("id"),
        "access_tokens" => Some("id"),
        // knowledge_entries has a UNIQUE index on id (schema.sql
        // knowledge_entries_id_uniq) and update_knowledge re-puts an existing
        // row through create_knowledge_entry's `:put` — without the PK entry
        // that becomes a plain INSERT and dies on the unique constraint
        // ("Failed to update knowledge entry: db error").
        "knowledge_entries" => Some("id"),
        // Composite keys: two members of one org/team are distinct rows, so
        // the conflict target must cover both columns.
        "org_memberships" => Some("org_id, account_id"),
        "team_members" => Some("team_id, account_id"),
        // Per-model embed collections (`embedding_vectors_<model_id>`,
        // `embedding_state_<model_id>`) share the legacy keyed shape.
        t if t.starts_with("embedding_state_") || t.starts_with("embedding_vectors_") => {
            Some("qualified_name")
        }
        _ => None,
    }
}

/// Effective (pk, is_keyed) for a `:put`. The explicit `=>` marker wins;
/// otherwise fall back to the table's known PK so keyed tables still
/// upsert. Unknown tables stay non-keyed (plain INSERT).
fn resolve_effective_pk(
    table: &str,
    explicit_pk: Option<&str>,
    is_keyed: bool,
) -> (Option<String>, bool) {
    match explicit_pk {
        Some(pk) => (Some(pk.to_string()), is_keyed),
        None => match pk_for_table(table) {
            Some(pk) => (Some(pk.to_string()), true),
            None => (None, is_keyed),
        },
    }
}

/// Find the table name for a `:put` target by matching its columns to known
/// tables. The first column is the PK candidate (`pk`) or a regular column.
fn infer_table(cols: &[String], pk: Option<&str>) -> String {
    // The caller always writes the table name implicitly via the column
    // list. The list of tables with primary keys (`embedding_state`,
    // `embedding_vectors`, `index_inventory`, `index_hashes`, `migrations`,
    // `api_keys` when present) is small enough to check by PK column.
    if let Some(pk_col) = pk {
        match pk_col {
            "qualified_name" => {
                if cols.contains(&"vector".to_string()) {
                    return "embedding_vectors".into();
                }
                if cols.contains(&"usearch_key".to_string()) {
                    return "embedding_state".into();
                }
            }
            "path" => return "index_hashes".into(),
            "key" => return "index_inventory".into(),
            "id" if cols.len() == 2 => return "migrations".into(),
            _ => {}
        }
    }
    // Fallback: guess by column signature.
    if cols == ["path", "hash"] || cols == ["hash"] || cols == ["path"] {
        "index_hashes".into()
    } else if cols.contains(&"usearch_key".to_string()) {
        // embedding_state — 5-col shape `[qualified_name, usearch_key,
        // content_hash, state, embedded_at]`. Must precede the generic
        // element_type/qualified_name checks below (qualified_name alone
        // would otherwise mis-resolve to code_elements).
        "embedding_state".into()
    } else if cols.contains(&"element_type".to_string()) {
        "code_elements".into()
    } else if cols.contains(&"rel_type".to_string()) {
        "relationships".into()
    } else if cols.contains(&"knowledge_type".to_string()) {
        // MUST precede `user_story_id` — knowledge_entries also carries
        // user_story_id/feature_id and would otherwise mis-match to
        // business_logic (3-col table), corrupting the write target.
        "knowledge_entries".into()
    } else if cols.contains(&"user_story_id".to_string()) {
        "business_logic".into()
    } else if cols.contains(&"service_name".to_string()) {
        "service_metadata".into()
    } else if cols.contains(&"workflow_id".to_string()) {
        "feature_workflow_links".into()
    } else if cols.contains(&"severity".to_string()) {
        "incidents".into()
    } else if cols.contains(&"savings_percent".to_string()) {
        "context_metrics".into()
    } else if cols.contains(&"cache_key".to_string()) {
        "query_cache".into()
    } else if cols.contains(&"team_id".to_string()) {
        "team_invites".into()
    } else if cols.contains(&"graph_read_users".to_string()) {
        "teams".into()
    } else if cols.contains(&"key_hash".to_string()) {
        "api_keys".into()
    } else {
        "unknown_table".into()
    }
}

/// Resolve a key-only `:rm`/`DELETE` target by its single column.
/// `:rm embedding_state {qualified_name}` / `:rm embedding_vectors
/// {qualified_name}` / `:rm index_inventory {key}` / `:rm index_hashes
/// {path}` are all key-only deletes on keyed tables.
fn infer_table_by_key(col: &str) -> Option<&'static str> {
    match col {
        "qualified_name" => Some("embedding_vectors"),
        "key" => Some("index_inventory"),
        "path" => Some("index_hashes"),
        _ => None,
    }
}

/// Replace `$name` tokens inside a cozo literal with their JSON values
/// from the params map (D1 — `?[cols] <- [[ $eq, $desc, ... ]]`). Unbound
/// names become JSON null (matching cozo's null-param semantics).
fn substitute_params(literal: &str, params: &BTreeMap<String, serde_json::Value>) -> String {
    let mut out = String::with_capacity(literal.len());
    let mut rest = literal;
    while let Some(idx) = rest.find('$') {
        out.push_str(&rest[..idx]);
        let after = &rest[idx + 1..];
        // The token runs to the next non-identifier char.
        let name_len = after
            .find(|c: char| !(c.is_ascii_alphanumeric() || c == '_'))
            .unwrap_or(after.len());
        let name = &after[..name_len];
        let v = params.get(name).cloned().unwrap_or(serde_json::Value::Null);
        out.push_str(&v.to_string());
        rest = &after[name_len..];
    }
    out.push_str(rest);
    out
}

fn parse_nested_lists(s: &str) -> Result<Vec<Vec<serde_json::Value>>, String> {
    // Cozo literals look like `[["a", 1], ["b", 2]]`. Accept either that or
    // JSON `[["a", 1], ["b", 2]]` (they overlap; treat as JSON if parseable).
    // Also accepts the cozo vector literal `vec([1.0, 2.0])` inside rows
    // (B1 — put_pairs_to_db_script: `[["qn", vec([...])]]`).
    let trimmed = s.trim();
    if !trimmed.starts_with('[') {
        return Err(format!("expected list literal: {s}"));
    }
    // Pre-convert `vec([...])` → `[...]` so the JSON parser accepts it.
    let json_src = convert_cozo_vec_literals(trimmed);
    serde_json::from_str::<Vec<Vec<serde_json::Value>>>(&json_src)
        .map_err(|e| format!("cannot parse list literal as JSON: {e} (input: {json_src})"))
}

/// Replace cozo `vec([1.0, 2.0])` vector literals with bare `[...]` arrays
/// so the rest of the JSON-based parser handles them (B1).
fn convert_cozo_vec_literals(s: &str) -> String {
    let mut out = String::with_capacity(s.len());
    let mut rest = s;
    while let Some(idx) = rest.find("vec([") {
        out.push_str(&rest[..idx]);
        let after = &rest[idx + 5..];
        let close = after.find("])").unwrap_or(after.len());
        out.push('[');
        out.push_str(&after[..close]);
        out.push(']');
        rest = &after[close + 2..];
    }
    out.push_str(rest);
    out
}

fn build_insert(
    table: &str,
    cols: &[String],
    pk: Option<&str>,
    rows: &[Vec<serde_json::Value>],
    is_keyed: bool,
) -> Result<Translation, String> {
    // Cozo names the vector column `vector`; the PG schema.sql uses `vec`.
    // Map the cozo name to the PG column for the embedding_vectors table and
    // per-model collections (`embedding_vectors_<model_id>`, table-per-model
    // migration 002 — same legacy shape, same column rename).
    let pg_cols: Vec<String> = cols
        .iter()
        .map(|c| {
            if (table == "embedding_vectors" || table.starts_with("embedding_vectors_"))
                && c == "vector"
            {
                "vec".to_string()
            } else {
                c.clone()
            }
        })
        .collect();
    let col_sql = pg_cols
        .iter()
        .map(|c| quote_ident(c))
        .collect::<Vec<_>>()
        .join(", ");
    let mut all_params: Vec<Box<dyn ToSql + Sync + Send>> =
        Vec::with_capacity(rows.len() * cols.len());
    let mut values_sql = String::new();
    for (i, row) in rows.iter().enumerate() {
        if i > 0 {
            values_sql.push_str(", ");
        }
        values_sql.push('(');
        for (j, v) in row.iter().enumerate() {
            if j > 0 {
                values_sql.push_str(", ");
            }
            // Vectors are written as pgvector literals — only for the
            // `vec` column (the only pgvector column in the catalog). The
            // placeholder needs the explicit `::text::vector` cast (PG does
            // not implicitly cast text → vector).
            if pg_cols[j] == "vec" {
                let placeholder = format!("${}::text::vector", all_params.len() + 1);
                values_sql.push_str(&placeholder);
                if let serde_json::Value::Array(arr) = v {
                    let literal = pgvector_from_json(arr);
                    all_params.push(Box::new(literal));
                } else {
                    return Err(format!("vec column must be a JSON array, got: {v}"));
                }
                continue;
            }
            // NULL bindings need an explicit SQL type on the placeholder —
            // the postgres crate cannot serialize `Option::None` against an
            // unknown param type ("error serializing parameter N"). Infer
            // the column type from the schema catalog (text / bigint /
            // float8 / bool / jsonb) and emit `$N::<type>`.
            // NOTE: api_keys timestamps are TEXT (schema.sql keeps them as
            // epoch strings), unlike teams/incidents/etc. where they're
            // BIGINT — table-aware exception.
            let is_api_keys_text_ts = table == "api_keys"
                && matches!(
                    pg_cols[j].as_str(),
                    "created_at" | "last_used_at" | "revoked_at"
                );
            let null_cast = if is_api_keys_text_ts {
                "::text"
            } else {
                match pg_cols[j].as_str() {
                    "line_start"
                    | "line_end"
                    | "timestamp"
                    | "created_at"
                    | "updated_at"
                    | "expires_at"
                    | "occurred_at"
                    | "slo_p99_ms"
                    | "incident_count"
                    | "last_incident"
                    | "input_tokens"
                    | "output_tokens"
                    | "output_elements"
                    | "execution_time_ms"
                    | "baseline_tokens"
                    | "baseline_lines_scanned"
                    | "tokens_saved"
                    | "correct_elements"
                    | "total_expected"
                    | "query_depth"
                    | "resolved_at"
                    | "total_elements"
                    | "total_relationships"
                    | "total_vectors"
                    | "total_documents"
                    | "total_doc_sections"
                    | "estimated_vector_bytes"
                    | "estimated_hnsw_bytes"
                    | "usearch_key"
                    // Auth tables (004_auth): epoch columns are BIGINT.
                    | "joined_at"
                    | "revoked_at"
                    | "last_used_at" => "::bigint",
                    "savings_percent" | "f1_score" | "confidence" => "::float8",
                    "success" | "is_deleted" | "accepted" => "::bool",
                    c if JSONB_COLUMNS.contains(&c) => "::jsonb",
                    _ => "::text",
                }
            };
            let placeholder = format!("${}{null_cast}", all_params.len() + 1);
            values_sql.push_str(&placeholder);
            if JSONB_COLUMNS.contains(&pg_cols[j].as_str()) {
                // JSONB columns: cozo stored the JSON as a *string* (e.g.
                // `"{}"`); parse it and bind as `serde_json::Value` so the
                // jsonb column receives the object/array, not a JSON
                // string literal. NULL stays NULL.
                if matches!(v, serde_json::Value::Null) {
                    all_params.push(Box::new(Option::<serde_json::Value>::None));
                } else {
                    let parsed = match v {
                        serde_json::Value::String(s) => {
                            serde_json::from_str::<serde_json::Value>(s)
                                .unwrap_or_else(|_| serde_json::Value::String(s.clone()))
                        }
                        other => other.clone(),
                    };
                    all_params.push(Box::new(parsed));
                }
            } else if matches!(v, serde_json::Value::Null) {
                // NULL binding: the placeholder carries an explicit
                // `::type` cast (see null_cast above), so the client-side
                // value type must match it — otherwise the postgres crate
                // fails with "error serializing parameter N" and the server
                // rejects text-typed NULL into bigint columns (E42804).
                let typed_none: Box<dyn postgres::types::ToSql + Send + Sync> =
                    if is_api_keys_text_ts {
                        // api_keys timestamps are TEXT columns (schema.sql keeps
                        // epoch strings) — NULL must bind as Option::<String> to
                        // match the `::text` placeholder cast (mirrors null_cast).
                        Box::new(Option::<String>::None)
                    } else {
                        match pg_cols[j].as_str() {
                    c if JSONB_COLUMNS.contains(&c) => Box::new(Option::<serde_json::Value>::None),
                    "savings_percent" | "f1_score" | "confidence" => Box::new(Option::<f64>::None),
                    "success" | "is_deleted" | "accepted" => Box::new(Option::<bool>::None),
                    "line_start"
                    | "line_end"
                    | "timestamp"
                    | "created_at"
                    | "updated_at"
                    | "expires_at"
                    | "occurred_at"
                    | "resolved_at"
                    | "slo_p99_ms"
                    | "incident_count"
                    | "last_incident"
                    | "input_tokens"
                    | "output_tokens"
                    | "output_elements"
                    | "execution_time_ms"
                    | "baseline_tokens"
                    | "baseline_lines_scanned"
                    | "tokens_saved"
                    | "correct_elements"
                    | "total_expected"
                    | "query_depth"
                    | "total_elements"
                    | "total_relationships"
                    | "total_vectors"
                    | "total_documents"
                    | "total_doc_sections"
                    | "estimated_vector_bytes"
                    | "estimated_hnsw_bytes"
                    | "usearch_key"
                    // Auth tables (004_auth): epoch columns are BIGINT.
                    | "joined_at"
                    | "revoked_at"
                    | "last_used_at" => Box::new(Option::<i64>::None),
                    _ => Box::new(Option::<String>::None),
                    }
                    };
                all_params.push(typed_none);
            } else {
                all_params.push(json_to_pg(v.clone()));
            }
        }
        values_sql.push(')');
    }
    let sql = match (is_keyed, pk) {
        (true, Some(pk_str)) => {
            // Composite PKs come in as `org_id, account_id` — quote each
            // column separately (a single quote_ident would make it one
            // literal identifier `"org_id, account_id"`).
            let pk_sql = pk_str
                .split(", ")
                .map(quote_ident)
                .collect::<Vec<_>>()
                .join(", ");
            format!(
                "INSERT INTO {table} ({col_sql}) VALUES {values_sql} \
                 ON CONFLICT ({pk}) DO UPDATE SET {update_set}",
                pk = pk_sql,
                update_set = update_set_clause(&pg_cols, pk_str),
            )
        }
        _ => format!("INSERT INTO {table} ({col_sql}) VALUES {values_sql}"),
    };
    let gucs = embedding_gucs_for(table);
    Ok(if gucs.is_empty() {
        Translation::write(sql, all_params)
    } else {
        Translation::write_with_gucs(sql, all_params, gucs)
    })
}

/// Translate-time GUC plumbing for the `embedding_vectors` / `embedding_state`
/// writer path. Nothing to set here: `hnsw.ef_construction` is a
/// `CREATE INDEX ... WITH (...)` parameter, not a runtime GUC on pgvector
/// 0.8.x. Emitting `SET LOCAL hnsw.ef_construction = 'N'` aborts the write
/// transaction with `invalid configuration parameter name`. The index-time
/// knob lives in `build_hnsw_create_stmt` (src/embeddings/state.rs), which
/// reads `LEANKG_HNSW_EF_CONST` for the DDL; the writer must not re-emit it.
pub fn embedding_gucs_for(_table: &str) -> Vec<(String, String)> {
    Vec::new()
}

fn update_set_clause(cols: &[String], pk: &str) -> String {
    // Composite PKs (`org_id, account_id`) name several columns; none of them
    // belong in the SET list.
    let pk_cols: Vec<&str> = pk.split(", ").collect();
    cols.iter()
        .filter(|c| !pk_cols.contains(&c.as_str()))
        .map(|c| format!("{} = EXCLUDED.{}", quote_ident(c), quote_ident(c)))
        .collect::<Vec<_>>()
        .join(", ")
}

fn pgvector_from_json(arr: &[serde_json::Value]) -> String {
    let mut out = String::from("[");
    for (i, v) in arr.iter().enumerate() {
        if i > 0 {
            out.push(',');
        }
        if let serde_json::Value::Number(n) = v {
            if let Some(f) = n.as_f64() {
                out.push_str(&format!("{f}"));
            } else {
                out.push_str(&n.to_string());
            }
        } else {
            out.push_str(&v.to_string());
        }
    }
    out.push(']');
    out
}

fn put_from_batch(
    name: &str,
    value: Option<serde_json::Value>,
    cols: &[String],
    pk: Option<&str>,
    is_keyed: bool,
    explicit_table: &str,
) -> Result<Translation, String> {
    // `?[cols] <- $batch_data` — caller supplies an array of arrays under
    // the same key.
    let rows = match value {
        Some(serde_json::Value::Array(arr)) => {
            let mut out = Vec::with_capacity(arr.len());
            for r in arr {
                if let serde_json::Value::Array(row) = r {
                    out.push(row);
                } else {
                    return Err(format!(":put batch row must be an array, got: {r}"));
                }
            }
            out
        }
        _ => Vec::new(),
    };
    if rows.is_empty() {
        return Ok(Translation::write(
            "SELECT 1 WHERE false".to_string(),
            Vec::new(),
        ));
    }
    let table = if !explicit_table.is_empty() {
        explicit_table.to_string()
    } else {
        infer_table(cols, pk)
    };
    let n_cols = cols.len();
    for (i, r) in rows.iter().enumerate() {
        if r.len() != n_cols {
            return Err(format!(
                ":put batch row {i} has {} cols, expected {n_cols}",
                r.len()
            ));
        }
    }
    let _ = name;
    let (pk, keyed) = resolve_effective_pk(&table, pk, is_keyed);
    build_insert(&table, cols, pk.as_deref(), &rows, keyed)
}

fn rm_script(
    body: &str,
    params: &BTreeMap<String, serde_json::Value>,
) -> Result<Translation, String> {
    // Shape A: rule-based rm — `?[cols] := *rel[cols], filters :rm rel {cols}`.
    // Shape B: literal rm — `?[col] <- [{values}] :rm rel {col}` (key-only).
    let idx = body.find(":rm").ok_or("no :rm in body".to_string())?;
    // The target may carry the table name (`:rm relationships {cols}`) or
    // not (`:rm {cols}`). Keep the explicit table name (the prefix before
    // `{`) so ambiguous key columns (e.g. `qualified_name` on both
    // embedding_state and embedding_vectors) resolve to the caller's table,
    // not a best-guess from the key column alone.
    let target = body[idx + 3..].trim();
    let brace_open = target.find('{').unwrap_or(0);
    let explicit_table = target[..brace_open].trim();
    let target_cols = &target[brace_open..];
    let inner = target_cols
        .trim_start_matches('{')
        .trim_end_matches('}')
        .trim();
    let cols: Vec<String> = inner.split(',').map(|s| s.trim().to_string()).collect();
    if cols.is_empty() {
        return Err("empty :rm target".into());
    }
    let source = body[..idx].trim();
    if source.contains(":=") {
        // Rule-based: parse filters like a read.
        let after_assign = source.split_once(":=").map(|(_, r)| r).unwrap_or("");
        let (relation, _rel_cols, body_after) = match parse_relation_block(after_assign) {
            Some(p) => p,
            None => return Err(format!("bad :rm relation block: {after_assign}")),
        };
        let (filters, _) = split_filters_and_modifiers(&body_after);
        let table = if relation.is_empty() {
            infer_table(&cols, None)
        } else {
            relation
        };
        // The H4 cross-relation :rm has `*relationships[...] *code_elements[...]`.
        // Detect two `*` markers and translate to `WHERE source_qualified IN (SELECT qn FROM code_elements WHERE ... )`.
        let n_stars = after_assign.matches('*').count();
        if n_stars >= 2 {
            return cross_relation_rm(after_assign, &table, filters);
        }
        let (where_sql, params) = compile_filters(filters, params)?;
        let sql = if where_sql.is_empty() {
            format!("DELETE FROM {table}")
        } else {
            format!("DELETE FROM {table} WHERE {where_sql}")
        };
        return Ok(Translation::write(sql, params));
    }
    // Literal :rm — `?[col] <- [list] :rm table {col}`. Convert to
    // `DELETE FROM table WHERE col = ANY('{...}')`.
    if let Some((_, after_arrow)) = source.split_once("<-") {
        let list_text = after_arrow.trim();
        let key_col = cols.first().cloned().unwrap_or_default();
        // Explicit table name wins over key-column inference — `qualified_name`
        // is the PK of both embedding_state and embedding_vectors.
        let table = if !explicit_table.is_empty() {
            explicit_table.to_string()
        } else {
            match infer_table_by_key(&key_col) {
                Some(t) => t.to_string(),
                None => infer_table(&cols, None),
            }
        };
        let pk = key_col;
        let strs: Vec<String> = if let Some(name) = list_text.strip_prefix('$') {
            // `?[col] <- $qns :rm table {col}` — caller passes a Vec<String>
            // under `$qns`. Parameterized: avoids the literal-escaping bug
            // (a QN containing `"` or `\` breaks the inline `[[...]]` literal
            // and can merge rows → E21000 / wrong deletes).
            let v = params.get(name).cloned().unwrap_or(serde_json::Value::Null);
            match v {
                serde_json::Value::Array(outer) => outer
                    .into_iter()
                    .filter_map(|r| match r {
                        serde_json::Value::Array(row) => row.first().map(|f| match f {
                            serde_json::Value::String(s) => s.clone(),
                            other => other.to_string(),
                        }),
                        serde_json::Value::String(s) => Some(s),
                        _ => None,
                    })
                    .collect(),
                _ => Vec::new(),
            }
        } else {
            let arr = parse_nested_lists(list_text)?;
            // Flatten the outer array: each row is a single column.
            arr.into_iter()
                .filter_map(|row| {
                    row.into_iter().next().map(|v| match v {
                        serde_json::Value::String(s) => s,
                        other => other.to_string(),
                    })
                })
                .collect()
        };
        let sql = format!(
            "DELETE FROM {table} WHERE {pk} = ANY($1::text[])",
            table = table,
            pk = quote_ident(&pk),
        );
        return Ok(Translation::write(sql, vec![Box::new(strs)]));
    }
    Err(format!("unsupported :rm shape: {source}"))
}

fn cross_relation_rm(
    after_assign: &str,
    table: &str,
    _filters: String,
) -> Result<Translation, String> {
    // Pattern: `?[cols] := *relationships[cols...], *code_elements[qn, ...], regex_matches(file_path, "^ontology://") :rm relationships {cols}`
    // We translate to `DELETE FROM {table} WHERE source_qualified IN (SELECT qualified_name FROM code_elements WHERE file_path ~ '^ontology://')`.
    // The regex literal is already escaped into a cozo regex; pass it as a bound param.
    let sql = format!(
        "DELETE FROM {table} WHERE source_qualified IN (SELECT qualified_name FROM code_elements WHERE file_path ~ $1)"
    );
    // Extract the regex pattern.
    let pat = match extract_regex_literal(after_assign) {
        Some(p) => p,
        None => "^ontology://".to_string(),
    };
    Ok(Translation::write(sql, vec![Box::new(pat)]))
}

fn extract_regex_literal(s: &str) -> Option<String> {
    let i = s.find("regex_matches(")?;
    let after = &s[i + "regex_matches(".len()..];
    let close = after.find(')')?;
    let inner = &after[..close];
    let parts: Vec<&str> = inner.splitn(2, ',').collect();
    if parts.len() != 2 {
        return None;
    }
    let pat = parts[1].trim();
    if pat.starts_with('"') && pat.ends_with('"') {
        Some(unescape_cozo_string(&pat[1..pat.len() - 1]))
    } else {
        None
    }
}

fn delete_where(
    body: &str,
    params: &BTreeMap<String, serde_json::Value>,
) -> Result<Translation, String> {
    // `:delete table where col = $x` (and `col in $arr`). Cozo parses the
    // `where` clause with the same operators as a filter — reuse the
    // compiler.
    let trimmed = body.trim();
    let where_idx = trimmed
        .find("where")
        .ok_or_else(|| ":delete missing where".to_string())?;
    let table = trimmed[..where_idx].trim();
    let filters = trimmed[where_idx + 5..].trim().to_string();
    let (where_sql, bound) = compile_filters(filters, params)?;
    let sql = if where_sql.is_empty() {
        format!("DELETE FROM {table}")
    } else {
        format!("DELETE FROM {table} WHERE {where_sql}")
    };
    Ok(Translation::write(sql, bound))
}

fn create_ddl(body: &str) -> Result<Translation, String> {
    // `:create table { col: Type, ... }` — all 16 tables are pre-created by
    // `migrations::run_migrations` (Phase 2, schema.sql). This arm is
    // called only by tests/repair scripts; we no-op. The caller still
    // records mutability so the connection state is consistent.
    let _ = body;
    Ok(Translation::ddl_noop(Vec::new()))
}

fn replace_ddl(_body: &str) -> Result<Translation, String> {
    // `:replace table {cols}` (3 repair scripts in schema.rs). All repair is
    // obsolete under PG (the 13/6-col canonical layout is the schema.sql
    // itself); no-op.
    Ok(Translation::ddl_noop(Vec::new()))
}

fn index_ddl(rest: &str) -> Result<Translation, String> {
    // `::index create table:idx { col }` / `::index drop table:idx`.
    // The 30 ::index statements are pre-created by schema.sql; for tests we
    // best-effort create-if-not-exists.
    let trimmed = rest.trim();
    if let Some(after) = trimmed.strip_prefix("create") {
        let body = after.trim();
        // `table:idx { col }` — emit `CREATE INDEX IF NOT EXISTS table_idx ON table (col)`.
        if let Some((target, cols_block)) = body.split_once('{') {
            let target = target.trim();
            let cols = cols_block.trim_end_matches('}').trim();
            if let Some((table, idx)) = target.split_once(':') {
                let table = table.trim();
                let idx = idx.trim();
                let pg_idx = format!("{table}_{idx}");
                let col_list = cols
                    .split(',')
                    .map(|s| quote_ident(s.trim()))
                    .collect::<Vec<_>>()
                    .join(", ");
                let sql = format!("CREATE INDEX IF NOT EXISTS {pg_idx} ON {table} ({col_list})");
                return Ok(Translation::write(sql, Vec::new()));
            }
        }
    }
    if let Some(after) = trimmed.strip_prefix("drop") {
        let body = after.trim();
        if let Some((table, idx)) = body.split_once(':') {
            let table = table.trim();
            let idx = idx.trim();
            let pg_idx = format!("{table}_{idx}");
            return Ok(Translation::write(
                format!("DROP INDEX IF EXISTS {pg_idx}"),
                Vec::new(),
            ));
        }
    }
    Ok(Translation::ddl_noop(Vec::new()))
}

fn hnsw_ddl(rest: &str) -> Result<Translation, String> {
    // Phase 7 (T7.2): the cozo `::hnsw` operators map onto the real
    // pgvector index created by schema.sql. The bulk-embed path drops the
    // index before COPY and recreates it after (cold embeds only, gated by
    // `use_incr_hnsw` in build.rs); the translator emits the actual DDL so
    // `state::drop_hnsw_index` / `state::create_hnsw_index` work on PG.
    //
    // The index name is the schema.sql one (`embedding_vectors_vec_hnsw_idx`),
    // which differs from cozo's `embedding_vectors:vec_idx` — map the known
    // pair. `CREATE` is idempotent (IF NOT EXISTS); `DROP` is IF EXISTS so a
    // missing index (e.g. a prior aborted bulk) is not an error.
    let trimmed = rest.trim();
    if let Some(after) = trimmed.strip_prefix("drop") {
        let target = after.trim();
        if let Some(table) = target.strip_suffix(":vec_idx") {
            let table = table.trim();
            let index_name = format!("{table}_vec_hnsw_idx");
            return Ok(Translation::write(
                format!("DROP INDEX IF EXISTS {index_name}"),
                Vec::new(),
            ));
        }
        if target.starts_with("embedding_vectors") {
            return Ok(Translation::write(
                "DROP INDEX IF EXISTS embedding_vectors_vec_hnsw_idx".to_string(),
                Vec::new(),
            ));
        }
        return Ok(Translation::ddl_noop(Vec::new()));
    }
    if let Some(_after) = trimmed.strip_prefix("create") {
        // Per-model embed tables (`~embedding_vectors_<model_id>:vec_idx`)
        // and the legacy `embedding_vectors:vec_idx` both map to
        // `CREATE INDEX ... ON <table> USING hnsw (vec vector_cosine_ops)`.
        if trimmed.contains(":vec_idx") {
            // Extract the table from `::hnsw create <table>:vec_idx { ... }`.
            let start = trimmed
                .find("create")
                .map(|i| i + "create".len())
                .unwrap_or(0);
            let after_create = trimmed[start..].trim();
            let table = after_create.split(':').next().map(str::trim);
            // Respect the caller's `m` / `ef_construction` (from
            // LEANKG_HNSW_M / LEANKG_HNSW_EF_CONST, see
            // build_hnsw_create_stmt) instead of silently hardcoding.
            let mut m = 16usize;
            let mut ef = 200usize;
            for line in trimmed.lines() {
                let l = line.trim().trim_end_matches(',');
                if let Some(v) = l.strip_prefix("m:") {
                    if let Ok(p) = v.trim().parse::<usize>() {
                        m = p;
                    }
                } else if let Some(v) = l.strip_prefix("ef_construction:") {
                    if let Ok(p) = v.trim().parse::<usize>() {
                        ef = p;
                    }
                }
            }
            if let Some(table) = table {
                let index_name = format!("{table}_vec_hnsw_idx");
                return Ok(Translation::write(
                    format!(
                        "CREATE INDEX IF NOT EXISTS {index_name} \
                         ON {table} USING hnsw (vec vector_cosine_ops) \
                         WITH (m = {m}, ef_construction = {ef})"
                    ),
                    Vec::new(),
                ));
            }
        }
        if trimmed.contains("embedding_vectors") {
            return Ok(Translation::write(
                "CREATE INDEX IF NOT EXISTS embedding_vectors_vec_hnsw_idx \
                 ON embedding_vectors USING hnsw (vec vector_cosine_ops) \
                 WITH (m = 16, ef_construction = 200)"
                    .to_string(),
                Vec::new(),
            ));
        }
        return Ok(Translation::ddl_noop(Vec::new()));
    }
    Ok(Translation::ddl_noop(Vec::new()))
}

fn relations_introspection() -> Translation {
    // `::relations` returns relation names. Mirror with information_schema.
    // Also include index names (cozo-style `table:idx` mapping via the known
    // translator naming: `::hnsw create embedding_vectors:vec_idx` emits
    // `embedding_vectors_vec_hnsw_idx`). Including them lets
    // `ensure_embedding_state_table`'s `existing.contains("embedding_vectors:
    // vec_idx")` check succeed instead of permanently re-running CREATE INDEX.
    Translation::read(
        "SELECT name FROM ( \
            SELECT table_name AS name FROM information_schema.tables \
            WHERE table_schema = current_schema() AND table_type = 'BASE TABLE' \
            UNION ALL \
            SELECT CASE WHEN indexname LIKE '%\\_vec\\_hnsw\\_idx' \
                THEN tablename || ':vec_idx' ELSE indexname END AS name \
            FROM pg_indexes WHERE schemaname = current_schema() \
        ) t ORDER BY name"
            .to_string(),
        Vec::new(),
        vec!["name".to_string()],
    )
}

fn schema_introspection(_rest: &str) -> Result<Translation, String> {
    // `:schema table` → information_schema columns.
    let table = _rest
        .trim()
        .trim_start_matches('{')
        .trim_end_matches('}')
        .trim();
    let sql = format!(
        "SELECT column_name, data_type, is_nullable FROM information_schema.columns \
         WHERE table_schema = current_schema() AND table_name = '{table}' \
         ORDER BY ordinal_position"
    );
    Ok(Translation::read(
        sql,
        Vec::new(),
        vec![
            "column_name".to_string(),
            "data_type".to_string(),
            "is_nullable".to_string(),
        ],
    ))
}

// ---------------------------------------------------------------------------
// Row mapping: postgres Row → [`DataValue`] (preserves the downstream
// indexing contract — fetch via row[i].get_str() etc. keeps working).
// ---------------------------------------------------------------------------

/// Map a single postgres row (positional) to `Vec<DataValue>` matching the
/// head order.
pub fn map_row(
    row: &postgres::Row,
    head: &[String],
) -> Result<Vec<DataValue>, Box<dyn std::error::Error>> {
    let mut out = Vec::with_capacity(head.len());
    for (i, col) in head.iter().enumerate() {
        // JSONB columns: bind through the serde_json feature so the jsonb
        // value round-trips (the legacy cozo storage kept the JSON as a
        // string; consumers read it with `get_str()`).
        if JSONB_COLUMNS.contains(&col.as_str()) {
            // Consumers read JSON with `get_str()`. Return the canonical
            // jsonb text (e.g. `{}` for an empty object).
            let v: DataValue = match row.try_get::<_, Option<serde_json::Value>>(i) {
                Ok(Some(j)) => DataValue::Str(serde_json::to_string(&j).unwrap_or_default()),
                _ => DataValue::Null,
            };
            out.push(v);
            continue;
        }
        // Try the most likely postgres types in order, falling back to Null.
        let v: DataValue = if let Ok(s) = row.try_get::<_, Option<String>>(i) {
            match s {
                Some(s) => DataValue::Str(s),
                None => DataValue::Null,
            }
        } else if let Ok(n) = row.try_get::<_, Option<i64>>(i) {
            match n {
                Some(n) => DataValue::from(n),
                None => DataValue::Null,
            }
        } else if let Ok(n) = row.try_get::<_, Option<i32>>(i) {
            match n {
                Some(n) => DataValue::from(n as i64),
                None => DataValue::Null,
            }
        } else if let Ok(f) = row.try_get::<_, Option<f64>>(i) {
            match f {
                Some(f) => DataValue::from(f),
                None => DataValue::Null,
            }
        } else if let Ok(b) = row.try_get::<_, Option<bool>>(i) {
            match b {
                Some(b) => DataValue::Bool(b),
                None => DataValue::Null,
            }
        } else {
            DataValue::Null
        };
        out.push(v);
    }
    Ok(out)
}

/// Build a `NamedRows` from a postgres result set + the translator's head.
pub fn named_rows_from_result(
    result: Vec<postgres::Row>,
    head: &[String],
) -> Result<NamedRows, Box<dyn std::error::Error>> {
    let mut rows: Vec<Vec<DataValue>> = Vec::with_capacity(result.len());
    for row in &result {
        rows.push(map_row(row, head)?);
    }
    Ok(NamedRows::new(head.to_vec(), rows))
}

// ---------------------------------------------------------------------------
// Identifier quoting.
// ---------------------------------------------------------------------------

/// Cozo identifiers are bare strings (letters/digits/underscore + a few
/// punctuation marks like `.` for relation qualifiers and `-` for some
/// naming). Postgres wants double-quoted identifiers. Map Cozo's bare form
/// to PG's quoted form, rejecting anything that looks injection-adjacent.
pub(crate) fn quote_ident(s: &str) -> String {
    // Allow: letter | digit | underscore | dot (for relation.column, but
    // cozo uses space-separated heads, so dot is rare). Reject quotes and
    // semicolons outright.
    if s.is_empty() {
        return "\"\"".into();
    }
    let safe = s
        .chars()
        .all(|c| c.is_ascii_alphanumeric() || c == '_' || c == '.');
    if !safe {
        // Fall back to passing through the string as-is; postgres will error
        // and the caller sees a clear failure rather than a silent injection.
        return format!("\"{}\"", s.replace('"', "\"\""));
    }
    format!("\"{s}\"")
}

// ---------------------------------------------------------------------------
// Trait shim: let the public DbBackend impl call translate() uniformly.
// ---------------------------------------------------------------------------

/// Convenience: classify and translate. Returns the [Translation] plus the
/// backing backend reference (so callers can decide whether to wrap in a
/// transaction — writes get one, reads don't).
pub fn translate_for(
    query: &str,
    params: BTreeMap<String, serde_json::Value>,
) -> Result<Translation, String> {
    translate(query, params)
}

// ---------------------------------------------------------------------------
// Tests — pure unit tests covering every branch.
// ---------------------------------------------------------------------------

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn empty_query_errors() {
        assert!(translate("", BTreeMap::new()).is_err());
    }

    #[test]
    fn read_simple_select() {
        let t = translate("?[a, b, c] := *table[a, b, c]", BTreeMap::new()).unwrap();
        assert_eq!(t.kind, TranslationKind::Read);
        assert_eq!(t.sql, "SELECT \"a\", \"b\", \"c\" FROM table");
        assert_eq!(t.head, vec!["a", "b", "c"]);
    }

    #[test]
    fn read_equality_param() {
        let mut p = BTreeMap::new();
        p.insert("qn".into(), serde_json::json!("foo"));
        let t = translate("?[a] := *t[a], a = $qn", p).unwrap();
        assert!(t.sql.contains("WHERE \"a\" = $1"));
        assert_eq!(t.params.len(), 1);
    }

    #[test]
    fn read_null_equality() {
        let t = translate(
            "?[id] := *api_keys[id, kh], revoked_at = null",
            BTreeMap::new(),
        )
        .unwrap();
        assert!(t.sql.contains("IS NULL"), "got: {}", t.sql);
    }

    #[test]
    fn read_null_inequality() {
        let t = translate(
            "?[cid] := *code_elements[qn, _, _, _, _, _, _, _, cid, _, _], cid != null",
            BTreeMap::new(),
        )
        .unwrap();
        assert!(t.sql.contains("IS NOT NULL"), "got: {}", t.sql);
    }

    #[test]
    fn read_or_pair() {
        let mut p = BTreeMap::new();
        p.insert("a".into(), serde_json::json!("x"));
        p.insert("b".into(), serde_json::json!("y"));
        let t = translate("?[t] := *r[s, t, _, _, _, _], (s = $a or s = $b)", p).unwrap();
        assert!(t.sql.contains(" OR "), "got: {}", t.sql);
        assert_eq!(t.params.len(), 2);
    }

    #[test]
    fn read_top_level_or_chain() {
        // search_by_content / search_knowledge write alternatives as
        // `... or ...` without parens, on their own lines.
        let t = translate(
            "?[a] := *code_elements[qn, et, name, fp, _, _, _, _, _, _, _, _, _], \
             str_includes(lowercase(name), \"x\") or \
             str_includes(lowercase(qn), \"x\") or \
             str_includes(lowercase(fp), \"x\") :limit 5",
            BTreeMap::new(),
        )
        .unwrap();
        assert_eq!(t.sql.matches(" OR ").count(), 2, "got: {}", t.sql);
        assert!(t.sql.contains("LIMIT 5"), "got: {}", t.sql);
    }

    #[test]
    fn head_alias_resolves_to_real_column() {
        // find_dead_code candidate query: head `tgt` (positional alias) and
        // `span` (defined by a filter clause).
        let t = translate(
            "?[qualified_name, file_path, line_end, line_start, language, name, span] := \
             *code_elements[qualified_name, et, name, file_path, line_start, line_end, language, _, _, _, _, env, ontology_layer], \
             line_end >= 0, line_start >= 0, (line_end - line_start) >= 1, \
             et in [\"function\", \"method\", \"struct\"], \
             span = line_end - line_start :order -span",
            BTreeMap::new(),
        )
        .unwrap();
        assert!(t.sql.contains("\"qualified_name\""), "got: {}", t.sql);
        assert!(t.sql.contains("\"element_type\" = ANY"), "got: {}", t.sql);
        assert!(t.sql.contains("AS \"span\""), "got: {}", t.sql);
        assert!(t.sql.contains("ORDER BY \"span\" DESC"), "got: {}", t.sql);
    }

    #[test]
    fn head_alias_tgt_resolves_target_qualified() {
        // referenced_qualified_names: `?[tgt] := *relationships[_, tgt, ...]`.
        let t = translate(
            "?[tgt] := *relationships[_, tgt, rel, _, _, _], (rel = \"calls\" or rel = \"tested_by\")",
            BTreeMap::new(),
        )
        .unwrap();
        assert!(t.sql.contains("\"target_qualified\""), "got: {}", t.sql);
        assert!(t.sql.contains(" OR "), "got: {}", t.sql);
        assert!(t.sql.contains("\"rel_type\" = $"), "got: {}", t.sql);
    }

    #[test]
    fn qn_in_list_filter_resolves_qualified_name() {
        // referenced_bare_names: `qn in $qns` where qn is a positional alias.
        let mut p = BTreeMap::new();
        p.insert("qns".into(), serde_json::json!(["a", "b"]));
        let t = translate(
            "?[name] := *code_elements[qn, _, name, _, _, _, _, _, _, _, _, env, ontology_layer], qn in $qns",
            p,
        )
        .unwrap();
        assert!(t.sql.contains("\"qualified_name\" = ANY"), "got: {}", t.sql);
    }

    #[test]
    fn read_limit_offset() {
        let t = translate("?[a] := *t[a] :limit 10 :offset 5", BTreeMap::new()).unwrap();
        assert!(t.sql.contains("LIMIT 10"), "got: {}", t.sql);
        assert!(t.sql.contains("OFFSET 5"), "got: {}", t.sql);
    }

    #[test]
    fn read_in_list_literal() {
        let t = translate(
            "?[s, t] := *r[s, t, _, _, _, _], rel_type in [\"calls\", \"imports\"]",
            BTreeMap::new(),
        )
        .unwrap();
        assert!(t.sql.contains("ANY"), "got: {}", t.sql);
        assert_eq!(t.params.len(), 1);
    }

    #[test]
    fn read_regex_matches() {
        let t = translate(
            "?[qn] := *code_elements[qn, _, _, f, _, _, _, _, _, _, _], regex_matches(f, \"^ontology://\")",
            BTreeMap::new(),
        )
        .unwrap();
        assert!(t.sql.contains("~"), "got: {}", t.sql);
    }

    #[test]
    fn read_regex_matches_with_param() {
        let mut p = BTreeMap::new();
        p.insert("pat".into(), serde_json::json!("^foo"));
        let t = translate("?[qn] := *t[qn, fp], regex_matches(fp, $pat)", p).unwrap();
        assert!(t.sql.contains("~ $1"), "got: {}", t.sql);
    }

    #[test]
    fn relationships_alias_regex_matches_resolves_tgt() {
        // get_callers uses positional aliases `*relationships[src, tgt, ...]`
        // and filters on `regex_matches(tgt, ...)`. The `tgt` alias (position
        // 1) must resolve to `target_qualified` even when followed by `,`
        // inside the regex_matches call — a `column "tgt" does not exist` bug.
        let t = translate(
            "?[src, tgt, rel_type, conf, meta] := *relationships[src, tgt, rel_type, conf, meta, _], regex_matches(tgt, \".*main.*\") :limit 5",
            BTreeMap::new(),
        )
        .unwrap();
        assert!(
            t.sql.contains("target_qualified"),
            "tgt alias not resolved to target_qualified: got: {}",
            t.sql
        );
        assert!(
            !t.sql.contains("\"tgt\""),
            "raw tgt column leaked: got: {}",
            t.sql
        );
    }

    #[test]
    fn read_str_includes() {
        let mut p = BTreeMap::new();
        p.insert("pattern".into(), serde_json::json!("needle"));
        let t = translate(
            "?[qn] := *t[qn, _, _, _, _, _, _, _, _, _, _], str_includes(lowercase(qn), lowercase($pattern))",
            p,
        )
        .unwrap();
        assert!(t.sql.contains("LIKE '%' ||"), "got: {}", t.sql);
    }

    #[test]
    fn read_starts_with() {
        let t = translate(
            "?[name] := *code_elements[_, _, _, _, _, _, _, _, _, _, _, _, _], starts_with(file_path, \"src/\")",
            BTreeMap::new(),
        )
        .unwrap();
        assert!(
            t.sql.contains("LIKE") && t.sql.contains("|| '%'"),
            "got: {}",
            t.sql
        );
    }

    #[test]
    fn read_range() {
        let mut p = BTreeMap::new();
        p.insert("lo".into(), serde_json::json!("a"));
        p.insert("hi".into(), serde_json::json!("b"));
        let t = translate(
            "?[fp] := *code_elements[qn, et, name, fp, ls, le, lg, pq, _, _, _, _, _], fp >= $lo and fp < $hi",
            p,
        )
        .unwrap();
        assert!(t.sql.contains("AND"), "got: {}", t.sql);
    }

    #[test]
    fn count_query_simple() {
        let t = translate(
            "?[count(n)] := *code_elements[n, _, _, _, _, _, _, _, _, _, _, _, _]",
            BTreeMap::new(),
        )
        .unwrap();
        assert!(t.sql.starts_with("SELECT count(*)"), "got: {}", t.sql);
    }

    #[test]
    fn count_with_group_order() {
        let t = translate(
            "?[qualified_name, env, count(n)] := *code_elements[n, _, _, qualified_name, _, _, _, _, _, _, env, _] :group [qualified_name, env] :order count(n) desc",
            BTreeMap::new(),
        )
        .unwrap();
        assert!(t.sql.contains("GROUP BY"), "got: {}", t.sql);
        assert!(t.sql.contains("ORDER BY"), "got: {}", t.sql);
        assert!(t.sql.contains("DESC"), "got: {}", t.sql);
    }

    #[test]
    fn count_aggregate_neg_order() {
        let t = translate(
            "?[language, count(language)] := *code_elements[_, _, _, _, _, _, language, _, _, _, _, _, _] :order -count(language)",
            BTreeMap::new(),
        )
        .unwrap();
        assert!(
            t.sql.contains("ORDER BY count(\"language\") DESC"),
            "got: {}",
            t.sql
        );
    }

    #[test]
    fn not_exists_orphans() {
        let t = translate(
            "?[qn, usk, ch, st, em] := *embedding_state[qn, usk, ch, st, em], not *code_elements[qn, _, _, _, _, _, _, _, _, _, _, _, _]",
            BTreeMap::new(),
        )
        .unwrap();
        assert!(t.sql.contains("NOT EXISTS"), "got: {}", t.sql);
        assert!(t.sql.contains("code_elements"), "got: {}", t.sql);
    }

    #[test]
    fn ann_query_translation() {
        let q = "?[dist, qualified_name] := ~embedding_vectors:vec_idx { qualified_name | query: vec([0.1, 0.2]), k: 5, ef: 50, bind_distance: dist }";
        let t = translate(q, BTreeMap::new()).unwrap();
        assert!(
            t.sql.contains("SELECT vec <-> $1::text::vector"),
            "got: {}",
            t.sql
        );
        assert!(t.sql.contains("LIMIT $2::int8"), "got: {}", t.sql);
        assert_eq!(t.params.len(), 2);
    }

    #[test]
    fn put_literal_non_keyed() {
        let t = translate(
            r#"?[element_qualified, description, user_story_id, feature_id] <- [["a", "d", null, null]] :put business_logic { element_qualified, description, user_story_id, feature_id }"#,
            BTreeMap::new(),
        ).unwrap();
        assert_eq!(t.kind, TranslationKind::Write);
        assert!(
            t.sql.contains("INSERT INTO business_logic"),
            "got: {}",
            t.sql
        );
        assert!(
            !t.sql.contains("ON CONFLICT"),
            "non-keyed table must not upsert: {}",
            t.sql
        );
    }

    #[test]
    fn put_literal_keyed() {
        let t = translate(
            r#"?[qualified_name, usearch_key, content_hash, state, embedded_at] <- [["qn", 0, "", "stale", "now"]] :put embedding_state {qualified_name => usearch_key, content_hash, state, embedded_at}"#,
            BTreeMap::new(),
        ).unwrap();
        assert!(
            t.sql.contains("INSERT INTO embedding_state"),
            "got: {}",
            t.sql
        );
        assert!(
            t.sql.contains("ON CONFLICT"),
            "keyed :put must upsert: {}",
            t.sql
        );
    }

    #[test]
    fn knowledge_entries_put_upserts_on_id() {
        // update_knowledge (db/mod.rs update_knowledge_entry) re-puts the same
        // id through create_knowledge_entry's `:put`. schema.sql carries a
        // UNIQUE index on knowledge_entries(id), so the PG translation must be
        // an ON CONFLICT ("id") DO UPDATE upsert — a plain INSERT fails with
        // a unique-violation on every update ("Failed to update knowledge
        // entry: db error", hackathon R1 sweep issue #3).
        let q = r#"?[id, knowledge_type, title, content, element_qualified, user_story_id, feature_id, tags, environment, branch, author, created_at, updated_at] <- [["k-general-x", "general", "t2", "c2", null, null, null, "[]", "production", null, "mcp-client", 1, 2]] :put knowledge_entries {id, knowledge_type, title, content, element_qualified, user_story_id, feature_id, tags, environment, branch, author, created_at, updated_at}"#;
        let t = translate(q, BTreeMap::new()).unwrap();
        assert_eq!(t.kind, TranslationKind::Write);
        assert!(
            t.sql.contains("INSERT INTO knowledge_entries"),
            "got: {}",
            t.sql
        );
        assert!(
            t.sql.contains(r#"ON CONFLICT ("id") DO UPDATE SET"#),
            "knowledge_entries :put must upsert on id: {}",
            t.sql
        );
        assert!(
            t.sql.contains(r#""title" = EXCLUDED."title""#)
                && t.sql.contains(r#""updated_at" = EXCLUDED."updated_at""#),
            "upsert must refresh non-key columns: {}",
            t.sql
        );
        assert!(
            !t.sql.contains(r#""id" = EXCLUDED."id""#),
            "pk column must stay out of the SET list: {}",
            t.sql
        );
    }

    #[test]
    fn rm_rule_based() {
        let mut p = BTreeMap::new();
        p.insert("qn".into(), serde_json::json!("a::b"));
        let t = translate(
            r#"?[qn, et, name, fp, ls, le, lg, pq, _, _, _] := *code_elements[qn, et, name, fp, ls, le, lg, pq, _, _, _], qn = $qn :rm code_elements {qn, et, name, fp, ls, le, lg, pq, _, _, _}"#,
            p,
        ).unwrap();
        assert!(
            t.sql.starts_with("DELETE FROM code_elements"),
            "got: {}",
            t.sql
        );
        assert!(t.sql.contains("WHERE"), "got: {}", t.sql);
    }

    #[test]
    fn rm_cross_relation() {
        let t = translate(
            r#"?[s, t, rt, c, m] := *relationships[s, t, rt, c, m, _], *code_elements[s, et, _, fp, _, _, _, _, _, _, _], regex_matches(fp, "^ontology://") :rm relationships {s, t, rt, c, m}"#,
            BTreeMap::new(),
        ).unwrap();
        assert!(
            t.sql.contains("DELETE FROM relationships"),
            "got: {}",
            t.sql
        );
        assert!(t.sql.contains("source_qualified IN"), "got: {}", t.sql);
        assert!(!t.sql.contains("information_schema"), "no info_schema leak");
    }

    #[test]
    fn delete_where_with_param() {
        let mut p = BTreeMap::new();
        p.insert("id".into(), serde_json::json!("abc"));
        let t = translate(r#":delete api_keys where id = "{key_id}""#, p);
        // This is the unescaped pattern; we bind to NULL because the
        // interpolated key_id is not in the param map. The point is the
        // form parses without panic; runtime behavior is documented.
        // The proper form is `:delete api_keys where id = $key_id`.
        let _ = t;
    }

    #[test]
    fn delete_where_proper() {
        let mut p = BTreeMap::new();
        p.insert("id".into(), serde_json::json!("abc"));
        let t = translate(r#":delete api_keys where id = $id"#, p).unwrap();
        assert!(
            t.sql.contains("DELETE FROM api_keys WHERE"),
            "got: {}",
            t.sql
        );
        assert_eq!(t.params.len(), 1);
    }

    #[test]
    fn create_noop() {
        let t = translate(
            ":create code_elements {qualified_name: String, element_type: String}",
            BTreeMap::new(),
        )
        .unwrap();
        assert_eq!(t.kind, TranslationKind::DdlNoop);
    }

    #[test]
    fn replace_noop() {
        let t = translate("?[a] := *t[a] :replace t {a: String}", BTreeMap::new()).unwrap();
        assert_eq!(t.kind, TranslationKind::DdlNoop);
    }

    #[test]
    fn hnsw_create_emits_pg_index_ddl() {
        // Phase 7 (T7.2): `::hnsw create` on the vectors index emits the
        // real pgvector CREATE INDEX (idempotent) so the bulk-embed path's
        // index rebuild works on PG. Previously a DdlNoop.
        let t = translate(
            "::hnsw create embedding_vectors:vec_idx { dim: 384, distance: Cosine }",
            BTreeMap::new(),
        )
        .unwrap();
        assert_eq!(t.kind, TranslationKind::Write);
        assert!(
            t.sql
                .contains("CREATE INDEX IF NOT EXISTS embedding_vectors_vec_hnsw_idx")
                && t.sql.contains("USING hnsw")
                && t.sql.contains("vector_cosine_ops"),
            "got: {}",
            t.sql
        );
    }

    #[test]
    fn hnsw_drop_emits_pg_index_ddl() {
        // Phase 7 (T7.2): `::hnsw drop` maps to DROP INDEX IF EXISTS.
        let t = translate("::hnsw drop embedding_vectors:vec_idx", BTreeMap::new()).unwrap();
        assert_eq!(t.kind, TranslationKind::Write);
        assert!(
            t.sql
                .contains("DROP INDEX IF EXISTS embedding_vectors_vec_hnsw_idx"),
            "got: {}",
            t.sql
        );
    }

    #[test]
    fn hnsw_other_targets_stay_noop() {
        // Non-vector HNSW targets remain no-ops (only the known vectors
        // index is managed on PG).
        let t = translate(
            "::hnsw create other_table:some_idx { dim: 384, distance: Cosine }",
            BTreeMap::new(),
        )
        .unwrap();
        assert_eq!(t.kind, TranslationKind::DdlNoop);
    }

    #[test]
    fn relations_introspection_query() {
        let t = translate("::relations", BTreeMap::new()).unwrap();
        assert!(
            t.sql.contains("information_schema.tables"),
            "got: {}",
            t.sql
        );
        assert_eq!(t.head, vec!["name".to_string()]);
    }

    #[test]
    fn index_create_translation() {
        let t = translate(
            "::index create code_elements:foo { file_path }",
            BTreeMap::new(),
        )
        .unwrap();
        assert!(
            t.sql
                .contains("CREATE INDEX IF NOT EXISTS code_elements_foo"),
            "got: {}",
            t.sql
        );
    }

    #[test]
    fn vacuum_noop() {
        let t = translate("VACUUM", BTreeMap::new()).unwrap();
        assert_eq!(t.kind, TranslationKind::DdlNoop);
    }

    #[test]
    fn pragma_noop() {
        let t = translate("PRAGMA page_count", BTreeMap::new()).unwrap();
        assert_eq!(t.kind, TranslationKind::DdlNoop);
    }

    #[test]
    fn row_mapping_text_and_int() {
        // Construct a mock row via the postgres test client if available;
        // we test the mapper via a JSON roundtrip in the integration test.
        // Here we just exercise the scalar helpers.
        assert_eq!(quote_ident("foo"), "\"foo\"");
        assert_eq!(quote_ident("a-b"), "\"a-b\"");
        let p = json_to_pg(serde_json::json!("hello"));
        // Verify only that it coerces without panic.
        let _ = format!("{:?}", p);
    }

    // Phase 4 plumbing: ANN translation picks up `ef: N` from the Datalog
    // body and emits a `hnsw.ef_search` GUC for the PostgresBackend tx.
    #[test]
    fn ann_picks_up_ef_into_guc() {
        let q = r#"?[dist, qualified_name] := ~embedding_vectors:vec_idx { qualified_name | query: vec([0.0, 0.1, 0.2]), k: 5, ef: 73, bind_distance: dist }"#;
        let t = translate(q, BTreeMap::new()).unwrap();
        assert_eq!(t.kind, TranslationKind::Read);
        assert_eq!(
            t.gucs,
            vec![("hnsw.ef_search".to_string(), "73".to_string())],
            "ef must surface as SET LOCAL hnsw.ef_search GUC"
        );
    }

    #[test]
    fn ann_omits_guc_when_ef_missing() {
        // ef defaults to 50 in the translator but no GUC is emitted unless
        // the Datalog body explicitly carries `ef:`. Today `pipeline.rs`
        // always includes `ef:` (FR-HNSW-F), so this shape is unreachable
        // from real callers — but the translator must stay safe.
        let q = r#"?[dist, qualified_name] := ~embedding_vectors:vec_idx { qualified_name | query: vec([0.0]), k: 5, bind_distance: dist }"#;
        let t = translate(q, BTreeMap::new()).unwrap();
        assert!(t.gucs.is_empty(), "missing ef: must not emit GUC");
    }

    // Phase 4 plumbing: `embedding_vectors` upsert must NEVER emit a
    // `hnsw.ef_construction` GUC. `hnsw.ef_construction` is a
    // `CREATE INDEX ... WITH (...)` parameter, not a runtime GUC on pgvector
    // 0.8.x — `SET LOCAL hnsw.ef_construction` aborts the write tx with
    // `invalid configuration parameter name`. The DDL knob stays in
    // `build_hnsw_create_stmt`.
    #[test]
    fn embedding_vectors_upsert_never_emits_ef_construction_guc() {
        // Setting LEANKG_HNSW_EF_CONST must not change the translator's
        // output: the env var is read only by the index-time DDL builder.
        let prev = std::env::var_os("LEANKG_HNSW_EF_CONST");
        std::env::set_var("LEANKG_HNSW_EF_CONST", "100");
        let gucs = embedding_gucs_for("embedding_vectors");
        match prev {
            Some(v) => std::env::set_var("LEANKG_HNSW_EF_CONST", v),
            None => std::env::remove_var("LEANKG_HNSW_EF_CONST"),
        }
        assert!(
            gucs.is_empty(),
            "ef_construction is a CREATE INDEX WITH param, never a runtime GUC"
        );
    }

    #[test]
    fn embedding_vectors_upsert_omits_guc_when_unset() {
        let prev = std::env::var_os("LEANKG_HNSW_EF_CONST");
        std::env::remove_var("LEANKG_HNSW_EF_CONST");
        let gucs = embedding_gucs_for("embedding_vectors");
        match prev {
            Some(v) => std::env::set_var("LEANKG_HNSW_EF_CONST", v),
            None => std::env::remove_var("LEANKG_HNSW_EF_CONST"),
        }
        assert!(gucs.is_empty(), "unset env must not emit GUC");
    }

    #[test]
    fn embedding_state_upsert_does_not_emit_guc() {
        // No upsert path tunes HNSW at runtime anymore (ef_construction is a
        // CREATE INDEX WITH param); embedding_state must never emit GUCs.
        let prev = std::env::var_os("LEANKG_HNSW_EF_CONST");
        std::env::set_var("LEANKG_HNSW_EF_CONST", "100");
        let gucs = embedding_gucs_for("embedding_state");
        match prev {
            Some(v) => std::env::set_var("LEANKG_HNSW_EF_CONST", v),
            None => std::env::remove_var("LEANKG_HNSW_EF_CONST"),
        }
        assert!(gucs.is_empty(), "embedding_state must not surface GUCs");
    }
}

// ---------------------------------------------------------------------------
// Phase 5 — additional shapes (multi-rule count, head alias, :put $args,
// key-only rm, attr-binding read).
// ---------------------------------------------------------------------------

#[test]
fn multi_rule_count_distinct() {
    // H6/G88: `files[f] := *code_elements[...]; ?[count(f)] := files[f]`
    let t = translate(
        "files[f] := *code_elements[n, a, b, f, c, d, e, g, h, i, j, k]\n?[count(f)] := files[f]",
        BTreeMap::new(),
    )
    .unwrap();
    assert!(
        t.sql.contains("count(DISTINCT \"file_path\")"),
        "got: {}",
        t.sql
    );
    assert!(t.sql.contains("FROM code_elements"), "got: {}", t.sql);
    assert_eq!(t.head, vec!["count(f)"]);
}

#[test]
fn head_alias_span_select() {
    // G107: `span = line_end - line_start` head alias + `:order -span`.
    let t = translate(
        r#"?[qualified_name, file_path, line_end, line_start, language, name, span] := *code_elements[qualified_name, et, name, file_path, line_start, line_end, language, _, _, _, _], line_end >= 0, line_start >= 0, (line_end - line_start) >= 5, et in ["function", "struct"], span = line_end - line_start:order -span"#,
        BTreeMap::new(),
    )
    .unwrap();
    assert!(
        t.sql.contains("\"line_end\" - \"line_start\" AS \"span\""),
        "got: {}",
        t.sql
    );
    assert!(t.sql.contains("ORDER BY \"span\" DESC"), "got: {}", t.sql);
    // The definition clause must NOT appear in WHERE (no alias column).
    assert!(!t.sql.contains("WHERE \"span\""), "got: {}", t.sql);
}

#[test]
fn put_object_args() {
    // CH2: `:put index_hashes {path, hash} <- $args` with an object param.
    let mut p = BTreeMap::new();
    p.insert("path".into(), serde_json::json!("a.rs"));
    p.insert("hash".into(), serde_json::json!("h1"));
    let t = translate(
        r#"?[path, hash] <- [[$path, $hash]] :put index_hashes {path => hash}"#,
        p,
    )
    .unwrap();
    assert!(t.sql.contains("INSERT INTO index_hashes"), "got: {}", t.sql);
    assert!(
        t.sql.contains("ON CONFLICT (\"path\")"),
        "index_hashes is keyed by path: got: {}",
        t.sql
    );
    assert_eq!(t.params.len(), 2, "path+hash bound, not interpolated");
}

#[test]
fn put_object_args_missing_param_is_noop() {
    // Missing params → the literal row is all-null; still a valid write.
    let t = translate(
        r#"?[path, hash] <- [[$path, $hash]] :put index_hashes {path => hash}"#,
        BTreeMap::new(),
    )
    .unwrap();
    assert_eq!(t.kind, TranslationKind::Write);
    assert!(t.sql.contains("INSERT INTO index_hashes"), "got: {}", t.sql);
}

#[test]
fn rm_key_only_infers_table() {
    // `:rm embedding_vectors {qualified_name}` — key-only delete on a
    // keyed table; the table name must be inferred from the key column.
    let t = translate(
        r#"?[qualified_name] <- [["a"], ["b"]] :rm embedding_vectors {qualified_name}"#,
        BTreeMap::new(),
    )
    .unwrap();
    assert!(
        t.sql
            .contains("DELETE FROM embedding_vectors WHERE \"qualified_name\" = ANY($1::text[])"),
        "got: {}",
        t.sql
    );
}

#[test]
fn read_attr_binding_syntax() {
    // Risk note 8: `*relation{col = $x, ...}` attribute-binding sugar.
    let mut p = BTreeMap::new();
    p.insert("svc".into(), serde_json::json!("my-service"));
    p.insert("env".into(), serde_json::json!("prod"));
    let t = translate(
        "?[service_name, env] := *service_metadata{service_name, env, team}, service_name == $svc, env == $env",
        p,
    )
    .unwrap();
    assert!(t.sql.contains("FROM service_metadata"), "got: {}", t.sql);
    assert!(
        t.sql.contains("WHERE \"service_name\" = $1"),
        "got: {}",
        t.sql
    );
    assert_eq!(t.head, vec!["service_name", "env"]);
}

#[test]
fn query_cache_attr_binding_read() {
    // P2: `*query_cache[cache_key = $key, value_json, created_at, ttl_seconds]`.
    let mut p = BTreeMap::new();
    p.insert("key".into(), serde_json::json!("k1"));
    let t = translate(
        "?[value_json, created_at, ttl_seconds] := *query_cache[cache_key = $key, value_json, created_at, ttl_seconds]",
        p,
    )
    .unwrap();
    assert!(t.sql.contains("FROM query_cache"), "got: {}", t.sql);
}

#[test]
fn rm_rule_based_with_table_prefix_and_params() {
    // Rule-based :rm with table-prefixed target AND bound params
    // (remove_relationships_by_source shape).
    let mut p = BTreeMap::new();
    p.insert("sq".into(), serde_json::json!("src/a.rs::f"));
    let t = translate(
        "?[source_qualified, target_qualified, rel_type, confidence, metadata] := *relationships[source_qualified, target_qualified, rel_type, confidence, metadata, _], source_qualified = $sq :rm relationships {source_qualified, target_qualified, rel_type, confidence, metadata}",
        p,
    )
    .unwrap();
    assert!(
        t.sql
            .contains("DELETE FROM relationships WHERE \"source_qualified\" = $1"),
        "got: {}",
        t.sql
    );
    assert_eq!(t.params.len(), 1, "param must be bound, not lost");
}

#[test]
fn rm_rule_based_in_list_params() {
    // remove_relationships_by_files_bulk shape: `in $sqs`.
    let mut p = BTreeMap::new();
    p.insert(
        "sqs".into(),
        serde_json::json!(["src/a.rs::f", "src/b.rs::g"]),
    );
    let t = translate(
        "?[source_qualified, target_qualified, rel_type, confidence, metadata] := *relationships[source_qualified, target_qualified, rel_type, confidence, metadata, _], source_qualified in $sqs :rm relationships {source_qualified, target_qualified, rel_type, confidence, metadata}",
        p,
    )
    .unwrap();
    assert!(
        t.sql
            .contains("DELETE FROM relationships WHERE \"source_qualified\" = ANY($1::text[])"),
        "got: {}",
        t.sql
    );
}

#[test]
fn rm_literal_key_only_with_table_prefix() {
    // `:rm embedding_vectors {qualified_name}` (build.rs remove_vectors).
    let t = translate(
        r#"?[qualified_name] <- [["a"], ["b"]] :rm embedding_vectors {qualified_name}"#,
        BTreeMap::new(),
    )
    .unwrap();
    assert!(
        t.sql
            .contains("DELETE FROM embedding_vectors WHERE \"qualified_name\" = ANY($1::text[])"),
        "got: {}",
        t.sql
    );
}

#[test]
fn rm_embedding_state_with_table_prefix() {
    // delete_state_rows targets `embedding_state`, whose PK `qualified_name`
    // is shared with embedding_vectors. The explicit table prefix must win
    // over key-column inference, else this deletes from the wrong table.
    let t = translate(
        r#"?[qualified_name] <- [["a"], ["b"]] :rm embedding_state {qualified_name}"#,
        std::collections::BTreeMap::new(),
    )
    .unwrap();
    assert!(
        t.sql
            .contains("DELETE FROM embedding_state WHERE \"qualified_name\" = ANY($1::text[])"),
        "got: {}",
        t.sql
    );
}

#[test]
fn put_embedding_vectors_maps_vector_to_vec() {
    // B1 — put_pairs_to_db_script shape: cozo `vector` col → PG `vec`.
    let t = translate(
        r#"?[qualified_name, vector] <- [["a", vec([1.0, 0.0, 0.0])]] :put embedding_vectors {qualified_name => vector}"#,
        BTreeMap::new(),
    )
    .unwrap();
    assert!(
        t.sql
            .contains("INSERT INTO embedding_vectors (\"qualified_name\", \"vec\")"),
        "got: {}",
        t.sql
    );
    assert!(
        t.sql.contains("ON CONFLICT (\"qualified_name\")"),
        "got: {}",
        t.sql
    );
    assert!(
        t.sql.contains("\"vec\" = EXCLUDED.\"vec\""),
        "got: {}",
        t.sql
    );
}

#[test]
fn put_embedding_vectors_never_emits_gucs_even_when_env_set() {
    // hnsw.ef_construction is a CREATE INDEX WITH param, not a runtime GUC;
    // the translated write must carry no SET LOCAL even when the env knob
    // that feeds the index DDL is present.
    let prev = std::env::var_os("LEANKG_HNSW_EF_CONST");
    std::env::set_var("LEANKG_HNSW_EF_CONST", "100");
    let t = translate(
        r#"?[qualified_name, vector] <- [["a", vec([1.0, 0.0, 0.0])]] :put embedding_vectors {qualified_name => vector}"#,
        BTreeMap::new(),
    )
    .unwrap();
    match prev {
        Some(v) => std::env::set_var("LEANKG_HNSW_EF_CONST", v),
        None => std::env::remove_var("LEANKG_HNSW_EF_CONST"),
    }
    assert!(
        t.gucs.is_empty(),
        "no runtime GUCs allowed on embedding_vectors writes: {:?}",
        t.gucs
    );
}

#[test]
fn regex_matches_param_wrapped_in_lowercase_binds() {
    let mut p = BTreeMap::new();
    p.insert("pattern".into(), serde_json::json!("^foo"));
    let t = translate(
        "?[a] := *t[a, b], regex_matches(lowercase(b), lowercase($pattern))",
        p,
    )
    .unwrap();
    assert!(
        t.sql.contains("lower($1)"),
        "param must bind through the lowercase wrapper: got: {}",
        t.sql
    );
    assert_eq!(t.params.len(), 1, "pattern must be a bound param");
}

#[test]
fn str_contains_param_wrapped_in_lowercase_binds() {
    let mut p = BTreeMap::new();
    p.insert("pattern".into(), serde_json::json!("needle"));
    let t = translate(
        "?[a] := *t[a, b], str_contains(lowercase(b), lowercase($pattern))",
        p,
    )
    .unwrap();
    assert!(t.sql.contains("lower($1)"), "got: {}", t.sql);
    assert_eq!(t.params.len(), 1);
}

#[test]
fn boolean_literal_filter_binds_param() {
    // `is_deleted = false` must bind `false` as a bool param, not emit a
    // bare `false` token (Postgres would read it as a column name → E42703).
    let t = translate(
        "?[tool_name, timestamp] := *context_metrics[tool_name, timestamp, project_path, input_tokens, output_tokens, output_elements, execution_time_ms, baseline_tokens, baseline_lines_scanned, tokens_saved, savings_percent, correct_elements, total_expected, f1_score, query_pattern, query_file, query_depth, success, is_deleted], is_deleted = false",
        BTreeMap::new(),
    )
    .unwrap();
    assert!(
        t.sql.contains("\"is_deleted\" = $1"),
        "bool literal must bind as param, got: {}",
        t.sql
    );
    assert_eq!(t.params.len(), 1, "one bool param");
}

#[test]
fn hnsw_create_respects_m_and_ef_from_stmt() {
    // ::hnsw create with explicit m / ef_construction values must be
    // honored, not silently replaced with hardcoded defaults.
    let t = translate(
        "::hnsw create embedding_vectors:vec_idx {\n    dim: 384,\n    dtype: F32,\n    fields: [vector],\n    distance: Cosine,\n    ef_construction: 40,\n    m: 32,\n    extend_candidates: false,\n    keep_pruned_connections: false\n}",
        BTreeMap::new(),
    )
    .unwrap();
    assert!(
        t.sql.contains("m = 32"),
        "m=32 must be honored, got: {}",
        t.sql
    );
    assert!(
        t.sql.contains("ef_construction = 40"),
        "ef_construction=40 must be honored, got: {}",
        t.sql
    );
}

#[test]
fn put_embedding_state_literal_rows() {
    // Exact shape from embeddings::state::mark_stale_for_qualified_names:
    // `?[cols] <- [[literal rows]]\n:put embedding_state {cols}`
    let t = translate(
        "?[qualified_name, usearch_key, content_hash, state, embedded_at] <- [[\"test/qn\", 0, \"\", \"stale\", \"2026-08-06T03:00:00Z\"]]\n:put embedding_state {qualified_name, usearch_key, content_hash, state, embedded_at}",
        std::collections::BTreeMap::new(),
    );
    match t {
        Ok(t) => {
            assert!(t.sql.contains("embedding_state"), "got: {}", t.sql);
            // Keyed table, no `=>` marker → must upsert, not plain INSERT.
            assert!(
                t.sql.contains("ON CONFLICT (\"qualified_name\")"),
                "expected upsert, got: {}",
                t.sql
            );
        }
        Err(e) => panic!("translate failed: {}", e),
    }
}

#[test]
fn rm_embedding_state_param_batch() {
    // `?[qn] <- $qns :rm embedding_state {qualified_name}` — parameterized
    // delete for QNs that may contain quotes/backslashes.
    let mut p = BTreeMap::new();
    p.insert(
        "qns".into(),
        serde_json::json!([["qn/a"], ["qn\"with\"quote"], ["qn\\backslash"]]),
    );
    let t = translate(
        r#"?[qualified_name] <- $qns :rm embedding_state {qualified_name}"#,
        p,
    )
    .unwrap();
    assert!(
        t.sql
            .contains("DELETE FROM embedding_state WHERE \"qualified_name\" = ANY($1::text[])"),
        "got: {}",
        t.sql
    );
}

#[test]
fn put_embedding_state_large_batch_preserves_row_count() {
    // The indexer marks up to UPSERT_CHUNK (500) qualified_names per `:put`.
    // A translator bug that drops or duplicates a row in a large VALUES list
    // would surface as PG E21000 (duplicate PK) or a wrong row count. Build
    // 500 distinct QNs and assert the SQL keeps all 500.
    let rows: Vec<String> = (0..500)
        .map(|i| format!("[\"qn_{i}\", 0, \"\", \"stale\", \"0\"]"))
        .collect();
    let values = rows.join(", ");
    let query = format!(
        "?[qualified_name, usearch_key, content_hash, state, embedded_at] <- [{values}]\n:put embedding_state {{qualified_name, usearch_key, content_hash, state, embedded_at}}"
    );
    let t = translate(&query, BTreeMap::new()).unwrap();
    // 500 rows × 5 cols = 2500 params. If the translator dropped or merged
    // a row, this count would be off and a re-write would E21000.
    assert_eq!(
        t.params.len(),
        2500,
        "500-row :put must yield 2500 params, got: {}",
        t.params.len()
    );
    let row_count = t.params.len() / 5;
    assert_eq!(row_count, 500, "500 rows expected, got {}", row_count);
}

#[test]
fn put_embedding_state_reput_existing_row_upserts() {
    // The regression this guards: a second `:put` of the same qualified_name
    // on a keyed table must not hit embedding_state_pkey (duplicate key).
    let q = "?[qualified_name, usearch_key, content_hash, state, embedded_at] <- [[\"same/qn\", 0, \"\", \"stale\", \"2026-08-06T03:00:00Z\"]]\n:put embedding_state {qualified_name, usearch_key, content_hash, state, embedded_at}";
    let t1 = translate(q, std::collections::BTreeMap::new()).unwrap();
    let t2 = translate(q, std::collections::BTreeMap::new()).unwrap();
    assert!(t1.sql.contains("ON CONFLICT"));
    assert_eq!(t1.sql, t2.sql);
}
