# Context Metrics

Track token savings and usage statistics to understand how LeanKG improves your AI tool's context efficiency.

## Commands

```bash
# View metrics summary
leankg metrics

# View with JSON output
leankg metrics --json

# Filter by time period
leankg metrics --since 7d

# Filter by tool name
leankg metrics --tool search_code

# Seed test data for demo
leankg metrics --seed

# Reset all metrics
leankg metrics --reset

# Cleanup old metrics (retention: 30 days default)
leankg metrics --cleanup --retention 60
```

## Metrics Schema

| Field | Type | Description |
|-------|------|-------------|
| `tool_name` | String | LeanKG tool name (search_code, get_context, etc.) |
| `timestamp` | Int | Unix timestamp of the call |
| `input_tokens` | Int | Tokens in the query |
| `output_tokens` | Int | Tokens returned |
| `tokens_saved` | Int | Tokens saved vs baseline grep scan |
| `savings_percent` | Float | Percentage savings |
| `baseline_tokens` | Int | Tokens a grep scan would use |
| `execution_time_ms` | Int | Tool execution time |
| `success` | Bool | Whether the tool succeeded |

## Example Output

```
=== LeanKG Context Metrics ===

Total Savings: 64,660 tokens across 5 calls
Average Savings: 99.5%
Retention: 30 days

By Tool:
  search_code: 2 calls, avg 100% saved, 25,903 tokens saved
  get_impact_radius: 1 calls, avg 99% saved, 24,820 tokens saved
  get_context: 1 calls, avg 100% saved, 7,965 tokens saved
  find_function: 1 calls, avg 100% saved, 5,972 tokens saved
```
