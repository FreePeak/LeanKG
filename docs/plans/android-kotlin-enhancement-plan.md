# LeanKG Android/Kotlin Enhancement Plan

## Overview
This plan addresses the four identified gaps in LeanKG's Android/Kotlin support:
1. Annotation processing beyond Hilt/Room
2. Kotlin syntax parsing (suspend fun, data class, generics)
3. Function-level call graph resolution
4. XML resource linking to code

---

## Gap 1: Annotation Processing

### Current State
- Hilt annotations (@Module, @Provides, @Inject) handled by `AndroidHiltExtractor`
- Room annotations (@Entity, @Dao, @Database) handled by `AndroidRoomExtractor`
- General annotations captured as `decorator` elements but not linked to targets

### Target State
- Extract all annotations with their targets
- Link annotations to annotated elements
- Support common Android patterns: @SerializedName, @JsonProperty, @Parcelize, etc.

### Implementation Plan

#### Phase 1.1: Enhanced Annotation Extraction (2-3 days)
**File**: `src/indexer/kotlin_annotations.rs` (new)

```rust
/// Extracts all annotations from Kotlin files with their targets
pub struct KotlinAnnotationExtractor<'a> {
    source: &'a [u8],
    file_path: &'a str,
}

pub struct AnnotationInfo {
    pub name: String,
    pub target_element: String,  // qualified name of annotated element
    pub target_type: String,     // "class", "function", "property", "parameter"
    pub arguments: Vec<(String, String)>,  // key-value pairs
    pub line: u32,
}
```

**Supported Annotations**:
- Serialization: `@SerializedName`, `@JsonProperty`, `@JsonIgnore`
- Android: `@Parcelize`, `@Keep`, `@JvmStatic`, `@JvmOverloads`
- Compose: `@Composable`, `@Preview`
- Testing: `@Test`, `@Before`, `@After`, `@Mock`

**Output Elements**:
- `annotation` type for each annotation found
- `annotates` relationship linking annotation to target

#### Phase 1.2: Integration (1 day)
**File**: `src/indexer/mod.rs`

Integrate into `extract_elements_for_file`:
```rust
if language == "kotlin" {
    let annotation_extractor = KotlinAnnotationExtractor::new(source, file_path);
    let (ann_elements, ann_rels) = annotation_extractor.extract();
    elements.extend(ann_elements);
    relationships.extend(ann_rels);
}
```

#### Phase 1.3: Tests (1 day)
**File**: `tests/kotlin_annotation_tests.rs`

Test cases:
- Class-level annotations
- Function-level annotations
- Property annotations
- Annotation with arguments

---

## Gap 2: Kotlin Syntax Parsing

### Current State
- Tree-sitter parses Kotlin syntax correctly
- But metadata like `suspend`, `data`, `sealed` not extracted
- Generic type parameters not captured

### Target State
- Capture Kotlin-specific modifiers in element metadata
- Extract generic type information
- Identify extension functions

### Implementation Plan

#### Phase 2.1: Enhanced Function Extraction (2 days)
**File**: `src/indexer/extractor.rs` - modify `extract_function`

Enhance function metadata:
```rust
metadata: serde_json::json!({
    "signature": signature,
    "signature_line_end": sig_end + 1,
    "is_suspend": self.has_modifier(node, "suspend"),
    "is_inline": self.has_modifier(node, "inline"),
    "is_operator": self.has_modifier(node, "operator"),
    "is_infix": self.has_modifier(node, "infix"),
    "is_extension": self.is_extension_function(node),
    "receiver_type": self.get_receiver_type(node),  // For extensions
    "type_parameters": self.get_type_parameters(node),  // <T, R>
}),
```

Add helper methods:
```rust
fn has_modifier(&self, node: Node, modifier: &str) -> bool {
    // Check for modifier node children
}

fn is_extension_function(&self, node: Node) -> bool {
    // Check for receiver_type in function declaration
}

fn get_receiver_type(&self, node: Node) -> Option<String> {
    // Extract receiver type for extension functions
}

fn get_type_parameters(&self, node: Node) -> Vec<String> {
    // Extract <T : Constraint> parameters
}
```

#### Phase 2.2: Enhanced Class Extraction (2 days)
**File**: `src/indexer/extractor.rs` - modify `extract_class`

Enhance class metadata:
```rust
metadata: serde_json::json!({
    "is_data_class": self.has_modifier(node, "data"),
    "is_sealed": self.has_modifier(node, "sealed"),
    "is_open": self.has_modifier(node, "open"),
    "is_abstract": self.has_modifier(node, "abstract"),
    "is_companion": node.kind() == "companion_object",
    "is_object": node.kind() == "object_declaration",
    "type_parameters": self.get_type_parameters(node),
}),
```

