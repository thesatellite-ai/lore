package orgconf_test

import (
	"strings"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/logic/orgconf"
	"github.com/thesatellite-ai/lore/logic/repourl"
)

// TestParse is the input matrix of config.yaml's org part: empty, valid,
// and every class of refusal (YAML, names, keys, stores, patterns), each
// refusal naming where the problem is.
func TestParse(t *testing.T) {
	cases := []struct {
		name    string
		yaml    string
		wantErr string // "" = valid; else a substring of the error
	}{
		{"empty", "", ""},
		{"no orgs", "identity:\n  key: human:a@example.com\n", ""},
		{"one org", "orgs:\n  - name: acme\n    identity: {key: human:a@acme.com}\n    stores: [{url: git@github.com:acme/lore-store.git}]\n    own: [\"github.com/acme/*\"]\n", ""},
		{"org folder root", "orgs:\n  - name: acme\n    stores: [{url: git@github.com:acme/s.git, org_folder: .}]\n", ""},
		{"bad yaml", "orgs: [\n", "not valid YAML"},
		{"bad org name", "orgs:\n  - name: Acme\n", `orgs[0] "Acme"`},
		{"reserved org name", "orgs:\n  - name: repo\n", "--to target"},
		{"duplicate org", "orgs:\n  - name: acme\n  - name: acme\n", "already used by orgs[0]"},
		{"bad identity key", "identity:\n  key: a@example.com\n", "identity.key"},
		{"bad handle", "identity:\n  handle: \"@aman\"\n", "identity.handle"},
		{"bad store url", "orgs:\n  - name: acme\n    stores: [{url: \"not a url\"}]\n", "stores[0].url"},
		{"two org folders", "orgs:\n  - name: acme\n    stores: [{url: git@github.com:acme/a.git, org_folder: org}, {url: git@github.com:acme/b.git, org_folder: co}]\n", "at most one store"},
		{"same store twice", "orgs:\n  - name: acme\n    stores: [{url: git@github.com:acme/a.git}, {url: https://github.com/acme/a}]\n", "same store and branch"},
		{"bad own pattern", "orgs:\n  - name: acme\n    own: [\"\"]\n", "own[0]"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, err := orgconf.Parse([]byte(tc.yaml))
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("Parse: %v", err)
				}
				if c == nil {
					t.Fatal("Parse returned a nil config without an error")
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("Parse error = %v, want it to contain %q", err, tc.wantErr)
			}
		})
	}
}

// TestFindOrgAndStoreFor: lookups by name and by normalised store url;
// misses are nil, never a guess.
func TestFindOrgAndStoreFor(t *testing.T) {
	c, err := orgconf.Parse([]byte("orgs:\n  - name: acme\n    stores: [{url: git@github.com:acme/s.git, branch: staging}]\n  - name: beta\n"))
	if err != nil {
		t.Fatal(err)
	}
	if got := c.OrgNames(); strings.Join(got, ",") != "acme,beta" {
		t.Fatalf("OrgNames = %v", got)
	}
	o := c.FindOrg("acme")
	if o == nil || c.FindOrg("nobody") != nil {
		t.Fatalf("FindOrg: acme=%v, nobody should be nil", o)
	}
	norm, err := repourl.Normalize("https://github.com/acme/s")
	if err != nil {
		t.Fatal(err)
	}
	s := o.StoreFor(norm)
	if s == nil || s.EffectiveBranch() != "staging" {
		t.Fatalf("StoreFor(%q) = %+v, want the staging store", norm, s)
	}
	if o.StoreFor("github.com/other/s") != nil {
		t.Fatal("StoreFor matched a store the entry does not list")
	}
	if (orgconf.Store{URL: "x"}).EffectiveBranch() != orgconf.DefaultStoreBranch {
		t.Fatal("an unset branch is not the default branch")
	}
}

// TestParseOrgFile: the committed .lore/org.yaml holds names only.
func TestParseOrgFile(t *testing.T) {
	cases := []struct {
		name, yaml string
		ok         bool
	}{
		{"org only", "org: acme\n", true},
		{"org and project", "org: acme\nproject: web\n", true},
		{"missing org", "project: web\n", false},
		{"bad project", "org: acme\nproject: \"a/b\"\n", false},
		{"bad yaml", "org: [\n", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := orgconf.ParseOrgFile([]byte(tc.yaml))
			if (err == nil) != tc.ok {
				t.Fatalf("ParseOrgFile ok=%v, err=%v", tc.ok, err)
			}
		})
	}
}

// TestParseDuration: days, bare seconds, Go syntax; negatives, empty and
// junk are refused.
func TestParseDuration(t *testing.T) {
	cases := []struct {
		in   string
		want time.Duration
		ok   bool
	}{
		{"7d", 7 * orgconf.Day, true},
		{"0d", 0, true},
		{"90", 90 * time.Second, true},
		{"36h", 36 * time.Hour, true},
		{" 1m ", time.Minute, true},
		{"", 0, false},
		{"-1", 0, false},
		{"-2d", 0, false},
		{"xd", 0, false},
		{"soon", 0, false},
	}
	for _, tc := range cases {
		d, err := orgconf.ParseDuration(tc.in)
		if (err == nil) != tc.ok || (tc.ok && d.D() != tc.want) {
			t.Errorf("ParseDuration(%q) = %v, %v; want %v ok=%v", tc.in, d.D(), err, tc.want, tc.ok)
		}
	}
}

// TestWindowsUnsafeFolderNames (B-XOS-1): a store folder or org name that
// Windows cannot check out (a device name in any case, with or without an
// extension, or a trailing '.') is refused on every system; names that
// only contain such a word pass.
func TestWindowsUnsafeFolderNames(t *testing.T) {
	t.Parallel()
	for name, bad := range map[string]bool{
		"con": true, "CON": true, "nul.txt": true, "Aux": true, "com1": true, "lpt9.x": true, "prn": true,
		"web.": true, "a..": true,
		"console": false, "nul-web": false, "com10": false, "lpt": false, "my.con": false, "web": false, "v1.2": false,
	} {
		err := orgconf.ValidateFolderName(name)
		if (err != nil) != bad {
			t.Errorf("ValidateFolderName(%q) = %v, want refused=%v", name, err, bad)
		}
		if bad && err != nil && !strings.Contains(err.Error(), "Windows") {
			t.Errorf("ValidateFolderName(%q) = %v, want the Windows reason", name, err)
		}
	}
	_, err := orgconf.Parse([]byte("orgs:\n  - name: nul\n    match: [\"github.com/x/*\"]\n    stores:\n      - url: git@github.com:x/s.git\n"))
	if err == nil || !strings.Contains(err.Error(), "Windows") {
		t.Errorf("org named nul: %v, want refused for Windows", err)
	}
}
