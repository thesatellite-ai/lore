//go:build stories && !windows

package stories

// O_personal_ask_test.go — story O, devco D-807 (gate G-3, bug hunt H-58):
// recalling from a personal store took a different command per question
// (resource show, search --full, task list --meta, memory list --since)
// and still missed a typo in a name, a nickname, a paraphrased decision
// and a fact that changed. Here an owner-shaped laptop keeps people and a
// project as resources in a personal store with dated notes, a journal in
// a second folder and tasks owed both ways, and one `lore ask --store me
// --query "<question>"` from the home folder answers each question type.

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestStoryOPersonalAsk: one call answers who, typo, nickname, debts,
// decision in other words, a changed fact and a day (devco D-807).
func TestStoryOPersonalAsk(t *testing.T) {
	s := New(t, "O")
	me := s.Remote("github.com/" + oAdminPersonal.Login + "/lore-me")
	ada := s.OwnerShaped(OwnerShape{Personal: oAdminPersonal, Org: oAdminOrg, OrgFolder: oOrg, Alias: oAlias, OrgOwners: []string{oOwner}})
	to := "me/" + psFolder
	s.Step("personal-store/people-notes-journal-tasks", func() {
		ada.Lore(ada.Home, "store", "init", "--name", "me", "--url", me.Dir, "--yes").OK(t)
		ada.Lore(ada.Home, "store", "folder", "add", "me", psFolder).OK(t)
		ada.Lore(ada.Home, "store", "folder", "add", "me", "journal").OK(t)
		for _, k := range []string{"person", "project"} {
			ada.Lore(ada.Home, "resource", "kind", "add", "--kind", k, "--name-format", "dns", "--to", to, "--yes").OK(t)
		}
		for _, f := range []string{"role", "company", "nickname"} {
			ada.Lore(ada.Home, "resource", "field", "add", "--kind", "person", "--field", f, "--type", "text", "--to", to, "--yes").OK(t)
		}
		ada.Lore(ada.Home, "resource", "add", "--kind", "person", "--name", "alice-ng", "--set", "role=engineer", "--set", "company=cobalt-co", "--to", to, "--yes").OK(t)
		ada.Lore(ada.Home, "resource", "edit", "--id", "alice-ng", "--set", "company=acme-co", "--store", to, "--yes").OK(t)
		ada.Lore(ada.Home, "resource", "add", "--kind", "person", "--name", "robert-kim", "--set", "role=designer", "--set", "nickname=Bobby", "--to", to, "--yes").OK(t)
		ada.Lore(ada.Home, "resource", "add", "--kind", "project", "--name", "orbit", "--to", to, "--yes").OK(t)
		for _, n := range []struct{ body, at string }{
			{"Decision for orbit: we chose postgres over mongodb because the reports need joins.", "2026-03-01"},
			{"Budget for orbit is 40k for this year.", "2026-04-01"},
			{"Budget for orbit moved to 55k (was 40k).", "2026-10-01"},
			{"Status update orbit: beta shipped to 12 users.", "2026-09-01"},
		} {
			ada.Lore(ada.Home, "memory", "add", "--body", n.body, "--at", n.at, "--to", to, "--yes").OK(t)
		}
		ada.Lore(ada.Home, "memory", "add", "--body", "Journal 2026-11-02: baked sourdough. Saw ${res:person/alice-ng} about ${res:project/orbit}.", "--at", "2026-11-02", "--meta", "day:date=2026-11-02", "--to", "me/journal", "--yes").OK(t)
		ada.Lore(ada.Home, "memory", "add", "--body", "Journal 2026-11-03: fixed the kitchen tap.", "--at", "2026-11-03", "--meta", "day:date=2026-11-03", "--to", "me/journal", "--yes").OK(t)
		ada.Lore(ada.Home, "task", "add", "--title", "send the price list to robert-kim", "--meta", "owed_by=me", "--meta", "owed_to=robert-kim", "--to", to, "--yes").OK(t)
		ada.Lore(ada.Home, "task", "add", "--title", "robert-kim owes me the board deck", "--meta", "owed_by=robert-kim", "--meta", "owed_to=me", "--to", to, "--yes").OK(t)
	})
	s.Step("personal-store/ask-each-question-type", func() {
		cases := []struct {
			q         string
			want, not []string
		}{
			{"who is alise-ng", []string{"person alice-ng", "role: engineer", "company: acme-co (changed", "was cobalt-co)"}, nil},
			{"what do I owe Bobby", []string{"send the price list to robert-kim"}, []string{"board deck"}},
			{"what does robert-kim owe me", []string{"robert-kim owes me the board deck"}, []string{"price list"}},
			{"what was the reasoning behind orbit's pick", []string{"we chose postgres over mongodb because the reports need joins"}, nil},
			{"how much money can orbit spend now", []string{"moved to 55k (was 40k)", "newest: it replaces"}, nil},
			{"what happened on 2026-11-02", []string{"baked sourdough", "(me/journal)"}, []string{"kitchen tap"}},
		}
		for _, c := range cases {
			r := ada.Lore(ada.Home, "ask", "--store", "me", "--query", c.q)
			r.OK(t)
			out := r.Out()
			for _, w := range c.want {
				if !strings.Contains(out, w) {
					t.Errorf("[O D-807] `lore ask --store me --query %q` does not say %q:\n%s", c.q, w, r)
				}
			}
			for _, n := range c.not {
				if strings.Contains(out, n) {
					t.Errorf("[O D-807] `lore ask --store me --query %q` says %q:\n%s", c.q, n, r)
				}
			}
			if !strings.Contains(out, "cited: ") {
				t.Errorf("[O D-807] `lore ask` cites no id:\n%s", r)
			}
		}
		j := ada.Lore(ada.Home, "ask", "--store", "me", "--query", "everything about alice-ng", "--max-chars", "400", "--json")
		j.OK(t)
		var got struct {
			Resources []struct{ Name string }  `json:"resources"`
			Rows      []struct{ Place string } `json:"rows"`
			Cited     []string                 `json:"cited"`
			Left      int                      `json:"left"`
		}
		if err := json.Unmarshal([]byte(j.Out()), &got); err != nil || len(got.Resources) != 1 || got.Resources[0].Name != "alice-ng" || len(got.Cited) == 0 {
			t.Errorf("[O D-807] ask --json: %v %+v\n%s", err, got, j)
		}
	})
}
