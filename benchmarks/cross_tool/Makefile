# Cross-Tool Agent A/B Benchmark (three arms)
#
# Compares three arms on the same repo, prompt and model:
#   baseline      grep/read only (no MCP)
#   context-mode  context-mode MCP server (pinned in tools.lock.yaml)
#   leankg        LeanKG MCP server (`leankg serve --stdio`, repo-root build)
#
# Setup and dry run (no agents, no API calls):
#   make lock                           # resolve repos.yaml refs to commit SHAs
#   make dry-run MODEL=<pinned id>      # validate pins, binaries, print the plan
#
# Real runs are a separate, user-approved step. See RUNNING.md. Each arm target
# refuses to start unless plan.py --require-ready passes (no PLACEHOLDER pins,
# every ref resolved, binaries present).
#
#   make setup                              # clone the 7 repos at locked SHAs
#   make baseline context-mode leankg REPO=gin N=4 MODEL=<pinned id>
#   make all REPO=gin N=4 MODEL=<pinned id> # one repo, three arms, then report
#   make report                             # aggregate JSONL -> Markdown + JSON

PYTHON ?= python3

CLAUDE_BIN ?= $(shell command -v claude)
LEANKG_BIN ?= $(CURDIR)/../../bin/leankg

REPOS_DIR := $(CURDIR)/repos
RESULTS_DIR := $(CURDIR)/results
RUNS_DIR := $(RESULTS_DIR)/runs
SCRATCH_DIR := $(RESULTS_DIR)/scratch

N ?= 4
# MODEL must be the pinned id from tools.lock.yaml. Empty = CLI default, which
# plan.py --require-ready refuses.
MODEL ?=
REPO ?=

DATE := $(shell date +%Y-%m-%d)

.PHONY: help lock setup index baseline context-mode leankg gate dry-run report score selftest clean all full check-leankg check-claude

help:
	@echo "Targets:"
	@echo "  lock                           resolve repos.yaml refs to commit SHAs (repos.lock.yaml)"
	@echo "  setup                          clone the 7 benchmark repos (depth 1, locked SHAs)"
	@echo "  dry-run  MODEL=<m> N=<n>       validate pins/binaries and print the plan; runs no agents"
	@echo "  index  REPO=<slug>             build the LeanKG index for one repo (repo-root bin/leankg)"
	@echo "  baseline      REPO=<slug> N=<n> MODEL=<m>  run the no-MCP arm N times"
	@echo "  context-mode  REPO=<slug> N=<n> MODEL=<m>  run the context-mode arm N times"
	@echo "  leankg        REPO=<slug> N=<n> MODEL=<m>  run the LeanKG arm N times"
	@echo "  report                         aggregate JSONL -> Markdown + JSON"
	@echo "  all REPO=<slug> N=<n> MODEL=<m>  run the three arms for one repo + report"
	@echo "  full MODEL=<m> N=<n>           run the 7-repo x 3-arm x N suite SERIALLY (slow)"
	@echo "  clean                          remove scratch + cloned repos + results"
	@echo ""
	@echo "Recommended: dispatch one run per repo via run_repo.sh <slug> <N> <model>"
	@echo "in parallel, then 'make report' to aggregate. See RUNNING.md."

check-claude:
	@test -n "$(CLAUDE_BIN)" || (echo "ERROR: 'claude' CLI not on PATH" && exit 2)
	@echo "claude:    $(CLAUDE_BIN)"

check-leankg:
	@test -x "$(LEANKG_BIN)" || (echo "ERROR: leankg binary not found at $(LEANKG_BIN) (build with: make go-build at the repo root)" && exit 2)
	@echo "leankg:    $(LEANKG_BIN)"

# Dry run: validates configs, pins, binaries and prints arms x repos x prompts x trials.
dry-run:
	@$(PYTHON) $(CURDIR)/plan.py --repos-dir $(REPOS_DIR) --leankg-bin "$(LEANKG_BIN)" --n $(N) --model "$(MODEL)"

# Gate for every real-run target: exits non-zero while a blocker applies to ARM.
gate:
	@$(PYTHON) $(CURDIR)/plan.py --require-ready --arm "$(ARM)" --repos-dir $(REPOS_DIR) --leankg-bin "$(LEANKG_BIN)" --n $(N) --model "$(MODEL)"

