package wire

import (
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/schema"
)

// liveResID is a resources row id that exists only on the live ref.
const liveResID = "res_01a1a59b891c70b789144cd3e711fb09"

// TestLiveResourcesRefused (RQ2): a resources file on the live ref is
// never brought in by lore migrate run, and the live note names it.
func TestLiveResourcesRefused(t *testing.T) {
	t.Parallel()
	repo := gitRepo(t)
	a := newTestApp(t, repo)
	withProject(t, a)
	path := liveRootFolder + "/" + schema.TableResources + "/" + liveResID + ".json"
	writeLiveRef(t, repo, liveRefLocal, map[string]string{
		path: `{"_table":"resources","_v":1,"id":"` + liveResID + `","name":"h1","_live":{"hlc":"` + liveNewer + `"}}`,
	})
	if note := a.LiveNote(background()); !strings.Contains(note, "resources files on the live ref are ignored") || !strings.Contains(note, path) {
		t.Fatalf("note %q", note)
	}
	got, err := a.MigrateLive(background(), false)
	if err != nil {
		t.Fatal(err)
	}
	for _, g := range got {
		if strings.Contains(g, liveResID) {
			t.Fatalf("a live resource was migrated: %v", got)
		}
	}
}
