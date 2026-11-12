package orgcfg

import (
	"slices"
	"testing"
)

// TestOrgFilePath (D-e): lore-org.yaml sits in the org folder itself
// (org/lore-org.yaml; lore-org.yaml at the store's top for org_folder ".")
// and readers still find a store not moved yet (org/.lore/lore-org.yaml),
// the current place first.
func TestOrgFilePath(t *testing.T) {
	t.Parallel()
	for folder, want := range map[string]string{"org": "org/lore-org.yaml", ".": "lore-org.yaml", "": "lore-org.yaml", "team/admin": "team/admin/lore-org.yaml"} {
		if got := orgFilePath(folder); got != want {
			t.Errorf("orgFilePath(%q) = %q, want %q", folder, got, want)
		}
	}
	if got := orgFilePaths("org"); !slices.Equal(got, []string{"org/lore-org.yaml", "org/.lore/lore-org.yaml"}) {
		t.Errorf("orgFilePaths(org) = %v", got)
	}
	if got := metaPathOf("acme"); got != "acme/_meta.json" {
		t.Errorf("metaPathOf(acme) = %q", got)
	}
	if got := metaPathOf("."); got != "_store/_meta.json" {
		t.Errorf("metaPathOf(.) = %q", got)
	}
}

// TestStoreAllowedLayout: a store tree in the store layout passes the
// "is this a lore store" check (rows under folders, folder files, the org
// file, the README); code does not.
func TestStoreAllowedLayout(t *testing.T) {
	t.Parallel()
	id := "tsk_01a1a5aca7f57659be7320c5f4b70033"
	for _, p := range []string{
		"lore-store.yaml", "README.md", "lore-org.yaml", "_meta.json",
		"_store/tasks/" + id + ".json", "_store/_meta.json", "tasks/" + id + ".json", "acme/tasks/" + id + ".json", "team/web/memories/mem_01a1a5aca7f57659be7320c5f4b70033.json",
		"acme/_meta.json", "acme/_purged.json", "org/lore-org.yaml",
		"acme/.lore/data/tasks/" + id + ".json", "org/.lore/lore-org.yaml",
	} {
		if !storeAllowed(p) {
			t.Errorf("storeAllowed(%q) = false, want a store file", p)
		}
	}
	for _, p := range []string{"main.go", "src/app.go", "acme/notes.txt", "tasks/memories/" + id + ".json", "acme/README.md", "_store/notes.txt", "_other/tasks/" + id + ".json"} {
		if storeAllowed(p) {
			t.Errorf("storeAllowed(%q) = true, want refused", p)
		}
	}
}
