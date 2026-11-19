//go:build journeys && !windows

package journeys

// write_to_folder_project_test.go — devco D-718. Lee works in the app
// repo (its own project) and writes a task, a memory, a rule and a
// decision straight into a client folder of the org store with
// `--to <org>/<folder>`. Each row belongs to that folder's project, never
// to the app repo's: the app's prj_ id must not reach the client folder
// (a store reader of client_a would see rows of a project it does not
// hold, and project-filtered reads there miss them).

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

// The folder and the rows written into it (placeholders).
const (
	jToFolder     = "client_a"
	jToTaskTitle  = "client_a: rotate the webhook secret"
	jToMemoryBody = "client_a deploys only on tuesdays"
	jToRuleBody   = "client_a never gets a staging url in a ticket"
	jToDecTitle   = "client_a keeps its own billing queue"
)

func TestJourneyWriteToFolderTakesFolderProject(t *testing.T) {
	t.Parallel()
	tt := newTLTeam(t)
	w := tt.w
	lee := tt.person(tlLee)
	app := tt.repo[tlLee.Email]
	parent := filepath.Join(lee.Home, "code")
	mainRef := "refs/heads/main"
	to := tlOrg + "/" + jToFolder

	w.Step("day1/start-team-and-a-client-folder", func() {
		tlStartTeam(t, lee, app.Dir, tlLeadSetup, tlOrg, tt.store.SSHURL(), tlLee, []string{tlFolders})
		lee.LoreIn(app, "init", "--non-interactive", "--name=app").OK(t)
		lee.Lore(parent, "store", "folder", "add", "team", jToFolder).OK(t)
		lee.Lore(parent, "sync").OK(t)
	})

	w.Step("day2/writes-from-the-app-repo-into-the-folder", func() {
		w.AdvanceDays(1)
		lee.LoreIn(app, "task", "add", jToTaskTitle, "--to", to).OK(t)
		// a tag made in the folder is found by a write to the folder (both
		// resolve the folder's project, not the app repo's)
		lee.LoreIn(app, "tag", "add", "--name", "billing", "--to", to).OK(t)
		lee.LoreIn(app, "memory", "add", "--body", jToMemoryBody, "--tag", "billing", "--to", to).OK(t)
		lee.LoreIn(app, "rule", "add", "--body", jToRuleBody, "--to", to, "--yes").OK(t)
		lee.LoreIn(app, "decision", "add", "--title", jToDecTitle, "--body", "one queue per client", "--to", to).OK(t)
		lee.Lore(parent, "sync").OK(t)

		var pl struct {
			Data []jProject `json:"data"`
		}
		tlJSON(t, lee.LoreIn(app, "project", "list", "--json").OK(t), &pl)
		folderPrj, appPrj := "", ""
		for _, p := range pl.Data {
			switch {
			case p.Name == jToFolder:
				folderPrj = p.ID
			case p.Source.Store == "repo":
				appPrj = p.ID
			}
		}
		if folderPrj == "" {
			t.Fatalf("project list has no %s project: %+v", jToFolder, pl.Data)
		}
		seen := 0
		for _, f := range tt.store.FilesMatching(mainRef, jToFolder+"/") {
			table := ""
			for _, tb := range []string{"/tasks/", "/memories/", "/rules/", "/decisions/"} {
				if strings.Contains(f, tb) {
					table = tb
				}
			}
			if table == "" {
				continue
			}
			doc, _ := tt.store.Read(mainRef, f)
			var r struct {
				ProjectID string `json:"project_id"`
			}
			if err := json.Unmarshal([]byte(doc), &r); err != nil {
				t.Fatalf("%s is not a row: %v", f, err)
			}
			seen++
			if r.ProjectID != folderPrj {
				t.Errorf("%s project_id = %s, want the folder's %s (app repo's is %s)", f, r.ProjectID, folderPrj, appPrj)
			}
		}
		if seen < 4 {
			t.Errorf("the store's %s holds %d of the 4 rows written: %v", jToFolder, seen, tt.store.Files(mainRef))
		}
		// the folder's own reads list them (they filter by its project)
		lee.LoreIn(app, "task", "list", "--to", to).OK(t).Says(t, jToTaskTitle)
	})
}
