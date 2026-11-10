package orgconf

import (
	"strings"
	"testing"
)

// TestParseWrongKindNamesLineAndKey: a value of the wrong kind in
// config.yaml or org.yaml names its line and key path, top level and
// nested (an org entry's block), never a Go type, yaml's wording or the
// value; unknown keys (also with a space) are not Parse's business (config
// check warns about them) and a syntax error stays "not valid YAML".
func TestParseWrongKindNamesLineAndKey(t *testing.T) {
	cases := []struct {
		name, body, want string
	}{
		{"top level", "upgrade_notice: [a]\n", "line 1: upgrade_notice: expected true or false, got a list"},
		{"nested block", "live:\n  autopush:\n    x: 1\n", "line 3: live.autopush: expected true or false, got a mapping"},
		{"orgs not a list", "orgs: acme\n", "line 1: orgs: expected a list, got text"},
		{"in an org entry", "orgs:\n  - name: acme\n    live:\n      autopush: perhaps\n", "line 4: orgs[0].live.autopush: expected true or false, got text"},
		{"block for a mapping", "live: on\n", "line 1: live: expected a mapping (key: value lines), got text"},
		{"syntax error", "orgs: [\n", "not valid YAML"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := Parse([]byte(c.body))
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("Parse(%q) = %v, want %q", c.body, err, c.want)
			}
			for _, leak := range []string{"not found in type", "cannot unmarshal", "orgconf.", "!!", "`"} {
				if strings.Contains(err.Error(), leak) {
					t.Errorf("error %q leaks %q", err, leak)
				}
			}
		})
	}
	for _, body := range []string{"my key: 1\n", "live:\n  fetch tll: 5m\n"} {
		if _, err := Parse([]byte(body)); err != nil {
			t.Errorf("an unknown key is left to config check: %q -> %v", body, err)
		}
	}
	for body, want := range map[string]string{
		"org: [a]\n": "line 1: org: expected text, got a list",
		"org: [\n":   "not valid YAML",
	} {
		_, err := ParseOrgFile([]byte(body))
		if err == nil || !strings.Contains(err.Error(), want) || strings.Contains(err.Error(), "orgconf.OrgFile") || strings.Contains(err.Error(), "!!") {
			t.Errorf("ParseOrgFile(%q) = %v, want %q", body, err, want)
		}
	}
}
