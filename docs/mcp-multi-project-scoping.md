# Why LeanKG "does not work" in xdev — root causes and the fix

**Symptom (live, 2026-10-20):** an xdev session calls `leankg_status` and gets

```text
mcp leankg/status: calling "tools/call": LEANKG_ERROR_MISSING_PARAM: this
server serves multiple projects (LEANKG_PROJECT_DIRS), so a tool call must
name one. Fix: add "project": "<repo dir or name>" to the tool arguments, …
```

**One-line cause:** the Go engine never implemented the project-resolution
mechanism the product depends on, while the same wave made every client
config emit a **projectless** URL. A projectless URL against a
multi-project server has no way to resolve the project, so the server
refuses — correctly — and the client has nothing to send.

---

## 1. The scene

| Fact | Evidence |
|---|---|
| The self-host serves **286 projects** from one process | `LEANKG_PROJECT_DIRS` is built by `leankg-scan-repos $HOME/work` (maxdepth 4, every git repo) and exported by `~/.local/bin/leankg-serve-wrapper` |
| The MCP surface refuses a project-less call when several projects are registered | `internal/mcp/server.go:109` (`engineFor`) |
| The refusal is deliberate, and the reason is measured | `internal/mcp/server.go:119-124` — a term absent from the served project still returned 10 hits from it |
| A `project` argument exists and works | tool schema property `project`; `internal/projects/projects.go:169` (`resolve`) accepts a directory path, a project name, or a path inside one |
| A `?project=` URL parameter works | `cmd/leankg/multiproject.go:41` (`routeByProject`) |
| The failing client is xdev | `~/.xdev/agent/mcp.yml` registers `leankg` at `http://localhost:9699/mcp` — the projectless URL |
| xdev's MCP client cannot add a project | `xdev/internal/mcpclient/mcp.go:30` (`ServerConfig`) has no field that can carry one; `mcp.go:324` (`remoteTool.Execute`) forwards the agent's arguments verbatim |
| The Go engine has **no** project auto-resolution | `grep -rn "roots/list\|ListRoots" internal/ cmd/` → no implementation; only comments (`cmd/leankg/connect.go:104`) that assume it exists |

---

## 2. Root causes

### RC1 — the Go port dropped FR-ZCP-01 clause 2, and the client configs were changed to depend on it

FR-ZCP-01 ("contextual project resolution", P0) is the product's answer to
"which project is this call about?". It has three clauses; the tracker
records clause 2 (server-initiated `roots/list` over HTTP) as **DONE
2026-09-04, commit 87e18287 — in the Rust line** (`src/mcp/server.rs`).
The Go rewrite did not carry it over: the Go engine has no roots/list
probing at all, and the Rust commit is not in this repository's history.

That clause is what the *client writers* depend on. The v4.15-bare-mcp-url
wave (2026-10-11) made every writer strip `?project=` and emit a bare URL,
and said so:

> "Per-request project resolution (FR-ZCP-01: stdio cwd, HTTP roots/list) is
> what the suffix was standing in for, and an absolute path baked into a
> config is stale the moment the config leaves the machine that wrote it."
> — `docs/prd.md:578`

So the configs were changed to a contract the Go engine does not implement.
On a **single-project** server nothing shows: the bare URL answers. On the
**286-project self-host** it shows immediately — the server refuses, and the
only way out is a project the config deliberately no longer carries.

**Fix (interim):** the explicit argument is the only resolution path the Go
engine actually has, so it is now part of the contract the client reads. On
a multi-project server the tool schemas **require** the `project` argument
(`internal/mcp/server.go:287`, `WithProjectRequired` at `:103`), and the
property's description says what to pass.

The friendly server error is **kept**: LeanKG registers its tools with the
SDK's low-level `ToolHandler`, which performs no input validation (go-sdk
`mcp/tool.go`, the `ToolHandler` doc), so the schema is a declaration to the
client and the server still answers a project-less call with the actionable
message.

### RC2 — stdio inherited the multi-project fail-closed rule (a real contract violation)

`leankg serve --stdio` is **one client over one repository**: the client
spawned the process inside the repo it is working in. FR-ZCP-01 clause 1
("stdio cwd") says the server resolves the project from the process cwd, and
the engine does — the cwd project is what it builds.

It was then defeated at the routing layer. Measured live before the fix:

```text
$ leankg serve --stdio            # with LEANKG_PROJECT_DIRS set
tools/call status  →  LEANKG_ERROR_MISSING_PARAM: this server serves
                     multiple projects … so a tool call must name one
```

Every host that exports `LEANKG_PROJECT_DIRS` — every host running the
self-host — had a broken stdio surface, and the contract it violated is
LeanKG's own (`internal/mcp/server.go:33-37`: "a stdio spawn is one agent
session over one project and must keep working").

**Fix:** stdio is now a scoped surface (`cmd/leankg/main.go:262`,
`newScopedMCPServer`). The process's own project answers a project-less
call; an explicit `project` argument still selects another one.

### RC3 — the unknown-project error dumped 286 absolute paths

