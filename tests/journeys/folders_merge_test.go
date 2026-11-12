//go:build journeys && !windows

package journeys

// folders_merge_test.go — repairing adopted v0.1.17 data in an org's
// second store (owner report 2026-11-12). Lee's org has two stores; the
// v0.1.17 repo's rows were adopted into a folder of the second one by an
// older lore, so its tasks still carry tasklist_id "" (v0.1.17 wrote ""
// for "no list") and one task assigned_to_actor_id "", and every row
// names Lee's old actor id. From the client repo linked to that folder
// Lee merges the old id into the roster's, runs doctor --fix, and lists
// the org's folders.

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

// The org's second store, the client repo and the v0.1.17 rows
// (placeholders).
const (
	jMergeTwoSlug  = "github.com/acme-co/notes-merge"
	jMergeHubSlug  = "github.com/client-co/merge-hub"
	jMergeFolder   = "client_m"
	jMergeProject  = "prj_01a1cb5d817172998c6a1880a90a3001"
	jMergeOldActor = "act_541757c158dc7fb58c1c338b0a603001"
	jMergeTaskStem = "tsk_01a1cb5d849374588a71"
	jMergeAnnActor = "act_541757c158dc7fb58c1c338b0a603002"
	jMergeAnnStem  = "tsk_01a1cb5d849374588a73"
	// jMergeAnnTasks name another actor: the merge leaves them, so their
	// tasklist_id "" is doctor --fix's to repair.
	jMergeAnnTasks = 40
	// jMergeTasks rows name the old actor; the owner had 66 (11.5 min).
	jMergeTasks = 120
	// jMergeBudget is the bar for the merge and doctor --fix of them all.
	jMergeBudget = 30 * time.Second
)

// jMergeLegacy writes the v0.1.17 repo's .lore/data into c: its project,
// Lee's old actor (no stable key, as v0.1.17 wrote it), jMergeTasks tasks
// with tasklist_id "" made and assigned by the old actor, the last one
// with assigned_to_actor_id "" too.
func jMergeLegacy(t *testing.T, c *teamharness.Clone) {
	t.Helper()
	data := filepath.Join(c.Dir, ".lore", "data")
	jWrite(t, filepath.Join(data, "_meta.json"), `{"_v": 1, "project_id": "`+jMergeProject+`"}`+"\n")
	jWrite(t, filepath.Join(data, "projects", jMergeProject+".json"), `{"_table": "projects", "_v": 1, "created_at": "2026-11-01T09:00:00.000000000Z", "id": "`+jMergeProject+`", "name": "merge-app", "updated_at": "2026-11-01T09:00:00.000000000Z"}`+"\n")
	jWrite(t, filepath.Join(data, "actors", jMergeOldActor+".json"), `{"_table": "actors", "_v": 1, "display_name": "Lee Lead", "id": "`+jMergeOldActor+`", "kind": "human"}`+"\n")
	jWrite(t, filepath.Join(data, "actors", jMergeAnnActor+".json"), `{"_table": "actors", "_v": 1, "display_name": "Ann Able", "id": "`+jMergeAnnActor+`", "kind": "human"}`+"\n")
	task := func(id, actor, assignee, title string) {
		jWrite(t, filepath.Join(data, "tasks", id+".json"), `{"_table": "tasks", "_v": 1, "assigned_to_actor_id": "`+assignee+`", "attempts": 0, "claim_epoch": 0, "commitment": "accepted", "created_at": "2026-11-03T14:44:00.000000000Z", "created_by_actor_id": "`+actor+
			`", "id": "`+id+`", "priority": "medium", "project_id": "`+jMergeProject+`", "status": "todo", "tasklist_id": "", "title": "`+title+`", "updated_at": "2026-11-03T14:44:00.000000000Z"}`+"\n")
	}
	for i := range jMergeTasks {
		assignee := jMergeOldActor
		if i == jMergeTasks-1 {
			assignee = ""
		}
		task(jAdoptID(jMergeTaskStem, i), jMergeOldActor, assignee, "old task "+fmt.Sprint(i))
	}
	for i := range jMergeAnnTasks {
		task(jAdoptID(jMergeAnnStem, i), jMergeAnnActor, "", "ann's task "+fmt.Sprint(i)) // unassigned: only doctor's empty_ref repair edits it
	}
	c.CommitAll("v0.1.17 notes")
	c.Git("push", "-q", "origin", "HEAD:main")
}

