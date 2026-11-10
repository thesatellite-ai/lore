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
