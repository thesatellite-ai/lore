package infra

import (
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/store"
)

// TestAppendFolderProjects: the org's folders are added to project list
// beside the rows, sorted by name, each once; a folder whose row is
// already listed is not added again, and that store row is named by the
// folder's store (the repo attaches its linked folder under the org's
// name, whichever store holds it); the repo's own row keeps v0.1.17's line.
func TestAppendFolderProjects(t *testing.T) {
	const (
		repoID   = "prj_0190f1a2b3c47d5e8f90a1b2c3d4e501"
		linkedID = "prj_0190f1a2b3c47d5e8f90a1b2c3d4e502"
		otherID  = "prj_0190f1a2b3c47d5e8f90a1b2c3d4e503"
		thirdID  = "prj_0190f1a2b3c47d5e8f90a1b2c3d4e504"
		twoURL   = "git@github.com:acme/notes-two"
	)
	data := []Project{
		{ID: repoID, Name: "app", line: repoID + "  app\n"},
		{ID: linkedID, Name: "client_a", Store: store.SourceRef{Store: "acme", Folder: "client_a"}, line: linkedID + "  client_a\n", label: "[acme/client_a]"},
	}
	folders := []FolderProject{
		{ID: thirdID, Name: "client_c", Source: store.SourceRef{Store: "acme", Folder: "client_c"}},
		{ID: linkedID, Name: "client_a", Source: store.SourceRef{Store: twoURL, Folder: "client_a"}},
		{ID: otherID, Name: "client_b", Source: store.SourceRef{Store: twoURL, Folder: "client_b"}},
	}
	got := appendFolderProjects(data, folders)
	var names []string
	for _, p := range got {
		names = append(names, p.Name)
	}
	if strings.Join(names, ",") != "app,client_a,client_b,client_c" {
		t.Fatalf("names = %v", names)
	}
	if got[1].Store.Store != twoURL {
		t.Errorf("the linked folder's row names store %q, want the store holding it", got[1].Store.Store)
	}
	text := ProjectListOut{Data: got}.Text()
	for _, want := range []string{
		repoID + "  app\n",
		linkedID + "  client_a  [" + twoURL + "/client_a]\n",
		otherID + "  client_b  (folder, no rows yet)  [" + twoURL + "/client_b]\n",
		thirdID + "  client_c  (folder, no rows yet)  [acme/client_c]\n",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("text lacks %q:\n%s", want, text)
		}
	}
}
