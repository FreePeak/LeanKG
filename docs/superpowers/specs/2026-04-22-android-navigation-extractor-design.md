# Android Navigation Extractor — Design Spec

**Date:** 2026-04-22
**Phase:** 1 of 5 (Modern Android Support)
**Status:** Approved

---

## Context

LeanKG indexes Kotlin/Android codebases but has no understanding of navigation structure. Navigation is architectural — it defines how screens connect, what arguments flow between them, and where deep links land. Without it, impact analysis misses cross-screen dependencies and AI tools can't answer "what breaks if I change this screen's args?"

This is Phase 1 of a 5-phase plan to support all modern Android development patterns:
1. **Navigation** (this spec)
2. ViewModel + data flow (LiveData, StateFlow, SharedFlow)
3. Compose UI component graph
4. WorkManager + DataStore + background
5. Paging + Flow + collections

---

## Scope

Support three navigation patterns equally:

1. **Jetpack Navigation** — XML nav graphs + Kotlin DSL (`NavGraphBuilder`, `composable("route")`)
2. **FragmentManager backstack** — `replace()`/`add()` with `addToBackStack()`, `startActivity` intents
3. **Leanback** — TV-specific presenter chain (`BrowseSupportFragment → DetailsFragment → PlaybackFragment`)

---

## Section 1: Data Model

### New Relationship Types (add to `RelationshipType` enum in `src/db/models.rs`)

| rel_type | meaning |
|---|---|
| `navigates_to` | call site → destination screen/composable |
| `nav_action` | source destination → target destination via named action |
| `provides_arg` | navigation call → argument it passes |
| `requires_arg` | destination → argument it declares as required |
| `deep_link` | URI pattern → destination |
| `presents` | Leanback presenter → detail/playback fragment |

### New Element Types (stored in `element_type` field of `CodeElement`)

| element_type | meaning |
|---|---|
| `nav_destination` | A screen node in a nav graph (fragment, composable, activity) |
| `nav_action` | A named edge between two destinations |
| `nav_argument` | An argument declared on a destination |
| `nav_deep_link` | A URI pattern registered on a destination |
| `nav_graph` | A nav graph container (XML file or DSL block) |

### Metadata fields (stored as JSON in `CodeElement.metadata`)

**nav_destination:** `{ "destination_id": "...", "class_name": "...", "start_destination": bool }`
**nav_action:** `{ "action_id": "...", "pop_up_to": "..." }`
**nav_argument:** `{ "arg_type": "string|int|...", "nullable": bool, "default_value": "..." }`
**nav_deep_link:** `{ "uri": "..." }`

---

## Section 2: Extractors

Four new files under `src/indexer/`:

### `android_nav_jetpack.rs` — `JetpackNavExtractor`

Handles both XML and Kotlin DSL.

**XML parsing** (no tree-sitter; use `roxmltree` or hand-rolled): walk `<navigation>`, `<fragment>`, `<action>`, `<argument>`, `<deepLink>` nodes. Build `nav_destination` elements and `nav_action`/`nav_argument`/`nav_deep_link` relationships. Triggered when file matches `res/navigation/*.xml`.

**Kotlin DSL parsing** (tree-sitter-kotlin-ng): detect `NavGraphBuilder` receiver or `navigation { }` / `composable("route") { }` call patterns. Extract route strings, args from `navArgument("key") { type = NavType.StringType }`. Map to same element/relationship types as XML for uniform querying.

Qualified name scheme: `<file_path>::nav_graph::<graph_id>::<destination_id>`

### `android_nav_fragments.rs` — `FragmentNavExtractor`

Regex + tree-sitter hybrid on `.kt` / `.java` files.

**FragmentManager calls:** detect `supportFragmentManager.beginTransaction()` chains containing `.replace(R.id.container, MyFragment())` or `.add(...)`. Extract container ID + fragment class. Detect `.addToBackStack("tag")` to capture backstack tags. Emit `navigates_to` relationship from containing function → `MyFragment`.

**startActivity:** detect `startActivity(Intent(this, TargetActivity::class.java))`. Emit `navigates_to` from caller → `TargetActivity`.

