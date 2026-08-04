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

## C API Service

A C example demonstrating struct + function + `#include` extraction.

**Location**: `c-api/`

**Features Verified**:
- Function extraction (`add`, `main`)
- Struct extraction (`Calculator`)
- Include relationship tracking (`stdio.h`, `calc.h`)

**Quick Start**:
```bash
cd examples/c-api
../../target/release/leankg index . --lang c
```

## Ruby API Service

A Ruby example demonstrating class + method + `require` extraction.

**Location**: `ruby-api/`

**Features Verified**:
- Class and module extraction (`User`, `Greeter`)
- Method extraction (`greet`, `hello`)
- `require` relationship tracking
- Test file detection (`*_spec.rb`)

**Quick Start**:
```bash
cd examples/ruby-api
../../target/release/leankg index . --lang rb
```

## Lua API Service

A Lua example demonstrating function extraction and `require` tracking.

**Location**: `lua-api/`

**Features Verified**:
- Function extraction (`add`, `square`)
- `require` relationship tracking

## Elixir API Service

An Elixir example demonstrating module + function extraction.

**Location**: `elixir-api/`

**Features Verified**:
- Module extraction (`Greeter`)
- Function extraction (`hello`, `secret`)

## Zig API Service

A Zig example demonstrating function + test extraction.

**Location**: `zig-api/`

**Features Verified**:
- Function extraction (`add`)
- Test declaration extraction

## Solidity API Service

A Solidity example demonstrating contract + function + import extraction.

**Location**: `solidity-api/`

**Features Verified**:
- Contract extraction (`Counter`)
- Function extraction (`increment`, `getCount`)
- Import relationship tracking (`./Helper.sol`)

## PHP

A PHP example demonstrating class + method extraction.

**Location**: `php/`

## C++ / C#

Existing examples under `cpp/` and `csharp/`.
