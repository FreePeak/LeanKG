# Agent Guidelines for LeanKG

## Project Overview

LeanKG is a Rust-based knowledge graph system that indexes codebases using tree-sitter parsers, stores data in CozoDB, and exposes functionality via CLI and MCP protocol.

**Tech Stack**: Rust 1.70+, CozoDB (embedded relational-graph), tree-sitter, Axum, Clap, Tokio

---

## Build Commands

### Standard Build
```bash
cargo build                    # Debug build
cargo build --release          # Release build
```

### Testing
```bash
cargo test                     # Run all tests
cargo test <test_name>         # Run specific test (partial name matches)
cargo test --package <pkg>     # Test specific package
cargo test -- --nocapture      # Show println output during tests
```

### Code Quality
```bash
cargo fmt                      # Format code
cargo fmt -- --check           # Check formatting without changes
cargo clippy                   # Run linter
cargo clippy -- -D warnings    # Treat warnings as errors
cargo check                    # Type check without building
cargo doc                      # Build documentation
```

### Codebase Indexing & Server
```bash
cargo run -- init              # Initialize LeanKG project
cargo run -- index ./src       # Index codebase
cargo run -- serve             # Start MCP server
cargo run -- impact <file> --depth 3   # Calculate impact radius
cargo run -- status            # Show index status
```

---

## Code Structure Overview

This codebase contains 103 elements and 79 relationships.

### Key Modules

```
src/
├── cli/          # Clap CLI commands
├── config/       # Project configuration
├── db/           # CozoDB layer (models, schema)
├── doc/          # Documentation generator
├── graph/        # Graph engine, query, traversal
├── indexer/      # tree-sitter parsers, entity extraction
├── mcp/          # MCP protocol implementation
├── watcher/      # File system watcher
├── web/          # Axum web server
└── main.rs       # CLI entry point
```

### Files


### Functions

- `./internal/api/handler.go::NewHandler` (./internal/api/handler.go:20)
- `./internal/api/handler.go::SetupRouter` (./internal/api/handler.go:28)
- `./internal/api/handler.go::createOrder` (./internal/api/handler.go:201)
- `./internal/api/handler.go::createUser` (./internal/api/handler.go:95)
- `./internal/api/handler.go::deleteUser` (./internal/api/handler.go:146)
- `./internal/api/handler.go::getOrder` (./internal/api/handler.go:183)
- `./internal/api/handler.go::getPathParam` (./internal/api/handler.go:236)
- `./internal/api/handler.go::getUser` (./internal/api/handler.go:77)
- `./internal/api/handler.go::handleError` (./internal/api/handler.go:250)
- `./internal/api/handler.go::healthCheck` (./internal/api/handler.go:48)
- `./internal/api/handler.go::listOrders` (./internal/api/handler.go:163)
- `./internal/api/handler.go::listUsers` (./internal/api/handler.go:58)
- `./internal/api/handler.go::parseIntParam` (./internal/api/handler.go:225)
- `./internal/api/handler.go::registerRoutes` (./internal/api/handler.go:36)
- `./internal/api/handler.go::updateUser` (./internal/api/handler.go:118)
- `./internal/api/handler.go::writeError` (./internal/api/handler.go:246)
- `./internal/api/handler.go::writeJSON` (./internal/api/handler.go:240)
- `./internal/middleware/middleware.go::AuthMiddleware` (./internal/middleware/middleware.go:29)
- `./internal/middleware/middleware.go::CORSMiddleware` (./internal/middleware/middleware.go:105)
- `./internal/middleware/middleware.go::Error` (./internal/middleware/middleware.go:21)
- `./internal/middleware/middleware.go::LoggingMiddleware` (./internal/middleware/middleware.go:72)
- `./internal/middleware/middleware.go::RateLimitMiddleware` (./internal/middleware/middleware.go:122)
- `./internal/middleware/middleware.go::WriteHeader` (./internal/middleware/middleware.go:100)
- `./internal/middleware/middleware.go::validateToken` (./internal/middleware/middleware.go:65)
- `./internal/models/models_test.go::TestOrderCancel` (./internal/models/models_test.go:132)
- `./internal/models/models_test.go::TestOrderValidation` (./internal/models/models_test.go:70)
- `./internal/models/models_test.go::TestUserValidation` (./internal/models/models_test.go:7)
- `./internal/models/order.go::CalculateTotal` (./internal/models/order.go:79)
- `./internal/models/order.go::CanCancel` (./internal/models/order.go:83)
- `./internal/models/order.go::Cancel` (./internal/models/order.go:87)
- `./internal/models/order.go::Validate` (./internal/models/order.go:43)
- `./internal/models/order.go::isValidOrderStatus` (./internal/models/order.go:62)
- `./internal/models/user.go::Validate` (./internal/models/user.go:37)
- `./internal/models/user.go::isValidEmail` (./internal/models/user.go:53)
- `./internal/repository/order_repository.go::Close` (./internal/repository/order_repository.go:132)
- `./internal/repository/order_repository.go::Create` (./internal/repository/order_repository.go:104)
- `./internal/repository/order_repository.go::Delete` (./internal/repository/order_repository.go:125)
- `./internal/repository/order_repository.go::GetByID` (./internal/repository/order_repository.go:59)
- `./internal/repository/order_repository.go::GetByUserID` (./internal/repository/order_repository.go:78)
- `./internal/repository/order_repository.go::List` (./internal/repository/order_repository.go:23)
- `./internal/repository/order_repository.go::NewOrderRepository` (./internal/repository/order_repository.go:15)
- `./internal/repository/order_repository.go::Update` (./internal/repository/order_repository.go:115)
- `./internal/repository/user_repository.go::Close` (./internal/repository/user_repository.go:107)
- `./internal/repository/user_repository.go::Create` (./internal/repository/user_repository.go:83)
- `./internal/repository/user_repository.go::Delete` (./internal/repository/user_repository.go:100)
- `./internal/repository/user_repository.go::GetByEmail` (./internal/repository/user_repository.go:65)
- `./internal/repository/user_repository.go::GetByID` (./internal/repository/user_repository.go:47)
- `./internal/repository/user_repository.go::List` (./internal/repository/user_repository.go:24)
- `./internal/repository/user_repository.go::NewUserRepository` (./internal/repository/user_repository.go:16)
- `./internal/repository/user_repository.go::Update` (./internal/repository/user_repository.go:93)
- ... and 35 more functions

### Classes/Structs


---

## Relationship Types

- `imports`: 47 occurrences
- `calls`: 30 occurrences
- `tested_by`: 2 occurrences

---

## Testing Guidelines

1. Unit tests are placed in `#[cfg(test)]` modules within each source file
2. Integration tests are located in the `tests/` directory
3. Use `tempfile::TempDir` for tests requiring filesystem access
4. Use `tokio::test` for async tests
5. Follow Arrange-Act-Assert pattern in all tests

