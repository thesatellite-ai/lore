package wire

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// TestPatternOwner: the literal owner (and name) of every repo pattern form
// config.yaml takes; a wildcard segment is never a name (PL-PORT-1).
func TestPatternOwner(t *testing.T) {
	t.Parallel()
	for _, c := range []struct{ pat, owner, name string }{
		{"github.com/betalabs/*", "betalabs", ""},
		{"github.com/Acme/App", "acme", "app"},
		{"https://github.com/acme/app.git", "acme", "app"},
		{"git@github.com:acme/app.git", "acme", "app"},
		{"ssh://git@github.com/acme/app", "acme", "app"},
		{"github.com/*/app", "", ""},
		{"github.com/acme-*/x", "", ""},
		{"github.com", "", ""},
		{"", "", ""},
	} {
		o, n := patternOwner(c.pat)
		if o != c.owner || n != c.name {
			t.Errorf("patternOwner(%q) = %q, %q; want %q, %q", c.pat, o, n, c.owner, c.name)
		}
	}
}

// TestMentions: whole words only, any case, sorted, deduplicated.
func TestMentions(t *testing.T) {
	t.Parallel()
	names := []string{"betalabs", "acme", "acme/app", "Betalabs", ""}
	for _, c := range []struct {
		text string
		want []string
	}{
		{"Betalabs's invoices go to finance@", []string{"betalabs"}},
		{"betalabstools is a word", nil},
		{"see acme/app and ACME", []string{"acme", "acme/app"}},
		{"nothing here", nil},
		{"", nil},
	} {
		if got := mentions(c.text, names); !slices.Equal(got, c.want) {
			t.Errorf("mentions(%q) = %v; want %v", c.text, got, c.want)
		}
	}
}

// TestKnownOrgNames: org names plus literal owners of own:/repos:; a
// missing config lists nothing; bad YAML is refused.
func TestKnownOrgNames(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	if got, err := knownOrgNames(home); err != nil || len(got) != 0 {
		t.Fatalf("no config: %v %v", got, err)
	}
	cfg := "orgs:\n  - name: northwind\n    own: [github.com/northwind/*]\n    repos: [github.com/acme/app, github.com/*/x]\n  - name: betalabs\n"
	if err := os.WriteFile(filepath.Join(home, ConfigFile), []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := knownOrgNames(home)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"acme", "acme/app", "betalabs", "northwind"}; !slices.Equal(got, want) {
		t.Fatalf("knownOrgNames = %v; want %v", got, want)
	}
	if err := os.WriteFile(filepath.Join(home, ConfigFile), []byte("orgs: [\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := knownOrgNames(home); err == nil {
		t.Fatal("bad YAML must be refused")
	}
}