`internal/projects/projects.go:193` named every registered project in the
error. That error travels into an agent's context on every retry, so the
wall of paths was paid for in tokens each time.

**Fix:** the list is bounded to 10 and the remainder is counted
(`internal/projects/projects.go:202`).

### RC4 — xdev's MCP client has no way to scope a shared server

xdev is a general coding agent used across the whole portfolio, and its MCP
config is one global file (`~/.xdev/agent/mcp.yml`) for every session. The
`project` selector is a property of the *caller's* context — the repository
the session is in — and xdev's client cannot express it:

- `ServerConfig` (`xdev/internal/mcpclient/mcp.go:30`) has no field that
  carries a per-session project;
- `remoteTool.Execute` (`mcp.go:324`) forwards the agent's arguments as-is;
- `mcp.yml` supports `${VAR}` in `url:` but **not** `!command`, so a
  session-scoped `?project=` cannot be written in the config either
  (`xdev/internal/mcpclient/config.go:167-173` — `resolveValues` runs
  `!command` for `env` and `headers` only).

The same process already knows the repository and already scopes its own
LeanKG REST client with it: `codeGraph.project` +
`codeGraphExpectDir` (`xdev/cmd/xdev/print.go:1929-1943`,
`xdev/internal/config/settings.go:335-350`).

**Status: NOT fixed here** — it is a change in the xdev repository. RC1 makes
the model supply the argument, which unblocks the reported failure; RC4 is
what makes it not depend on the model.

### RC5 — a project selector that matched NOTHING answered from the cwd's project

Found while reviewing RC1's fix on the live self-host, 2026-10-20.

The serve process's cwd is the leankg repository, and on the self-host that
repository is a **registered** project. The resolver canonicalized a bare
name against the cwd and then walked up the filesystem, so a name that
matched nothing climbed to the cwd and resolved to that project:

```text
$ curl -s -X POST 'http://127.0.0.1:9699/mcp' -d '{"method":"tools/call",
    "params":{"name":"status","arguments":{"project":"totally-not-a-real-project-xyz"}}}'
  → the leankg project's status: 11,246 elements, no error

$ curl -o /dev/null -w '%{http_code}' -X POST \
    'http://127.0.0.1:9699/mcp?project=totally-not-a-real-project-xyz' …
  → 200
```

An **absolute** path outside every project was refused correctly
(`unknown project "/tmp/definitely-not-a-project"`), so the hole was in the
NAME branch only — the one the model is told to use.

This is the silent wrong-project fallback FR-ZCP-02 forbids, and it is what
the fail-closed rule exists to prevent: a confident answer built from the
WRONG graph, handed to a model that asked about a repository that is not
there. The schema change (RC1) made the model name a project on every
call, which is exactly when this hole starts to matter.

**Why the test was green:** the rule had a test, and it passed — because the
test process's cwd was not a registered project, so the walk climbed out of
the repository and found nothing. The live configuration is the opposite:
the cwd IS a project.

**Fix:** the ancestor walk is for PATHS only ("this place"); a bare NAME
matches a project's basename or errors. The order changed for paths too: a
path inside a registered project resolves to THAT project even when its
basename is also another project's name. Both errors now say how to pick a
project, and the ambiguous one names its candidates.

---

## 3. What changed in LeanKG

| Change | Where | Why |
|---|---|---|
| `project` is REQUIRED in the tool schemas on a multi-project server | `internal/mcp/server.go:103`, `:274-297`, `cmd/leankg/main.go:511` | RC1 — the rule reaches the model before the call |
| stdio and `?project=` are scoped surfaces | `cmd/leankg/main.go:262-283`, `:492`, `:518` | RC2 — one session, one project |
| the unknown-project error is bounded | `internal/projects/projects.go:196-220` | RC3 — no context wall |
| the agent protocol states what THIS surface requires | `internal/mcp/server.go` — `instructions()`, `projectRuleRequired` / `projectRuleFixed` | RC1 — the sentence the model reads must not be a condition it cannot evaluate |
| a selector that matches nothing errors instead of climbing to the cwd's project | `internal/projects/projects.go` — `resolve()`, `isPathSelector` | RC5 — no wrong-graph answers |
| both selector errors say how to pick a project, and name the candidates | `internal/projects/projects.go` — `unknownProjectError`, `ambiguousProjectError` | RC5 — a retry needs a next step |

Behaviour after the fix, measured on the 286-project self-host:

| Surface | `tools/list` `required` | `status` with no arguments |
|---|---|---|
| stdio | `[]` | **answers** with the session's own project |
| HTTP `?project=leankg` | `[]` | **answers** with that project |
| HTTP (unscoped, 286 projects) | `["project"]` | refused, with the actionable error |

| Selector | Before | After |
|---|---|---|
| `{"project":"<a name that matches nothing>"}` | answered from the cwd's project | refused, with the actionable error |
| `?project=<a name that matches nothing>` | 200, the cwd's project | 404, with the actionable error |
| `{"project":"/abs/path/outside/every/project"}` | refused | refused (unchanged) |
| `{"project":"/abs/path/inside/a/project"}` | that project | that project (unchanged) |
| `{"project":"<a name>"}` | that project | that project (unchanged) |

