.PHONY: benchmark benchmark-mcp benchmark-e2e benchmark-ab benchmark-report benchmark-clean benchmark-single

BENCHMARK_DIR := tests/benchmark
RESULTS_DIR := $(BENCHMARK_DIR)/results

benchmark: benchmark-mcp benchmark-e2e benchmark-ab
	@echo "All benchmarks complete. See $(RESULTS_DIR)/"

benchmark-mcp:
	@echo "Running MCP tool unit tests..."
	cd ../.. && cargo test --test '*' mcp_tools -- --nocapture 2>/dev/null || cargo test --lib mcp_tools -- --nocapture
	@echo "MCP tests complete"

benchmark-e2e:
	@echo "Running Kilo E2E tests..."
	@echo "Ensure LeanKG is indexed: cargo run -- index ./src"
	@echo "Then run: kilo run --session <id> <query>"
	$(BENCHMARK_DIR)/scripts/run_benchmark.sh

benchmark-ab:
	@echo "Running A/B comparison tests..."
	python3 $(BENCHMARK_DIR)/scripts/compare_results.py $(RESULTS_DIR)

benchmark-report:
	@echo "Generating benchmark report..."
	@mkdir -p $(RESULTS_DIR)/$$(date +%Y-%m-%d)

benchmark-clean:
	@echo "Cleaning results..."
	rm -rf $(RESULTS_DIR)/*
	touch $(RESULTS_DIR)/.gitkeep

benchmark-single:
	@echo "Usage: make benchmark-single ID=<query_id>"