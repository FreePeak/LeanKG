# Java API Service Example

A simple Java microservice demonstrating LeanKG's Java indexing capabilities.

## Project Structure

```
src/main/java/com/example/
├── Application.java          # Main entry point
├── model/
│   ├── User.java             # User model
│   ├── Order.java            # Order model
│   └── OrderStatus.java      # Enum
├── service/
│   ├── UserService.java      # User service with business logic
│   └── OrderService.java     # Order service calling UserService
├── controller/
│   └── ApiController.java    # Controller calling services
└── util/
    └── Validator.java        # Utility class

src/test/java/com/example/service/
└── UserServiceTest.java      # Test file
```

## Features Verified

- **Class extraction** — `User`, `Order`, `UserService`, `OrderService`, `ApiController`
- **Interface extraction** — `Repository` interface  
- **Method extraction** — business methods like `findById`, `createOrder`
- **Constructor extraction** — `User(String, String)`
- **Enum extraction** — `OrderStatus`
- **Import relationships** — cross-package imports
- **Call relationships** — controller → service → model
- **Annotation/decorator** — `@Override`
- **Test file detection** — `UserServiceTest.java`

## Quick Start

```bash
cd examples/java-api-service

# Init and index
../../target/release/leankg init
../../target/release/leankg index ./src --lang java

# Check status
../../target/release/leankg status

# Query elements
../../target/release/leankg query UserService --kind name

# Impact analysis
../../target/release/leankg impact src/main/java/com/example/service/UserService.java --depth 2
```
