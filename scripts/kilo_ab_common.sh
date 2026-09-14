#!/bin/bash
# scripts/kilo_ab_common.sh — shared FR-ZCP-08 (issue #276) hardening for
# the kilo A/B runners (run_kilo_ab_final.sh, run_kilo_ab_test.sh).
#
# Guarantees (the Go side of the harness, benchmark/ab/harness.go, is
# the source of truth; this file only resolves pins and pipes rows):
#   * PINNED SHAs/PROMPTS: every trial row carries the 40-hex corpus
#     commit, 40-hex commit SHAs for each measured tool (leankg build
#     source + kilo CLI), and the SHA-256 of the arm's prompt template.
#     abrun record REFUSES an unpinned row — the run aborts, nothing is
#     recorded.
#   * >=3 TRIALS/ARM: AB_TRIALS_PER_ARM (default 3) drives the loops; the
#     scorer independently fails any arm below the floor.
#   * JUDGE-BLIND + CHECKLIST: produced by `ab_score` via abrun
#     (shuffled blind groups, computed zg pitfalls checklist) into the
#     results JSON report.
#
# Usage (from a runner, after WORKTREE_DIR is set):
#   source "<repo>/scripts/kilo_ab_common.sh"
#   ab_resolve_pins <prompt_version> <baseline-template> <leankg-template> || exit 1
#   ab_init_trials <file.jsonl>
#   ... per trial: ab_record_trial <file.jsonl> <arm> <task> <n> <tokens> <question> <answer-file>
#   ab_score <file.jsonl> <report.json>
#
# kilo tool pin: resolved from the kilo binary's enclosing git checkout.
# Package installs (no git) must pin explicitly:  AB_KILO_SHA=<40-hex> ./run_kilo_ab_*.sh
# Unresolvable => the run is refused (never recorded unpinned).

AB_SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
AB_GO_DIR="$AB_SCRIPT_DIR"
AB_TRIALS_PER_ARM=${AB_TRIALS_PER_ARM:-3}
if [ "$AB_TRIALS_PER_ARM" -lt 3 ]; then
    echo "ab_common: AB_TRIALS_PER_ARM=$AB_TRIALS_PER_ARM below the FR-ZCP-08 floor; using 3" >&2
    AB_TRIALS_PER_ARM=3
fi

ab_sha256() { printf '%s' "$1" | shasum -a 256 | cut -d' ' -f1; }

# ab_is40 <s> — strict 40-hex test.
ab_is40() {
    case "$1" in
        *[!0-9a-f]*) return 1 ;;
        *) [ "${#1}" -eq 40 ] ;;
    esac
}