#### Phase 2.3: Property Enhancement (1 day)
**File**: `src/indexer/extractor.rs` - modify `extract_property`

Capture:
- `lateinit` modifier
- `const` modifier
- Delegate properties (`by lazy`, `by viewModels()`)
- Property type with generics

#### Phase 2.4: Tests (1 day)
Update existing tests in `tests/kotlin_extraction_tests.rs`:
- Verify `suspend` modifier extraction
- Verify `data class` identification
- Verify generic type parameters
- Verify extension function detection

---

## Gap 3: Call Graph Resolution

### Current State
- `extract_call` creates `calls` relationships with `__unresolved__` targets
- Low confidence (0.5) for all calls
- `resolve_call_edges_inline` attempts resolution but limited

### Target State
- Resolve calls within the same file to qualified names
- Distinguish between class methods, top-level functions, and extension functions
- Higher confidence for resolved calls

### Implementation Plan

#### Phase 3.1: Multi-Pass Extraction (3 days)
**File**: `src/indexer/call_graph.rs` (new)

Two-pass approach:

```rust
pub struct CallGraphBuilder<'a> {
    source: &'a [u8],
    file_path: &'a str,
    language: &'a str,
    // First pass collects all function definitions
    defined_functions: HashMap<String, String>,  // name -> qualified_name
    // Track class context for method calls
    class_methods: HashMap<String, Vec<String>>,  // class_name -> method_names
}

impl<'a> CallGraphBuilder<'a> {
    /// First pass: collect all defined functions with their qualified names
    pub fn collect_definitions(&mut self, tree: &Tree) {
        // Walk tree and populate defined_functions
    }
    
    /// Second pass: resolve calls using collected definitions
    pub fn resolve_calls(&self, node: Node, current_class: Option<&str>) -> Vec<Relationship> {
        // For each call_expression:
        // 1. Get call target name
        // 2. Check if it's a method on current_class
        // 3. Check if it's a top-level function in this file
        // 4. Check if it's an extension function
        // 5. Create resolved or unresolved relationship
    }
}
```

**Resolution Strategy**:
1. Same-class method call: `file::ClassName::methodName` → confidence 0.95
2. Same-file top-level function: `file::functionName` → confidence 0.9
3. Import-based call: Use import info → confidence 0.7
4. Unresolved: Keep `__unresolved__` prefix → confidence 0.5

#### Phase 3.2: Kotlin-Specific Call Resolution (2 days)
Handle Kotlin patterns:
- Extension function calls: `String.extension()`
- Infix calls: `a plus b`
- Operator overloading: `a + b` → `plus()`
- Scope functions: `let`, `run`, `apply`, `also` - don't create call edges for these

#### Phase 3.3: Integration (1 day)
**File**: `src/indexer/extractor.rs`

Replace current `extract_call` with call graph builder in `visit_node`.

#### Phase 3.4: Tests (1 day)
**File**: `tests/call_graph_tests.rs`

Test cases:
- Same-class method calls
- Same-file function calls
- Extension function calls
- Cross-file calls (with imports)
- Unresolved calls

---

## Gap 4: Resource Linking

### Current State
- `AndroidResourceRefExtractor` extracts R.xxx.yyy references
- `XmlLayoutExtractor` parses layout XML files
- Limited linking: synthetic imports, findViewById, ViewBinding

### Target State
- Link Kotlin code to specific XML layout elements
- Track which layouts are inflated in which Activities/Fragments
- Connect click handlers to their view declarations

### Implementation Plan

#### Phase 4.1: Layout Inflation Detection (2 days)
**File**: `src/indexer/android_layout_usage.rs` (new)

```rust
pub struct LayoutUsageExtractor<'a> {
    source: &'a [u8],
    file_path: &'a str,
}

pub struct LayoutUsage {
    pub layout_name: String,
    pub usage_type: LayoutUsageType,  // setContentView, inflate, ViewBinding
    pub container_class: Option<String>,  // Activity/Fragment class
    pub line: u32,
}
```

**Detection Patterns**:
```kotlin
// setContentView
setContentView(R.layout.activity_main)

// LayoutInflater
layoutInflater.inflate(R.layout.item_row, parent, false)
View.inflate(context, R.layout.dialog, null)

// ViewBinding
val binding = ActivityMainBinding.inflate(layoutInflater)
val binding = FragmentHomeBinding.bind(view)

// DataBinding
DataBindingUtil.setContentView<ActivityMainBinding>(this, R.layout.activity_main)
```

