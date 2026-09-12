package projectcfg

import "testing"

// IsMappingDocument is the gate the setup pipeline's config writer consults
// before a read-modify-write: anything that is not a mapping document is not
// project config and must be left exactly as the user wrote it.
func TestIsMappingDocument(t *testing.T) {
	cases := map[string]bool{
		"project:\n  name: x\n":           true,
		"# only a comment\n":              false, // nothing to preserve
		"":                                false,
		"just-a-string\n":                 false,
		"- one\n- two\n":                  false,
		":::: not yaml :::\n":             false,
		"project:\n  name: x\n  bad: [\n": false,
	}
	for src, want := range cases {
		if got := IsMappingDocument(src); got != want {
			t.Errorf("IsMappingDocument(%q) = %v, want %v", src, got, want)
		}
	}
	// The fixture the setup pipeline merges against is a mapping document.
	if !IsMappingDocument(readFixture(t, "unknown_keys.yaml")) {
		t.Error("unknown_keys.yaml must be a mapping document")
	}
	if IsMappingDocument(readFixture(t, "malformed.yaml")) {
		t.Error("malformed.yaml must not be a mapping document")
	}
}