ab_kilo_git_sha() {
    # Resolve the kilo CLI's exact git commit ONLY when its install tree
    # is a kilo source checkout. Package-manager installs sit inside
    # unrelated git repos (this host: /opt/homebrew is a repo — pinning
    # kilo to its HEAD would be a fake pin), so the nearest .git is
    # accepted only if its manifest names kilo; otherwise the caller
    # must supply AB_KILO_SHA explicitly or the run is refused.
    local bin real dir name
    bin="$(command -v kilo 2>/dev/null)" || return 0
    real="$(readlink -f "$bin" 2>/dev/null || echo "$bin")"
    dir="$(dirname "$real")"
    while [ "$dir" != "/" ]; do
        if [ -d "$dir/.git" ] || [ -f "$dir/.git" ]; then
            name="$(jq -r '.name // empty' "$dir/package.json" 2>/dev/null)"
            if [ -z "$name" ] && [ -f "$dir/Cargo.toml" ]; then
                name="$(sed -n 's/^name *= *"\([^"]*\)".*/\1/p' "$dir/Cargo.toml" | head -1)"
            fi
            if [ -z "$name" ] && [ -f "$dir/package.json" ]; then
                name="$(sed -n 's/.*"name" *: *"\([^"]*\)".*/\1/p' "$dir/package.json" | head -1)"
            fi
            case "$name" in
                kilo|kilo-*|*@kilo/*) git -C "$dir" rev-parse HEAD 2>/dev/null ;;
            esac
            return 0
        fi
        dir="$(dirname "$dir")"
    done
    return 0
}

# ab_resolve_pins <prompt_version> <baseline-template> <leankg-template>
ab_resolve_pins() {
    AB_PROMPT_VERSION="$1"
    AB_PROMPT_SHA_BASELINE="$(ab_sha256 "$2")"
    AB_PROMPT_SHA_LEANKG="$(ab_sha256 "$3")"
    local corpus_dir="${WORKTREE_DIR:-$AB_SCRIPT_DIR}"
    AB_CORPUS_SHA="$(git -C "$corpus_dir" rev-parse HEAD 2>/dev/null || true)"
    # The leankg tool under test is built from the same checkout that is
    # indexed (cargo run / go build in WORKTREE_DIR); AB_LEANKG_SHA
    # overrides for a separately built binary.
    AB_LEANKG_SHA="${AB_LEANKG_SHA:-$AB_CORPUS_SHA}"
    AB_KILO_SHA="${AB_KILO_SHA:-$(ab_kilo_git_sha)}"
    if ! ab_is40 "$AB_CORPUS_SHA" || ! ab_is40 "$AB_LEANKG_SHA" || ! ab_is40 "$AB_KILO_SHA"; then
        echo "ABORT (FR-ZCP-08): exact commit pins unresolved (corpus=$AB_CORPUS_SHA leankg=$AB_LEANKG_SHA kilo='${AB_KILO_SHA}')." >&2
        echo "  Pin the kilo CLI explicitly: AB_KILO_SHA=<40-hex git commit> (its install is not a git checkout)." >&2
        echo "  The run is REFUSED: an unpinned A/B measurement is not recordable." >&2
        return 1
    fi
    echo "[Pins] corpus=$AB_CORPUS_SHA leankg=$AB_LEANKG_SHA kilo=$AB_KILO_SHA prompt=$AB_PROMPT_VERSION (baseline $(echo "$AB_PROMPT_SHA_BASELINE" | cut -c1-12)…, leankg $(echo "$AB_PROMPT_SHA_LEANKG" | cut -c1-12)…)"
}

ab_prompt_sha() {
    # $1 = arm
    if [ "$1" = "leankg" ]; then printf '%s' "$AB_PROMPT_SHA_LEANKG"; else printf '%s' "$AB_PROMPT_SHA_BASELINE"; fi
}

ab_toolcalls() {
    # Best-effort tool-use count from the kilo JSON envelope (parity with
    # the cross_tool harness counting mcp__ tool events in run events).
    # Baseline must report 0 — any positive count there trips the
    # no_leakage gate at scoring time, which is exactly the point.
    local arm="$1" output="$2"
    if [ "$arm" = "baseline" ]; then echo 0; return; fi
    printf '%s' "$output" | grep -o '"tool_use"\|"tool-call"\|"mcp__leankg' | wc -l | tr -d ' '
}

# ab_record_trial <jsonl> <arm> <task> <n> <tokens> <question> <answer-file> <output>
ab_record_trial() {
    local jsonl="$1" arm="$2" task="$3" n="$4" tokens="$5" question="$6" answer_file="$7" output="$8"
    AB_ARM="$arm" AB_TASK="$task" AB_N="$n" AB_TOKENS="$tokens" AB_QUESTION="$question" \
    AB_ANSWER_FILE="$answer_file" AB_TOOLCALLS="$(ab_toolcalls "$arm" "$output")" \
    AB_MCP="$([ "$arm" = leankg ] && echo true || echo false)" \
    AB_CORPUS_SHA="$AB_CORPUS_SHA" AB_LEANKG_SHA="$AB_LEANKG_SHA" AB_KILO_SHA="$AB_KILO_SHA" \
    AB_PROMPT_VERSION="$AB_PROMPT_VERSION" AB_PROMPT_SHA="$(ab_prompt_sha "$arm")" \
    python3 - <<'PY' | (cd "$AB_GO_DIR" && go run ./benchmark/ab/abrun record -out "$jsonl")
import json, os, sys
e = os.environ
answer = ""
af = e.get("AB_ANSWER_FILE", "")
if af and os.path.exists(af):
    answer = open(af, encoding="utf-8", errors="replace").read()
print(json.dumps({
    "arm": e["AB_ARM"], "task": e["AB_TASK"], "trial": int(e["AB_N"]),
    "tokens": int(e["AB_TOKENS"]), "success": int(e["AB_TOKENS"]) > 0,
    "mcp_tool_count": int(e["AB_TOOLCALLS"] or 0),
    "mcp_attached": e["AB_MCP"] == "true",
    "question": e["AB_QUESTION"], "answer": answer,
    "pins": {
        "corpus_sha": e["AB_CORPUS_SHA"],
        "tool_shas": {"leankg": e["AB_LEANKG_SHA"], "kilo": e["AB_KILO_SHA"]},
        "prompt_version": e["AB_PROMPT_VERSION"],
        "prompt_sha256": e["AB_PROMPT_SHA"],
    },
}))
PY
}

ab_init_trials() { : > "$1"; }

ab_score() {
    (cd "$AB_GO_DIR" && go run ./benchmark/ab/abrun score \
        -in "$1" -out "$2" ${AB_JUDGE:+-judge "$AB_JUDGE"} ${AB_SEED:+-seed "$AB_SEED"})
}

ab_median() {
    # shellcheck disable=SC2001
    printf '%s\n' "$@" | sort -n | awk '{v[NR]=$1} END{if(NR==0){print 0} else if(NR%2){print v[(NR+1)/2]} else {printf "%g\n",(v[NR/2]+v[NR/2+1])/2}}'
}
