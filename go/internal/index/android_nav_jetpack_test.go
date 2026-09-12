package index

import (
	"testing"

	"github.com/FreePeak/LeanKG/go/internal/store"
)

func TestJetpackNavXMLGraph(t *testing.T) {
	xml := `<?xml version="1.0" encoding="utf-8"?>
<navigation xmlns:android="http://schemas.android.com/apk/res/android"
    xmlns:app="http://schemas.android.com/apk/res-auto"
    android:id="@+id/nav_graph"
    app:startDestination="@id/homeFragment">

    <fragment
        android:id="@+id/homeFragment"
        android:name="com.example.HomeFragment">
        <action
            android:id="@+id/action_home_to_detail"
            app:destination="@id/detailFragment" />
        <argument
            android:name="userId"
            app:argType="string"
            app:nullable="true" />
    </fragment>

    <fragment
        android:id="@+id/detailFragment"
        android:name="com.example.DetailFragment">
        <deepLink app:uri="example://detail/{id}" />
    </fragment>
</navigation>`
	els, rels := ExtractJetpackNavXML("res/navigation/nav_graph.xml", []byte(xml))

	if got := countType(els, "nav_destination"); got != 2 {
		t.Fatalf("destinations = %d, want 2", got)
	}
	if !hasTypedName(els, "nav_destination", "homeFragment") || !hasTypedName(els, "nav_destination", "detailFragment") {
		t.Fatal("expected homeFragment and detailFragment destinations")
	}
	if got := countRels(rels, "nav_action"); got != 1 {
		t.Fatalf("nav_action = %d, want 1", got)
	}
	if got := countType(els, "nav_argument"); got != 1 {
		t.Fatalf("arguments = %d, want 1", got)
	}
	if got := countRels(rels, "deep_link"); got != 1 {
		t.Fatalf("deep_link = %d, want 1", got)
	}

	graph := findElem(els, "nav_graph", "")
	if graph == nil {
		t.Fatal("missing nav_graph element")
	}
	if graph.Name != "nav_graph" {
		t.Fatalf("graph name = %q, want nav_graph", graph.Name)
	}
	if graph.Metadata["start_destination"] != "homeFragment" {
		t.Fatalf("graph start_destination = %v", graph.Metadata["start_destination"])
	}
}

func TestJetpackNavXMLStartDestination(t *testing.T) {
	xml := `<?xml version="1.0" encoding="utf-8"?>
<navigation xmlns:app="http://schemas.android.com/apk/res-auto"
    android:id="@+id/nav_main"
    app:startDestination="@id/loginFragment">
    <fragment android:id="@+id/loginFragment" android:name="com.example.LoginFragment" />
    <fragment android:id="@+id/dashboardFragment" android:name="com.example.DashboardFragment" />
</navigation>`
	els, _ := ExtractJetpackNavXML("res/navigation/nav_main.xml", []byte(xml))

	graph := findElem(els, "nav_graph", "")
	if graph == nil {
		t.Fatal("should have a nav_graph element")
	}
	start := findElem(els, "nav_destination", "loginFragment")
	if start == nil {
		t.Fatal("missing loginFragment destination")
	}
	if start.Metadata["start_destination"] != true {
		t.Fatalf("loginFragment start_destination = %v, want true", start.Metadata["start_destination"])
	}
	if start.ParentQualified != graph.QualifiedName {
		t.Fatalf("destination parent = %q, want %q", start.ParentQualified, graph.QualifiedName)
	}
}

func TestJetpackNavXMLInjectsMissingAndroidNamespace(t *testing.T) {
	// Some fixtures omit xmlns:android; the Rust extractor injects it so the
	// prefix resolves. The Go path does the same.
	xml := `<navigation xmlns:app="http://schemas.android.com/apk/res-auto"
    app:startDestination="@id/homeFragment">
    <fragment android:id="@+id/homeFragment" android:name="com.example.HomeFragment" />
</navigation>`
	els, _ := ExtractJetpackNavXML("res/navigation/nav_graph.xml", []byte(xml))
	if got := countType(els, "nav_destination"); got != 1 {
		t.Fatalf("destinations = %d, want 1", got)
	}
	if !hasTypedName(els, "nav_destination", "homeFragment") {
		t.Fatal("missing homeFragment destination")
	}
}

func TestJetpackNavXMLRejectsNonNavigationRoot(t *testing.T) {
	els, rels := ExtractJetpackNavXML("res/layout/activity_main.xml", []byte(`<resources></resources>`))
	if len(els) != 0 || len(rels) != 0 {
		t.Fatalf("non-navigation root produced %d elements / %d relationships", len(els), len(rels))
	}
}

