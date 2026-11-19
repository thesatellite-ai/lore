package wire

import (
	"testing"

	"github.com/thesatellite-ai/lore/adapters/orgcfg"
	"github.com/thesatellite-ai/lore/logic/orgconf"
	"github.com/thesatellite-ai/lore/logic/orgresolve"
	"github.com/thesatellite-ai/lore/logic/repourl"
)

// TestLinkTargetNamesTheLinksFolder is devco:D-670: the --to a refusal in a
// linked repo prints for a folder that is not attached is the link's own
// store (by its short name, which --to resolves from config.yaml) and
// folder: the default link first; nothing when the link's store is in no
// org entry, so no command that cannot work is printed.
func TestLinkTargetNamesTheLinksFolder(t *testing.T) {
	const url = "git@git.example.test:acme/kb-acme.git"
	norm, err := repourl.NormalizeStore(url)
	if err != nil {
		t.Fatal(err)
	}
	cfg := &orgconf.Config{Orgs: []orgconf.Org{{Name: "acme", Stores: []orgconf.Store{{URL: url}}}}}
	sit := func(links ...orgresolve.Link) *OrgResolution {
		return &OrgResolution{done: true, sit: &orgcfg.Situation{Config: cfg, Result: orgresolve.Result{Links: links}}}
	}
	cases := []struct {
		name string
		r    *OrgResolution
		want string
	}{
		{"one link", sit(orgresolve.Link{Entry: "acme", StoreURL: norm, Folder: "web"}), "kb-acme/web"},
		{"default wins", sit(orgresolve.Link{Entry: "acme", StoreURL: norm, Folder: "api"}, orgresolve.Link{Entry: "acme", StoreURL: norm, Folder: "web", Default: true}), "kb-acme/web"},
		{"store not in the entry", sit(orgresolve.Link{Entry: "acme", StoreURL: "git.example.test/other/kb", Folder: "web"}), ""},
		{"entry not configured", sit(orgresolve.Link{Entry: "globex", StoreURL: norm, Folder: "web"}), ""},
		{"no link", sit(), ""},
	}
	for _, c := range cases {
		if got := c.r.LinkTarget(t.Context()); got != c.want {
			t.Errorf("%s: LinkTarget = %q, want %q", c.name, got, c.want)
		}
	}
}
