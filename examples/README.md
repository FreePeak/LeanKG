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

## Java API Service

A simple Java microservice demonstrating LeanKG's Java language support.

**Location**: `java-api-service/`

**Features Verified**:
- Class and interface extraction
- Method and constructor extraction
- Enum extraction (Java 16+ records supported)
- Import relationship tracking (fully-qualified)
- Call graph: controller → service → model
- Java annotation extraction (`@Override`)
- Test file detection (`*Test.java`)
- `tested_by` relationship mapping

**Quick Start**:
```bash
cd examples/java-api-service
../../target/release/leankg init
../../target/release/leankg index ./src --lang java
../../target/release/leankg status
../../target/release/leankg query UserService --kind name
```

See [java-api-service/README.md](java-api-service/README.md) for details.

## Kotlin API Service

A simple Kotlin microservice demonstrating LeanKG's Kotlin language support.

**Location**: `kotlin-api-service/`

**Features Verified**:
- Class and data class extraction
- Object declaration extraction (singletons)
- Companion object extraction
- Function and secondary constructor extraction
- Enum class extraction
- Import relationship tracking
- Call graph: controller → service → model
- Test file detection (`*Test.kt`)
- `tested_by` relationship mapping

**Quick Start**:
```bash
cd examples/kotlin-api-service
../../target/release/leankg init
../../target/release/leankg index ./src --lang kotlin
../../target/release/leankg status
../../target/release/leankg query UserService --kind name
```

See [kotlin-api-service/README.md](kotlin-api-service/README.md) for details.
