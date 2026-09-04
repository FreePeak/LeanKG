# LeanKG Architectural Review - March 2026

## Architectural Critique (Summary)

- **AST `calls` edges are file-local only** — `extract_call` at `extractor.rs:431` always builds `target_qualified` as `{file_path}::{name}`, so cross-file call edges are never created. This makes `get_call_graph` nearly useless for inter-module analysis.
- **`implements` detection is heuristic and wrong** — `extract_go_implementations` at `extractor.rs:267` marks every field whose type is not `"struct"` as an `implements` edge. This floods the graph with false positives (e.g., every embedded struct field, every primitive-typed field that happens to have a non-`"struct"` type string).
- **All MCP query functions call `all_elements()`** — `find_function`, `query_file`, `search_code`, and `get_dependents` each call `all_elements()` (`handler.rs:508`, `332`, `558`, `506`) and filter in Rust. This loads the entire element table into memory on every request. There is no Datalog push-down for these cases.
- **No depth guard on `get_call_graph`** — `get_call_graph` is documented as "full depth" and executes a single-hop query (`handler.rs:533-549`). There is no recursion, but also no cap, so future multi-hop expansion would immediately cause neighbor explosion.
- **`get_context` has no signature-only mode** — `handler.rs:460-501` always returns full `line_start`/`line_end` metadata. The LLM receives no abbreviated "header only" view; it must infer body size from line numbers alone without fetching source.
- **`query_file` loads all 7115 elements then does a substring scan** — `handler.rs:325-351`. For large codebases this is O(n) RAM and CPU.
- **SQL-injection style string interpolation throughout `query.rs`** — every Datalog query is built with `format!()` directly substituting user strings. A qualified name containing `"` breaks the query or enables arbitrary Datalog injection.
- **`get_dependencies` returns elements in the file, not true import targets** — `query.rs:98-142` queries `code_elements` filtered by `file_path`, not the `relationships` table. The returned data is the file's own symbols, not what it imports.

---

## 1. AST & Edge Extraction

### Problem: `calls` edges are always file-local

**Evidence:** `extractor.rs:431`
```rust
let target_qualified = format!("{}::{}", self.file_path, name);
```
The callee is always assumed to live in the same file. Cross-module calls are silently dropped.

### Fix: Store the bare callee name in metadata; resolve cross-file at query time

Store the bare name and let the graph engine resolve it post-index:

```rust
// extractor.rs — replace extract_call
fn extract_call(
    &self,
    node: Node,
    parent: Option<&str>,
    _elements: &mut Vec<CodeElement>,
    relationships: &mut Vec<Relationship>,
) {
    let mut cursor = node.walk();
    for child in node.children(&mut cursor) {
        if child.kind() == "identifier" {
            if let Some(bytes) = self.source.get(child.byte_range()) {
                if let Ok(name) = std::str::from_utf8(bytes) {
                    // Skip stdlib / single-char / noise identifiers
                    if is_noise_call(name) {
                        break;
                    }
                    let source = match parent {
                        Some(p) if !p.is_empty() => format!("{}::{}", self.file_path, p),
                        _ => self.file_path.to_string(),
                    };
                    // Use a sentinel prefix "__unresolved__" so the
                    // post-index pass can do a name-only lookup.
                    let target_qualified = format!("__unresolved__{}", name);
                    relationships.push(Relationship {
                        id: None,
                        source_qualified: source,
                        target_qualified,
                        rel_type: "calls".to_string(),
                        metadata: serde_json::json!({
                            "bare_name": name,
                            "callee_file_hint": self.file_path,
                        }),
                    });
                }
            }
            break;
        }
    }
}

/// Filter identifiers that are never meaningful call targets.
fn is_noise_call(name: &str) -> bool {
    matches!(
        name,
        "println" | "print" | "eprintln" | "format" | "vec" | "assert"
        | "assert_eq" | "assert_ne" | "panic" | "unwrap" | "expect"
        | "clone" | "to_string" | "into" | "from" | "len" | "is_empty"
        | "ok" | "err" | "map" | "and_then" | "or_else" | "collect"
        | "iter" | "push" | "pop" | "insert" | "get" | "contains"
    ) || name.len() == 1  // single-letter variables
}
```

