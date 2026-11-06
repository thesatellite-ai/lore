package wire

import (
	"testing"

	"github.com/thesatellite-ai/lore/engine/attach"
)

// TestAttachLocationMatrix: every spelling of one store folder gives one
// engine.Attached.Location (so prepareTarget reuses an attachment and the
// engine counts a row there once), and a different folder, branch or store
// gives a different one. The root is the same whether written "", "." or
// carried in the url's ?folder=.
func TestAttachLocationMatrix(t *testing.T) {
	t.Parallel()
	const gitURL = "https://example.com/acme/store.git"
	scp := "git@example.com:acme/store.git"
	at := func(s attach.Store, f string) string {
		t.Helper()
		got, err := attachLocation(s, f)
		if err != nil {
			t.Fatalf("attachLocation(%+v, %q): %v", s, f, err)
		}
		return got
	}
	base := at(attach.Store{URL: gitURL}, "globex")
	same := []struct {
		name string
		s    attach.Store
		f    string
	}{
		{"same url and folder", attach.Store{URL: gitURL}, "globex"},
		{"another entry name", attach.Store{Name: "northwind/globex", URL: gitURL}, "globex"},
		{"url already carries ?folder=", attach.Store{URL: gitURL + "?folder=globex"}, ""},
	}
	for _, c := range same {
		if got := at(c.s, c.f); got != base {
			t.Errorf("%s: %q, want %q", c.name, got, base)
		}
	}
	differ := []struct {
		name string
		s    attach.Store
		f    string
	}{
		{"another folder", attach.Store{URL: gitURL}, "acme"},
		{"the root", attach.Store{URL: gitURL}, ""},
		{"another branch", attach.Store{URL: gitURL, Branch: "next"}, "globex"},
		{"another store", attach.Store{URL: "https://example.com/acme/other.git"}, "globex"},
		{"unicode folder", attach.Store{URL: gitURL}, "glöbex"},
	}
	for _, c := range differ {
		if got := at(c.s, c.f); got == base {
			t.Errorf("%s: %q must differ from %q", c.name, got, base)
		}
	}
	roots := []string{at(attach.Store{URL: gitURL}, ""), at(attach.Store{URL: gitURL}, "."), at(attach.Store{URL: gitURL + "?folder=."}, "")}
	for _, r := range roots[1:] {
		if r != roots[0] {
			t.Errorf("root spellings differ: %q", roots)
		}
	}
	if a, b := at(attach.Store{URL: scp}, "x"), at(attach.Store{URL: "ssh://git@example.com/acme/store.git"}, "x"); a != b {
		t.Errorf("scp and ssh:// spellings of one store differ: %q %q", a, b)
	}
	// OV4: a credential is cleaned on read, so the store keeps one location
	if a, b := at(attach.Store{URL: "https://user:" + "pw@example.com/x.git"}, "f"), at(attach.Store{URL: "https://example.com/x.git"}, "f"); a != b {
		t.Errorf("a url with a password must map to the clean url's location: %q %q", a, b)
	}
}
