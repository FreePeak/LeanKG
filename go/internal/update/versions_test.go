package update

import "testing"

func TestCompareVersions(t *testing.T) {
	for _, tc := range []struct {
		a, b string
		want int
	}{
		{"0.31.0", "0.31.0", 0},
		{"v0.31.0", "0.31.0", 0},       // tags are v-prefixed; the prefix must not matter
		{"0.31.0", "v0.31.0", 0},       // either side
		{"0.31", "0.31.0", 0},          // a missing component reads as 0
		{"0.31.0+build7", "0.31.0", 0}, // build metadata is not precedence
		{" 0.31.0 ", "0.31.0", 0},      // trimmed
		{"0.31.1", "0.31.0", 1},        // newer patch
		{"0.32.0", "0.31.9", 1},        // newer minor outranks a patch
		{"1.0.0", "0.31.0", 1},         // newer major
		{"0.30.9", "0.31.0", -1},       // older
		{"0.9.0", "0.10.0", -1},        // numeric, not lexical
		{"1.0.0", "1.0.0-rc1", 1},      // a release outranks its prerelease
		{"1.0.0-rc1", "1.0.0", -1},     // and loses to it
		{"1.0.0-rc1", "1.0.0-rc2", -1}, // prerelease labels order lexically
		{"1.0.0-rc1", "1.0.0-rc1", 0},  // identical prerelease
	} {
		got, err := Compare(tc.a, tc.b)
		if err != nil {
			t.Errorf("Compare(%q, %q): unexpected error: %v", tc.a, tc.b, err)
			continue
		}
		if got != tc.want {
			t.Errorf("Compare(%q, %q) = %d, want %d", tc.a, tc.b, got, tc.want)
		}
	}
}

// TestCompareRejectsUnknownVersions is the safety half of the contract: an
// unparseable version is an error, never a silent 0, because "equal" would let a
// dev build believe it matches every release.
func TestCompareRejectsUnknownVersions(t *testing.T) {
	for _, tc := range []struct{ a, b string }{
		{"", "0.31.0"},
		{"   ", "0.31.0"},
		{"dev", "0.31.0"},
		{"0.31.0", "unknown"},
		{"0.31.0.1", "0.31.0"}, // four components is not semver
		{"0.31.x", "0.31.0"},
		{"1.-2.3", "1.0.0"},
	} {
		if got, err := Compare(tc.a, tc.b); err == nil {
			t.Errorf("Compare(%q, %q) = %d with no error; an unparseable version must fail loudly", tc.a, tc.b, got)
		}
	}
}
