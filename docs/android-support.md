# Android Support in LeanKG

LeanKG provides comprehensive support for Android projects, including Kotlin code analysis, XML resource extraction, and Android-specific pattern recognition.

## Supported Android Patterns

### Kotlin Code

- **Room Database**: Entities, DAOs, Database classes with relationships
- **Hilt/Dagger**: Modules, Providers, Injection sites
- **Coroutines**: Suspend functions, Flows, async/await
- **Data Classes**: Standard, sealed, inline classes
- **Extensions**: Extension functions and properties
- **Generics**: Type constraints, variance, reified types

### XML Resources

- **AndroidManifest.xml**: Components, permissions, intent filters
- **Layouts**: View hierarchies, IDs, widget types
- **Values**: Strings, colors, styles, dimensions
- **Drawables**: Shapes, selectors, vector graphics
- **Preferences**: Settings screens
- **Menus**: Navigation menus

## Quick Start

### Indexing an Android Project

```bash
# Navigate to Android project root
cd /path/to/android-app

# Initialize LeanKG (auto-detects Android)
leankg init

# Index the codebase
leankg index

# For TV apps with src/main structure
leankg index ./app/src/main
```

### Querying Android Code

```bash
# Find all Activities
leankg query "android_activity" --kind type

# Find Room entities
leankg query "room_entity" --kind type

# Find Hilt modules  
leankg query "hilt_module" --kind type

# Calculate impact of changing a Room entity
leankg impact src/data/entity/ChannelEntity.kt --depth 3
```

## Element Types

### Kotlin Elements

| Element Type | Description | Example |
|--------------|-------------|---------|
| `room_entity` | Room @Entity class | `@Entity class User` |
| `room_dao` | Room @Dao interface | `@Dao interface UserDao` |
| `room_database` | Room @Database class | `@Database class AppDatabase` |
| `hilt_module` | Hilt @Module class | `@Module class AppModule` |
| `hilt_provider` | Hilt @Provides method | `@Provides fun provideDb()` |
| `function` | Kotlin function | `suspend fun load()` |
| `class` | Kotlin class | `class MainActivity` |
| `method` | Class method | `fun onCreate()` |

### XML Elements

| Element Type | Description | Example |
|--------------|-------------|---------|
| `android_manifest` | AndroidManifest.xml file | - |
| `android_activity` | <activity> declaration | `<activity android:name=".Main" />` |
| `android_service` | <service> declaration | `<service android:name=".Sync" />` |
| `android_permission` | <uses-permission> | `<uses-permission name="INTERNET" />` |
| `android_layout` | Layout XML file | res/layout/main.xml |
| `android_widget` | View widget | `<Button android:id="@+id/btn" />` |
| `android_view_id` | @+id/ definitions | `@+id/submit_button` |
| `XMLDocument` | Generic XML file | Any .xml file |

## Relationships

### Room Database

```
room_entity_has_foreign_key → Entity references another Entity
room_dao_queries_entity       → DAO queries specific Entity
room_database_contains_entity → Database includes Entity
room_database_contains_dao    → Database includes DAO
```

### Hilt DI

```
hilt_provides           → Module provides type
hilt_module_provides    → Module contains provider
hilt_injected           → Class has @Inject constructor
hilt_field_injected     → Field has @Inject annotation
```

### Resource References

```
uses_string_resource    → Kotlin references R.string.xxx
uses_drawable_resource  → Kotlin references R.drawable.xxx
uses_layout_resource    → Kotlin references R.layout.xxx
references_view_by_id   → Kotlin references R.id.xxx
uses_color_resource     → Kotlin references R.color.xxx
uses_style_resource     → Kotlin references R.style.xxx
```

### Android Manifest

```
declares_component        → Manifest declares Activity/Service/etc
declares_intent_filter    → Component has intent-filter
has_metadata              → Component has meta-data
has_application_class     → Manifest references Application class
requires_permission       → Manifest requires permission
declares_feature          → Manifest declares uses-feature
```

## MCP Tool Examples

### Search for Room Entities

```json
{
  "tool": "search_code",
  "arguments": {
    "query": "ChannelEntity",
    "element_type": "room_entity"
  }
}
```

### Find Impact of Changing an Entity

```json
{
  "tool": "get_impact_radius",
  "arguments": {
    "file": "app/src/data/entity/User.kt",
    "depth": 3
  }
}
```

### Get Room DAO Dependencies

```json
{
  "tool": "get_dependencies",
  "arguments": {
    "file": "app/src/data/dao/UserDao.kt"
  }
}
```

## Configuration

### leankg.yaml for Android

```yaml
project:
  name: MyAndroidApp
  root: ./app/src/main
  languages:
    - kotlin
    - xml

indexer:
  exclude:
    - "**/build/**"
    - "**/.gradle/**"
    - "**/res/raw/**"
```

## Limitations

- Resource reference extraction requires R class usage in Kotlin code
- Some complex Kotlin generics may not be fully extracted
- Hilt injection into abstract classes may not be detected
- Custom Room annotations beyond @Entity/@Dao/@Database not tracked

## Examples

See `tests/fixtures/` for example Android patterns:
- `kotlin_patterns/` - Kotlin code samples
- `android_xml/` - XML resource samples  
- `complex_scenarios/tv_app/` - Full TV app structure