**Navigation Component imperative calls:** `findNavController().navigate(R.id.action_home_to_detail)` or `navigate("route")`. Link action ID / route string to known `nav_destination` elements if indexing has seen them.

### `android_nav_leanback.rs` — `LeanbackNavExtractor`

Targets TV-specific patterns in `.kt` files.

**Presenter detection:** find classes extending `BrowseSupportFragment`, `DetailsFragment`, `PlaybackSupportFragment`. Detect `setOnItemViewClickedListener` calls that instantiate or navigate to another presenter/fragment. Emit `presents` relationship.

**Fragment transitions:** detect `startActivity(Intent(activity, DetailsActivity::class.java))` within browse callbacks. Detect `DetailsFragmentBackStack` usage. Emit `navigates_to` from browse presenter → details.

Confidence: 0.80 (heuristic, not structural).

### `android_nav_model.rs` — shared types

```rust
pub struct NavDestination { pub id: String, pub class_name: Option<String>, pub dest_type: DestType }
pub enum DestType { Fragment, Composable, Activity, Dialog }
pub struct NavArg { pub name: String, pub arg_type: String, pub nullable: bool, pub default_value: Option<String> }
```

Used by all three extractors to produce consistent `CodeElement` / `Relationship` output.

### Integration in `mod.rs`

`index_file_sync` routing:
- `res/navigation/*.xml` → `JetpackNavExtractor` (XML mode)
- `*.kt` with nav imports → `JetpackNavExtractor` (DSL) + `FragmentNavExtractor` + `LeanbackNavExtractor`
- `build.gradle.kts` → already handled by `GradleModuleExtractor` (nav dependency captured as `uses_library`)

---

## Section 3: MCP Tools

Four new tools, added to `src/mcp/tools.rs` + `src/mcp/handler.rs`:

**`get_nav_graph`**
- Input: `file_path` or `graph_id`
- Output: full graph — nodes (destinations), edges (actions/routes), args per destination, start destination
- Use case: "show me the nav structure of the onboarding flow"

**`find_route`**
- Input: route string (e.g. `"profile/{userId}"`)
- Output: destination file, required args + types, composable/fragment backing it
- Use case: "where does `DeepLink("profile/123")` land?"

**`get_screen_args`**
- Input: destination name or file path
- Output: arg names, types, required/optional, default values
- Use case: "what do I need to pass to `CheckoutFragment`?"

**`get_nav_callers`**
- Input: destination name or route
- Output: list of call sites with file + line
- Use case: impact radius for changing required args — who breaks?

All four follow existing handler patterns (CozoDB query + compress response). No new tool categories.

---

## Section 4: Testing Strategy

### Unit tests (per extractor file, following existing pattern)

`android_nav_jetpack.rs`:
- Parse XML nav graph: destinations, actions, args all extracted
- Parse Kotlin DSL `NavGraphBuilder` block: routes + args extracted
- Nested nav graphs handled correctly

`android_nav_fragments.rs`:
- `replace()`/`add()` calls → `navigates_to` emitted
- `addToBackStack("tag")` captured in metadata
- `startActivity(Intent(...))` → `navigates_to` emitted

`android_nav_leanback.rs`:
- Class extending `BrowseSupportFragment` detected
- `setOnItemViewClickedListener` → `presents` relationship emitted
- `startActivity` in browse callback → `navigates_to` to `DetailsActivity`

### Integration tests (extend `tests/android_integration_tests.rs`)

- Full Kotlin file with multiple nav patterns → all rel types emitted
- Gradle file with `androidx.navigation` dependency → `uses_library` captured
- Cross-extractor: `@NavDeepLink` annotation on composable + matching route string

### MCP tool tests (extend `tests/mcp_tools_tests.rs`)

- Index fixture nav graph → `get_nav_graph` returns correct node/edge structure
- `find_route` resolves route string to correct destination file
- `get_nav_callers` returns all call sites for a destination

---

## Out of Scope (future phases)

- ViewModel navigation events (Phase 2)
- Compose `NavHost` type-safe routes (Phase 3)
- Deep link testing / URI validation
- Dynamic feature module navigation
