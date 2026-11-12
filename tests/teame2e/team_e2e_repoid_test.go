//go:build teame2e

package teame2e

import (
	"strings"
	"testing"
)

// TestE2ERepoSetURLFollowsStoreLinks pins Q7 (PROV2): confirming another
// address of a linked repo with `lore repo set-url --mirror` appends a
// v0.1.17-shape link entry for it in the org store folder that links the
// repo, so org rule 4 matches the new address too; the old link stays.
func TestE2ERepoSetURLFollowsStoreLinks(t *testing.T) {
	a := newAgency(t)
	a.aliceApp.MustLore("sync")
	mirror := "github.com/acme/app-mirror"
	res := a.aliceApp.MustLore("repo", "set-url", "https://"+mirror+".git", "--mirror")
	if !strings.Contains(res.Stdout, "mirror address added: "+mirror) {
		t.Fatalf("set-url output: %s", res.Stdout)
	}
	meta, _ := a.store.Read(refMain, folderAcme+"/"+metaFile)
	for _, slug := range []string{slugApp, mirror} {
		if !strings.Contains(meta, `"remote": "`+slug+`"`) && !strings.Contains(meta, `"remote":"`+slug+`"`) {
			t.Fatalf("store folder link for %s missing: %s", slug, meta)
		}
	}
	local, _ := a.aliceApp.ReadFile(dataDir + "/" + metaFile)
	if !strings.Contains(local, mirror) {
		t.Fatalf("the repo's _meta.json must list the confirmed mirror: %s", local)
	}
}
