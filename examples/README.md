# Examples

This directory contains example projects demonstrating LeanKG's capabilities.

## Go API Service

A realistic Go microservice showing how LeanKG achieves **~98% token savings** on impact analysis.

**Location**: `go-api-service/`

**Benchmark Results**:
| Scenario | Without LeanKG | With LeanKG | Savings |
|----------|----------------|-------------|---------|
| Impact Analysis | 835 tokens | 13 tokens | **98.4%** |
| Full Feature Testing | 9,601 tokens | 42 tokens | **99.6%** |

**Features Verified**:
- Status reporting
- Code querying
- Impact radius analysis
- Dependency graph traversal

**Quick Start**:
```bash
cd examples/go-api-service
../../target/release/leankg init
../../target/release/leankg index ./internal --lang go
../../target/release/leankg status
python3 benchmark.py
```

See [go-api-service/README.md](go-api-service/README.md) for details.
