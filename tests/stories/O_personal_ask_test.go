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
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
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
			{"who is alise-ng", []string{"alice-ng (person): ", "role: engineer", "company: acme-co (changed", "was cobalt-co)"}, nil},
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
			if !strings.HasPrefix(c.q, "who is ") && !strings.Contains(out, "cited: ") {
				t.Errorf("[O D-807] `lore ask` cites no id:\n%s", r)
			}
		}
		// devco D-827 (recall gate G-4): typed answers stay lighter than
		// the files grep reads: "who is X" is one card line (no id, no
		// echo of the question; --json keeps the id), a debt answer its
		// task lines and the ids once (no heading, status, place, person
		// card)
		who := ada.Lore(ada.Home, "ask", "--store", "me", "--query", "who is alise-ng").OK(t).Out()
		if lines := strings.Split(strings.TrimSpace(who), "\n"); len(lines) != 1 || strings.Contains(who, "res_") || strings.Contains(who, "who is") {
			t.Errorf("[O D-827] who is X is not one card line:\n%s", who)
		}
		owe := ada.Lore(ada.Home, "ask", "--store", "me", "--query", "what do I owe Bobby").OK(t).Out()
		if !strings.HasPrefix(owe, "send the price list to robert-kim\ncited: tsk_") || strings.Count(owe, "\n") != 2 {
			t.Errorf("[O D-827] the debt answer is not its task line and the id once:\n%s", owe)
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
	// devco D-822 (bug hunt H-81): a small model summarising everything
	// about a person dropped "watched the match after lunch" from a
	// journal row printed as "Saw ${res:person/…} about ${res:project/…}".
	// The newest row of each folder prints one line each, with its day
	// and folder, a reference read as the name; --json keeps the stored
	// text.
	s.Step("personal-store/about-one-line-each", func() {
		r := ada.Lore(ada.Home, "ask", "--store", "me", "--query", "everything about alice-ng")
		r.OK(t)
		out := r.Out()
		// devco D-836 (bug hunt H-93): plain headings, each row one line
		// word for word in quotes, the repeated day and every id left
		// out, so a small model keeps the journal activity
		lines := "  2026-11-02, me/journal: \"baked sourdough. Saw alice-ng about orbit.\"\n"
		body, _, _ := strings.Cut(out, "cited: ")
		if !strings.Contains(out, "\nwho: person alice-ng (") || !strings.Contains(out, "\nlatest (the newest row of each folder and the newest decision, status, budget and meeting row, word for word):\n") || !strings.Contains(out, lines) || strings.Contains(out, "${res:") || strings.Contains(body, "mem_") || strings.Contains(body, "res_") {
			t.Errorf("[O D-822/D-836] everything about: want who:/latest headings, the journal row as one quoted line with names, no ${res:…}, no id before cited:\n%s", r)
		}
		j := ada.Lore(ada.Home, "ask", "--store", "me", "--query", "everything about alice-ng", "--json")
		j.OK(t)
		if !strings.Contains(j.Out(), "Saw ${res:person/alice-ng} about ${res:project/orbit}.") {
			t.Errorf("[O D-822] ask --json does not keep the stored text:\n%s", j)
		}
	})
	// devco D-844 (bug hunt H-101): in a blind agent run a small model
	// answered "who is X" with the card's fields and no name (the card
	// began "person alice-ng:"), and an agent that asked a bare project
	// name got the three newest rows, not the decision. The card leads
	// with the name; a bare name is everything about it, whose latest rows
	// hold the newest decision, status and budget row next to the newest
	// row of each folder.
	s.Step("personal-store/name-first-and-bare-name", func() {
		who := ada.Lore(ada.Home, "ask", "--store", "me", "--query", "who is alise-ng").OK(t).Out()
		if !strings.HasPrefix(who, "alice-ng (person): ") {
			t.Errorf("[O D-844] the who-is card does not lead with the name:\n%s", who)
		}
		out := ada.Lore(ada.Home, "ask", "--store", "me", "--query", "orbit").OK(t).Out()
		latest, _, _ := strings.Cut(out, "\nopen tasks")
		latest, _, _ = strings.Cut(latest, "cited: ")
		for _, want := range []string{"\nwho: project orbit (", "\nlatest (the newest row of each folder and the newest decision, status, budget and meeting row, word for word):\n",
			`"Decision for orbit: we chose postgres over mongodb because the reports need joins."`, `"Budget for orbit moved to 55k (was 40k)."`, `"Status update orbit: beta shipped to 12 users."`} {
			if !strings.Contains(latest, want) {
				t.Errorf("[O D-844] `lore ask --store me --query orbit`: no %q among its latest rows:\n%s", want, out)
			}
		}
	})
	// devco D-836 (bug hunt H-93): a small model summarising a changed
	// fact's two rows lost "moved to 55k (was 40k)"; the answer leads with
	// one "now:" line holding the newest text, the day it changed and what
	// it was before.
	s.Step("personal-store/changed-fact-now-line", func() {
		r := ada.Lore(ada.Home, "ask", "--store", "me", "--query", "how much money can orbit spend now")
		r.OK(t)
		first, _, _ := strings.Cut(r.Out(), "\n")
		if !strings.HasPrefix(first, "now: ") || !strings.Contains(first, "moved to 55k (was 40k)") || !strings.Contains(first, "(changed ") || !strings.Contains(first, "before that: ") {
			t.Errorf("[O D-836] a changed fact does not lead with its now line:\n%s", r)
		}
	})
	// devco:D-826: a warm read opens the folder caches from their stamps
	// (no schema parse) and starts no git process outside a repository;
	// it must answer exactly as the cold read, and a fact written between
	// two reads (the stamp no longer matches) shows on the next one.
	s.Step("personal-store/warm-read-answers-as-cold", func() {
		q := "what is the status of orbit"
		cold := ada.Lore(ada.Home, "ask", "--store", "me", "--query", q)
		cold.OK(t)
		for i := range 2 {
			if warm := ada.Lore(ada.Home, "ask", "--store", "me", "--query", q); warm.Out() != cold.Out() {
				t.Errorf("[O D-826] warm ask %d differs from the cold one:\ncold:\n%s\nwarm:\n%s", i+1, cold, warm)
			}
		}
		ada.Lore(ada.Home, "memory", "add", "--body", "Status update orbit: general availability for 40 users.", "--at", "2026-11-15", "--to", to, "--yes").OK(t)
		after := ada.Lore(ada.Home, "ask", "--store", "me", "--query", q)
		after.OK(t)
		if !strings.Contains(after.Out(), "general availability for 40 users") {
			t.Errorf("[O D-826] the ask after a write misses the new status (a stale cache was read):\n%s", after)
		}
		if again := ada.Lore(ada.Home, "ask", "--store", "me", "--query", q); again.Out() != after.Out() {
			t.Errorf("[O D-826] the warm ask after the write differs:\nfirst:\n%s\nwarm:\n%s", after, again)
		}
	})
	s.Step("personal-store/date-question-follows-edits", func() {
		// D-834: row_meta reads a metadata copy kept beside the cached rows;
		// a date question must follow an add, a changed day and a delete
		on := func(day string) string {
			r := ada.Lore(ada.Home, "ask", "--store", "me", "--query", "what happened on "+day)
			r.OK(t)
			return r.Out()
		}
		add := ada.Lore(ada.Home, "memory", "add", "--body", "Journal 2026-11-05: repotted the fern.", "--at", "2026-11-05", "--meta", "day:date=2026-11-05", "--to", "me/journal", "--yes", "--json")
		add.OK(t)
		id := regexp.MustCompile(`mem_[0-9a-f]{20,}`).FindString(add.Out())
		if id == "" {
			t.Fatalf("[O D-834] no memory id in the add output:\n%s", add)
		}
		if out := on("2026-11-05"); !strings.Contains(out, "repotted the fern") {
			t.Errorf("[O D-834] the new day is not found:\n%s", out)
		}
		ada.Lore(ada.Home, "memory", "edit", "--id", id, "--meta", "day:date=2026-11-06", "--at", "2026-11-06", "--yes").OK(t)
		if out := on("2026-11-05"); strings.Contains(out, "repotted the fern") {
			t.Errorf("[O D-834] the old day still answers after the edit (stale metadata):\n%s", out)
		}
		if out := on("2026-11-06"); !strings.Contains(out, "repotted the fern") {
			t.Errorf("[O D-834] the edited day is not found:\n%s", out)
		}
		ada.Lore(ada.Home, "memory", "delete", "--id", id, "--yes").OK(t)
		if out := on("2026-11-06"); strings.Contains(out, "repotted the fern") {
			t.Errorf("[O D-834] a deleted row still answers a date question:\n%s", out)
		}
	})
	// devco:D-833 (bug hunt H-90): a one-shot ask cannot answer warm in
	// under 50 ms (process start + one schema parse of every cache), so a
	// local read server runs reads with its caches open. It must answer
	// every question exactly as the one-shot process (stdout, stderr and
	// exit code), show a write made between two asks, and when it dies
	// mid-command the command still answers, one-shot.
	s.Step("personal-store/read-server-answers-as-one-shot", func() {
		qs := []string{"who is alise-ng", "what do I owe Bobby", "how much money can orbit spend now", "what happened on 2026-11-02", "everything about alice-ng", "where does alice-ng work now"}
		ask := func(q string) teamharness.Result { return ada.Lore(ada.Home, "ask", "--store", "me", "--query", q) }
		oneShot := map[string]teamharness.Result{}
		for _, q := range qs {
			oneShot[q] = ask(q) // a test build runs one-shot unless LORE_READ_SERVER=1
		}
		refused := ada.Lore(ada.Home, "ask", "--store", "nope", "--query", "who is alice-ng")
		log := filepath.Join(t.TempDir(), "read-server.log")
		sock := filepath.Join(ada.Home, ".lore", "run", "read.sock")
		ada.SetEnv("LORE_READ_SERVER", "1")
		ada.SetEnv("LORE_TEST_READ_SERVER_LOG", log)
		ada.SetEnv("LORE_TEST_READ_SERVER_IDLE", "1m")
		defer func() {
			for _, k := range []string{"LORE_READ_SERVER", "LORE_TEST_READ_SERVER_LOG", "LORE_TEST_READ_SERVER_IDLE", "LORE_TEST_READ_SERVER_DIE"} {
				ada.SetEnv(k, "")
			}
			_ = os.Remove(sock) // the server stops once its socket is gone
		}()
		started := func() {
			t.Helper()
			for deadline := time.Now().Add(20 * time.Second); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
				if _, err := os.Stat(sock); err == nil {
					return
				}
			}
			t.Fatalf("[O D-833] no read server listens on %s after a read started it", sock)
		}
		if r := ask(qs[0]); r.Out() != oneShot[qs[0]].Out() { // starts the server; runs one-shot itself
			t.Errorf("[O D-833] the read that starts the server differs:\n%s\nwant:\n%s", r, oneShot[qs[0]])
		}
		started()
		_ = os.Remove(log)
		same := func(what string, got, want teamharness.Result) {
			t.Helper()
			if got.Stdout != want.Stdout || got.Stderr != want.Stderr || got.Code != want.Code {
				t.Errorf("[O D-833] %s: served differs from one-shot:\nserved: %s\none-shot: %s", what, got, want)
			}
		}
		for _, q := range qs {
			same(q, ask(q), oneShot[q])
		}
		same("a refused read", ada.Lore(ada.Home, "ask", "--store", "nope", "--query", "who is alice-ng"), refused)
		if b, _ := os.ReadFile(log); strings.Count(string(b), "served\n") != len(qs)+1 || strings.Contains(string(b), "one-shot") {
			t.Errorf("[O D-833] want every ask served by the read server, the log says:\n%s", b)
		}
		ada.Lore(ada.Home, "memory", "add", "--body", "Status update orbit: 60 users on the paid plan.", "--at", "2026-11-16", "--to", to, "--yes").OK(t)
		if r := ask("what is the status of orbit"); !strings.Contains(r.Out(), "60 users on the paid plan") {
			t.Errorf("[O D-833] the served ask after a write misses it (a stale lens was reused):\n%s", r)
		}
		// devco:D-842 (bug hunt H-99): the server keeps the resources and
		// open tasks every ask reads until a file changes; a new person, a
		// changed field and a new owed task show in the next served ask.
		ask("who is robert-kim") // kept by the server
		ada.Lore(ada.Home, "resource", "add", "--kind", "person", "--name", "nora-quill", "--set", "role=accountant", "--to", to, "--yes").OK(t)
		ada.Lore(ada.Home, "resource", "edit", "--id", "robert-kim", "--set", "role=art-director", "--store", to, "--yes").OK(t)
		ada.Lore(ada.Home, "task", "add", "--title", "pay nora-quill the audit fee", "--meta", "owed_by=me", "--meta", "owed_to=nora-quill", "--to", to, "--yes").OK(t)
		for q, want := range map[string]string{"who is nora-quill": "accountant", "who is robert-kim": "art-director", "what do I owe nora-quill": "pay nora-quill the audit fee"} {
			r := ask(q)
			ada.SetEnv("LORE_READ_SERVER", "0")
			one := ask(q)
			ada.SetEnv("LORE_READ_SERVER", "1")
			if !strings.Contains(r.Out(), want) {
				t.Errorf("[O D-842] the served ask %q after a write misses %q (a kept answer outlived its rows):\n%s", q, want, r)
			}
			same(q+" after a write", r, one)
		}
		// devco:D-846 (bug hunt H-103): a served status, decision or date
		// question picks its rows from the text rows the server keeps (one
		// statement naming nothing of the question) until a file changes; a
		// new decision, a memory dated in the window and an edited memory
		// show in the next served ask, exactly as one-shot prints them.
		for _, q := range []string{"what did we decide for orbit and why", "what happened on 2026-11-17", "what is the status of orbit"} {
			ask(q) // kept by the server
		}
		ada.Lore(ada.Home, "decision", "add", "--title", "orbit bills yearly", "--body", "orbit moves to yearly billing because churn halves", "--to", to, "--yes").OK(t)
		ada.Lore(ada.Home, "memory", "add", "--body", "Signed the lease for the quiet studio.", "--at", "2026-11-17", "--to", to, "--yes").OK(t)
		ada.Lore(ada.Home, "memory", "add", "--body", "Status update orbit: 75 users on the paid plan.", "--at", "2026-11-18", "--to", to, "--yes").OK(t)
		for q, want := range map[string]string{"what did we decide for orbit and why": "yearly billing because churn halves", "what happened on 2026-11-17": "lease for the quiet studio", "what is the status of orbit": "75 users on the paid plan"} {
			r := ask(q)
			ada.SetEnv("LORE_READ_SERVER", "0")
			one := ask(q)
			ada.SetEnv("LORE_READ_SERVER", "1")
			if !strings.Contains(r.Out(), want) {
				t.Errorf("[O D-846] the served ask %q after a write misses %q (kept text rows outlived their files):\n%s", q, want, r)
			}
			same(q+" after a write (text rows)", r, one)
		}
		// devco:D-847 (bug hunt H-104): an everything-about answer splits
		// each row into words once and tests every topic against that set;
		// a changed company and a new row holding a budget word show in the
		// next served "where does X work now", exactly as one-shot prints it.
		ask("where does alice-ng work now") // kept by the server
		ada.Lore(ada.Home, "resource", "edit", "--id", "alice-ng", "--set", "company=lantern-co", "--store", to, "--yes").OK(t)
		ada.Lore(ada.Home, "memory", "add", "--body", "Lunch with alice-ng: the lantern-co move has a budget of 3k.", "--at", "2026-11-19", "--to", to, "--yes").OK(t)
		for _, want := range []string{"lantern-co", "the lantern-co move has a budget of 3k"} {
			r := ask("where does alice-ng work now")
			ada.SetEnv("LORE_READ_SERVER", "0")
			one := ask("where does alice-ng work now")
			ada.SetEnv("LORE_READ_SERVER", "1")
			if !strings.Contains(r.Out(), want) {
				t.Errorf("[O D-847] the served ask %q after a write misses %q:\n%s", "where does alice-ng work now", want, r)
			}
			same("where does alice-ng work now after a write (one word split per row)", r, one)
		}
		ada.SetEnv("LORE_TEST_READ_SERVER_DIE", "1")
		_ = os.Remove(log)
		same("a server gone mid-command", ask(qs[1]), oneShot[qs[1]])
		if b, _ := os.ReadFile(log); string(b) != "one-shot\n" {
			t.Errorf("[O D-833] the call whose server died should run one-shot, the log says:\n%s", b)
		}
		// devco:D-838 (bug hunt H-95): a server killed after it printed the
		// start of an answer must not make the CLI run the command again
		// (the start would show twice): the CLI exits 1 and says so.
		ada.SetEnv("LORE_TEST_READ_SERVER_DIE", "")
		_ = os.Remove(sock)                                                  // the dead server's
		_ = os.Remove(filepath.Join(ada.Home, ".lore", "run", "read.start")) // a start was asked for under 30 s ago
		ask(qs[0])                                                           // starts a new server
		started()
		ada.SetEnv("LORE_TEST_READ_SERVER_DIE", "printing")
		_ = os.Remove(log)
		r := ask(qs[1])
		if r.Stdout != "the start of an answer\n" || r.Code != 1 || !strings.Contains(r.Stderr, "the local read server stopped while it was answering") || !strings.Contains(r.Stderr, "LORE_READ_SERVER=0") {
			t.Errorf("[O D-838] a server killed mid-print: want the start printed once, exit 1 and the reason; got:\n%s", r)
		}
		if b, _ := os.ReadFile(log); string(b) != "lost\n" {
			t.Errorf("[O D-838] the call whose server died mid-print must not run again one-shot, the log says:\n%s", b)
		}
		// devco:D-845 (bug hunt H-102): the server grew to ~600 MB RSS after
		// a few hundred calls (each call's schema registry stayed held by a
		// process-wide memo). Once warm, its live heap after a full
		// collection stays where it was over many more asks.
		ada.SetEnv("LORE_TEST_READ_SERVER_DIE", "")
		_ = os.Remove(sock)
		_ = os.Remove(filepath.Join(ada.Home, ".lore", "run", "read.start"))
		ask(qs[0])
		started()
		heap := filepath.Join(t.TempDir(), "heap")
		ada.SetEnv("LORE_TEST_READ_SERVER_HEAP", heap)
		defer ada.SetEnv("LORE_TEST_READ_SERVER_HEAP", "")
		const calls, warm = 90, 20
		for i := range calls {
			ask(qs[i%len(qs)]).OK(t)
		}
		var live []int
		raw, _ := os.ReadFile(heap)
		for _, f := range strings.Fields(string(raw)) {
			if v, err := strconv.Atoi(f); err == nil {
				live = append(live, v)
			}
		}
		if len(live) != calls {
			t.Fatalf("[O D-845] the server reported its heap after %d of %d asks (not all served by one server)", len(live), calls)
		}
		base, top := slices.Max(live[warm-len(qs):warm]), slices.Max(live[warm:])
		if top > base+4<<20 {
			t.Errorf("[O D-845] the read server's live heap grew from %d KB once warm to %d KB over %d more asks: something it keeps across calls has no bound", base>>10, top>>10, calls-warm)
		}
	})
	s.Step("personal-store/sql-reads-the-tables-it-names", func() {
		// D-837: lore sql builds views only for the tables a statement
		// names; the answer is the full lens's (a 'sqlite_full' literal
		// forces one), a derived view, a schema listing or a pragma still
		// sees every table, and a name in any case or as <table>_all reads
		sql := func(q, want string) {
			r := ada.Lore(ada.Home, "sql", "--store", "me", "--query", q)
			r.OK(t)
			full := ada.Lore(ada.Home, "sql", "--store", "me", "--query", "SELECT * FROM ("+q+") WHERE 'sqlite_full' <> ''")
			full.OK(t)
			if !strings.Contains(r.Out(), want) || r.Out() != full.Out() {
				t.Errorf("[O D-837] lore sql %q misses %q or differs from the full lens:\n%s\nfull lens:\n%s", q, want, r, full)
			}
		}
		sql("SELECT body FROM MEMORIES_ALL WHERE body LIKE '%postgres over mongodb%'", "we chose postgres over mongodb")
		sql("SELECT name FROM resources_all WHERE name = 'robert-kim'", "robert-kim")
		sql("SELECT name, kind FROM res__all WHERE name = 'orbit'", "project")
		sql("SELECT name FROM sqlite_temp_master WHERE name = 'decisions_all'", "decisions_all")
		sql("SELECT name FROM pragma_table_info('tasks') WHERE name = 'title'", "title")
		sql("SELECT t.title FROM tasks_all t, \"resources_all\" r WHERE r.name = 'robert-kim' AND t.title LIKE '%price list%'", "send the price list")
	})
}
