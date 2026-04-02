# Kotlin API Service Example

A simple Kotlin microservice demonstrating LeanKG's Kotlin indexing capabilities.

## Project Structure

```
src/main/kotlin/com/example/
├── Application.kt            # Main entry point
├── model/
│   ├── User.kt               # Data class
│   ├── Order.kt              # Data class with companion object
│   └── OrderStatus.kt        # Enum class
├── service/
│   ├── UserService.kt        # User service with business logic
│   └── OrderService.kt       # Order service calling UserService
├── controller/
│   └── ApiController.kt      # Controller calling services
└── util/
    └── Validator.kt           # Utility object

src/test/kotlin/com/example/service/
└── UserServiceTest.kt         # Test file
```

## Features Verified

- **Class extraction** — `User`, `Order`, `UserService`, `OrderService`, `ApiController`
- **Object extraction** — `Validator` singleton, `Order.Factory` companion object
- **Function extraction** — `findById`, `createOrder`, `main`
- **Secondary constructor** — `Order(id, userId)`
- **Enum extraction** — `OrderStatus`
- **Import relationships** — cross-package imports
- **Call relationships** — controller → service → model
- **Test file detection** — `UserServiceTest.kt`

## Quick Start

```bash
cd examples/kotlin-api-service

# Init and index
../../target/release/leankg init
../../target/release/leankg index ./src --lang kotlin

# Check status
../../target/release/leankg status

# Query elements
../../target/release/leankg query UserService --kind name

# Impact analysis
../../target/release/leankg impact src/main/kotlin/com/example/service/UserService.kt --depth 2
```
