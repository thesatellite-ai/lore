package orgcfg

import (
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/logic/orgconf"
)

// TestLinkedLaterFolderGetsOrgFloor (devco:D-735): a repo linked after the
// org declared min_lore_version gets a folder whose _meta.json declares
// it, so an older lore is read-only there as in every folder that existed
// at the declaration; an org with no floor writes none, as before.
func TestLinkedLaterFolderGetsOrgFloor(t *testing.T) {
	marker := map[string]string{"lore-store.yaml": nmMarker}
	for _, c := range []struct{ name, floor string }{{"org floor", "0.1.20"}, {"no org floor", ""}} {
		t.Run(c.name, func(t *testing.T) {
			w := newNoMarkerWorld(t, marker)
			w.links.Config.Orgs[0].MinLoreVersion = c.floor
			res, err := w.linkWith(t, "webapp", false)
			if err != nil || !res.CreatedFolder {
				t.Fatalf("link: created=%v err=%v", res.CreatedFolder, err)
			}
			doc := testGit(t, w.origin, "show", "main:webapp/"+rowfs.MetaFileName)
			got, err := rowfs.FloorOfDoc([]byte(doc), "webapp/"+rowfs.MetaFileName)
			if err != nil || got != c.floor {
				t.Fatalf("the linked folder's min_lore_version = %q (err %v), want %q:\n%s", got, err, c.floor, doc)
			}
		})
	}
}

// TestRaiseOrgFileFloor (devco:D-735): lore-org.yaml records the org's
// floor beside its other keys (added when absent, raised, never lowered),
// a member's org entry copies it on refresh, and an older lore's lenient
// read of the file ignores the key.
func TestRaiseOrgFileFloor(t *testing.T) {
	raw := []byte("name: acme\nstores:\n  - url: git@example.test:acme/lore.git\n    org_folder: org\nown:\n  - example.test/acme/*\n")
	out, changed, err := raiseOrgFileFloor(raw, "0.1.20")
	if err != nil || !changed || !strings.Contains(string(out), "min_lore_version: 0.1.20") || !strings.Contains(string(out), "own:\n    - example.test/acme/*") {
		t.Fatalf("add: changed=%v err=%v\n%s", changed, err, out)
	}
	if _, changed, err = raiseOrgFileFloor(out, "0.1.19"); err != nil || changed {
		t.Fatalf("a lower floor changed the file: changed=%v err=%v", changed, err)
	}
	if _, changed, _ = raiseOrgFileFloor(out, "0.1.20"); changed {
		t.Fatal("the same floor changed the file")
	}
	up, changed, err := raiseOrgFileFloor(out, "0.1.21")
	if err != nil || !changed || !strings.Contains(string(up), "min_lore_version: 0.1.21") || strings.Contains(string(up), "0.1.20") {
		t.Fatalf("raise: changed=%v err=%v\n%s", changed, err, up)
	}
	doc, err := parseOrgFileDoc(string(up))
	if err != nil || doc.MinLoreVersion != "0.1.21" {
		t.Fatalf("parse: %+v %v", doc, err)
	}
	o := &orgconf.Org{Name: "acme"}
	if !orgconf.Refresh(o, Suggestion{Doc: doc}.Shared(), orgconf.OrgSource{Store: "sto_1", Version: "v1"}) || o.MinLoreVersion != "0.1.21" {
		t.Fatalf("the org entry did not copy the floor: %+v", o)
	}
}