Add a **resolution pass** after all files are indexed in `graph/query.rs`:

```rust
// graph/query.rs — add after bulk insert
pub fn resolve_call_edges(&self) -> Result<usize, Box<dyn std::error::Error>> {
    // Find all unresolved call targets
    let query = r#"
        ?[source_qualified, target_qualified, metadata]
        := *relationships[source_qualified, target_qualified, "calls", metadata],
           starts_with(target_qualified, "__unresolved__")
    "#;
    let result = self.db.run_script(query, Default::default())?;
    let mut resolved = 0;

    for row in &result.rows {
        let source = row[0].as_str().unwrap_or("").to_string();
        let unresolved = row[1].as_str().unwrap_or("").to_string();
        let bare_name = unresolved.trim_start_matches("__unresolved__");
        let meta_str = row[2].as_str().unwrap_or("{}");
        let meta: serde_json::Value = serde_json::from_str(meta_str).unwrap_or_default();

        // Prefer functions in the same file, then fall back to any match
        let callee_file_hint = meta.get("callee_file_hint")
            .and_then(|v| v.as_str())
            .unwrap_or("");

        let lookup = format!(
            r#"?[qn] := *code_elements[qn, "function", "{bare}", fp, _, _, _, _, _],
                       starts_with(fp, "{hint}")
               :limit 1"#,
            bare = escape_datalog(bare_name),
            hint = escape_datalog(callee_file_hint),
        );

        if let Ok(res) = self.db.run_script(&lookup, Default::default()) {
            if let Some(target_row) = res.rows.first() {
                if let Some(target_qn) = target_row[0].as_str() {
                    // Replace unresolved with real target
                    self.db.run_script(
                        &format!(
                            r#"?[source_qualified, target_qualified, rel_type, metadata]
                               <- [["{src}", "{tgt}", "calls", "{meta}"]]
                               :put relationships {{source_qualified, target_qualified, rel_type, metadata}}"#,
                            src = escape_datalog(&source),
                            tgt = escape_datalog(target_qn),
                            meta = escape_datalog(meta_str),
                        ),
                        Default::default(),
                    )?;
                    resolved += 1;
                }
            }
        }

        // Delete the unresolved placeholder regardless
        self.db.run_script(
            &format!(
                r#":delete relationships where source_qualified = "{src}"
                   and target_qualified = "{tgt}""#,
                src = escape_datalog(&source),
                tgt = escape_datalog(&unresolved),
            ),
            Default::default(),
        )?;
    }

    Ok(resolved)
}
```

### Problem: `implements` detection is wrong for Go

**Evidence:** `extractor.rs:285-301` — any struct field whose type string is not `"struct"` is mapped as an `implements` edge. This fires on `name string`, `age int`, etc.

### Fix: Only emit `implements` for embedded (anonymous) fields

```rust
// extractor.rs — replace extract_go_implementations
fn extract_go_implementations(
    &self,
    node: Node,
    struct_qualified: String,
    relationships: &mut Vec<Relationship>,
) {
    let mut cursor = node.walk();
    for child in node.children(&mut cursor) {
        if child.kind() != "field_declaration_list" {
            continue;
        }
        let mut field_cursor = child.walk();
        for field in child.children(&mut field_cursor) {
            if field.kind() != "field_declaration" {
                continue;
            }
            // An embedded field has a type but NO field name identifier before it.
            // tree-sitter-go represents anonymous fields as:
            //   field_declaration { type: type_identifier }  (no "name" field)
            let has_name = field.child_by_field_name("name").is_some();
            if has_name {
                continue; // named field — not an embedding
            }
            if let Some(type_node) = field.child_by_field_name("type") {
                let type_str = std::str::from_utf8(
                    self.source.get(type_node.byte_range()).unwrap_or(&[]),
                )
                .unwrap_or("")
                .trim_start_matches('*'); // handle pointer embedding

                if !type_str.is_empty() && !type_str.contains(' ') {
                    // Only emit for single-token type names (interfaces/structs)
                    relationships.push(Relationship {
                        id: None,
                        source_qualified: struct_qualified.clone(),
                        target_qualified: format!("{}::{}", self.file_path, type_str),
                        rel_type: "implements".to_string(),
                        metadata: serde_json::json!({"embedded": true}),
                    });
                }
            }
        }
    }
}
```

