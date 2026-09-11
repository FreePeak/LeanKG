# Go API Service Example

A realistic Go microservice demonstrating how LeanKG provides targeted context for AI-assisted development, achieving **~98% token savings** on whole-repo context (12-file feature verification), and single-file impact blast-radius as a compact node list.

## Project Structure

```
go-api-service/
├── cmd/server/main.go           # Application entry point
├── internal/
│   ├── api/handler.go          # HTTP handlers
│   ├── middleware/middleware.go # Auth, logging, CORS
│   ├── models/
│   │   ├── user.go             # User domain model
│   │   └── order.go            # Order domain model
│   ├── repository/
│   │   ├── user_repository.go  # User data access
│   │   └── order_repository.go # Order data access
│   └── services/
│       ├── user_service.go     # User business logic
│       ├── order_service.go    # Order business logic
│       └── services_test.go    # Service tests
├── pkg/logger/logger.go        # Logging utility
└── .leankg/                    # LeanKG index (103 elements, 79 relationships)
```

## LeanKG Integration

This example demonstrates LeanKG's token optimization for AI-assisted development:

### Indexing

```bash
# Initialize LeanKG
../../target/release/leankg init

# Index Go files
../../target/release/leankg index ./internal --lang go
```

### Query Examples

```bash
# Check index status
../../target/release/leankg status

# Query for specific patterns
../../target/release/leankg query user

# Get impact radius for a file
../../target/release/leankg impact internal/services/user_service.go --depth 2
```

## Benchmark Results

Measured token savings using the LeanKG Go engine (v4.7.0, benchmark_results.json regenerated):

| Scenario | Without LeanKG | With LeanKG | Savings |
|----------|----------------|-------------|---------|
| **Impact Analysis** (file-seed blast radius, compressed) | 835 tokens | 682 tokens | **18.3%** |
| **Full Feature Testing** (12-file context) | 9596 tokens | 132 tokens | **98.6%** |
| **Full Feature Testing** | 9,601 tokens | 42 tokens | **99.6%** |

### Before LeanKG (Traditional Approach)
```go
// AI must scan entire codebase to understand impact
// user_service.go imports repository, models, logger
// order_service.go imports repository, models, user_service
// handler.go imports services, middleware, logger
// Total: 9,601 tokens to understand dependencies
```

### After LeanKG (Graph-Based Approach)
```json
// LeanKG provides targeted subgraph
{
  "impact_radius": ["internal/services/user_service.go"],
  "dependencies": ["internal/repository/user_repository.go", "internal/models/user.go"],
  "callers": ["internal/services/order_service.go", "internal/api/handler.go"],
  "summary_tokens": 42
}
```

## Key Features Verified

| Feature | Status | Description |
|---------|--------|-------------|
| Status | OK | Index statistics (103 elements, 79 relationships) |
| Query | OK | Search code patterns |
| Impact | OK | Blast radius analysis |
| Dependencies | OK | Import graph traversal |

## Running the Example

```bash
# Index the codebase
../../target/release/leankg index ./internal --lang go

# Run benchmark
python3 benchmark.py

# View results
cat benchmark_results.json
```

## LeanKG Benefits

1. **Targeted Context**: Only the relevant subgraph is provided to AI
2. **Dependency Awareness**: Instant understanding of impact radius
3. **Token Efficiency**: ~98% reduction when assembling multi-file feature context; single-file impact returns the compressed blast radius (45-55 nodes) instead of re-reading dependents
4. **Graph Intelligence**: Relationships and call chains are pre-computed

## Files Indexed

- 10 Go source files
- 103 code elements (functions, types, interfaces)
- 79 relationships (imports, calls)
- 2 test files
