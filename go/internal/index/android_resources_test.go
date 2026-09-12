package index

import (
	"testing"
)

func TestExtractStringResources(t *testing.T) {
	src := `<?xml version="1.0" encoding="utf-8"?>
<resources>
    <string name="app_name">My App</string>
    <string name="greeting">Hello World!</string>
    <string name="empty_string"></string>
</resources>`
	els, rels := ExtractAndroidResources("res/values/strings.xml", []byte(src))
	if got := countType(els, "android_string"); got != 3 {
		t.Fatalf("strings = %d, want 3", got)
	}
	if els[1].Name != "app_name" {
		t.Fatalf("first string = %q, want app_name", els[1].Name)
	}
	if els[1].Metadata["value"] != "My App" {
		t.Fatalf("value = %v, want My App", els[1].Metadata["value"])
	}
	if got := countRels(rels, "defines_string"); got != 3 {
		t.Fatalf("defines_string = %d, want 3", got)
	}
}

func TestExtractColorResources(t *testing.T) {
	src := `<?xml version="1.0" encoding="utf-8"?>
<resources>
    <color name="primary">#FF6200EE</color>
    <color name="secondary">#FF03DAC5</color>
</resources>`
	els, _ := ExtractAndroidResources("res/values/colors.xml", []byte(src))
	colors := 0
	for _, e := range els {
		if e.ElementType == "android_color" {
			colors++
			if colors == 1 {
				if e.Name != "primary" || e.Metadata["value"] != "#FF6200EE" {
					t.Fatalf("first color = %s %v", e.Name, e.Metadata["value"])
				}
			}
		}
	}
	if colors != 2 {
		t.Fatalf("colors = %d, want 2", colors)
	}
}

func TestExtractStyleWithParent(t *testing.T) {
	src := `<?xml version="1.0" encoding="utf-8"?>
<resources>
    <style name="AppTheme" parent="Theme.MaterialComponents.Light.DarkActionBar">
        <item name="colorPrimary">@color/primary</item>
    </style>
    <style name="AppTheme.NoActionBar">
        <item name="windowActionBar">false</item>
    </style>
</resources>`
	els, rels := ExtractAndroidResources("res/values/styles.xml", []byte(src))
	if got := countType(els, "android_style"); got != 2 {
		t.Fatalf("styles = %d, want 2", got)
	}
	if els[1].Name != "AppTheme" {
		t.Fatalf("first style = %q, want AppTheme", els[1].Name)
	}
	if els[1].Metadata["parent"] != "Theme.MaterialComponents.Light.DarkActionBar" {
		t.Fatalf("parent = %v", els[1].Metadata["parent"])
	}
	if got := countRels(rels, "inherits_from"); got != 1 {
		t.Fatalf("inherits_from = %d, want 1", got)
	}
}

func TestExtractDimenBoolIntegerArray(t *testing.T) {
	dimenSrc := `<resources>
    <dimen name="padding_small">8dp</dimen>
    <dimen name="padding_medium">16dp</dimen>
    <dimen name="text_size_large">24sp</dimen>
</resources>`
	els, _ := ExtractAndroidResources("res/values/dimens.xml", []byte(dimenSrc))
	if got := countType(els, "android_dimen"); got != 3 {
		t.Fatalf("dimens = %d, want 3", got)
	}
	if els[1].Name != "padding_small" || els[1].Metadata["value"] != "8dp" {
		t.Fatalf("first dimen = %s %v", els[1].Name, els[1].Metadata["value"])
	}

	boolSrc := `<resources>
    <bool name="is_tablet">false</bool>
    <bool name="enable_logging">true</bool>
</resources>`
	els, _ = ExtractAndroidResources("res/values/bools.xml", []byte(boolSrc))
	if got := countType(els, "android_bool"); got != 2 {
		t.Fatalf("bools = %d, want 2", got)
	}

	intSrc := `<resources>
    <integer name="max_retries">3</integer>
    <integer name="timeout_seconds">30</integer>
</resources>`
	els, _ = ExtractAndroidResources("res/values/integers.xml", []byte(intSrc))
	if got := countType(els, "android_integer"); got != 2 {
		t.Fatalf("integers = %d, want 2", got)
	}

	arraySrc := `<resources>
    <string-array name="planets">
        <item>Mercury</item>
        <item>Venus</item>
    </string-array>
    <integer-array name="scores">
        <item>100</item>
        <item>200</item>
    </integer-array>
</resources>`
	els, _ = ExtractAndroidResources("res/values/arrays.xml", []byte(arraySrc))
	if got := countType(els, "android_array"); got != 2 {
		t.Fatalf("arrays = %d, want 2", got)
	}
}

func TestExtractMixedResources(t *testing.T) {
	src := `<?xml version="1.0" encoding="utf-8"?>
<resources>
    <string name="app_name">My App</string>
    <color name="primary">#FF6200EE</color>
    <dimen name="padding">16dp</dimen>
    <bool name="is_tablet">false</bool>
    <integer name="max_retries">3</integer>
    <style name="AppTheme" parent="Theme.MaterialComponents">
        <item name="colorPrimary">@color/primary</item>
    </style>
</resources>`
	els, rels := ExtractAndroidResources("res/values/resources.xml", []byte(src))
	if len(els) < 6 {
		t.Fatalf("elements = %d, want >= 6", len(els))
	}
	if len(rels) < 6 {
		t.Fatalf("relationships = %d, want >= 6", len(rels))
	}
	// Unknown file name maps to the android_values file element type.
	if els[0].ElementType != "android_values" {
		t.Fatalf("file element type = %q, want android_values", els[0].ElementType)
	}
}

func TestExtractResourcesFixture(t *testing.T) {
	els, rels := ExtractAndroidResources("res/values/strings.xml", mustReadFixture(t, "strings.xml"))
	if els[0].ElementType != "android_strings" {
		t.Fatalf("file element type = %q, want android_strings", els[0].ElementType)
	}
	if got := countType(els, "android_string"); got != 2 {
		t.Fatalf("strings = %d, want 2", got)
	}
	if got := countType(els, "android_color"); got != 1 {
		t.Fatalf("colors = %d, want 1", got)
	}
	if got := countType(els, "android_dimen"); got != 1 {
		t.Fatalf("dimens = %d, want 1", got)
	}
	if got := countType(els, "android_bool"); got != 1 {
		t.Fatalf("bools = %d, want 1", got)
	}
	if got := countType(els, "android_integer"); got != 1 {
		t.Fatalf("integers = %d, want 1", got)
	}
	if got := countType(els, "android_style"); got != 2 {
		t.Fatalf("styles = %d, want 2", got)
	}
	if got := countRels(rels, "inherits_from"); got != 1 {
		t.Fatalf("inherits_from = %d, want 1", got)
	}
}
