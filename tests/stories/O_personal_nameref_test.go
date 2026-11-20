//go:build stories && !windows

package stories

// O_personal_nameref_test.go — story O, devco D-766 (owner P-10, bug hunt
// H-25): an old-format import of a personal knowledge base held tasks
// whose tasklist_id was a list's name ("Backlog"), not a tlt_ id. Both
// ways in wrote it as is (`lore store adopt` into a personal store
// folder, `lore migrate run` in the repo), so every later edit of such a
// task was refused "field tasklist_id: want a ref value" and the task
// belonged to no list. Now each conversion maps the name to the list of
// that title in the task's project, or clears it, and says so per task.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

// nrV017Tasks adds to a 0.1.17 project at dir (project prj) a list
// "Backlog" and two tasks whose tasklist_id is a name: the list's, in
// another case, and one no list has. It returns the list and task ids.
func nrV017Tasks(t *testing.T, dir, prj string) (list, named, unknown string) {
	t.Helper()
	list, named, unknown = v017ID("tlt", 901), v017ID("tsk", 902), v017ID("tsk", 903)
	const at = "2026-10-02T09:00:00.000000000Z"
	files := map[string]string{
		"task_lists/" + list + ".json": `{"_table":"task_lists","_v":1,"id":"` + list + `","project_id":"` + prj + `","title":"Backlog","created_at":"` + at + `","updated_at":"` + at + `"}`,
		"tasks/" + named + ".json":     `{"_table":"tasks","_v":1,"id":"` + named + `","project_id":"` + prj + `","title":"order printer paper","status":"todo","priority":"medium","commitment":"accepted","tasklist_id":"backlog","created_at":"` + at + `","updated_at":"` + at + `"}`,
		"tasks/" + unknown + ".json":   `{"_table":"tasks","_v":1,"id":"` + unknown + `","project_id":"` + prj + `","title":"renew the domain","status":"todo","priority":"medium","commitment":"accepted","tasklist_id":"someday","created_at":"` + at + `","updated_at":"` + at + `"}`,
	}
	for p, doc := range files {
		full := filepath.Join(dir, ".lore", "data", filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(doc+"\n"), 0o644); err != nil { // committed files are readable (story invariant)
			t.Fatal(err)
		}
	}
	return list, named, unknown
}

// TestStoryOPersonalNameRefs: a list name in tasklist_id is mapped or
// cleared, with a warning, by store adopt into the personal store and by
// migrate run (devco D-766).
func TestStoryOPersonalNameRefs(t *testing.T) {
	s := New(t, "O")
	me := s.Remote("github.com/" + oAdminPersonal.Login + "/lore-me")
	notes := s.Seed(s.Remote("github.com/"+oAdminPersonal.Login+"/old-notes"), func(dir string) { PlainRepo(t, dir) })
	ada := s.OwnerShaped(OwnerShape{Personal: oAdminPersonal, Org: oAdminOrg, OrgFolder: oOrg, Alias: oAlias, OrgOwners: []string{oOwner}})
	folder := psFolder // oStoreCloneLocal finds the clone by this folder

	old := func(name string) (*teamharness.Clone, string, string, string) {
		c := ada.CloneTo(notes, ada.Code(name))
		prj := V017Repo(t, c.Dir, "old-notes", SmallShape)
		list, named, unknown := nrV017Tasks(t, c.Dir, prj.ID)
		c.Git("add", "-A", "-f", ".lore/data")
		c.Git("-c", "core.hooksPath=/dev/null", "commit", "-q", "-m", "lore 0.1.17 notes")
		return c, list, named, unknown
	}

	s.Step("D-766/adopt-into-the-personal-store-maps-the-list-name", func() {
		ada.Lore(ada.Home, "store", "init", "--name", "me", "--url", me.Dir, "--yes").OK(t)
		ada.Lore(ada.Home, "store", "folder", "add", "me", folder).OK(t)
		c, list, named, unknown := old("old-notes")
		ada.Lore(c.Dir, "store", "adopt", "--target", "me/"+folder, "--yes").OK(t).
			Says(t, `warning: tasks/`+named+`: tasklist_id "backlog" is a name, not an id; now the task_lists row `+list,
				`warning: tasks/`+unknown+`: tasklist_id "someday" is a name, not an id; cleared: no task_lists row has that name`)
		dir := filepath.Join(oStoreCloneLocal(t, ada), folder, "tasks")
		for id, want := range map[string]string{named: `"tasklist_id": "` + list + `"`, unknown: ""} {
			b, err := os.ReadFile(filepath.Join(dir, id+".json"))
			if err != nil {
				t.Fatal(err)
			}
			doc := string(b)
			if (want != "" && !strings.Contains(doc, want)) || (want == "" && strings.Contains(doc, "tasklist_id")) ||
				strings.Contains(doc, `"backlog"`) || strings.Contains(doc, `"someday"`) {
				t.Errorf("[O D-766 adopt] the adopted task %s:\n%s", id, doc)
			}
		}
	})

	s.Step("D-766/migrate-run-maps-the-list-name-and-the-task-takes-edits", func() {
		c, list, named, unknown := old("old-notes-2")
		ada.Lore(c.Dir, "migrate", "run").OK(t).
			Says(t, `warning: tasks/`+named+`: tasklist_id "backlog" is a name, not an id; now the task_lists row `+list,
				`warning: tasks/`+unknown+`: tasklist_id "someday" is a name, not an id; cleared`)
		ada.Lore(c.Dir, "task", "list").OK(t).Never(t, "picked up a hand edit")
		ada.Lore(c.Dir, "task", "edit", "--id", named, "--title", "order printer paper (A4)").OK(t).Never(t, "want a ref value")
		ada.Lore(c.Dir, "task", "edit", "--id", unknown, "--title", "renew the domain (5 years)").OK(t).Never(t, "want a ref value")
	})
}