**Output Relationships**:
- `inflates_layout`: Activity/Fragment → layout file
- `uses_viewbinding`: Class → generated binding class

#### Phase 4.2: Click Handler Linking (2 days)
**File**: `src/indexer/android_click_handlers.rs` (new)

Link click handlers to view IDs:

```rust
pub struct ClickHandlerExtractor<'a> {
    source: &'a [u8],
    file_path: &'a str,
}

// Detect patterns:
// button.setOnClickListener { ... }
// button.setOnClickListener(this)
// binding.submitButton.setOnClickListener { ... }
// findViewById<Button>(R.id.submit).setOnClickListener { ... }
```

**Output Relationships**:
- `on_click_handler`: View ID → handler function
- `handles_click`: Activity/Fragment → view ID (for `this` pattern)

#### Phase 4.3: XML Layout Index Enhancement (1 day)
**File**: `src/indexer/xml_layout.rs`

Add to existing layout extraction:
- Extract all view IDs with their types
- Extract custom view class names
- Track which layouts include other layouts (`<include>`)

#### Phase 4.4: Integration (1 day)
**File**: `src/indexer/mod.rs`

Add to `extract_elements_for_file`:
```rust
if language == "kotlin" {
    let layout_usage = LayoutUsageExtractor::new(source, file_path);
    let (_, layout_rels) = layout_usage.extract();
    relationships.extend(layout_rels);
    
    let click_handler = ClickHandlerExtractor::new(source, file_path);
    let (_, click_rels) = click_handler.extract();
    relationships.extend(click_rels);
}
```

#### Phase 4.5: Tests (1 day)
**File**: `tests/android_resource_linking_tests.rs`

Test cases:
- setContentView detection
- ViewBinding usage
- LayoutInflater.inflate
- Click handler lambda
- Click handler with `this`
- findViewById click handler

---

## Implementation Order

### Sprint 1: Annotations + Syntax (Week 1)
1. Day 1-2: KotlinAnnotationExtractor
2. Day 3: Integrate annotation extraction
3. Day 4-5: Enhanced function/class metadata (suspend, data class, etc.)

### Sprint 2: Call Graph (Week 2)
1. Day 1-3: CallGraphBuilder with two-pass extraction
2. Day 4: Kotlin-specific call patterns
3. Day 5: Integration and tests

### Sprint 3: Resource Linking (Week 3)
1. Day 1-2: LayoutUsageExtractor
2. Day 3-4: ClickHandlerExtractor
3. Day 5: XML layout enhancements and tests

### Sprint 4: Integration & Polish (Week 4)
1. Day 1-2: End-to-end testing with real Android project
2. Day 3-4: Performance optimization
5. Day 5: Documentation and final review

---

## Testing Strategy

### Unit Tests
- Each extractor has dedicated test file
- Mock Kotlin source for predictable AST
- Assert on extracted elements and relationships

### Integration Tests
- Test with `tests/fixtures/kotlin_patterns/` files
- Verify round-trip: extract → store → query

### E2E Tests
- Index a real Android project (if available)
- Query for specific patterns
- Validate impact radius works with new relationships

---

## Risk Mitigation

### Risk 1: Tree-sitter AST changes
**Mitigation**: Version pin tree-sitter-kotlin-ng, add AST structure tests

### Risk 2: Performance impact
**Mitigation**: 
- Lazy extraction (only for Kotlin files)
- Benchmark before/after with large codebase
- Consider parallel extraction

### Risk 3: False positives in call resolution
**Mitigation**:
- Keep confidence scores accurate
- Don't over-promise in resolution
- Provide "is_resolved" flag in metadata

---

## Success Metrics

1. **Annotation Coverage**: 90% of annotations in test fixtures extracted
2. **Syntax Metadata**: All functions/classes have modifier metadata
3. **Call Resolution**: 80% of same-file calls resolved to qualified names
4. **Resource Linking**: 100% of setContentView/inflate calls linked to layouts
5. **Performance**: <10% indexing time increase

---

## Open Questions

1. Should we handle Jetpack Compose specially (beyond @Composable annotation)?
2. How deep should we go with generic type resolution (just parameter names or full constraints)?
3. Should call graph include cross-file resolution or stay intra-file only?
4. Do we need to handle Kotlin Multiplatform (expect/actual) patterns?

---

*Plan created: 2026-04-22*
*Estimated effort: 4 weeks (1 developer)*
