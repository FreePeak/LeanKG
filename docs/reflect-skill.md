# Reflect / Query-Outcome Loop (US-GF-16)

Default skill guidance for agents: **record what worked, load it next session.**

## Loop

```
answer (useful / dead_end / corrected)
  → report_query_outcome(question, nodes, outcome, note)   # append LESSONS.md
  → next session: session recall / overview enrichment loads LESSONS
```

## When to report

Call `report_query_outcome` after any LeanKG graph query that resolves a task:

| Outcome | Use when |
| ------- | -------- |
| `useful` | Query result directly answered the question |
| `dead_end` | Query returned nothing useful; note what to try instead |
| `corrected` | You fixed a wrong answer; note the correct resolution |

Guidance:

- Report **once per task**, not per tool call.
- `note` is optional but high-value: capture the resolution so the next agent
  skips the dead end.
- Useful results bias future ranking toward the same nodes.

## Where lessons land

- `.leankg/reflections/LESSONS.md` — append-only journal written by
  `report_query_outcome` (US-GF-09 / FR-GF-19).
- `.leankg/sessions/recall_index.jsonl` — recall index fed by LESSONS,
  diary, and knowledge writes (US-SM-01..04).
- Session start: `get_overview_context` with `recall=true` injects ranked
  lessons (US-SM-02, default OFF); `session_recall` recovers a single ref.

## Default skill guidance

Add this block to the agent's `using-leankg` skill / AGENTS.md:

```markdown
### Reflect

After a graph query resolves a task, call
`report_query_outcome(question, nodes, outcome: useful|dead_end|corrected, note)`
once. If `dead_end` or `corrected`, include what actually worked in `note`.
Do not report per-tool-call; one report per task.
```

## Verify

```bash
ls .leankg/reflections/LESSONS.md          # exists after first report
tail -5 .leankg/reflections/LESSONS.md     # latest entry
```
