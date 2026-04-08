# Context Saving Metrics Tracking - Design Spec

**Date:** 2026-04-08
**Status:** IMPLEMENTING
**Feature:** FR-METRIC-01 to FR-METRIC-05

---

## 1. Problem Statement

LeanKG doesn't track how much context/token savings each tool provides. Users have no visibility into:
- How many tokens each LeanKG tool call saved vs grep
- Cumulative savings over time
- Per-tool effectiveness metrics

We need a built-in tracking system that compares LeanKG tool usage against actual grep baseline execution.

---

## 2. Core Concept: Grep Baseline Comparison

The **baseline** for comparison is what would happen if the agent used `grep` instead of LeanKG tools:

| Tool Category | Grep Baseline Command | Token Estimation |
|---------------|----------------------|------------------|
| `search_code` | `grep -rn "query" ./src` | lines * 4 chars |
| `find_function` | `grep -rn "func name" ./src` | lines * 4 chars |
| `query_file` | `find ./src -name "pattern"` | files * 50 tokens |
| `get_dependencies` | `grep -n "import" file.rs` | lines * 4 chars |
| `get_dependents` | `grep -rn "import.*file" ./src` | lines * 4 chars |
| `get_impact_radius` | Full traversal simulation | estimated |
| `get_context` | `cat file.rs` | full file chars |

**Actual Execution:** When `record_metric()` is called, it optionally runs the equivalent grep command and measures:
1. How many lines/files grep would scan
2. Estimated tokens from grep output
3. Compare to LeanKG's actual output tokens

---

## 3. CozoDB Schema: `context_metrics`

```cozo
:create context_metrics {
    id: String,                   // UUID
    tool_name: String,            // e.g., "get_impact_radius"
    timestamp: Int,               // Unix timestamp (seconds)
    project_path: String,         // Project root being queried
    
    // LeanKG tool execution
    input_tokens: Int,           // Tokens in the query/request
    output_tokens: Int,           // Tokens in LeanKG response
    output_elements: Int,         // Number of elements returned
    execution_time_ms: Int,       // Tool execution time
    
    // Baseline (grep comparison)
    baseline_tokens: Int,        // Estimated tokens if using grep
    baseline_lines_scanned: Int, // Lines grep would scan
    tokens_saved: Int,           // baseline_tokens - output_tokens
    savings_percent: Float,      // (tokens_saved / baseline_tokens) * 100
    
    // Quality metrics (when ground truth available)
    correct_elements: Int?,      // Elements matching expected
    total_expected: Int?,         // Total expected elements
    f1_score: Float?,             // Precision/recall F1
    
    // Context
    query_pattern: String?,      // What was being searched
    query_file: String?,          // File being queried (if applicable)
    query_depth: Int?,           // Depth parameter (if applicable)
    success: Bool                // Tool succeeded
}

// Indexes
:create context_metrics::tool_name_index { ref: (tool_name), compressed: true }
:create context_metrics::timestamp_index { ref: (timestamp), compressed: true }
:create context_metrics::project_path_index { ref: (project_path), compressed: true }
```

---

## 4. Baseline Estimation Logic

### 4.1 Search Operations (search_code, find_function)

```rust
fn estimate_grep_baseline(query: &str, project_path: &str) -> BaselineResult {
    // Run: grep -rn "query" project_path
    // Count lines, estimate tokens = lines * 4
    // Return tokens_saved = grep_output_tokens - leankg_output_tokens
}
```

### 4.2 Navigation Operations (query_file)

```rust
fn estimate_find_baseline(pattern: &str) -> BaselineResult {
    // Run: find project_path -name "pattern"
    // Count files, estimate tokens = files * 50
}
```

### 4.3 Context Operations (get_context, get_dependents)

```rust
fn estimate_cat_baseline(file: &str) -> BaselineResult {
    // Count file lines, tokens = lines * 4
}
```

### 4.4 Impact Operations (get_impact_radius)

```rust
fn estimate_impact_baseline(file: &str, depth: u32) -> BaselineResult {
    // Simulate full graph traversal
    // Estimate tokens based on affected files count
}
```

---

## 5. MCP Tools

### 5.1 `get_metrics_summary`

