package wire

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestResourceNamingRemote: a resource row naming the repo's remote in any
// spelling (ssh, https, nested in a list) is found with its name; one
// naming another repo is not (devco D-556).
func TestResourceNamingRemote(t *testing.T) {
	dir := t.TempDir()
	mine := map[string]bool{"github.com/acme-co/web": true}
	write := func(name, body string) string {
		p := filepath.Join(dir, name+".json")
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	cases := []struct {
		body, name string
		ok         bool
	}{
		{`{"id":"res_1","name":"web_app","fields":{"url":"git@github.com:acme-co/web.git"}}`, "web_app", true},
		{`{"id":"res_2","name":"mirror","fields":{"urls":["https://example.org/x","https://github.com/acme-co/web"]}}`, "mirror", true},
		{`{"id":"res_3","name":"api","fields":{"url":"git@github.com:acme-co/api.git"}}`, "", false},
		{`not json`, "", false},
	}
	for i, c := range cases {
		p := write(strings.Repeat("r", i+1), c.body)
		name, remote, ok := resourceNamingRemote(p, mine)
		if ok != c.ok || name != c.name || (ok && remote != "github.com/acme-co/web") {
			t.Errorf("case %d: got (%q, %q, %v), want (%q, …, %v)", i, name, remote, ok, c.name, c.ok)
		}
	}
	note := resourceFolder{Org: "acme", Folder: "sync", Resource: "web_app", Remote: "github.com/acme-co/web"}.note()
	for _, w := range []string{"read acme/sync", "lore org link --org acme --project sync --own"} {
		if !strings.Contains(note, w) {
			t.Errorf("note %q lacks %q", note, w)
		}
	}
}
