package index

import (
	"strings"
	"testing"
)

func TestExtractStringRef(t *testing.T) {
	src := `
            val title = getString(R.string.app_name)
            val desc = resources.getString(R.string.description)
        `
	rels := ExtractResourceRefs("./Test.kt", []byte(src))
	stringRefs := filterRels(rels, "uses_string_resource")
	if len(stringRefs) != 2 {
		t.Fatalf("string refs = %d, want 2 (one per occurrence)", len(stringRefs))
	}
	var hasAppName, hasDescription bool
	for _, r := range stringRefs {
		hasAppName = hasAppName || r.Target == "res/values/strings.xml/app_name"
		hasDescription = hasDescription || r.Target == "res/values/strings.xml/description"
	}
	if !hasAppName || !hasDescription {
		t.Fatalf("targets = %v", stringRefs)
	}
	for _, r := range stringRefs {
		if r.Source != "./Test.kt" || r.Confidence != 1.0 {
			t.Fatalf("unexpected ref %v", r)
		}
	}
}

func TestExtractDrawableRef(t *testing.T) {
	src := `imageView.setImageResource(R.drawable.ic_launcher)`
	rels := ExtractResourceRefs("./Test.kt", []byte(src))
	drawableRefs := filterRels(rels, "uses_drawable_resource")
	if len(drawableRefs) != 1 {
		t.Fatalf("drawable refs = %d, want 1", len(drawableRefs))
	}
	if drawableRefs[0].Target != "res/drawable/ic_launcher" {
		t.Fatalf("target = %q", drawableRefs[0].Target)
	}
}

func TestExtractLayoutRef(t *testing.T) {
	src := `
            setContentView(R.layout.activity_main)
            val view = layoutInflater.inflate(R.layout.item_row, null)
        `
	rels := ExtractResourceRefs("./Test.kt", []byte(src))
	if got := countRels(rels, "uses_layout_resource"); got != 2 {
		t.Fatalf("layout refs = %d, want 2", got)
	}
}

func TestExtractIdRef(t *testing.T) {
	src := `val button = findViewById<Button>(R.id.submit_button)`
	rels := ExtractResourceRefs("./Test.kt", []byte(src))
	idRefs := filterRels(rels, "references_view_by_id")
	if len(idRefs) != 1 {
		t.Fatalf("id refs = %d, want 1", len(idRefs))
	}
	if idRefs[0].Target != "res/values/ids.xml/submit_button" {
		t.Fatalf("target = %q", idRefs[0].Target)
	}
}

func TestResourceRefsFixture(t *testing.T) {
	rels := ExtractResourceRefs("ui/MainActivity.kt", mustReadFixture(t, "MainActivity.kt"))
	if got := countRels(rels, "uses_string_resource"); got != 2 {
		t.Fatalf("string refs = %d, want 2", got)
	}
	if got := countRels(rels, "uses_drawable_resource"); got != 1 {
		t.Fatalf("drawable refs = %d, want 1", got)
	}
	// R.id.container, R.id.submit_button, R.id.action_home_to_detail.
	if got := countRels(rels, "references_view_by_id"); got != 3 {
		t.Fatalf("id refs = %d, want 3", got)
	}
}

func TestLinkSetContentView(t *testing.T) {
	src := `
            class MainActivity : AppCompatActivity() {
                override fun onCreate(savedInstanceState: Bundle?) {
                    super.onCreate(savedInstanceState)
                    setContentView(R.layout.activity_main)
                }
            }
        `
	rels := ExtractResourceLinks("./MainActivity.kt", []byte(src))
	inflates := filterRels(rels, "inflates_layout")
	if len(inflates) == 0 {
		t.Fatal("should find setContentView inflate")
	}
	if inflates[0].Target != "res/layout/activity_main.xml" {
		t.Fatalf("target = %q", inflates[0].Target)
	}
	if inflates[0].Metadata["method"] != "setContentView" {
		t.Fatalf("method = %v", inflates[0].Metadata["method"])
	}
}

func TestLinkViewbindingUsage(t *testing.T) {
	src := `
            val binding = ActivityMainBinding.inflate(layoutInflater)
            binding.submitButton.setOnClickListener { ... }
        `
	rels := ExtractResourceLinks("./Test.kt", []byte(src))
	bindings := filterRels(rels, "uses_viewbinding")
	if len(bindings) == 0 {
		t.Fatal("should detect ViewBinding")
	}
	if bindings[0].Metadata["binding_class"] != "ActivityMainBinding" {
		t.Fatalf("binding_class = %v", bindings[0].Metadata["binding_class"])
	}
	if bindings[0].Target != "generated/ActivityMainBinding.java" {
		t.Fatalf("target = %q", bindings[0].Target)
	}
	var hasInferred bool
	for _, r := range rels {
		if r.RelType == "inflates_layout" && r.Target == "res/layout/activity_main.xml" &&
			r.Metadata["inferred_from_binding"] == "ActivityMainBinding" {
			hasInferred = true
		}
	}
	if !hasInferred {
		t.Fatal("missing inferred layout from binding")
	}
}

func TestLinkClickHandler(t *testing.T) {
	src := `
            submitButton.setOnClickListener {
                handleSubmit()
            }
        `
	rels := ExtractResourceLinks("./Test.kt", []byte(src))
	handlers := filterRels(rels, "on_click_handler")
	if len(handlers) == 0 {
		t.Fatal("should find click handler")
	}
	if handlers[0].Target != "__view__/submitButton" {
		t.Fatalf("target = %q", handlers[0].Target)
	}
	if snippet, _ := handlers[0].Metadata["handler_body_snippet"].(string); !strings.Contains(snippet, "handleSubmit()") {
		t.Fatalf("snippet = %q, want it to contain handleSubmit()", snippet)
	}
}

func TestLinkFindViewByIdClickHandler(t *testing.T) {
	src := `findViewById<Button>(R.id.submit_button).setOnClickListener { }`
	rels := ExtractResourceLinks("./Test.kt", []byte(src))
	var found bool
	for _, r := range rels {
		if r.RelType == "on_click_handler" && r.Target == "res/layout/__unknown__/@+id/submit_button" {
			found = true
		}
	}
	if !found {
		t.Fatalf("findViewById click handler missing: %v", rels)
	}
}

func TestLinkBindingToLayout(t *testing.T) {
	cases := map[string]string{
		"ActivityMainBinding": "activity_main",
		"ItemRowBinding":      "item_row",
		"FragmentHomeBinding": "fragment_home",
	}
	for in, want := range cases {
		if got := bindingToLayout(in); got != want {
			t.Fatalf("bindingToLayout(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestLinkFixture(t *testing.T) {
	rels := ExtractResourceLinks("ui/MainActivity.kt", mustReadFixture(t, "MainActivity.kt"))
	if got := countRels(rels, "inflates_layout"); got != 2 {
		t.Fatalf("inflates_layout = %d, want 2 (setContentView + binding inference)", got)
	}
	if got := countRels(rels, "uses_viewbinding"); got != 1 {
		t.Fatalf("uses_viewbinding = %d, want 1", got)
	}
	if got := countRels(rels, "on_click_handler"); got != 2 {
		t.Fatalf("on_click_handler = %d, want 2", got)
	}
}