func TestJourneyFoldersMerge(t *testing.T) {
	t.Parallel()
	tt := newTLTeam(t)
	w := tt.w
	lee := tt.person(tlLee)
	parent := filepath.Join(lee.Home, "code")
	app := tt.repo[tlLee.Email]
	two := w.Remote(jMergeTwoSlug)
	mainRef := "refs/heads/main"
	var hub *teamharness.Clone

	w.Step("day1/an-org-with-two-stores", func() {
		tlStartTeam(t, lee, app.Dir, tlLeadSetup, tlOrg, tt.store.SSHURL(), tlLee, []string{tlFolders})
		s := lee.Clone(tt.store, "team-store")
		jWrite(t, filepath.Join(s.Dir, filepath.FromSlash(jManyOrgFile)), "name: "+tlOrg+"\nstores:\n"+
			"    - url: "+tt.store.SSHURL()+"\n      org_folder: org\n"+
			"    - url: "+two.SSHURL()+"\n")
		s.CommitAll("list both stores")
		s.Git("push", "-q", "origin", "HEAD:main")
		lee.Lore(parent, "org", "refresh", tlOrg).OK(t)
		lee.Lore(parent, "store", "folder", "add", two.SSHURL(), jMergeFolder).OK(t)
		// the v0.1.17 rows land as an older lore adopted them: tasklist_id ""
		// kept (this lore's adopt writes null, so the files are put back)
		old := jPlainRepo(t, lee, w.Remote(jAdoptLegacySlug), filepath.Join(parent, "merge-app"))
		jMergeLegacy(t, old)
		lee.Lore(parent, "store", "adopt", tlOrg+"/"+jMergeFolder, "--from", old.Dir, "--yes").OK(t)
		task0 := jMergeFolder + "/.lore/data/tasks/" + jAdoptID(jMergeTaskStem, 0) + ".json"
		if doc, _ := two.Read(mainRef, task0); strings.Contains(doc, `"tasklist_id": ""`) || !strings.Contains(doc, jMergeOldActor) {
			t.Fatalf("adopt kept tasklist_id \"\" (or lost the actor):\n%s", doc)
		}
		c := lee.Clone(two, "notes-merge-older")
		for _, f := range two.FilesMatching(mainRef, jMergeFolder+"/.lore/data/tasks/") {
			doc, _ := two.Read(mainRef, f)
			doc = strings.Replace(doc, `"project_id"`, `"tasklist_id": "", "project_id"`, 1) // what 0.1.18-dev wrote
			if strings.Contains(f, jAdoptID(jMergeTaskStem, jMergeTasks-1)) {
				doc = strings.Replace(doc, `"attempts"`, `"assigned_to_actor_id": "", "attempts"`, 1)
			}
			jWrite(t, filepath.Join(c.Dir, filepath.FromSlash(f)), doc)
		}
		c.CommitAll("rows as an older lore adopted them")
		c.Git("push", "-q", "origin", "HEAD:main")
		hub = jPlainRepo(t, lee, w.Remote(jMergeHubSlug), filepath.Join(parent, "merge-hub"))
		lee.LoreIn(hub, "org", "link", tlOrg, "--project", jMergeFolder, "--store", two.SSHURL(), "--yes").OK(t)
	})

	w.Step("day2/folders-each-store-once", func() {
		// each block is headed by the store that holds its folders, once
		r := lee.LoreIn(hub, "store", "folder", "list").OK(t)
		t.Logf("store folder list:\n%s", r.Out())
		out := r.Out()
		head := "store " + two.SSHURL() + " folders:"
		if strings.Count(out, head) != 1 || strings.Count(out, "store "+tlOrg+" folders:") != 1 {
			t.Errorf("each store must head its block once (%q, %q):\n%s", head, "store "+tlOrg+" folders:", out)
		}
		if i, j := strings.Index(out, head), strings.Index(out, "store "+tlOrg+" folders:"); i < 0 || j < 0 ||
			!strings.Contains(blockAt(out, i), jMergeFolder) || strings.Contains(blockAt(out, i), " org ") ||
			!strings.Contains(blockAt(out, j), " org ") || strings.Contains(blockAt(out, j), jMergeFolder) {
			t.Errorf("a folder is listed under a store that does not hold it:\n%s", out)
		}
	})

	w.Step("day2/merge-the-old-id", func() {
		into := tt.roster(t, tlLee)["human:"+tlLee.Email].ID
		where := two.SSHURL() + "/" + jMergeFolder
		r := lee.LoreIn(hub, "actor", "merge", jMergeOldActor, into, "--dry-run").OK(t).
			Says(t, fmt.Sprintf("%d rows would name %s", jMergeTasks, into), fmt.Sprintf("%s: %d to edit", where, jMergeTasks), "stores read: ", where).
			Never(t, "stores read: repo, me, "+tlOrg+",")
		t.Logf("actor merge --dry-run:\n%s", r.Out())
		before := two.CommitCount(mainRef)
		start := time.Now()
		r = lee.LoreIn(hub, "actor", "merge", jMergeOldActor, into, "--yes").OK(t).
			Says(t, fmt.Sprintf("%d rows now name %s", jMergeTasks, into), fmt.Sprintf("%s: %d edited", where, jMergeTasks),
				"history stamps (created_by, updated_by) keep "+jMergeOldActor+"; reads show "+into)
		took := time.Since(start)
		t.Logf("actor merge (%s, %d store commits):\n%s", took, two.CommitCount(mainRef)-before, r.Out())
		if n := two.CommitCount(mainRef) - before; n != 1 {
			t.Errorf("the merge made %d commits in the folder's store, want ONE for all %d rows", n, jMergeTasks)
		}
		if took > jMergeBudget {
			t.Errorf("the merge of %d rows took %s, over %s", jMergeTasks, took, jMergeBudget)
		}
		if log := two.Log(mainRef); len(log) == 0 || !strings.Contains(log[0], "actor merge "+jMergeOldActor+" into "+into) {
			t.Errorf("the merge commit is not titled by the merge: %v", log[:min(2, len(log))])
		}
		// a rerun has nothing left
		lee.LoreIn(hub, "actor", "merge", jMergeOldActor, into, "--yes").OK(t).Says(t, "no rows name "+jMergeOldActor)
		// reads follow the merge: --created-by the kept id finds the rows
		// whose history stamps keep the old id
		lst := lee.LoreIn(hub, "task", "list", "--created-by", into, "--all", "--json").OK(t)
		if n := strings.Count(lst.Stdout, jMergeTaskStem); n != jMergeTasks {
			t.Errorf("task list --created-by %s lists %d of the %d tasks made under the merged id\n%s", into, n, jMergeTasks, lst.Stderr)
		}
		show := lee.LoreIn(hub, "task", "show", jAdoptID(jMergeTaskStem, 0)).OK(t)
		t.Logf("task show after the merge:\n%s", show.Out())
		t.Logf("actor list after the merge:\n%s", lee.LoreIn(hub, "actor", "list").OK(t).Out())
		// the roster keeps the merged id's mark, which reads follow
		if got := tt.store.FilesMatching(mainRef, "org/.lore/data/actors/"+jMergeOldActor); len(got) != 1 {
			t.Errorf("the roster holds no marked row of the merged id: %v", got)
		} else if doc, _ := tt.store.Read(mainRef, got[0]); !strings.Contains(doc, `"merged_into": "`+into+`"`) {
			t.Errorf("the roster's row of the merged id has no merged_into:\n%s", doc)
		}
	})

	w.Step("day2/doctor-fix-empty-refs", func() {
		// the linked client repo runs the store checks (its folder's rows)
		r := lee.LoreIn(hub, "doctor").Says(t, "empty_ref", fmt.Sprintf("%d rows", jMergeAnnTasks), "tasks.tasklist_id", "lore doctor --fix")
		t.Logf("doctor:\n%s", grepLines(r.Out(), "empty_ref"))
		before := two.CommitCount(mainRef)
		start := time.Now()
		r = lee.LoreIn(hub, "doctor", "--fix", "--yes").Says(t, "wrote null in "+fmt.Sprint(jMergeAnnTasks)+" rows")
		took := time.Since(start)
		t.Logf("doctor --fix (%s, %d store commits):\n%s", took, two.CommitCount(mainRef)-before, grepLines(r.Out(), "empty_ref"))
		// two repairs ran (task leads, empty refs): one store commit each
		if n := two.CommitCount(mainRef) - before; n != 2 {
			t.Errorf("doctor --fix made %d commits, want one per repair (2)", n)
		}
		after := lee.LoreIn(hub, "doctor").Never(t, "empty_ref", "task_lead")
		t.Logf("doctor after --fix:\n%s", after.Out())
		for _, f := range two.FilesMatching(mainRef, jMergeFolder+"/.lore/data/tasks/") {
			if doc, _ := two.Read(mainRef, f); strings.Contains(doc, `": ""`) {
				t.Errorf("%s still holds an empty-string ref", f)
			}
		}
		// a ref can be cleared on edit
		id := jAdoptID(jMergeTaskStem, 1)
		lee.LoreIn(hub, "task", "edit", id, "--clear-assignee").OK(t)
		lee.LoreIn(hub, "task", "edit", id, "--clear-tasklist").OK(t)
	})
}

// blockAt is the store block of out starting at i (up to the next blank line).
func blockAt(out string, i int) string {
	b := out[i:]
	if j := strings.Index(b, "\n\n"); j >= 0 {
		return b[:j]
	}
	return b
}

// grepLines keeps the lines of out holding word.
func grepLines(out, word string) string {
	var keep []string
	for _, l := range strings.Split(out, "\n") {
		if strings.Contains(l, word) {
			keep = append(keep, l)
		}
	}
	return strings.Join(keep, "\n")
}
