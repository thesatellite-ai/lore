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
	"regexp"
	"strings"
	"testing"
)

// TestStoryOPersonalAsk: one call answers who, typo, nickname, debts,
// decision in other words, a changed fact and a day (devco D-807); the
// item that answers the question is read first: the status row before the
// project, the owed task before the person, the person then their newest
// journal row for everything about X (devco D-818). A fact that changed
// (a newer note on the same thing, memory add --supersedes, memory
// invalidate) is never handed out as the answer: the current row leads and
// the old one is marked as history in ask, search and memory list (devco
// D-820).
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
	s.Step("personal-store/answer-first", func() {
		type ranked struct {
			Ranked    []struct{ Kind, ID string }        `json:"ranked"`
			Rows      []struct{ ID, Body, Place string } `json:"rows"`
			Tasks     []struct{ ID, Title string }       `json:"tasks"`
			Resources []struct{ ID, Name string }        `json:"resources"`
		}
		first := func(q string) (ranked, string) {
			r := ada.Lore(ada.Home, "ask", "--store", "me", "--query", q, "--json")
			r.OK(t)
			var got ranked
			if err := json.Unmarshal([]byte(r.Out()), &got); err != nil || len(got.Ranked) == 0 {
				t.Fatalf("[O D-818] ask --json %q: %v\n%s", q, err, r)
			}
			return got, r.Out()
		}
		got, out := first("what is the status of orbit")
		if got.Ranked[0].Kind != "row" || len(got.Rows) == 0 || got.Ranked[0].ID != got.Rows[0].ID || !strings.Contains(got.Rows[0].Body, "Status update orbit") {
			t.Errorf("[O D-818] the status row is not read first:\n%s", out)
		}
		got, out = first("what do I owe Bobby")
		if got.Ranked[0].Kind != "task" || len(got.Tasks) == 0 || !strings.Contains(got.Tasks[0].Title, "price list") {
			t.Errorf("[O D-818] the owed task is not read first:\n%s", out)
		}
		got, out = first("everything about alice-ng")
		if len(got.Ranked) < 2 || got.Ranked[0].Kind != "resource" || got.Ranked[1].Kind != "row" {
			t.Errorf("[O D-818] everything about: not the person then a row:\n%s", out)
		}
		text := ada.Lore(ada.Home, "ask", "--store", "me", "--query", "what is the status of orbit")
		text.OK(t)
		if st, pr := strings.Index(text.Out(), "Status update orbit"), strings.Index(text.Out(), "project orbit"); st < 0 || pr < 0 || st > pr {
			t.Errorf("[O D-818] the text prints the project before its status:\n%s", text)
		}
	})
	s.Step("personal-store/stale-facts-are-history", func() {
		memID := regexp.MustCompile(`mem_[0-9a-f]{20,}`)
		add := func(args ...string) string {
			r := ada.Lore(ada.Home, append([]string{"memory", "add"}, append(args, "--to", to, "--yes")...)...)
			r.OK(t)
			return memID.FindString(r.Out())
		}
		old := add("--body", "gym: ironworks gym")
		cur := add("--body", "Since the move I train at peakfit, the gym two streets from the new flat.", "--supersedes", old)
		ins := add("--body", "My car insurance: harbourline.")
		ada.Lore(ada.Home, "memory", "invalidate", "--id", ins, "--yes").OK(t)
		if old == "" || cur == "" || ins == "" {
			t.Fatalf("[O D-820] memory add printed no id: %q %q %q", old, cur, ins)
		}
		type row struct{ ID, Body, History string }
		ask := func(q string) ([]row, string) {
			r := ada.Lore(ada.Home, "ask", "--store", "me", "--query", q, "--json")
			r.OK(t)
			var got struct {
				Rows []row `json:"rows"`
			}
			if err := json.Unmarshal([]byte(r.Stdout), &got); err != nil || len(got.Rows) == 0 {
				t.Fatalf("[O D-820] ask --json %q: %v\n%s", q, err, r)
			}
			return got.Rows, r.Stdout
		}
		// a newer note on the same thing: the older one is history
		rows, out := ask("how much money can orbit spend now")
		if rows[0].History != "" || !strings.Contains(rows[0].Body, "moved to 55k") {
			t.Errorf("[O D-820] the newest budget is not the answer:\n%s", out)
		}
		for _, r := range rows {
			if strings.Contains(r.Body, "is 40k") && !strings.HasPrefix(r.History, "history: replaced by ") {
				t.Errorf("[O D-820] the old budget is presented as current:\n%s", out)
			}
		}
		// --supersedes: the superseded row never leads, and is marked
		rows, out = ask("what is my gym now")
		if rows[0].ID != cur || rows[0].History != "" {
			t.Errorf("[O D-820] the current gym is not the answer:\n%s", out)
		}
		for _, r := range rows {
			if r.ID == old && r.History != "history: superseded by "+cur {
				t.Errorf("[O D-820] the superseded gym is not marked as history:\n%s", out)
			}
		}
		// memory invalidate: past valid_until is history
		rows, out = ask("who is my car insurance with")
		if rows[0].ID != ins || !strings.HasPrefix(rows[0].History, "history: valid until ") {
			t.Errorf("[O D-820] the invalidated insurance is not marked as history:\n%s", out)
		}
		sr := ada.Lore(ada.Home, "search", "--query", "gym", "--store", "me")
		sr.OK(t)
		if c, o := strings.Index(sr.Out(), cur), strings.Index(sr.Out(), old); c < 0 || o < 0 || c > o || !strings.Contains(sr.Out(), "[history: superseded by "+cur+"]") {
			t.Errorf("[O D-820] lore search lists the superseded gym first or unmarked:\n%s", sr)
		}
		l := ada.Lore(ada.Home, "memory", "list", "--store", to)
		l.OK(t)
		if !strings.Contains(l.Out(), "[history: superseded by "+cur+"]") || !strings.Contains(l.Out(), "[history: valid until ") {
			t.Errorf("[O D-820] memory list does not mark old facts as history:\n%s", l)
		}
	})
}
