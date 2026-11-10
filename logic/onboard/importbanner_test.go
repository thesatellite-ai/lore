package onboard

import (
	"strings"
	"testing"
)

// The banner never hides an older install it found (realmac #20), says why
// a proposal was renamed (#22 name collision), and its dismiss key follows
// what it says (polish item 3).
func TestImportBannerCoveredAndRenamed(t *testing.T) {
	legacy := []string{"~/.lore/identity.yaml.migrated"}
	covered := []string{"personal (setup personal is a solo setup with the same personal store)"}
	for _, c := range []struct {
		name    string
		f       BannerFacts
		has     []string
		isEmpty bool
	}{
		{"nothing at all", BannerFacts{}, nil, true},
		{"covered, no older install: lore's own state, silent", BannerFacts{Covered: covered, IdentityName: "A"}, nil, true},
		{"older install, all covered", BannerFacts{Legacy: true, LegacyFiles: legacy, Covered: covered},
			[]string{"Found an existing lore install (~/.lore/identity.yaml.migrated)", "nothing to import", "setup personal is a solo setup"}, false},
		{"older install, nothing covered, nothing proposed", BannerFacts{Legacy: true, LegacyFiles: legacy}, nil, true},
		{"renamed, older install", BannerFacts{Legacy: true, Proposals: []string{"personal-2"}, Renamed: map[string]string{"personal-2": "personal is taken by a setup in the trash"}},
			[]string{"import 1 setup (personal-2: personal is taken by a setup in the trash)?"}, false},
		{"renamed, config only", BannerFacts{PersonalStore: "git@github.com:o/r.git", Proposals: []string{"personal-2"}, Renamed: map[string]string{"personal-2": "personal is taken by an existing setup"}},
			[]string{"make 1 setup (personal-2: personal is taken by an existing setup) from it?"}, false},
		{"not renamed", BannerFacts{PersonalStore: "git@github.com:o/r.git", Proposals: []string{"personal"}}, []string{"make a setup from it?"}, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			got := ImportBanner(c.f)
			if (got == "") != c.isEmpty {
				t.Fatalf("banner = %q, want empty %v", got, c.isEmpty)
			}
			for _, h := range c.has {
				if !strings.Contains(got, h) {
					t.Fatalf("banner = %q, want it to contain %q", got, h)
				}
			}
		})
	}
	// Dismissed once, the covered line stays dismissed while the install it
	// found is the same, however the covering setup is worded; a new file
	// of the old install, or a real proposal, brings a line back.
	a := BannerFacts{Legacy: true, LegacyFiles: legacy, Covered: covered}
	b := BannerFacts{Legacy: true, LegacyFiles: legacy, Covered: []string{"personal (disabled) already has the same identity; enable it with: lore setup enable personal"}}
	c := BannerFacts{Legacy: true, LegacyFiles: append(legacy, "~/.lore/identity.toml"), Covered: covered}
	d := BannerFacts{Legacy: true, LegacyFiles: legacy}
	if ImportBannerKey(a) != ImportBannerKey(b) {
		t.Fatal("covered key follows the covering setup's wording")
	}
	if ImportBannerKey(a) == ImportBannerKey(c) || ImportBannerKey(a) == ImportBannerKey(d) {
		t.Fatal("covered key ignores the install found or the covered state")
	}
}
