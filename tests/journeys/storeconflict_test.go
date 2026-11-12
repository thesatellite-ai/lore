//go:build journeys && !windows

package journeys

import (
	"path/filepath"
	"strings"
	"testing"
)

// storeconflict_test.go — two machines change the same row of a team
// store (bug hunt B2-2, B2-3, B2-10): the second sync is refused and the
// person sees both versions (sync conflicts, explain), settles it by
// taking theirs, keeping theirs own, or writing a third value, and both
// machines end equal. Named TestJourneyTeamLead… so the teamlead gate step runs it.

// TestJourneyTeamLeadStoreConflict is a lead and a teammate retitling one task.
func TestJourneyTeamLeadStoreConflict(t *testing.T) {
	t.Parallel()
	tt := newTLTeam(t)
	w := tt.w
	lee := tt.person(tlLee)
	leeDir := tt.repo[tlLee.Email].Dir
	var taskID string
	w.Step("day1/team-of-two", func() {
		tlStartTeam(t, lee, leeDir, tlLeadSetup, tlOrg, tt.store.SSHURL(), tlLee, []string{tlFolders})
		tt.lore(tlLee, "init").OK(t)
		c := tt.repo[tlLee.Email]
		c.CommitAll("lore init")
		c.Git("push", "-q", "origin", "HEAD")
		tt.lore(tlLee, "memory", "add", tlRuleBody, "--to", tlOrg+"/"+tlProject, "--create").OK(t)
		tt.lore(tlLee, "org", "link", tlOrg, "--project", tlProject, "--yes").OK(t)
		r := tt.lore(tlLee, "task", "add", "ship the login page", "--to", tlOrg+"/"+tlProject, "--json").OK(t)
		var o struct {
			Data struct {
				ID string `json:"id"`
			} `json:"data"`
		}
		tlJSON(t, r, &o)
		taskID = o.Data.ID
		if taskID == "" {
			t.Fatalf("task add --json: %s", r.Stdout)
		}
		_, line := tlInvite(t, lee, leeDir, tlAnn.Email)
		ann := tt.person(tlAnn)
		tlJoin(t, ann, tt.repo[tlAnn.Email].Dir, tlTeamSetup, line, tlAnn, []string{tlFolders})
		tt.lore(tlAnn, "org", "link", tlOrg, "--project", tlProject, "--yes").OK(t)
		tt.lore(tlAnn, "task", "show", taskID).OK(t).Says(t, "ship the login page")
	})
	// conflict makes both people retitle the task, Ann's edit waiting
	// offline, then Lee's lands first: Ann's sync is refused (E_STALE).
	conflict := func(lee, ann string) {
		t.Helper()
		tt.lore(tlAnn, "sync", "run").OK(t)
		tt.m[tlAnn.Email].SetEnv("LORE_OFFLINE", "1")
		tt.lore(tlAnn, "task", "show", taskID).OK(t)
		tt.lore(tlAnn, "task", "edit", taskID, "--title", ann, "--yes").OK(t)
		tt.lore(tlLee, "task", "edit", taskID, "--title", lee, "--yes").OK(t)
		tt.lore(tlLee, "sync", "run").OK(t)
		tt.m[tlAnn.Email].SetEnv("LORE_OFFLINE", "0")
		r := tt.lore(tlAnn, "sync", "run")
		r.Fails(t, 23, "E_STALE", taskID, "lore sync conflicts", "lore sync resolve")
		r.Never(t, "lore memory show")
		t.Logf("the refused sync:\n%s", r)
	}
	title := func(p tlPerson) string {
		t.Helper()
		var o struct {
			Data struct {
				Title string `json:"title"`
			} `json:"data"`
		}
		tlJSON(t, tt.lore(p, "task", "show", taskID, "--json").OK(t), &o)
		return o.Data.Title
	}
	agree := func(want string) {
		t.Helper()
		tt.lore(tlLee, "sync", "run").OK(t)
		for _, p := range []tlPerson{tlLee, tlAnn} {
			if got := title(p); got != want {
				t.Errorf("%s reads the task as %q, want %q", p.Name, got, want)
			}
		}
		tt.lore(tlAnn, "sync", "status").OK(t).Says(t, "refused pushes:   0")
		tt.lore(tlAnn, "sync", "conflicts").OK(t).Says(t, "no conflicts")
	}
	storeFolder := filepath.Join(tt.m[tlAnn.Email].Home, ".lore", "stores", "acme-co", "lore-team", tlProject)

	w.Step("day2/see-both-then-take-theirs", func() {
		w.AdvanceDays(1)
		conflict("Title by LEE", "Title by ANN")
		// both versions, the fields and the command that settles it
		cf := tt.lore(tlAnn, "sync", "conflicts").OK(t)
		t.Logf("sync conflicts:\n%s", cf)
		cf.Says(t, "#1 tasks/"+taskID, "store "+tlOrg+"/"+tlProject,
			"both sides changed title", `"Title by ANN"`, `"Title by LEE"`, "lore sync resolve 1 --take kept|other")
		var js struct {
			Data []struct {
				Store    string   `json:"store"`
				RowID    string   `json:"row_id"`
				Fields   []string `json:"fields"`
				KeptDoc  string   `json:"kept_doc"`
				OtherDoc string   `json:"other_doc"`
			} `json:"data"`
		}
		tlJSON(t, tt.lore(tlAnn, "sync", "conflicts", "--json").OK(t), &js)
		if len(js.Data) != 1 || js.Data[0].RowID != taskID || !strings.Contains(js.Data[0].KeptDoc, "Title by ANN") || !strings.Contains(js.Data[0].OtherDoc, "Title by LEE") || len(js.Data[0].Fields) != 1 || js.Data[0].Fields[0] != "title" {
			t.Errorf("sync conflicts --json does not carry both versions: %+v", js.Data)
		}
		ex := tt.lore(tlAnn, "explain", taskID).OK(t)
		t.Logf("explain:\n%s", ex)
		ex.Says(t, "store "+tlOrg+"/"+tlProject, "CONFLICT", "lore sync conflicts").Never(t, "not found")
		tt.lore(tlAnn, "sync", "status").OK(t).Says(t, taskID, "lore sync conflicts")
		// the folder lore names in its hand-edit hints answers too
		tt.m[tlAnn.Email].Lore(storeFolder, "sync", "conflicts").OK(t).Says(t, taskID, `"Title by LEE"`)
		tt.m[tlAnn.Email].Lore(storeFolder, "sync", "status").OK(t).Says(t, taskID)
		tt.lore(tlAnn, "sync", "resolve", "1", "--take", "other").OK(t).Says(t, "resolved #1", taskID, "took other")
		agree("Title by LEE")
	})

	w.Step("day3/keep-mine", func() {
		w.AdvanceDays(1)
		conflict("Lee again", "Ann again")
		tt.lore(tlAnn, "sync", "resolve", "1", "--take", "kept").OK(t).Says(t, "took kept")
		agree("Ann again")
	})

	w.Step("day4/a-third-title", func() {
		w.AdvanceDays(1)
		conflict("Lee's third", "Ann's third")
		// an edit made after the conflict was reported settles it, whatever its value
		tt.lore(tlAnn, "task", "edit", taskID, "--title", "the agreed title", "--yes").OK(t)
		tt.lore(tlAnn, "sync", "run").OK(t)
		agree("the agreed title")
	})
}