# FR-ZCP-08: resolve every repos.yaml ref to a 40-hex commit SHA (no clone).
lock:
	@$(PYTHON) $(CURDIR)/clone_repos.py --repos $(CURDIR)/repos.yaml --target $(REPOS_DIR) --lock-only

setup: check-claude
	@echo "Cloning 7 benchmark repos (depth 1) into $(REPOS_DIR)..."
	@mkdir -p $(REPOS_DIR)
	@$(PYTHON) $(CURDIR)/clone_repos.py --repos $(CURDIR)/repos.yaml --target $(REPOS_DIR)

index: check-leankg
	@test -n "$(REPO)" || (echo "ERROR: REPO=<slug> required" && exit 2)
	@echo "Indexing $(REPO) with LeanKG..."
	@rm -rf $(REPOS_DIR)/$(REPO)/.leankg
	@cd $(REPOS_DIR)/$(REPO) && $(LEANKG_BIN) index .

baseline: ARM := baseline
baseline: check-claude gate
	@test -n "$(REPO)" || (echo "ERROR: REPO=<slug> required" && exit 2)
	@bash $(CURDIR)/run_arm.sh $(REPO) baseline $(N) "$(MODEL)"

context-mode: ARM := context-mode
context-mode: check-claude gate
	@test -n "$(REPO)" || (echo "ERROR: REPO=<slug> required" && exit 2)
	@bash $(CURDIR)/run_arm.sh $(REPO) context-mode $(N) "$(MODEL)"

leankg: ARM := leankg
leankg: check-claude check-leankg gate
	@test -n "$(REPO)" || (echo "ERROR: REPO=<slug> required" && exit 2)
	@bash $(CURDIR)/run_arm.sh $(REPO) leankg $(N) "$(MODEL)"

report:
	@$(PYTHON) $(CURDIR)/aggregate.py --results $(RESULTS_DIR) --repos $(CURDIR)/repos.yaml

# FR-ZCP-08: judge-blind answer scoring (requires claude CLI; run AFTER the
# arms, BEFORE report so the report merges the scores).
score:
	@$(PYTHON) $(CURDIR)/score.py --results $(RESULTS_DIR) --repos $(CURDIR)/repos.yaml

selftest:
	@$(PYTHON) $(CURDIR)/score.py --selftest
	@$(PYTHON) $(CURDIR)/aggregate.py --selftest
	@$(PYTHON) $(CURDIR)/plan.py --selftest
	@$(PYTHON) $(CURDIR)/clone_repos.py --selftest
	@$(PYTHON) -c "import ast; [ast.parse(open(f).read()) for f in ['aggregate.py','clone_repos.py','get_prompt.py','plan.py','score.py']]; print('py-syntax OK')"
	@bash -n run_one.sh && bash -n run_arm.sh && bash -n run_repo.sh && bash -n install_leankg_mcp.sh && echo "bash-syntax OK"

all: setup
	@$(MAKE) baseline     REPO=$(REPO) N=$(N) MODEL=$(MODEL)
	@$(MAKE) context-mode REPO=$(REPO) N=$(N) MODEL=$(MODEL)
	@$(MAKE) leankg       REPO=$(REPO) N=$(N) MODEL=$(MODEL)
	@$(MAKE) report

full: setup
	@for slug in vscode excalidraw django tokio okhttp gin alamofire; do \
	  echo ""; \
	  echo "######## $$slug ########"; \
	  $(MAKE) baseline     REPO=$$slug N=$(N) MODEL=$(MODEL); \
	  $(MAKE) context-mode REPO=$$slug N=$(N) MODEL=$(MODEL); \
	  $(MAKE) leankg       REPO=$$slug N=$(N) MODEL=$(MODEL); \
	done
	@$(MAKE) report

clean:
	@echo "Removing $(SCRATCH_DIR) and $(RUNS_DIR) and cloned repos..."
	@rm -rf $(SCRATCH_DIR)
	@rm -rf $(RUNS_DIR)
	@find $(RESULTS_DIR) -maxdepth 1 -name 'cross_tool-*' -delete
	@rm -rf $(REPOS_DIR)
	@echo "Cleaned."