---

## 2. CozoDB Query Optimization

### Problem: `all_elements()` is called before filtering in Rust

**Evidence:** `handler.rs:508`, `332`, `558`, `694` — every lookup fetches the full table.

### Fix: Push predicates into Datalog; add a shared escaping helper

First, add a safe parameter escaping helper (addresses injection risk):

```rust
// graph/query.rs — add near top
/// Escape a string value for safe inline Datalog string literals.
/// CozoDB does not yet support parameterized queries, so we must escape manually.
fn escape_datalog(s: &str) -> String {
    s.replace('\\', "\\\\").replace('"', "\\\"")
}
```

Replace `all_elements()` call sites in `GraphEngine` with pushed-down queries:

```rust
// graph/query.rs — replace search_by_name
pub fn search_by_name_typed(
    &self,
    name: &str,
    element_type: Option<&str>,
    limit: usize,
) -> Result<Vec<CodeElement>, Box<dyn std::error::Error>> {
    let safe_name = escape_datalog(&name.to_lowercase());
    let type_clause = match element_type {
        Some(t) => format!(r#", element_type = "{}""#, escape_datalog(t)),
        None => String::new(),
    };
    let query = format!(
        r#"?[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, metadata]
           := *code_elements[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, metadata]{type_clause},
              regex_matches(lowercase(name), "{pattern}")
           :limit {limit}"#,
        type_clause = type_clause,
        pattern = safe_name,
        limit = limit,
    );
    self.run_element_query(&query)
}

// graph/query.rs — replace find_element_by_name  
pub fn find_elements_by_name_exact(
    &self,
    name: &str,
    element_type: Option<&str>,
) -> Result<Vec<CodeElement>, Box<dyn std::error::Error>> {
    let safe_name = escape_datalog(name);
    let type_clause = match element_type {
        Some(t) => format!(r#", element_type = "{}""#, escape_datalog(t)),
        None => String::new(),
    };
    let query = format!(
        r#"?[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, metadata]
           := *code_elements[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, metadata]{type_clause},
              name = "{name}"
           :limit 20"#,
        type_clause = type_clause,
        name = safe_name,
    );
    self.run_element_query(&query)
}

// graph/query.rs — add helper to de-duplicate row mapping
fn run_element_query(
    &self,
    query: &str,
) -> Result<Vec<CodeElement>, Box<dyn std::error::Error>> {
    let result = self.db.run_script(query, Default::default())?;
    Ok(result.rows.iter().map(|row| {
        let parent_qualified = row[7].as_str().map(String::from);
        let metadata_str = row[8].as_str().unwrap_or("{}");
        CodeElement {
            qualified_name: row[0].as_str().unwrap_or("").to_string(),
            element_type: row[1].as_str().unwrap_or("").to_string(),
            name: row[2].as_str().unwrap_or("").to_string(),
            file_path: row[3].as_str().unwrap_or("").to_string(),
            line_start: row[4].as_i64().unwrap_or(0) as u32,
            line_end: row[5].as_i64().unwrap_or(0) as u32,
            language: row[6].as_str().unwrap_or("").to_string(),
            parent_qualified,
            metadata: serde_json::from_str(metadata_str)
                .unwrap_or(serde_json::json!({})),
        }
    }).collect())
}
```

### Depth-limited `get_call_graph` with neighbor cap (Datalog)

The current `get_call_graph` is 1-hop only (no recursion). Here is a bounded 2-hop Datalog version with a row cap to prevent explosion:

