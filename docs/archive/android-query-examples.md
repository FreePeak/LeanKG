# Query Examples for Android Projects

Practical examples for querying Android codebases with LeanKG.

## Finding Components

### Find All Activities

```bash
leankg query "" --kind type | grep android_activity
```

Or via MCP:
```json
{
  "tool": "search_code",
  "arguments": {
    "query": "activity",
    "element_type": "android_activity"
  }
}
```

### Find Main Activity (Entry Point)

```bash
# Look for activity with MAIN action in manifest
leankg query "MAIN" --kind pattern
```

### Find All Room Entities

```bash
leankg query "" --kind type | grep room_entity
```

### Find Specific Entity

```bash
leankg query "User" --kind name
```

### Find All DAOs

```bash
leankg query "" --kind type | grep room_dao
```

### Find Hilt Modules

```bash
leankg query "" --kind type | grep hilt_module
```

## Impact Analysis

### What Uses This Entity?

```bash
# Find all code affected by changing User entity
leankg impact app/src/data/entity/User.kt --depth 3
```

Or via MCP:
```json
{
  "tool": "get_impact_radius",
  "arguments": {
    "file": "app/src/data/entity/User.kt",
    "depth": 3
  }
}
```

### What Files Depend on This Layout?

```bash
# Find Kotlin files referencing activity_main layout
leankg query "activity_main" --kind pattern
```

### Repository Dependencies

```bash
# Find what UserRepository depends on
leankg dependencies app/src/data/repository/UserRepository.kt
```

## Resource Analysis

### Find Unused Strings

```bash
# Get all strings defined
leankg query "string" --kind type

# Cross-reference with code references
# (Manual analysis or custom script)
```

### Where Is This Drawable Used?

```bash
leankg query "ic_launcher" --kind pattern
```

### Find Layout Usage

```bash
leankg query "R.layout.main" --kind pattern
```

## Cross-File Relationships

### Database → Entity Chain

```bash
# Find database
leankg query "AppDatabase" --kind name

# See its relationships (includes entities)
leankg dependents app/src/data/AppDatabase.kt
```

### DAO → Repository Chain

```bash
# Find UserDao
leankg query "UserDao" --kind name

# See what uses it
leankg dependents app/src/data/dao/UserDao.kt
```

### DI Graph

```bash
# Find AppModule
leankg query "AppModule" --kind name

# See what it provides
leankg dependencies app/src/di/AppModule.kt
```

## TV-Specific Queries

### Find Leanback Components

```bash
# Search for leanback references
leankg query "leanback" --kind pattern

# Find TV activities
leankg query "LEANBACK_LAUNCHER" --kind pattern
```

### Find Player Components

```bash
# Search for ExoPlayer/Media3
leankg query "PlayerActivity\|ExoPlayer" --kind pattern
```

## Complex Queries

### Find All Database-Related Files

```bash
# Combine multiple searches
leankg query "room" --kind pattern > room_files.txt
leankg query "entity\|dao\|database" --kind pattern >> room_files.txt
sort room_files.txt | uniq
```

### Find Entry Points

```bash
# Activities with MAIN action
leankg query "android.intent.action.MAIN" --kind pattern
```

### Find Permission Usage

```bash
# What needs INTERNET permission?
leankg query "INTERNET\|network" --kind pattern
```

## Verification Queries

### Check Room Setup Complete

```bash
# Should find: Database, Entities, DAOs
echo "Database:"
leankg query "" --kind type | grep room_database | wc -l

echo "Entities:"
leankg query "" --kind type | grep room_entity | wc -l

echo "DAOs:"
leankg query "" --kind type | grep room_dao | wc -l
```

### Check Hilt Setup

```bash
# Should find: Application with @HiltAndroidApp, Modules
echo "Application class:"
leankg query "Application" --kind name | grep -i application

echo "Modules:"
leankg query "" --kind type | grep hilt_module | wc -l
```

## Export for Analysis

### Export Graph

```bash
# Export relationships for visualization
leankg export --format json --output android_graph.json

# Or DOT format for Graphviz
leankg export --format dot --output android_graph.dot
```

### Generate Documentation

```bash
# Generate docs for database layer
leankg generate --template docs/db_template.md
```

## Tips

### Use Partial Matching

```bash
# Find all "Channel" related code
leankg query "Channel" --kind name
# Matches: ChannelEntity, ChannelDao, ChannelRepository, etc.
```

### Combine with grep

```bash
# Find TV-specific activities only
leankg query "" --kind type | grep android_activity | grep tv
```

### Check Confidence Levels

When using MCP tools, relationships include confidence scores:
- 1.0 = Certain (declared in manifest, explicit annotation)
- 0.9 = High confidence (parsed from code)
- 0.7 = Medium confidence (heuristic detection)

Filter low confidence if needed:
```json
{
  "tool": "get_dependencies",
  "arguments": {
    "file": "app/src/data/AppDatabase.kt",
    "min_confidence": 0.8
  }
}
```
