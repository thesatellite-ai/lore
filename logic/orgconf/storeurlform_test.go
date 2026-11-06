package orgconf_test

import (
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/logic/orgconf"
)

// TestStoreURLForm (D-DOCS-1): an org store url and personal.store must be
// an address git can fetch (scheme or scp form); the bare host/path id form
// is refused when the config is read, naming the key and the two forms.
func TestStoreURLForm(t *testing.T) {
	org := func(u string) string { return "orgs:\n  - name: acme\n    stores: [{url: \"" + u + "\"}]\n" }
	personal := func(u string) string { return "personal:\n  store: \"" + u + "\"\n" }
	for _, c := range []struct {
		name, yaml, wantErr string
	}{
		{"org scp", org("git@github.com:acme/notes"), ""},
		{"org ssh", org("ssh://git@github.com/acme/notes"), ""},
		{"org https", org("https://github.com/acme/notes"), ""},
		{"org host/path", org("github.com/acme/notes"), "stores[0].url"},
		{"org host/path .git", org("github.com/acme/notes.git"), "stores[0].url"},
		{"personal scp", personal("git@github.com:me/lore-me"), ""},
		{"personal https", personal("https://github.com/me/lore-me"), ""},
		{"personal folder", personal("file:///space/lore/me"), ""},
		{"personal host/path", personal("github.com/me/lore-me"), "personal.store"},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, err := orgconf.Parse([]byte(c.yaml))
			if c.wantErr == "" {
				if err != nil {
					t.Fatalf("Parse: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("Parse accepted a host/path store url")
			}
			for _, want := range []string{c.wantErr, "git@host:owner/repo", "https://host/owner/repo"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q does not name %q", err, want)
				}
			}
		})
	}
}
