package stores

import (
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
)

// TestCheckName: a store name is snake_case and not reserved (repo, me,
// team, company); `me` passes only where the caller allows the personal
// store (store init me).
func TestCheckName(t *testing.T) {
	for _, c := range []struct {
		name    string
		allowMe bool
		ok      bool
	}{
		{"agency", false, true}, {"acme_hub", false, true}, {"a1", false, true},
		{"me", true, true}, {"me", false, false},
		{"repo", true, false}, {"team", false, false}, {"company", false, false},
		{"", false, false}, {"Agency", false, false}, {"acme-hub", false, false}, {"agency/org", false, false},
		{"../x", false, false}, {"ägency", false, false}, {"a b", false, false}, {strings.Repeat("a", 300), false, false},
	} {
		err := CheckName(c.name, c.allowMe)
		if c.ok != (err == nil) {
			t.Errorf("CheckName(%q, %v) = %v, want ok=%v", c.name, c.allowMe, err, c.ok)
		}
		if err != nil && !errs.Has(err, errs.InvalidInput) {
			t.Errorf("CheckName(%q): code %s, want E_INVALID_INPUT", c.name, errs.CodeOf(err))
		}
	}
}

// BATCH2 #9: store init without a url is E_INVALID_INPUT naming the
// missing url and the usage; only `me` has a default url.
func TestMissingInitURL(t *testing.T) {
	err := MissingInitURL("agency")
	if !errs.Has(err, errs.InvalidInput) {
		t.Fatalf("code %s, want E_INVALID_INPUT", errs.CodeOf(err))
	}
	for _, want := range []string{"store init agency", "url is missing", "lore store init <name> <url>"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("%q lacks %q", err.Error(), want)
		}
	}
}
