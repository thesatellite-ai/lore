//go:build journeys && !windows

package journeys

import (
	"strconv"
	"testing"
)

// storeconflict_holdsone_test.go — devco O-5 and O-6. Ann edits three
// tasks of the team store while offline; Lee retitles two of them first.
// Ann's sync is refused for the two clashing tasks only: her edit of the
// third one is sent and Lee reads it at once (O-5). She then works down
// the conflict list as printed: resolve --id 1 --take other shows Lee's
// title on her machine at once and leaves #2 open, and resolve --id 2
// still means the row printed as #2 (O-6; it used to fail E_NOT_FOUND).
// Named TestJourneyTeamLead… so the teamlead gate step runs it.

// TestJourneyTeamLeadStoreConflictHoldsOnlyItsRows is the scenario above.
func TestJourneyTeamLeadStoreConflictHoldsOnlyItsRows(t *testing.T) {
	t.Parallel()
	tt := newTLTeam(t)
	w := tt.w
	lee := tt.person(tlLee)
	leeDir := tt.repo[tlLee.Email].Dir
	var clash1, clash2, clean string
	add := func(title string) string {
		t.Helper()
		var o struct {
			Data struct {
				ID string `json:"id"`
			} `json:"data"`
		}
		tlJSON(t, tt.lore(tlLee, "task", "add", title, "--to", tlOrg+"/"+tlProject, "--json").OK(t), &o)
		if o.Data.ID == "" {
			t.Fatalf("task add %q printed no id", title)
		}
		return o.Data.ID
	}
	title := func(p tlPerson, id string) string {
		t.Helper()
		var o struct {
			Data struct {
				Title string `json:"title"`
			} `json:"data"`
		}
		tlJSON(t, tt.lore(p, "task", "show", id, "--json").OK(t), &o)
		return o.Data.Title
	}

	w.Step("day1/team-of-two-three-tasks", func() {
		tlStartTeam(t, lee, leeDir, tlLeadSetup, tlOrg, tt.store.SSHURL(), tlLee, []string{tlFolders})
		tt.lore(tlLee, "init").OK(t)
		c := tt.repo[tlLee.Email]
		c.CommitAll("lore init")
		c.Git("push", "-q", "origin", "HEAD")
		tt.lore(tlLee, "memory", "add", tlRuleBody, "--to", tlOrg+"/"+tlProject, "--create").OK(t)
		tt.lore(tlLee, "org", "link", tlOrg, "--project", tlProject, "--yes").OK(t)
		clash1, clash2, clean = add("ship the login page"), add("write the release notes"), add("tidy the backlog")
		tt.lore(tlLee, "sync", "run").OK(t)
		_, line := tlInvite(t, lee, leeDir, tlAnn.Email)
		ann := tt.person(tlAnn)
		tlJoin(t, ann, tt.repo[tlAnn.Email].Dir, tlTeamSetup, line, tlAnn, []string{tlFolders})
		tt.lore(tlAnn, "org", "link", tlOrg, "--project", tlProject, "--yes").OK(t)
		tt.lore(tlAnn, "task", "show", clean).OK(t).Says(t, "tidy the backlog")
	})

	w.Step("day2/the-clean-edit-goes-out-the-clashes-wait", func() {
		w.AdvanceDays(1)
		tt.m[tlAnn.Email].SetEnv("LORE_OFFLINE", "1")
		tt.lore(tlAnn, "task", "show", clean).OK(t).Says(t, clean)
		tt.lore(tlAnn, "task", "edit", clash1, "--title", "login by ANN", "--yes").OK(t)
		tt.lore(tlAnn, "task", "edit", clash2, "--title", "notes by ANN", "--yes").OK(t)
		tt.lore(tlAnn, "task", "edit", clean, "--title", "backlog by ANN", "--yes").OK(t)
		tt.lore(tlLee, "task", "edit", clash1, "--title", "login by LEE", "--yes").OK(t)
		tt.lore(tlLee, "task", "edit", clash2, "--title", "notes by LEE", "--yes").OK(t)
		tt.lore(tlLee, "sync", "run").OK(t)
		tt.m[tlAnn.Email].SetEnv("LORE_OFFLINE", "0")
		r := tt.lore(tlAnn, "sync", "run").Fails(t, 23, "E_STALE", "lore sync conflicts")
		t.Logf("the refused sync:\n%s", r)
		tt.lore(tlLee, "sync", "run").OK(t)
		if got := title(tlLee, clean); got != "backlog by ANN" {
			t.Fatalf("Lee reads the task only Ann changed as %q, want her edit sent while the clashes wait (O-5)", got)
		}
		for id, want := range map[string]string{clash1: "login by LEE", clash2: "notes by LEE"} {
			if got := title(tlLee, id); got != want {
				t.Fatalf("Lee reads %s as %q, want his own title (Ann's edit is held)", id, got)
			}
		}
		tt.lore(tlAnn, "sync", "status").OK(t).Says(t, clash1, clash2)
	})

	w.Step("day2/work-down-the-printed-list", func() {
		var js struct {
			Data []struct {
				ID    int    `json:"id"`
				RowID string `json:"row_id"`
			} `json:"data"`
		}
		tlJSON(t, tt.lore(tlAnn, "sync", "conflicts", "--json").OK(t), &js)
		if len(js.Data) != 2 {
			t.Fatalf("want the two clashing tasks listed, got %+v", js.Data)
		}
		first, second := js.Data[0], js.Data[1]
		leeTitle := map[string]string{clash1: "login by LEE", clash2: "notes by LEE"}
		annTitle := map[string]string{clash1: "login by ANN", clash2: "notes by ANN"}
		tt.lore(tlAnn, "sync", "resolve", strconv.Itoa(first.ID), "--take", "other").OK(t).
			Says(t, "resolved #"+strconv.Itoa(first.ID), first.RowID, "took other", "1 other conflict(s) still open")
		if got := title(tlAnn, first.RowID); got != leeTitle[first.RowID] {
			t.Fatalf("after take other Ann reads %q, want Lee's title at once (O-6)", got)
		}
		tt.lore(tlAnn, "sync", "resolve", strconv.Itoa(second.ID), "--take", "kept").OK(t).
			Says(t, "resolved #"+strconv.Itoa(second.ID), second.RowID, "took kept").Never(t, "E_NOT_FOUND")
		tt.lore(tlAnn, "sync", "resolve", strconv.Itoa(first.ID), "--take", "other").Fails(t, 27, "E_NOT_FOUND", "is not open any more")
		tt.lore(tlLee, "sync", "run").OK(t)
		for _, p := range []tlPerson{tlLee, tlAnn} {
			if got := title(p, first.RowID); got != leeTitle[first.RowID] {
				t.Errorf("%s reads %s as %q, want %q", p.Name, first.RowID, got, leeTitle[first.RowID])
			}
			if got := title(p, second.RowID); got != annTitle[second.RowID] {
				t.Errorf("%s reads %s as %q, want %q", p.Name, second.RowID, got, annTitle[second.RowID])
			}
		}
		tt.lore(tlAnn, "sync", "conflicts").OK(t).Says(t, "no conflicts")
	})
}
