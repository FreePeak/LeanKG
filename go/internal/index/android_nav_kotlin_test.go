package index

import (
	"testing"

	"github.com/FreePeak/LeanKG/go/internal/store"
)

func TestFragmentNavReplace(t *testing.T) {
	src := `
fun navigate() {
    supportFragmentManager.beginTransaction()
        .replace(R.id.container, DetailFragment())
        .addToBackStack("detail")
        .commit()
}
`
	rels := ExtractFragmentNav("ui/MainActivity.kt", []byte(src))
	nav := findRel(rels, "navigates_to")
	if nav == nil {
		t.Fatal("should find navigates_to relationship")
	}
	if nav.Target != "class:DetailFragment" {
		t.Fatalf("target = %q, want class:DetailFragment", nav.Target)
	}
	if nav.Metadata["backstack_tag"] != "detail" {
		t.Fatalf("backstack_tag = %v, want detail", nav.Metadata["backstack_tag"])
	}
	if nav.Metadata["nav_type"] != "fragment_manager" {
		t.Fatalf("nav_type = %v", nav.Metadata["nav_type"])
	}
}

func TestFragmentNavStartActivity(t *testing.T) {
	src := `
fun openProfile() {
    startActivity(Intent(this, ProfileActivity::class.java))
}
`
	rels := ExtractFragmentNav("ui/HomeFragment.kt", []byte(src))
	nav := findRel(rels, "navigates_to")
	if nav == nil {
		t.Fatal("should find navigates_to from startActivity")
	}
	if nav.Target != "class:ProfileActivity" {
		t.Fatalf("target = %q, want class:ProfileActivity", nav.Target)
	}
	if nav.Confidence != 0.90 {
		t.Fatalf("confidence = %v, want 0.90", nav.Confidence)
	}
}

func TestFragmentNavNavController(t *testing.T) {
	src := `
fun goToDetail() {
    findNavController().navigate(R.id.action_home_to_detail)
}
`
	rels := ExtractFragmentNav("ui/HomeFragment.kt", []byte(src))
	nav := findRel(rels, "navigates_to")
	if nav == nil {
		t.Fatal("should find navigates_to from NavController")
	}
	if nav.Target != "nav_action:action_home_to_detail" {
		t.Fatalf("target = %q, want nav_action:action_home_to_detail", nav.Target)
	}
	// Route-string navigate calls are captured too.
	rels = ExtractFragmentNav("ui/HomeFragment.kt", []byte(`navController().navigate("home")`))
	nav = findRel(rels, "navigates_to")
	if nav == nil || nav.Target != `nav_action:home` {
		t.Fatalf("route navigate = %v", nav)
	}
}

func TestFragmentNavNoMatches(t *testing.T) {
	rels := ExtractFragmentNav("ui/Plain.kt", []byte("class Plain { fun go() {} }"))
	if len(rels) != 0 {
		t.Fatalf("unexpected relationships: %v", rels)
	}
}

func TestLeanbackBrowseFragmentDetected(t *testing.T) {
	src := `
class MainFragment : BrowseSupportFragment() {
    override fun onViewCreated(view: View, savedInstanceState: Bundle?) {
        setOnItemViewClickedListener { _, item, _, _ ->
            startActivity(Intent(activity, DetailsActivity::class.java))
        }
    }
}
`
	els, rels := ExtractLeanbackNav("tv/MainFragment.kt", []byte(src))
	browse := findElem(els, "nav_destination", "MainFragment")
	if browse == nil {
		t.Fatal("should detect BrowseSupportFragment as nav_destination")
	}
	if browse.Metadata["dest_type"] != "leanback_browse" {
		t.Fatalf("dest_type = %v", browse.Metadata["dest_type"])
	}

	navRels := filterRels(rels, "presents")
	if len(navRels) == 0 {
		t.Fatal("should find presents relationship")
	}
	if navRels[0].Target != "class:DetailsActivity" {
		t.Fatalf("first presents target = %q, want class:DetailsActivity", navRels[0].Target)
	}
	if navRels[0].Confidence != 0.80 {
		t.Fatalf("confidence = %v, want 0.80", navRels[0].Confidence)
	}
}

func TestLeanbackNonLeanbackNotDetected(t *testing.T) {
	src := `
class RegularFragment : Fragment() {
    override fun onViewCreated(view: View, savedInstanceState: Bundle?) {}
}
`
	els, rels := ExtractLeanbackNav("ui/RegularFragment.kt", []byte(src))
	if len(els) != 0 {
		t.Fatalf("non-leanback fragment should produce no elements, got %v", els)
	}
	if len(rels) != 0 {
		t.Fatalf("non-leanback fragment should produce no relationships, got %v", rels)
	}
}

func TestLeanbackFixture(t *testing.T) {
	els, rels := ExtractLeanbackNav("tv/MainFragment.kt", mustReadFixture(t, "MainFragment.kt"))
	if got := countType(els, "nav_destination"); got != 1 {
		t.Fatalf("destinations = %d, want 1", got)
	}
	presents := filterRels(rels, "presents")
	var hasDetails, hasDetailsFragment bool
	for _, r := range presents {
		if r.Target == "class:DetailsActivity" {
			hasDetails = true
		}
		if r.Target == "class:DetailsFragment" {
			hasDetailsFragment = true
		}
	}
	if !hasDetails {
		t.Fatal("missing presents -> class:DetailsActivity")
	}
	if !hasDetailsFragment {
		t.Fatal("missing presents -> class:DetailsFragment (click handler)")
	}
}
func filterRels(rels []store.Relationship, typ string) []store.Relationship {
	var out []store.Relationship
	for _, r := range rels {
		if r.RelType == typ {
			out = append(out, r)
		}
	}
	return out
}
