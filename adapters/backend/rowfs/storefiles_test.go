package rowfs

import "testing"

// TestStoreFileAllowed pins the one definition of a store's files: every
// file a writer of lore puts into a store passes, code and stray files do
// not.
func TestStoreFileAllowed(t *testing.T) {
	t.Parallel()
	const id = "01a1a5aca7f57659be7320c5f4b70033"
	for _, p := range []string{
		MarkerFile, "README.md", "readme.txt", "LICENSE", "LICENCE.md", "COPYING", ".gitignore", ".gitattributes",
		".github/workflows/lore-check.yml", OrgFileName, OrgFileKeptName, MetaFileName, PurgedFileName,
		"_store/actors/act_" + id + ".json", "_store/" + MetaFileName, "_store/" + PurgedFileName,
		"org/tasks/tsk_" + id + ".json", "org/" + MetaFileName, "org/" + PurgedFileName, "org/" + OrgFileName, "org/" + OrgFileKeptName,
		"team/web/memories/mem_" + id + ".json", "team/web/" + MetaFileName,
		"memories/mem_" + id + ".json",
		".lore/data/tasks/tsk_" + id + ".json", "org/.lore/data/_meta.json", "team/web/.lore/lore-org.yaml",
	} {
		if !StoreFileAllowed(p) {
			t.Errorf("StoreFileAllowed(%q) = false, want a store file", p)
		}
	}
	for _, p := range []string{
		"", "/abs", "a/../b", "src/main.go", "main.go", "org/notes.txt", "org/tasks/notes.md", "org/lore-org.yaml.bak",
		"_x/" + OrgFileName, ".github/workflows/ci.yml", "org/nosuchtable/tsk_" + id + ".json", ".hidden/x",
	} {
		if StoreFileAllowed(p) {
			t.Errorf("StoreFileAllowed(%q) = true, want refused", p)
		}
	}
}
