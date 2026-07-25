# Cross-Tool Agent A/B Benchmark
#
# Reproduces the codegraph 7-repo suite (re-validated 2026-07-21, Opus 4.8)
# against LeanKG's MCP server. Each repo, each arm, each N runs.
#
# Usage (single repo):
#   make setup                          # clone the 7 repos at pinned refs
#   make index   REPO=gin               # build LeanKG index for one repo
#   make with    REPO=gin N=4
#   make without REPO=gin N=4
#   make report                        # aggregate JSONL -> cross_tool-YYYY-MM-DD.md
#   make all     REPO=gin N=4          # = with + without + report
#
# Usage (parallel via subagents — recommended for the full suite):
#   The orchestrator (main chat or another tool) dispatches one Task tool call
#   per repo with `bash benchmarks/cross_tool/run_repo.sh <slug> 4` and waits
#   for all 7 to finish in parallel. Then `make report`.
#
# Pin `claude` CLI: 2.1.201 was verified working on 2026-07-23.

PYTHON ?= python3

CLAUDE_BIN ?= $(shell command -v claude)
LEANKG_BIN ?= $(shell command -v leankg || echo "$(CURDIR)/../../target/release/leankg")

REPOS_DIR := $(CURDIR)/repos
RESULTS_DIR := $(CURDIR)/results
RUNS_DIR := $(RESULTS_DIR)/runs
SCRATCH_DIR := $(RESULTS_DIR)/scratch

N ?= 4
# MODEL is optional. Empty = use claude -p default.
MODEL ?=
REPO ?=

DATE := $(shell date +%Y-%m-%d)

.PHONY: help setup index with without report clean all full check-leankg check-claude

help:
	@echo "Targets:"
	@echo "  setup                          clone the 7 benchmark repos (depth 1, pinned)"
	@echo "  index  REPO=<slug>             build LeanKG index for one repo"
	@echo "  with    REPO=<slug> N=<n> MODEL=<m>  run WITH-arm (LeanKG MCP) N times"
	@echo "  without REPO=<slug> N=<n> MODEL=<m>  run WITHOUT-arm (empty MCP) N times"
	@echo "  report                        aggregate JSONL -> Markdown + JSON"
	@echo "  all REPO=<slug> N=<n> MODEL=<m>  run a single repo both arms + report"
	@echo "  full MODEL=<m> N=<n>          run full 7-repo x 2 arm x N suite SERIALLY (slow)"
	@echo "  clean                         remove scratch + cloned repos + results"
	@echo ""
	@echo "Recommended: dispatch one subagent per repo via the Task tool and run"
	@echo "  bash benchmarks/cross_tool/run_repo.sh <slug> 4"
	@echo "in each, then 'make report' to aggregate."

check-claude:
	@test -n "$(CLAUDE_BIN)" || (echo "ERROR: 'claude' CLI not on PATH" && exit 2)
	@echo "claude:    $(CLAUDE_BIN)"

check-leankg:
	@test -x "$(LEANKG_BIN)" || (echo "ERROR: leankg binary not found (build with: cargo build --release)" && exit 2)
	@echo "leankg:    $(LEANKG_BIN)"

setup: check-claude
	@echo "Cloning 7 benchmark repos (depth 1) into $(REPOS_DIR)..."
	@mkdir -p $(REPOS_DIR)
	@$(PYTHON) $(CURDIR)/clone_repos.py --repos $(CURDIR)/repos.yaml --target $(REPOS_DIR)

index: check-leankg
	@test -n "$(REPO)" || (echo "ERROR: REPO=<slug> required" && exit 2)
	@echo "Indexing $(REPO) with LeanKG..."
	@rm -rf $(REPOS_DIR)/$(REPO)/.leankg
	@cd $(REPOS_DIR)/$(REPO) && $(LEANKG_BIN) init && $(LEANKG_BIN) index . && $(LEANKG_BIN) embed --wait

with: check-claude check-leankg
	@test -n "$(REPO)" || (echo "ERROR: REPO=<slug> required" && exit 2)
	@bash $(CURDIR)/run_arm.sh $(REPO) with $(N) "$(MODEL)"

without: check-claude
	@test -n "$(REPO)" || (echo "ERROR: REPO=<slug> required" && exit 2)
	@bash $(CURDIR)/run_arm.sh $(REPO) without $(N) "$(MODEL)"

report:
	@$(PYTHON) $(CURDIR)/aggregate.py --results $(RESULTS_DIR) --repos $(CURDIR)/repos.yaml

all: setup
	@$(MAKE) with    REPO=$(REPO) N=$(N) MODEL=$(MODEL)
	@$(MAKE) without REPO=$(REPO) N=$(N) MODEL=$(MODEL)
	@$(MAKE) report

full: setup
	@for slug in vscode excalidraw django tokio okhttp gin alamofire; do \
	  echo ""; \
	  echo "######## $$slug ########"; \
	  $(MAKE) with    REPO=$$slug N=$(N) MODEL=$(MODEL); \
	  $(MAKE) without REPO=$$slug N=$(N) MODEL=$(MODEL); \
	done
	@$(MAKE) report

clean:
	@echo "Removing $(SCRATCH_DIR) and $(RUNS_DIR) and cloned repos..."
	@rm -rf $(SCRATCH_DIR)
	@rm -rf $(RUNS_DIR)
	@find $(RESULTS_DIR) -maxdepth 1 -name 'cross_tool-*' -delete
	@rm -rf $(REPOS_DIR)
	@echo "Cleaned."