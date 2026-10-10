# Telemetry and the dashboard

LeanKG can record how well it serves your coding agents and show the result in a local web dashboard (`leankg dashboard`). This page covers what is recorded, where it is stored, how to turn it on and off, and how to delete it. The design and work items are in [`plan-dashboard.md`](plan-dashboard.md) (DS-01..DS-27).

## Off by default

Nothing is recorded until you consent. With capture off:
- no file is created,
- no queue runs inside the server,
- no agent transcript is opened.

Consent is stored in `$LEANKG_HOME/telemetry.yaml`, and `$LEANKG_HOME` defaults to `~/.leankg`. You can give it in two ways:
- run `leankg telemetry enable`, which asks y/N on a terminal
- use the consent screen that `leankg dashboard` opens on first run

The environment variable `LEANKG_TELEMETRY=off|metadata` can lower the level for one process. It can never raise the level above what you granted.

## Levels

| Level | What each call records |
|---|---|
| `off` | nothing (default) |
| `metadata` | call metadata, listed below; no argument values and no response bodies |
| `bodies` | everything in `metadata`, plus the request arguments and response body, after redaction and capped at `max_body_bytes` (16 KiB) |

`metadata` records these fields for each call:
- tool, action and command
- outcome label and reason
- error code
- retrieval rung, confidence and freshness
- hit count and the files the response named
- latency
- tokens before and after the budget, the baseline estimate and tokens saved
- client name and version, and client session id
- argument keys and a SHA-256 hash of the arguments

Redaction runs before anything is queued. It masks:
- bearer tokens and JWTs
- `sk-`, `ghp_`, `github_pat_`, `AKIA` and `xox*` keys
- `password=`, `token=` and `secret=` values
- PEM private keys

It also rewrites paths under your home directory to `~`.

## Transcript reading (separate grant)

To work out whether LeanKG's answer was actually used, the dashboard can read the calling agent's own session transcript. It finds:
- the prompt that led to the call
- the turns around it
- whether the agent then read the files LeanKG returned
- whether it fell back to grep instead

Transcript reading is a separate opt-in for each client, through `leankg telemetry enable --sessions claude-code,opencode` or the consent screen. It is read-only, runs in the dashboard process rather than during the tool call, and stores only a redacted, capped window around each LeanKG call.

| Client | Transcript location read |
|---|---|
| Claude Code | `~/.claude/projects/` (or `$CLAUDE_CONFIG_DIR`) |
| xdev | `~/.xdev/agent/sessions/` (or `$XDEV_AGENT_DIR`) |
| omp | `~/.omp/agent/sessions/` |
| pi | `~/.pi/agent/sessions/` (or `$PI_CODING_AGENT_DIR`) |
| opencode | `~/.local/share/opencode/opencode.db` (read-only) |
| Grok CLI | `~/.grok/sessions/` (experimental) |
| Codex CLI | `~/.codex/sessions/` (experimental) |
| Gemini CLI | `~/.gemini/tmp/` (experimental) |

## Where it is stored

Everything lives in one SQLite file, `$LEANKG_HOME/telemetry.db`.
- It is per user, not per project, because one agent session can span projects.
- Every LeanKG server process on the machine writes to it: the stdio servers each agent starts, and `serve`.
- Writes are batched in the background. A full queue drops events rather than slowing a tool call, and the dashboard shows the drop count.
- Nothing is sent off the machine. The dashboard binds to `127.0.0.1`, and remote access needs both `--allow-remote` and `--token`.

## Commands

```bash
leankg telemetry status [--json]                 # level, grants, paths, ledger size
leankg telemetry enable [--bodies] [--sessions claude-code,pi|all] [--yes]
leankg telemetry disable                         # capture off, transcript reading off
leankg telemetry purge [--before 30d | --all]    # delete recorded data
leankg telemetry link [--older-than 2m]          # link calls to transcripts once
leankg telemetry import-ab <dir>                 # load controlled A/B benchmark results

leankg dashboard                                 # web UI on 127.0.0.1:9701, opens the browser
leankg dashboard --no-open --addr 127.0.0.1:9800
leankg dashboard --format text|json              # the previous text/JSON usage tables
```

Records older than `retention_days` (default 30) are swept daily.

## How to read the numbers

**Measured** means taken straight from the call, or from the controlled A/B benchmark:
- outcomes, latency, tokens delivered
- the controlled A/B results

**Estimate** means a counterfactual. The badge shows its method:
- tokens saved: whole-file tokens of the files a hit named, minus what LeanKG returned (`file_read`), or a lines-of-code rule when a file is unreadable (`sloc`)
- the observational with/without comparison of task segments, which is confounded and not causal

**Proxy** means inferred from the transcript:
- used-hit precision
- fallback to grep or read after a LeanKG answer
- re-query
- memory reuse

## Client identity

The dashboard groups calls into agent sessions.
- **stdio:** the server reads the agent's session id from its environment (`CLAUDE_CODE_SESSION_ID`, `PI_SESSION_ID`, `XDEV_SESSION_ID`, `OPENCODE_SESSION_ID`). That lets a call be matched exactly to its transcript.
- **HTTP:** clients can send the `X-LeanKG-Client`, `X-LeanKG-Session` and `X-LeanKG-Cwd` headers. Without them, the server works out the client from `User-Agent` and matches calls to transcripts by working directory, time and arguments, and the dashboard labels those matches heuristic.