Returns aggregated metrics (default: all time):

```json
{
  "total_invocations": 150,
  "total_tokens_saved": 125000,
  "average_savings_percent": 87.5,
  "retention_days": 30,
  "by_tool": {
    "search_code": { "calls": 50, "avg_savings": "91%", "total_saved": 45000 },
    "get_impact_radius": { "calls": 30, "avg_savings": "85%", "total_saved": 35000 },
    "get_context": { "calls": 70, "avg_savings": "88%", "total_saved": 45000 }
  },
  "by_day": [
    { "date": "2026-04-08", "calls": 12, "savings": 8500 },
    { "date": "2026-04-07", "calls": 25, "savings": 22000 }
  ]
}
```

**Parameters:**
- `since` (optional): Unix timestamp, default all time
- `tool` (optional): Filter by tool name
- `project_path` (optional): Filter by project

### 5.2 `reset_metrics`

Clears all metrics (for testing/user request):

```json
{
  "cleared": true,
  "rows_deleted": 150
}
```

---

## 6. CLI Command: `leankg metrics`

```bash
# Show all-time metrics
leankg metrics

# Show last 7 days
leankg metrics --since 7d

# Show specific tool
leankg metrics --tool search_code

# JSON output
leankg metrics --json

# Show this session only
leankg metrics --session

# Reset metrics
leankg metrics --reset

# Set retention (days)
leankg metrics --retention 60
```

**Output:**
```
=== LeanKG Context Metrics ===

Total Savings: 125,000 tokens across 150 calls
Average Savings: 87.5%
Retention: 30 days

By Tool:
  search_code:        50 calls,  avg 91% saved, 45,000 tokens saved
  get_impact_radius:  30 calls,  avg 85% saved, 35,000 tokens saved
  get_context:        70 calls,  avg 88% saved, 45,000 tokens saved

By Day:
  2026-04-08:  12 calls, 8,500 tokens saved
  2026-04-07:  25 calls, 22,000 tokens saved

Session: 5 calls, 4,200 tokens saved
```

---

## 7. Data Retention

- **Default:** 30 days
- **Configurable:** Via CLI `--retention N` or `leankg.yaml` config
- **Auto-cleanup:** On metric insert, delete records older than retention period
- **Manual cleanup:** `leankg metrics --cleanup` runs immediate cleanup

---

## 8. Implementation Order

### Phase 1: Core Infrastructure
1. Add `context_metrics` schema to CozoDB
2. Create `ContextMetric` model struct
3. Add `record_metric()` and `get_metrics_summary()` DB functions

### Phase 2: MCP Integration
4. Add `get_metrics_summary` and `reset_metrics` tool definitions
5. Instrument `execute_tool()` to call `record_metric()` after each tool

### Phase 3: CLI + Baseline
6. Add `leankg metrics` CLI command
7. Implement grep baseline estimation logic
8. Add retention configuration

### Phase 4: Polish
9. Update PRD with new requirements
10. Add documentation
11. Write tests

---

## 9. File Changes

| File | Change |
|------|--------|
| `src/db/schema.rs` | Add `context_metrics` relation with indexes |
| `src/db/models.rs` | Add `ContextMetric`, `MetricsSummary` structs |
| `src/db/mod.rs` | Add `record_metric()`, `get_metrics_summary()`, `cleanup_metrics()` |
| `src/mcp/tools.rs` | Add `get_metrics_summary`, `reset_metrics` tools |
| `src/mcp/handler.rs` | Instrument every tool to record metric |
| `src/cli/mod.rs` | Add `metrics` command with all options |
| `docs/requirement/prd-leankg.md` | Add FR-METRIC-01 to FR-METRIC-05 |

---

## 10. Acceptance Criteria

- [ ] Every MCP tool call records metrics to CozoDB
- [ ] Baseline comparison uses actual grep execution (not estimation)
- [ ] `get_metrics_summary` returns aggregated stats (all time default)
- [ ] CLI `leankg metrics` shows human-readable summary
- [ ] Metrics persist across server restarts
- [ ] Retention is 30 days by default, configurable
- [ ] `leankg metrics --reset` clears all metrics
- [ ] Old metrics auto-deleted based on retention setting