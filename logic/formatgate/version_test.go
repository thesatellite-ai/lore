package formatgate

import "testing"

// TestVersionBelowDevBuilds (FMT-DEV): a development or snapshot build is
// never below a floor; a release compares numerically; an unparsable
// release counts as below (the safe direction: ask once too often).
func TestVersionBelowDevBuilds(t *testing.T) {
	cases := []struct {
		v, floor string
		below    bool
	}{
		{"dev", "0.1.18", false},
		{"0.1.0-dev", "0.1.18", false},
		{"v0.1.17-dev", "0.1.18", false},
		{"0.1.18-SNAPSHOT-abc", "0.1.18", false},
		{"0.1.17-snapshot", "0.1.18", false},
		{"0.1.17", "0.1.18", true},
		{"v0.1.17", "0.1.18", true},
		{"0.1.18", "0.1.18", false},
		{"0.2.0", "0.1.18", false},
		{"garbage", "0.1.18", true},
		{"develop", "0.1.18", true},
		{"0.1.17", "not-a-version", false},
		// Source builds stamped by git describe with no release tag near.
		{"archive/2026-11-08d-issues-2-gdcb78a91-dirty", "0.1.18", false},
		{"archive/2026-11-08d-issues-2-gdcb78a91", "0.1.18", false},
		{"dcb78a91", "0.1.18", false},
		{"dcb78a91-dirty", "0.1.18", false},
		// A describe stamp past a release tag is a source build too.
		{"0.1.17-34-gabc1234", "0.1.18", false},
		{"v0.1.17-100-ge2aad585", "0.1.18", false},
		{"v0.1.17-100-ge2aad585-dirty", "0.1.18", false},
		{"archive/x-2-gnothex", "0.1.18", true},
	}
	for _, c := range cases {
		if got := VersionBelow(c.v, c.floor); got != c.below {
			t.Errorf("VersionBelow(%q, %q) = %v, want %v", c.v, c.floor, got, c.below)
		}
	}
}