---

## 4. What this means for FR-ZCP-01

FR-ZCP-01 is the right idea — the working directory is the identity, and a
path baked into a config is stale the moment the config moves. But the
mechanism it names for the HTTP case is **MCP `roots/list`, which is
deprecated**:

> "Deprecated: the roots feature is deprecated as of protocol version
> 2026-07-28 (SEP-2577). It remains functional during the deprecation
> window (at least twelve months)."
> — go-sdk `mcp/server.go:1563` and `mcp/client.go:655`

So FR-ZCP-01 clause 2 cannot simply be ported from the Rust line: it would
land on a feature with a 12-month window, and it would need **every** MCP
client to implement it, or the same failure returns for the clients that
did not.

The evidence from three clients on this machine is worth weighing:

| Client | How it scopes the shared server | Cost |
|---|---|---|
| opencode | `?project=/Users/linh.doan/work/harvey/freepeak/leankg` in its config | the path is baked into a global config — exactly what the v4.15-bare-mcp-url wave removed from the writers as "stale the moment the config leaves the machine" |
| Claude Code / DSH | the `be-kg` entry is scoped to `/Users/linh.doan/work/be` | scoped to one workspace; the local graph needs another entry |
| xdev | **nothing** | the reported failure |

**The question FR-ZCP-01 should now answer is not "which mechanism resolves
the project" but "who supplies it".** The server cannot know the caller's
repository (MCP has no cwd on the wire, and roots is going away). The
client can: it knows the directory it is working in, and it already uses
that knowledge for its own REST graph client. So the durable shape is:

1. the client sends the project (tool argument, or a per-server setting) —
   the contract this change makes legible, and the only one the Go engine
   implements today;
2. the server keeps failing closed when it is not told, because a wrong
   graph is worse than no answer;
3. the auto-resolution path (if it is wanted) waits for a non-deprecated
   mechanism.

**Recommendation:** land the interim contract (done), port FR-ZCP-01 clause 2
to the Go engine **only if** a non-deprecated mechanism appears, and
re-point the client writers at the explicit argument rather than at a bare
URL in the meantime.

---

## 5. Two operational defects found on the way

### OD1 — two servers were answering on 9699

```text
$ lsof -nP -iTCP:9699 -sTCP:LISTEN
leankg 65353 … IPv4 … TCP 127.0.0.1:9699 (LISTEN)   ← launchd
leankg 80183 … IPv6 … TCP *:9699 (LISTEN)            ← a second server
```

The second was started by an **xdev session's MCP autoStart**: the recipe
in `~/.xdev/agent/mcp.yml` launches `leankg serve --http :9699 …` — a
**wildcard** bind, while the launchd-supervised server holds
`127.0.0.1:9699`. Both bind successfully (different address families), so
the autoStart always succeeds in creating a duplicate, and a client that
resolves `localhost` to `::1` lands on the duplicate.

The recipe sets no `pidFile`, so nothing reaps it. `lsof` printing two
lines is two **sockets**, not two servers — read the pid, keep launchd's.

Killed during this work; the supervised server (pid 65353) now owns both
9699 and 9700.

**Recommendation (xdev repo):** the autoStart recipe should not exist for a
server that launchd already supervises, or it should bind `127.0.0.1:9699`
(the same address as the supervisor) so a second bind fails loudly instead
of silently creating a second server.

### OD2 — `freepeak/.mcp.json` has the same defect as xdev

```json
{ "mcpServers": { "leankg": { "type": "http", "url": "http://localhost:9699/mcp" } } }
```

Any client that reads that file hits RC1 exactly as xdev did.

---

## 6. What is NOT the cause

- **Not the embedder.** The store is healthy: 11,246 elements, 100% gemma
  coverage on the leankg project.
- **Not the server being down.** `/health` returns 200 on 9699 and 9700.
- **Not a duplicate server** (that was a real finding, but a second one —
  OD1 — and it does not produce this error: the duplicate is configured
  identically).
- **Not the fail-closed rule.** That rule is the reason the failure is
  *safe*. It was kept; what changed is that the model is now told. (It did
  turn out to have a hole of its own — RC5 — but in the SELECTOR, not in the
  rule: a selector that matched nothing was answered from the cwd's
  project. That is fixed too.)

---

## 7. Verification

```bash
go test ./internal/mcp/ ./internal/projects/ -count=1     # schema + resolver
go test ./cmd/leankg/ -run MCPServer -count=1            # stdio vs unscoped, over the wire
go vet ./internal/... ./cmd/...
```

Live, against the 286-project self-host (a side instance on port 9799, so
the supervised server was never touched):

- `tools/list` on the unscoped surface → `required: ["project"]` for all
  three tools;
- `tools/list` on `?project=leankg` and on stdio → `required: []`;
- `status` with no arguments on stdio → the project's own status;
- `status` with no arguments on the unscoped surface → the actionable
  refusal;
- `status` with `{"project":"leankg"}` → the leankg project's status.

---

*Evidence gathered on 2026-10-20 against the live self-host (pid 65353,
286 registered projects).*
