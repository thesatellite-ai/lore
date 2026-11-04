package attach

import (
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
)

// TestParseMatrix walks stores.yaml entries across the input space: every
// reserved name alone (refused) and as the base of a "<store>/<folder>"
// entry (a recorded folder target: allowed when the folder key repeats the
// folder), snake_case and odd names (empty, unicode, digits, huge,
// upper case, dashes), duplicates (also a folder target twice), unknown and
// misspelled keys, branch and folder keys, an empty file and an empty
// list. A url with a password parses (Parse never reads urls) but
// NormalizeURL refuses it without echoing it, and no Parse error echoes a
// url either.
func TestParseMatrix(t *testing.T) {
	secret := "tok" + strings.Repeat("Z", 9)
	entry := func(name, extra string) string {
		return "  - name: " + name + "\n    url: https://example.com/" + secret + ".git\n" + extra
	}
	type tc struct {
		doc string
		ok  bool
	}
	cases := map[string]tc{
		"empty file":               {"", true},
		"empty list":               {"stores: []\n", true},
		"plain":                    {"stores:\n" + entry("agency", ""), true},
		"digits after a letter":    {"stores:\n" + entry("kb2", ""), true},
		"folder key":               {"stores:\n" + entry("agency", "    folder: acme-hub\n"), true},
		"branch key":               {"stores:\n" + entry("agency", "    branch: staging\n"), true},
		"read_only key":            {"stores:\n" + entry("agency", "    read_only: true\n"), true},
		"folder target":            {"stores:\n" + entry("agency/acme", "    folder: acme\n"), true},
		"folder target, wrong key": {"stores:\n" + entry("agency/acme", "    folder: other\n"), false},
		"folder target, no key":    {"stores:\n" + entry("agency/acme", ""), false},
		"folder target, bad base":  {"stores:\n" + entry("Agency/acme", "    folder: acme\n"), false},
		"folder target, empty":     {"stores:\n" + entry("agency/", "    folder: \"\"\n"), false},
		"folder target twice":      {"stores:\n" + entry("agency/acme", "    folder: acme\n") + entry("agency/acme", "    folder: acme\n"), false},
		"duplicate":                {"stores:\n" + entry("agency", "") + entry("agency", ""), false},
		"store and its folder":     {"stores:\n" + entry("agency", "") + entry("agency/acme", "    folder: acme\n"), true},
		"empty name":               {"stores:\n" + entry(`""`, ""), false},
		"upper case":               {"stores:\n" + entry("Agency", ""), false},
		"dash":                     {"stores:\n" + entry("agency-ui", ""), false},
		"leading digit":            {"stores:\n" + entry("2kb", ""), false},
		"unicode":                  {"stores:\n" + entry("ägency", ""), false},
		"huge":                     {"stores:\n" + entry(strings.Repeat("a", 4096), ""), false},
		"unknown key":              {"stores:\n" + entry("agency", "    readonly: true\n"), false},
		"camelCase key":            {"stores:\n" + entry("agency", "    readOnly: true\n"), false},
		"top-level unknown":        {"store:\n" + entry("agency", ""), false},
		"no url":                   {"stores:\n  - name: agency\n", false},
		"url a list":               {"stores:\n  - name: agency\n    url: [a]\n", false},
		"read_only not a bool":     {"stores:\n" + entry("agency", "    read_only: maybe\n"), false},
		"two problems at once":     {"stores:\n" + entry("Bad", "") + "  - name: also_bad\n", false},
	}
	for _, r := range Reserved() {
		cases["reserved "+r] = tc{"stores:\n" + entry(r, ""), false}
	}
	for name, c := range cases {
		got, err := Parse([]byte(c.doc))
		if c.ok != (err == nil) {
			t.Errorf("%s: err = %v, want ok=%v", name, err, c.ok)
			continue
		}
		if err != nil {
			if !errs.Has(err, errs.ConfigInvalid) {
				t.Errorf("%s: %v, want E_CONFIG_INVALID", name, err)
			}
			if strings.Contains(err.Error(), secret) {
				t.Errorf("%s: the error echoes the url: %v", name, err)
			}
			continue
		}
		for _, s := range got.Stores {
			if s.URL == "" {
				t.Errorf("%s: a parsed entry lost its url", name)
			}
		}
	}
	if got, err := Parse([]byte("stores:\n" + entry("agency", "    branch: staging\n    folder: acme\n    read_only: true\n"))); err != nil ||
		got.Stores[0].Branch != "staging" || got.Stores[0].Folder != "acme" || !got.Stores[0].ReadOnly {
		t.Fatalf("keys not read: %+v %v", got, err)
	}
	two, err := Parse([]byte("stores:\n" + entry("Bad", "") + "  - name: also_bad\n"))
	if err == nil || !strings.Contains(err.Error(), "store 1") || !strings.Contains(err.Error(), "store 2") {
		t.Fatalf("every problem must be reported at once, by position: %+v %v", two, err)
	}
	pw := "https://bob:" + secret + "@example.com/kb.git"
	if _, err := Parse([]byte("stores:\n  - name: kb\n    url: " + pw + "\n")); err != nil {
		t.Fatalf("Parse reads no url: %v", err)
	}
	if _, err := NormalizeURL(pw); !errs.Has(err, errs.URLUserinfo) || strings.Contains(err.Error(), secret) {
		t.Fatalf("a url with a password: %v (must be E_URL_CREDENTIALS, without the password)", err)
	}
}
