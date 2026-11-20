//go:build stories && !windows

package stories

// O_link_after_storeinit_test.go — devco:D-750 (bug hunt H-9) on the
// owner-shaped machine (story O): in an own repo where `lore store init`
// ran (it leaves .lore/stores.yaml and nothing else) and then `lore org
// link --own`, whoami named the project and "reads <org>/<folder> (this
// repo's link)", yet `lore rule add` and `lore memory add` failed
// E_NOT_FOUND "this repository has no lore project" with a hint to run the
// org link just run. Such a repo holds no project of its own, so it is a
// repo linked by its remote: its writes land in the linked folder and the
// repo gets no .lore/data.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStoryOLinkAfterStoreInitWrites(t *testing.T) {
	s := New(t, "O")
	team := s.Remote("github.com/" + oOwner + "/lore-team")
	scratch := s.Remote("github.com/" + oOwner + "/scratch-store")
	platform := s.Seed(s.Remote("github.com/"+oOwner+"/platform"), func(dir string) { PlainRepo(t, dir) })
	app := s.Seed(s.Remote("github.com/"+oOwner+"/storeinit-app"), func(dir string) { PlainRepo(t, dir) })

	ada := s.OwnerShaped(OwnerShape{Personal: oAdminPersonal, Org: oAdminOrg, OrgFolder: oOrg, Alias: oAlias, OrgOwners: []string{oOwner}})
	pc := ada.CloneTo(platform, ada.OrgDir("platform"))
	oSetupFlow(t, ada, pc.Dir, oSetup, "admin", oAdminOrg, oAlias,
		map[string]any{"org": oOrg, "store_url": team.SSHURL()}, []string{"~/code/" + oOrg + "/**"})
	ac := ada.CloneTo(app, ada.OrgDir("storeinit-app"))
	const folder = "storeinit_app"
	place := oOrg + "/" + folder
	clone := oStoreClone(ada, oOwner, "lore-team")

	s.Step("D-750/store-init-then-own-link", func() {
		ada.Lore(ac.Dir, "store", "init", "--name", "scratch", "--url", scratch.SSHURL(), "--yes").OK(t)
		if !fileExists(filepath.Join(ac.Dir, ".lore", "stores.yaml")) || fileExists(filepath.Join(ac.Dir, ".lore", "data", "_meta.json")) {
			t.Fatalf("store init no longer leaves only .lore/stores.yaml; the cell's shape is gone")
		}
		ada.Lore(ac.Dir, "org", "config", "set", "--org", oOrg, "--key", "own", "--add", "github.com/"+oOwner+"/*", "--yes").OK(t)
		ada.Lore(ac.Dir, "org", "link", "--org", oOrg, "--project", folder, "--own", "--yes").OK(t)
		ada.Lore(ac.Dir, "whoami").Says(t, "this repo's link")
	})

	s.Step("D-750/rule-and-memory-add-land-in-the-linked-folder", func() {
		const rule = "always check the input at the edge"
		const mem = "the cache warms at boot"
		r := ada.Lore(ac.Dir, "rule", "add", "--body", rule, "--severity", "must")
		if r.Code != 0 || strings.Contains(r.Out(), "no lore project") {
			t.Fatalf("rule add in a repo linked after store init (exit %d):\n%s", r.Code, r.Out())
		}
		ada.Lore(ac.Dir, "memory", "add", "--body", mem).OK(t)
		for _, body := range []string{rule, mem} {
			if !dirHolds(t, filepath.Join(clone, folder), body) {
				t.Errorf("%q is not in the linked store folder %s", body, place)
			}
		}
		if fileExists(filepath.Join(ac.Dir, ".lore", "data")) {
			t.Errorf("a write in the linked repo made .lore/data in the code repo")
		}
		ada.Lore(ac.Dir, "rule", "list").Says(t, rule)
	})
}

// fileExists reports whether p exists (file or folder).
func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}
