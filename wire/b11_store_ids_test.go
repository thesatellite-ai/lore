package wire

// Port of saas/cmd/cli stores_project_id_test.go
// TestStoreFolderProjectIDIsOnePerFolder (B11): two creators of one store
// folder derive the same prj_ id; any difference in store, branch or folder
// gives another, and moving a character between parts never collides. (The
// v0.1.17 random id of a folder without a store identity has no
// counterpart: org link writes always name a store.)

import (
	"testing"

	"github.com/thesatellite-ai/lore/model/ids"
)

func TestStoreFolderProjectIDIsOnePerFolder(t *testing.T) {
	const st, br, fo = "github.com/northwind/lore", "main", "org"
	first, err := storeFolderProjectID(st, br, fo)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = ids.Parse[ids.Project](first); err != nil {
		t.Fatalf("not a project id: %s (%v)", first, err)
	}
	again, err := storeFolderProjectID(st, br, fo)
	if err != nil || again != first {
		t.Fatalf("same folder, two ids: %s vs %s (%v)", first, again, err)
	}
	for name, k := range map[string][3]string{
		"other store":   {"github.com/betalabs/lore", br, fo},
		"other branch":  {st, "staging", fo},
		"other folder":  {st, br, "acme"},
		"root folder":   {st, br, ""},
		"shifted parts": {st + "m", "ain", fo},
		"unicode":       {st, br, "ørg"},
	} {
		id, err := storeFolderProjectID(k[0], k[1], k[2])
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if id == first {
			t.Errorf("%s: shares the project id of the base folder", name)
		}
	}
}
