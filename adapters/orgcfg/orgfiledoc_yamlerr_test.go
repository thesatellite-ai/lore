package orgcfg

import (
	"strings"
	"testing"
)

// TestParseOrgFileDocWrongKindNamesLineAndKey: a store's lore-org.yaml with
// a value of the wrong kind names the line and key path (top level, in a
// store entry, under defaults), never a Go type or the value; unknown keys
// (also with a space) are ignored.
func TestParseOrgFileDocWrongKindNamesLineAndKey(t *testing.T) {
	for body, want := range map[string]string{
		"name: [a]\n": "line 1: name: expected text, got a list",
		"stores:\n  - url: file:///x\n    branch: [m]\n":   "line 3: stores[0].branch: expected text, got a list",
		"stores: https://user:" + "pw@example.com/s.git\n": "line 1: stores: expected a list, got text",
		"defaults:\n  client_repos: [a]\n":                 "line 2: defaults.client_repos: expected a mapping (key: value lines), got a list",
		"name: acme\nmy key: 1\n":                          "",
	} {
		_, err := parseOrgFileDoc(body)
		if want == "" {
			if err != nil {
				t.Errorf("%q: %v, unknown keys are ignored", body, err)
			}
			continue
		}
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: %v, want %q", body, err, want)
			continue
		}
		for _, leak := range []string{"not found in type", "cannot unmarshal", "orgcfg.", "!!", "`", "pw@"} {
			if strings.Contains(err.Error(), leak) {
				t.Errorf("%q: %q leaks %q", body, err, leak)
			}
		}
	}
}

// TestOrgFileDocShortNames: a lore-org.yaml with two stores of one short
// name is refused with both urls and the key to set; stores[].name is read
// and tells them apart.
func TestOrgFileDocShortNames(t *testing.T) {
	a, b := "git@git.example.test:acme/lore-x.git", "git@git.example.test:other/lore-x.git"
	doc, err := parseOrgFileDoc("name: acme\nstores:\n  - url: " + a + "\n  - url: " + b + "\n")
	if err != nil {
		t.Fatal(err)
	}
	err = doc.checkShortNames()
	if err == nil {
		t.Fatal("two lore-x stores accepted")
	}
	for _, want := range []string{a, b, "set stores[].name in lore-org.yaml"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal %q lacks %q", err, want)
		}
	}
	doc, err = parseOrgFileDoc("name: acme\nstores:\n  - url: " + a + "\n  - url: " + b + "\n    name: other-x\n")
	if err != nil || doc.Stores[1].Name != "other-x" {
		t.Fatalf("stores[].name not read: %+v %v", doc, err)
	}
	if err := doc.checkShortNames(); err != nil {
		t.Errorf("named stores refused: %v", err)
	}
	if got := (Suggestion{Doc: doc}).Shared().Stores[1].Name; got != "other-x" {
		t.Errorf("the org entry copy lost stores[].name: %q", got)
	}
}