func TestJetpackNavXMLFixture(t *testing.T) {
	els, rels := ExtractJetpackNavXML("app/src/main/res/navigation/nav_graph.xml", mustReadFixture(t, "nav_graph.xml"))
	if got := countType(els, "nav_destination"); got != 3 {
		t.Fatalf("destinations = %d, want 3 (2 fragments + 1 dialog)", got)
	}
	if got := countType(els, "nav_argument"); got != 1 {
		t.Fatalf("arguments = %d, want 1", got)
	}
	if got := countType(els, "nav_deep_link"); got != 1 {
		t.Fatalf("deep links = %d, want 1", got)
	}
	if got := countRels(rels, "nav_action"); got != 1 {
		t.Fatalf("nav_action = %d, want 1", got)
	}
	if got := countRels(rels, "requires_arg"); got != 1 {
		t.Fatalf("requires_arg = %d, want 1", got)
	}
	if got := countRels(rels, "deep_link"); got != 1 {
		t.Fatalf("deep_link = %d, want 1", got)
	}
	dest := findElem(els, "nav_destination", "homeFragment")
	if dest == nil {
		t.Fatal("missing homeFragment")
	}
	if dest.Metadata["class_name"] != "com.example.HomeFragment" {
		t.Fatalf("class_name = %v", dest.Metadata["class_name"])
	}
	if dest.Metadata["start_destination"] != true {
		t.Fatalf("homeFragment start_destination = %v, want true", dest.Metadata["start_destination"])
	}
	if dest.LineStart <= 0 || dest.LineEnd <= dest.LineStart {
		t.Fatalf("destination byte range = [%d,%d], want a positive span", dest.LineStart, dest.LineEnd)
	}
	action := findRel(rels, "nav_action")
	if action == nil {
		t.Fatal("missing nav_action")
	}
	if action.Metadata["action_id"] != "action_home_to_detail" {
		t.Fatalf("action_id = %v, want action_home_to_detail (id prefix stripped)", action.Metadata["action_id"])
	}
	// app:popUpTo is stored raw, as in the Rust reference.
	if action.Metadata["pop_up_to"] != "@id/homeFragment" {
		t.Fatalf("pop_up_to = %v, want @id/homeFragment", action.Metadata["pop_up_to"])
	}
}

func TestJetpackNavKotlinDSLFixture(t *testing.T) {
	els, rels := ExtractJetpackNavKotlinDSL("app/src/main/java/nav/NavHost.kt", mustReadFixture(t, "NavHost.kt"))

	if got := countType(els, "nav_graph"); got != 1 {
		t.Fatalf("nav_graph = %d, want 1", got)
	}
	if got := countType(els, "nav_destination"); got != 4 {
		t.Fatalf("destinations = %d, want 4 (3 composable + 1 navigation)", got)
	}
	// Parity: the Rust argument regex is `argument(name = "…")`, so the
	// idiomatic `navArgument(name = "itemId")` in the fixture does NOT match.
	if got := countType(els, "nav_argument"); got != 0 {
		t.Fatalf("arguments = %d, want 0 (regex matches `argument(`, not `navArgument(`)", got)
	}
	nested := findElem(els, "nav_destination", "settings")
	if nested == nil {
		t.Fatal("missing navigation() destination")
	}
	if nested.Metadata["dest_type"] != "navigation" || nested.Metadata["start_destination"] != "settingsRoot" {
		t.Fatalf("navigation metadata = %v", nested.Metadata)
	}
	if got := countRels(rels, "requires_arg"); got != 0 {
		t.Fatalf("requires_arg = %d, want 0 (navArgument is not matched)", got)
	}
	// navigate("details") is deduped and linked from the fallback source
	// (the graph element) when no block ends before the call line.
	var navActions []string
	for _, r := range rels {
		if r.RelType == "nav_action" {
			navActions = append(navActions, r.Source+" -> "+r.Target)
		}
	}
	if len(navActions) != 1 {
		t.Fatalf("nav_action = %v, want exactly 1", navActions)
	}
	want := "app/src/main/java/nav/NavHost.kt::nav_graph::compose_nav -> " +
		"app/src/main/java/nav/NavHost.kt::nav_graph::compose_nav::details"
	if navActions[0] != want {
		t.Fatalf("nav_action = %q, want %q", navActions[0], want)
	}
}

func TestJetpackNavKotlinDSLArgument(t *testing.T) {
	// Exercises the argument(name = "...") path the Rust regex targets.
	src := `NavHost(navController, startDestination = "home") {
    composable(route = "home") {
        argument(name = "userId") { type = NavType.StringType }
    }
}`
	els, rels := ExtractJetpackNavKotlinDSL("nav/Nav.kt", []byte(src))
	if got := countType(els, "nav_argument"); got != 1 {
		t.Fatalf("arguments = %d, want 1", got)
	}
	if got := countRels(rels, "requires_arg"); got != 1 {
		t.Fatalf("requires_arg = %d, want 1", got)
	}
	arg := findElem(els, "nav_argument", "userId")
	if arg == nil {
		t.Fatal("missing userId argument")
	}
	if arg.Metadata["arg_type"] != "string" || arg.Metadata["nullable"] != false {
		t.Fatalf("argument metadata = %v", arg.Metadata)
	}
	if arg.LineStart <= 0 || arg.LineStart != arg.LineEnd {
		t.Fatalf("argument line range = [%d,%d]", arg.LineStart, arg.LineEnd)
	}
}

func TestJetpackNavDSLOnlyMarkers(t *testing.T) {
	// Without the dispatcher markers the Rust dispatcher never calls the DSL
	// extractor; KotlinExtras enforces the same guard.
	src := `class Foo { fun bar() {} }`
	els, rels := KotlinExtras("Foo.kt", []byte(src))
	for _, e := range els {
		if e.ElementType == "nav_graph" {
			t.Fatalf("DSL extractor ran without markers: %v", e)
		}
	}
	if len(rels) != 0 {
		t.Fatalf("unexpected relationships: %v", rels)
	}
}

func findElem(els []store.Element, typ, name string) *store.Element {
	for i := range els {
		if els[i].ElementType == typ && (name == "" || els[i].Name == name) {
			return &els[i]
		}
	}
	return nil
}

func findRel(rels []store.Relationship, typ string) *store.Relationship {
	for i := range rels {
		if rels[i].RelType == typ {
			return &rels[i]
		}
	}
	return nil
}
