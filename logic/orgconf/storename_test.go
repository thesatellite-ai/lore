package orgconf

import (
	"strings"
	"testing"
)

// TestStoreShortName: the short name is stores[].name when set, else the
// repo name of the url without .git, in lower case, in every url form.
func TestStoreShortName(t *testing.T) {
	for _, c := range []struct {
		s    Store
		want string
	}{
		{Store{URL: "git@github.com:acme/lore-x.git"}, "lore-x"},
		{Store{URL: "git@github.com:acme/lore-x"}, "lore-x"},
		{Store{URL: "ssh://git@git.example.test:2222/acme/Notes.git"}, "notes"},
		{Store{URL: "https://git.example.test/acme/team/lore-y.git/"}, "lore-y"},
		{Store{URL: "https://git.example.test/acme/lore-z.git?branch=staging"}, "lore-z"},
		{Store{URL: "file:///srv/stores/kit"}, "kit"},
		{Store{URL: "git@github.com:acme/lore-x.git", Name: "notes"}, "notes"},
	} {
		if got := c.s.ShortName(); got != c.want {
			t.Errorf("ShortName(%+v) = %q, want %q", c.s, got, c.want)
		}
	}
}

// TestShortNameDuplicateRefused: two stores of one org with the same
// short name are refused with both urls and the key to set; stores[].name
// tells them apart; one store on two branches is one name, no refusal.
func TestShortNameDuplicateRefused(t *testing.T) {
	a, b := "git@github.com:acme/lore-x.git", "git@git.example.test:other/lore-x.git"
	cfg := `orgs:
  - name: acme
    stores:
      - url: ` + a + `
        org_folder: org
      - url: ` + b + `
`
	_, err := Parse([]byte(cfg))
	if err == nil {
		t.Fatal("two stores named lore-x were accepted")
	}
	for _, want := range []string{a, b, `"lore-x"`, "set stores[].name in lore-org.yaml"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal %q does not name %q", err, want)
		}
	}
	if _, err := Parse([]byte(cfg + "        name: other-x\n")); err != nil {
		t.Errorf("stores[].name did not tell them apart: %v", err)
	}
	branches := `orgs:
  - name: acme
    stores:
      - url: ` + a + `
      - url: ` + a + `
        branch: staging
`
	if _, err := Parse([]byte(branches)); err != nil {
		t.Errorf("one store on two branches was refused: %v", err)
	}
}

// TestShortNameRules: a set name must be one lower-case word that is not
// reserved and not url-shaped.
func TestShortNameRules(t *testing.T) {
	for _, bad := range []string{"team", "me", "repo", "Lore-X", "a/b", "_x", "git@x", ""} {
		if ValidateStoreShortName(bad) == nil {
			t.Errorf("ValidateStoreShortName(%q) = nil, want a refusal", bad)
		}
	}
	for _, ok := range []string{"lore-x", "notes", "kit_2", "a.b"} {
		if err := ValidateStoreShortName(ok); err != nil {
			t.Errorf("ValidateStoreShortName(%q) = %v", ok, err)
		}
	}
	cfg := "orgs:\n  - name: acme\n    stores:\n      - url: git@github.com:acme/x.git\n        name: team\n"
	if _, err := Parse([]byte(cfg)); err == nil || !strings.Contains(err.Error(), "stores[0].name") {
		t.Errorf("a reserved stores[].name was accepted or not located: %v", err)
	}
}

// TestStoreByShortName finds a store by its short name.
func TestStoreByShortName(t *testing.T) {
	o := Org{Name: "acme", Stores: []Store{{URL: "git@github.com:acme/team.git"}, {URL: "git@github.com:acme/lore-x.git"}, {URL: "git@github.com:acme/y.git", Name: "why"}}}
	if s := o.StoreByShortName("lore-x"); s == nil || s.URL != o.Stores[1].URL {
		t.Errorf("lore-x = %+v", s)
	}
	if s := o.StoreByShortName("why"); s == nil || s.URL != o.Stores[2].URL {
		t.Errorf("why = %+v", s)
	}
	if s := o.StoreByShortName("y"); s != nil {
		t.Errorf("a renamed store still answers its repo name: %+v", s)
	}
}