```rust
// graph/query.rs — add method
pub fn get_call_graph_bounded(
    &self,
    source_qualified: &str,
    max_depth: u32,    // caller must cap at 2-3 for LLM use
    max_results: usize,
) -> Result<Vec<(String, String, u32)>, Box<dyn std::error::Error>> {
    // CozoDB recursive rules via fixed-point iteration
    // We unroll manually for depth ≤ 3 to avoid unbounded recursion.
    let safe_src = escape_datalog(source_qualified);
    let query = match max_depth {
        1 => format!(
            r#"?[src, tgt, depth] :=
               *relationships["{src}", tgt, "calls", _],
               src = "{src}", depth = 1
               :limit {limit}"#,
            src = safe_src, limit = max_results,
        ),
        2 => format!(
            r#"hop1[src, tgt] := *relationships[src, tgt, "calls", _], src = "{src}"
               hop2[src2, tgt2] := hop1[_, src2], *relationships[src2, tgt2, "calls", _]
               ?[src, tgt, depth] := hop1[src, tgt], depth = 1
               ?[src, tgt, depth] := hop2[src, tgt], depth = 2
               :limit {limit}"#,
            src = safe_src, limit = max_results,
        ),
        _ => format!(   // depth 3 default for get_call_graph
            r#"hop1[src, tgt] := *relationships[src, tgt, "calls", _], src = "{src}"
               hop2[s2, t2] := hop1[_, s2], *relationships[s2, t2, "calls", _]
               hop3[s3, t3] := hop2[_, s3], *relationships[s3, t3, "calls", _]
               ?[src, tgt, depth] := hop1[src, tgt], depth = 1
               ?[src, tgt, depth] := hop2[src, tgt], depth = 2
               ?[src, tgt, depth] := hop3[src, tgt], depth = 3
               :limit {limit}"#,
            src = safe_src, limit = max_results,
        ),
    };

    let result = self.db.run_script(&query, Default::default())?;
    Ok(result.rows.iter().filter_map(|row| {
        Some((
            row[0].as_str()?.to_string(),
            row[1].as_str()?.to_string(),
            row[2].as_i64()? as u32,
        ))
    }).collect())
}
```

---

## 3. Token Efficiency Routing

### Problem: No signature-only mode exists

`get_context` (`handler.rs:460`) always returns full `line_start`/`line_end`. The LLM receives no abbreviated view. For large files this leads to downstream tools (like `view_file`) fetching entire function bodies unnecessarily.

### Fix: Add a `signature_only` flag and store signatures during indexing

**Step 1:** Store the signature in `CodeElement.metadata` at index time:

