# Laya shared service — general use

**Service:** container `laya-sidecar-laya-sidecar-1` via `docker compose` (since 2026-09-23; previously launchd `com.freepeak.laya-sidecar`, now disabled)
**URL:** `http://127.0.0.1:8091` (published loopback-only)
**Assets:** `~/.local/share/laya-sidecar/` — `compose.yaml`, `Dockerfile`, `laya-sidecar.py`, `requirements.txt`
**Image:** `laya-sidecar:0.2` (python:3.12-slim + laya 0.3.4 / torch 2.14.0)
**Weights:** mounted from the host HF cache `convaiinnovations/laya` (`~/.cache/huggingface`, ~4.5 GB, all three checkpoints)
**Memory:** limit 4 GB; **measured steady ~2.9 GB** with the English checkpoint resident
**Logs:** `docker logs laya-sidecar-laya-sidecar-1`

Any app on this machine can use it — LeanKG is just one client.
LeanKG `dsh-usage` is optional (`-tags dshusage`) and Laya scoring inside it is default-off
until `LAYA_URL` / `LEANKG_JUDGE_SIDECAR_URL` / `--laya-url` is set.
The contract mirrors what onegw forwards to TypeSafe Jev, so a client
switches backends by changing the base URL, never the code.

## Memory — read this before changing anything

Memory is dominated by **which checkpoints are resident**, not request volume:

| resident set | measured phys_footprint |
|---|---|
| `english` only (current, `LAYA_PRELOAD=english`) | 2674 MB, peak 3228 MB |
| all three (`preload=True`, the old host default) | 5520 MB, peak 6063 MB |

The container therefore preloads English only. Add `multilingual` to
`LAYA_PRELOAD` only when non-English traffic is real — it costs ~1.3 GB more.

**The colima VM must be ≥ 4 GB** (currently 6 GB: `colima start --memory 6`).
At its old 2.05 GB the container was OOM-killed 10 consecutive times, because
English alone needs 2.7 GB against ~1.39 GB free. Do not shrink it back; a
container cannot honor a limit larger than the VM.

## Endpoints

| Method | Path | Purpose |
|---|---|---|
| GET | `/health` | `{"ok": true, "model": "laya", "loaded": ["english"]}` |
| POST | `/v1/systemone` | Score one batch of typed questions over a state string |

## Request

```json
{
  "state": "free text: whatever is being judged",
  "model": "laya",
  "questions": {
    "needs_fix": {
      "type": "choice",
      "instructions": "Does this step show a defect to fix?",
      "criteria": {"critical": "…", "high": "…", "no": "…"}
    },
    "urgency": {
      "type": "score",
      "instructions": "How urgent?",
      "criteria": ["low", "medium", "high", "critical"]
    },
    "needs_review": {
      "type": "noul",
      "instructions": "Does this step need a human review?"
    }
  }
}
```

Question types: `choice` (pick one criterion key), `score` (ordinal ladder —
**must include `criteria`**, else the sidecar defaults to
`["low","medium","high","critical"]`), `noul` (P(yes)).

## Response

```json
{
  "model": "english",
  "answers": {
    "needs_fix": {
      "type": "choice", "choice": "critical",
      "probabilities": {"critical": 0.41, "high": 0.29, "no": 0.28},
      "confidence": 0.01, "action": {"act_probability": 1.0}
    }
  },
  "usage": {"input_tokens": 0, "output_tokens": 0, "local_ms": 10752.4}
}
```

## Probe

```bash
curl -s http://127.0.0.1:8091/health
curl -s -X POST http://127.0.0.1:8091/v1/systemone \
  -H 'content-type: application/json' \
  -d '{"state":"…","model":"laya","questions":{"q":{"type":"score","instructions":"…","criteria":["low","medium","high","critical"]}}}'
```

## Current clients

| App | How it uses Laya |
|---|---|
| LeanKG `dsh-usage` dashboard | `?laya=1` adds `laya_score` / `laya_critical` on steps; rules still win |
| dsh-feature-loop `OnegwJudge` | Same wire, different base URL |
| agentloop guardrail screen | Same wire |

## Ops

```bash
docker ps | grep laya                                  # running?
docker stats --no-stream laya-sidecar-laya-sidecar-1   # memory vs the 4 GB limit
docker logs --tail 50 laya-sidecar-laya-sidecar-1      # one line per request + load status
cd ~/.local/share/laya-sidecar && docker compose up -d     # start / apply config
cd ~/.local/share/laya-sidecar && docker compose build     # rebuild after edits
```

The host launchd unit is **disabled** (`~/Library/LaunchAgents/com.freepeak.laya-sidecar.plist.disabled-by-container-20260923`).
Do not re-enable it while the container runs: both bind `:8091`.
To go back to the host service, `docker compose down` first, then restore the plist.

**Known limits:** first start loads weights (~30 s warm page-in, longer cold);
`score` without `criteria` gets the default 0–3 ladder; confidence is the
act/don't-act axis — gate on it, not the answer alone; a wide `choice` answers
with low confidence by construction, so decompose into `noul`s.
