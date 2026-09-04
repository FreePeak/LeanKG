# Android Extraction

LeanKG extracts Android-specific code relationships for XML layouts, resources, and manifests.

## Supported File Types

- `**/*.xml` (Android layouts)
- `**/res/values/*.xml` (resources)
- `**/AndroidManifest.xml`

## Extracted Element Types

| Element Type | Description |
|--------------|-------------|
| `android_layout` | Layout XML file |
| `android_view_id` | View ID defined with `@+id/` |
| `android_view_reference` | View reference with `@id/` |
| `android_manifest` | AndroidManifest.xml |
| `android_string`, `android_color`, `android_dimen`, `android_drawable`, `android_style` | Resource files |

## Extracted Relationships

| Relationship Type | Description |
|-------------------|-------------|
| `defines_widget` | Layout defines a view ID |
| `contains_child` | Layout contains child element |
| `on_click_handler` | onClick attribute detected |
| `binds_view` | ViewBinding connection |
| `references_view` | Layout references external view |
| `associated_with` | Component linked to activity/service |
| `references_class` | Java/Kotlin class reference |
| `uses_string` | String resource usage |
| `uses_color` | Color resource usage |
| `uses_dimen` | Dimension resource usage |
| `uses_drawable` | Drawable resource usage |
| `uses_style` | Style resource usage |

## Example

Indexing this layout:
```xml
<Button android:id="@+id/submitBtn"
        android:onClick="onSubmit" />
```

Produces:
- Element: `submitBtn` (type: `android_view_id`)
- Relationship: `layout.xml` -> `defines_widget` -> `submitBtn`
- Relationship: `MainActivity.kt` -> `on_click_handler` -> `onSubmit`
