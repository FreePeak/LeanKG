# LeanKG Improvements for Android, Kotlin, XML

This document summarizes the improvements made to LeanKG for better Android, Kotlin, and XML support.

## Overview

These improvements enable LeanKG to:
- Index all XML files (not just Android-specific ones)
- Extract Room database relationships
- Map Hilt dependency injection graphs
- Track Android resource references in code
- Enhanced AndroidManifest.xml parsing

## Changes by Phase

### Phase 1: Foundation (XML Support)

**Files Modified:**
- `src/indexer/mod.rs` - Added "xml" to file extensions

**Files Created:**
- `src/indexer/xml_generic.rs` - Generic XML extractor

**Features:**
- All `.xml` files now indexed
- Generic XML files create XMLDocument elements
- Android-specific XML still handled by dedicated extractors

### Phase 2: Test Fixtures

**Created 34 test fixture files:**

Kotlin Patterns (7 files):
- `tests/fixtures/kotlin_patterns/room_entities.kt`
- `tests/fixtures/kotlin_patterns/room_dao.kt`
- `tests/fixtures/kotlin_patterns/hilt_module.kt`
- `tests/fixtures/kotlin_patterns/data_class.kt`
- `tests/fixtures/kotlin_patterns/coroutines.kt`
- `tests/fixtures/kotlin_patterns/extension_functions.kt`
- `tests/fixtures/kotlin_patterns/generics.kt`

Android XML (11 files):
- `tests/fixtures/android_xml/AndroidManifest.xml`
- `tests/fixtures/android_xml/browse_fragment.xml`
- `tests/fixtures/android_xml/player_activity.xml`
- `tests/fixtures/android_xml/preferences.xml`
- `tests/fixtures/android_xml/searchable.xml`
- `tests/fixtures/android_xml/selector_button.xml`
- `tests/fixtures/android_xml/ic_launcher_foreground.xml`
- `tests/fixtures/android_xml/strings.xml`
- `tests/fixtures/android_xml/colors.xml`
- `tests/fixtures/android_xml/styles.xml`
- `tests/fixtures/android_xml/main_menu.xml`

TV App Scenario (16 files in complex_scenarios/tv_app/):
- Complete TV app structure with Room, Hilt, UI layers

### Phase 3: Deep Extraction

**Files Created:**

1. `src/indexer/android_room.rs`
   - Extracts @Entity, @Dao, @Database
   - Creates foreign key relationships
   - Links DAOs to queried entities
   - 3 unit tests

2. `src/indexer/android_hilt.rs`
   - Extracts @Module, @Provides, @Inject
   - Creates DI graph relationships
   - Tracks provider dependencies
   - 3 unit tests

3. `src/indexer/android_resource_refs.rs`
   - Detects R.string, R.drawable, R.layout, etc.
   - Creates resource usage relationships
   - 4 unit tests

**Files Enhanced:**

4. `src/indexer/android_manifest.rs`
   - Added intent filter extraction
   - Added metadata extraction
   - Added application class detection
   - 6 existing tests still pass

### Phase 4: Comprehensive Testing

**Test Files Created:**

1. `tests/kotlin_extraction_tests.rs` (8 tests)
   - Room entity extraction
   - DAO extraction
   - Hilt module extraction
   - Pattern detection

2. `tests/xml_extraction_tests.rs` (12 tests)
   - Manifest parsing
   - Intent filter detection
   - Resource file validation

3. `tests/android_integration_tests.rs` (8 tests)
   - Cross-file relationships
   - Full TV app scenario
   - Structure completeness

4. `tests/android_performance_tests.rs` (6 tests)
   - Extraction speed benchmarks
   - Batch processing
   - Scalability

**Test Results:**
- Total new tests: 34
- All passing: 34 ✓
- Build time: < 0.5s

## Technical Details

### Regex Patterns Used

**Room Entity:**
```regex
(?s)@Entity\s*(?:\(.*?\))?\s*data\s+class\s+(\w+)
```

**Room DAO:**
```regex
@Dao\s*\n?\s*\n?(?:interface|class)\s+(\w+)
```

**Hilt Module:**
```regex
(?s)@Module\s*\n?\s*(?:@InstallIn\(.*?\)\s*\n?\s*)?(?:abstract\s+)?(?:class|object)\s+(\w+)
```

**Hilt Provider:**
```regex
@Provides\s*\n?(?:@Singleton\s*\n?)?\s*fun\s+(\w+)\s*\([^)]*\)\s*:\s*(\w+)
```

**Resource References:**
```regex
R\.(\w+)\.(\w+)
```

### Integration Points

All extractors integrated into `extract_elements_for_file()`:

```rust
// For Kotlin files
if language == "kotlin" {
    // Room extraction
    let room_extractor = AndroidRoomExtractor::new(source, file_path);
    let (room_elements, room_rels) = room_extractor.extract();
    
    // Hilt extraction
    let hilt_extractor = AndroidHiltExtractor::new(source, file_path);
    let (hilt_elements, hilt_rels) = hilt_extractor.extract();
    
    // Resource reference extraction
    let res_ref_extractor = AndroidResourceRefExtractor::new(source, file_path);
    let (_, res_refs) = res_ref_extractor.extract();
}
```

## Performance

| Operation | Time | Notes |
|-----------|------|-------|
| Room extraction | ~28ms | 4 entities |
| Hilt extraction | ~25ms | 5 elements |
| Manifest parsing | ~16ms | 5 elements |
| Batch (4 files) | ~291ms | Mixed content |

## Migration Guide

### For Existing Projects

No changes needed. Re-index to get new Android elements:

```bash
leankg index --force
```

### For New Android Projects

1. Initialize in project root:
```bash
leankg init
```

2. Index includes src/ automatically:
```bash
leankg index
```

## Future Improvements

Potential enhancements:
- Compose UI extraction
- Navigation component graph parsing
- Gradle dependency analysis
- Test coverage integration
- Resource overlay detection

## References

- Test fixtures: `tests/fixtures/`
- Extractor source: `src/indexer/android_*.rs`
- Documentation: `docs/android-support.md`