```rust
// extractor.rs — update extract_function to capture signature line
fn extract_function(&self, node: Node, parent: Option<&str>, elements: &mut Vec<CodeElement>) {
    if let Some(name) = self.get_node_name(node) {
        let qualified_name = format!("{}::{}", self.file_path, name);

        // Capture only the first line as the "signature"
        let sig_line = node.start_position().row as u32;
        let sig_end = self.find_body_start_line(node)
            .unwrap_or(sig_line); // line just before `{`

        // Extract signature text (bytes of just the first line)
        let sig_bytes_range = node.start_byte()
            ..self.source.iter()
                .skip(node.start_byte())
                .position(|&b| b == b'\n')
                .map(|p| node.start_byte() + p)
                .unwrap_or(node.end_byte());
        let signature = std::str::from_utf8(
            self.source.get(sig_bytes_range).unwrap_or(&[])
        )
        .unwrap_or("")
        .trim()
        .to_string();

        elements.push(CodeElement {
            qualified_name,
            element_type: "function".to_string(),
            name,
            file_path: self.file_path.to_string(),
            line_start: node.start_position().row as u32 + 1,
            line_end: node.end_position().row as u32 + 1,
            language: self.language.to_string(),
            parent_qualified: parent.map(String::from),
            metadata: serde_json::json!({
                "signature": signature,
                "signature_line_end": sig_end + 1,
            }),
        });
    }
}

fn find_body_start_line(&self, node: Node) -> Option<u32> {
    let mut cursor = node.walk();
    for child in node.children(&mut cursor) {
        if child.kind() == "block" || child.kind() == "statement_block" {
            return Some(child.start_position().row as u32);
        }
    }
    None
}
```

**Step 2:** Expose `signature_only` in `get_context` tool and handler:

```rust
// tools.rs — update get_context schema
ToolDefinition {
    name: "get_context".to_string(),
    description: "Get AI context for file. By default returns only function signatures \
                  (token-optimized). Set signature_only=false to include full line ranges."
        .to_string(),
    input_schema: json!({
        "type": "object",
        "properties": {
            "file": {"type": "string", "description": "File path to get context for"},
            "signature_only": {
                "type": "boolean",
                "default": true,
                "description": "Return only signatures (default). Set false for full body metadata."
            },
            "max_tokens": {
                "type": "integer",
                "default": 4000,
                "description": "Token budget cap"
            }
        },
        "required": ["file"]
    }),
},
```

```rust
// handler.rs — update get_context
fn get_context(&self, args: &Value) -> Result<Value, String> {
    let file = args["file"].as_str().ok_or("Missing 'file' parameter")?;
    let max_tokens = args["max_tokens"].as_u64().unwrap_or(4000) as usize;
    let signature_only = args["signature_only"].as_bool().unwrap_or(true);

    let result = self
        .graph_engine
        .get_context(file, max_tokens)
        .map_err(|e| e.to_string())?;

    let elements_json: Vec<_> = result
        .elements
        .iter()
        .map(|ctx_elem| {
            let elem = &ctx_elem.element;
            let priority_str = match ctx_elem.priority {
                crate::graph::ContextPriority::RecentlyChanged => "recently_changed",
                crate::graph::ContextPriority::Imported => "imported",
                crate::graph::ContextPriority::Contained => "contained",
            };

            if signature_only {
                // Return signature text + single line number
                let sig = elem.metadata.get("signature")
                    .and_then(|v| v.as_str())
                    .unwrap_or(&elem.name);
                json!({
                    "qualified_name": elem.qualified_name,
                    "name": elem.name,
                    "type": elem.element_type,
                    "file": elem.file_path,
                    "line": elem.line_start,
                    "signature": sig,
                    "priority": priority_str,
                })
            } else {
                json!({
                    "qualified_name": elem.qualified_name,
                    "name": elem.name,
                    "type": elem.element_type,
                    "file": elem.file_path,
                    "line_start": elem.line_start,
                    "line_end": elem.line_end,
                    "priority": priority_str,
                    "token_count": ctx_elem.token_count,
                })
            }
        })
        .collect();

    Ok(json!({
        "file": file,
        "signature_only": signature_only,
        "elements": elements_json,
        "total_tokens": result.total_tokens,
        "truncated": result.truncated,
        "prompt": result.to_prompt()
    }))
}
```

---

## 4. MCP Tool Definition Review

### Current Issues

| Tool | Problem | Fix |
|------|---------|-----|
| `get_call_graph` | Described as "full depth" — misleads LLM into expecting recursive results | Add `depth` param; rename description |
| `get_dependencies` | Name implies imports, but impl (`query.rs:98`) returns elements in the file | Fix impl or rename to `get_file_elements` |
| `get_impact_radius` | `depth` has no `required: false` + no description of explosion risk | Add warning in description |
| `query_file` | No `element_type` filter — returns mixed noise | Add `element_type` optional filter |
| `find_function` | No `file` scoping parameter — all matches returned | Add optional `file` scope filter |
| `search_code` | `limit` defaults to 100 — too high for LLM context windows | Default to 20, max 50 |
| All tools | No `required` arrays — LLM may omit required params | Add `required` arrays |

### Improved Tool Definitions

```rust
// tools.rs — targeted replacements

// get_call_graph: add depth + fix description
ToolDefinition {
    name: "get_call_graph".to_string(),
    description: "Get bounded function call chain. Use depth=1 for direct callees, \
                  depth=2 for two hops. Avoid depth>3 to prevent neighbor explosion."
        .to_string(),
    input_schema: json!({
        "type": "object",
        "properties": {
            "function": {
                "type": "string",
                "description": "Qualified name, e.g. src/auth.rs::authenticate"
            },
            "depth": {
                "type": "integer",
                "default": 2,
                "description": "Max traversal hops (1-3 recommended)"
            },
            "max_results": {
                "type": "integer",
                "default": 30,
                "description": "Cap on returned edges to prevent explosion"
            }
        },
        "required": ["function"]
    }),
},

// find_function: add file scope + required
ToolDefinition {
    name: "find_function".to_string(),
    description: "Locate function definition by name. Optionally scope to a file.".to_string(),
    input_schema: json!({
        "type": "object",
        "properties": {
            "name": {
                "type": "string",
                "description": "Function name or substring"
            },
            "file": {
                "type": "string",
                "description": "Optional: scope search to this file path"
            }
        },
        "required": ["name"]
    }),
},

// search_code: lower default limit
ToolDefinition {
    name: "search_code".to_string(),
    description: "Search code elements by name/type. Default limit is 20 to fit LLM context.".to_string(),
    input_schema: json!({
        "type": "object",
        "properties": {
            "query": {"type": "string", "description": "Name substring to search"},
            "element_type": {
                "type": "string",
                "enum": ["function", "class", "struct", "interface", "decorator", "document"],
                "description": "Filter by element type"
            },
            "limit": {
                "type": "integer",
                "default": 20,
                "description": "Max results (default 20, max 50)"
            }
        },
        "required": ["query"]
    }),
},

// get_impact_radius: document explosion risk
ToolDefinition {
    name: "get_impact_radius".to_string(),
    description: "Get all elements transitively affected by changing a file, up to N hops. \
                  Keep depth<=2 for LLM context budgets. Depth 3 may return hundreds of nodes."
        .to_string(),
    input_schema: json!({
        "type": "object",
        "properties": {
            "file": {
                "type": "string",
                "description": "File path to analyze"
            },
            "depth": {
                "type": "integer",
                "default": 2,
                "description": "Traversal depth (1-3). Default 2."
            }
        },
        "required": ["file"]
    }),
},

// query_file: add element_type filter
ToolDefinition {
    name: "query_file".to_string(),
    description: "Find files or elements by name pattern. Use element_type to narrow results.".to_string(),
    input_schema: json!({
        "type": "object",
        "properties": {
            "pattern": {"type": "string", "description": "Substring to match against file paths"},
            "element_type": {
                "type": "string",
                "description": "Optional: filter by element type"
            }
        },
        "required": ["pattern"]
    }),
},
```

### Handler updates for `find_function` (file scoping) and `search_code` (limit cap)

```rust
// handler.rs — replace find_function
fn find_function(&self, args: &Value) -> Result<Value, String> {
    let name = args["name"].as_str().ok_or("Missing 'name' parameter")?;
    let file_scope = args["file"].as_str();

    let matches = self
        .graph_engine
        .find_elements_by_name_exact(name, Some("function"))  // pushed-down query
        .map_err(|e| e.to_string())?;

    let results: Vec<_> = matches
        .iter()
        .filter(|e| {
            file_scope.map(|f| e.file_path.contains(f)).unwrap_or(true)
        })
        .take(20)
        .map(|e| {
            let sig = e.metadata.get("signature")
                .and_then(|v| v.as_str())
                .unwrap_or(&e.name);
            json!({
                "qualified_name": e.qualified_name,
                "name": e.name,
                "file": e.file_path,
                "line": e.line_start,
                "signature": sig,
            })
        })
        .collect();

    Ok(json!({ "functions": results }))
}

// handler.rs — replace search_code limit cap
fn search_code(&self, args: &Value) -> Result<Value, String> {
    let query = args["query"].as_str().ok_or("Missing 'query' parameter")?;
    let raw_limit = args["limit"].as_i64().unwrap_or(20) as usize;
    let limit = raw_limit.min(50); // hard cap prevents explosion
    let element_type = args["element_type"].as_str();

    let matches = self
        .graph_engine
        .search_by_name_typed(query, element_type, limit)  // pushed-down query
        .map_err(|e| e.to_string())?;

    let results: Vec<_> = matches
        .iter()
        .map(|e| json!({
            "qualified_name": e.qualified_name,
            "name": e.name,
            "type": e.element_type,
            "file": e.file_path,
            "line": e.line_start,
        }))
        .collect();

    Ok(json!({ "results": results, "count": results.len() }))
}
```

---

## Priority Implementation Order

| Priority | Change | Impact |
|----------|--------|--------|
| P0 | Add `escape_datalog` helper + use it everywhere | Security / correctness |
| P0 | Fix `get_dependencies` to actually query `relationships` table | Correctness |
| P1 | Push-down queries in `search_by_name_typed` + `find_elements_by_name_exact` | Performance |
| P1 | Fix `is_noise_call` filter + `__unresolved__` call resolution pass | Graph quality |
| P1 | Fix `implements` detection to embedded-only | Graph quality |
| P2 | Add `signature_only` to `get_context` | Token efficiency |
| P2 | Depth-limited `get_call_graph_bounded` | Neighbor explosion prevention |
| P2 | MCP tool definition improvements (`required` arrays, limits, descriptions) | LLM usability |
