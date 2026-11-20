//go:build journeys && !windows

package journeys

// bulkmove_folders_test.go — Pat keeps notes in folders of a personal store
// and moves a folder's rows on twice: a -> b, then b -> c (devco D-761,
// H-20). The second move failed E_NOT_FOUND "refers to rows no attached
// store holds: project_id" because the first one left every row pointing at
// folder a's project, while its --dry-run passed (it ran only the secret
// scan). The same first move "left tasklist … behind" for every task
// although the list moved in the same batch (tasks moved before their list).
// Now each moved row takes the target folder's project, a task keeps its
// list, and the dry run refuses whatever the move refuses.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestJourneyBulkMoveBetweenPersonalFolders(t *testing.T) {
	t.Parallel()
	w := NewWorld(t)
	m := w.Persona("Pat Example", "pat@example.test").Laptop()
	store := w.Remote("github.com/pat/notes")
	const dir = "work"
	if err := os.MkdirAll(filepath.Join(m.Home, dir), 0o755); err != nil {
		t.Fatal(err)
	}
	var memID, taskID string

	w.Step("day1/personal-store-with-three-folders", func() {
		m.Lore(dir, "store", "init", "--name", "me", "--url", store.SSHURL(), "--yes").OK(t)
		for _, f := range []string{"a", "b", "c"} {
			m.Lore(dir, "store", "folder", "add", "--store", "me", "--folder", f).OK(t)
		}
		var mem struct {
			ID string `json:"id"`
		}
		var task jBulkFolderRow
		tlJSON(t, m.Lore(dir, "memory", "add", "--to", "me/a", "--body", "first note about rounding", "--json").OK(t), &mem)
		m.Lore(dir, "memory", "add", "--to", "me/a", "--body", "second note about invoices").OK(t)
		tlJSON(t, m.Lore(dir, "task", "add", "--to", "me/a", "--title", "check the invoice totals", "--json").OK(t), &task)
		memID, taskID = mem.ID, task.Data.ID
		if memID == "" || taskID == "" {
			t.Fatalf("no ids: memory %q task %q", memID, taskID)
		}
	})

	w.Step("day1/a-to-b-keeps-the-task-in-its-list", func() {
		m.Lore(dir, "move", "--from", "me/a", "--to", "me/b", "--yes").OK(t).
			Says(t, "✓ moved 4 row(s) from me/a to me/b", "memories 2, task_lists 1, tasks 1").
			Never(t, "left tasklist", "behind")
	})

	w.Step("day2/b-to-c-dry-run-then-move", func() {
		m.Lore(dir, "move", "--from", "me/b", "--to", "me/c", "--dry-run").OK(t).
			Says(t, "would move 4 row(s) from me/b to me/c (nothing written, --dry-run)", "next: lore move --from me/b --to me/c --yes")
		m.Lore(dir, "move", "--from", "me/b", "--to", "me/c", "--yes").OK(t).
			Says(t, "✓ moved 4 row(s) from me/b to me/c").
			Never(t, "E_NOT_FOUND", "project_id", "left tasklist")
		// folder c's own project: the one a row written there points at
		var own jBulkFolderRow
		tlJSON(t, m.Lore(dir, "task", "add", "--to", "me/c", "--title", "c's own task", "--json").OK(t), &own)
		cProject := own.Data.Source.FolderID
		if !strings.HasPrefix(cProject, "prj_") {
			t.Fatalf("no folder project for me/c: %q", cProject)
		}
		for _, id := range []string{memID, taskID} {
			var got jBulkShown
			tlJSON(t, m.Lore(dir, "show", "--id", id, "--store", "me/c", "--json").OK(t), &got)
			if got.Store != "me/c" || got.Fields.ProjectID != cProject {
				t.Errorf("%s in %q: project_id %s, want folder c's project %s", id, got.Store, got.Fields.ProjectID, cProject)
			}
			if id == taskID && !strings.HasPrefix(got.Fields.TasklistID, "tlt_") {
				t.Errorf("moved task lost its list (tasklist_id %q)", got.Fields.TasklistID)
			}
		}
	})
}

// jBulkFolderRow is the part of a `task add --json` this journey reads:
// the row's id and the project id of the folder it was written to.
type jBulkFolderRow struct {
	Data struct {
		ID     string `json:"id"`
		Source struct {
			FolderID string `json:"folder_id"`
		} `json:"_source"`
	} `json:"data"`
}

// jBulkShown is the part of a `show --json` this journey checks: where the
// row sits and the project and list it points at.
type jBulkShown struct {
	Store  string `json:"store"`
	Fields struct {
		ProjectID  string `json:"project_id"`
		TasklistID string `json:"tasklist_id"`
	} `json:"fields"`
}
