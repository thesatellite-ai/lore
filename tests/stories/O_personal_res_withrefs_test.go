//go:build stories && !windows

package stories

// O_personal_res_withrefs_test.go — story O, devco D-792 (owner P-19, bug
// hunt H-49): "everything about alice" in a personal knowledge base took
// one call per note: resource show printed the person only, graph --up
// listed the notes by title, and neither took --kind or --name. Here an
// owner-shaped laptop keeps people as resources in a personal store with
// dated notes and a task pointing at one, and one `resource show --kind
// person --name alice-ng --with-refs` from the home folder prints the
// person and every note and task about them, text in full, newest first;
// --max-refs caps it and counts the rest.

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestStoryOPersonalResWithRefs: one read returns a person and every row
// that references them, newest first (devco D-792).
func TestStoryOPersonalResWithRefs(t *testing.T) {
	s := New(t, "O")
	me := s.Remote("github.com/" + oAdminPersonal.Login + "/lore-me")
	ada := s.OwnerShaped(OwnerShape{Personal: oAdminPersonal, Org: oAdminOrg, OrgFolder: oOrg, Alias: oAlias, OrgOwners: []string{oOwner}})
	to := "me/" + psFolder
	notes := []struct{ body, at string }{ // oldest first
		{"alice-ng was at globex, met at the 2019 summit ${res:person/alice-ng}", "2020-01-10"},
		{"${res:person/alice-ng} joins acme-co as CFO\nshe owns the orbit budget now", "2021-03-01"},
	}
	s.Step("personal-store/people-and-notes", func() {
		ada.Lore(ada.Home, "store", "init", "--name", "me", "--url", me.Dir, "--yes").OK(t)
		ada.Lore(ada.Home, "store", "folder", "add", "me", psFolder).OK(t)
		ada.Lore(ada.Home, "resource", "kind", "add", "--kind", "person", "--name-format", "dns", "--to", to, "--yes").OK(t)
		for _, p := range []string{"alice-ng", "bob-ray"} {
			ada.Lore(ada.Home, "resource", "add", "--kind", "person", "--name", p, "--to", to, "--yes").OK(t)
		}
		for _, n := range notes {
			ada.Lore(ada.Home, "memory", "add", "--body", n.body, "--at", n.at, "--to", to, "--yes").OK(t)
		}
		ada.Lore(ada.Home, "memory", "add", "--body", "${res:person/bob-ray} runs orbit", "--at", "2021-06-01", "--to", to, "--yes").OK(t)
		ada.Lore(ada.Home, "task", "add", "--title", "send the orbit budget to alice", "--ref", "person/alice-ng", "--to", to, "--yes").OK(t)
	})
	s.Step("personal-store/show-with-refs", func() {
		r := ada.Lore(ada.Home, "resource", "show", "--kind", "person", "--name", "alice-ng", "--with-refs", "--store", "me")
		r.OK(t)
		out := r.Out()
		at := func(text string) int {
			i := strings.Index(out, text)
			if i < 0 {
				t.Fatalf("[O D-792] resource show --with-refs does not print %q:\n%s", text, r)
			}
			return i
		}
		task, newer, older := at("send the orbit budget to alice"), at("she owns the orbit budget now"), at("alice-ng was at globex")
		if !(at("person/alice-ng") < task && task < newer && newer < older) || !strings.Contains(out, "referenced by 3 rows, newest first:") {
			t.Errorf("[O D-792] want the person, then the task, the 2021 note and the 2020 note (newest first, bodies in full):\n%s", r)
		}
		if strings.Contains(out, "runs orbit") {
			t.Errorf("[O D-792] bob-ray's note is listed under alice-ng:\n%s", r)
		}
		c := ada.Lore(ada.Home, "resource", "show", "--kind", "person", "--name", "alice-ng", "--with-refs", "--max-refs", "1", "--store", "me", "--json")
		c.OK(t)
		var got struct {
			ReferencedBy []struct {
				Table, Body string
			} `json:"referenced_by"`
			ReferencedTotal int `json:"referenced_total"`
		}
		if err := json.Unmarshal([]byte(c.Out()), &got); err != nil || len(got.ReferencedBy) != 1 || got.ReferencedTotal != 3 || got.ReferencedBy[0].Table != "tasks" {
			t.Errorf("[O D-792] --max-refs 1 --json: %v %+v, want the task only and referenced_total 3\n%s", err, got, c)
		}
	})
	// A journal note in another folder of the store names the person kept
	// in the notes folder (its reference reaches row_refs since bug hunt
	// H-50, P-12): --with-refs lists it with --store me and also with
	// --store me/notes, which reads the store's other folders for the rows
	// that reference the resource.
	s.Step("personal-store/show-with-refs-other-folder", func() {
		ada.Lore(ada.Home, "store", "folder", "add", "me", "journal").OK(t)
		ada.Lore(ada.Home, "memory", "add", "--body", "journal: lunch with ${res:person/alice-ng}", "--at", "2022-05-05", "--to", "me/journal", "--yes").OK(t)
		for _, st := range []string{"me", to} {
			r := ada.Lore(ada.Home, "resource", "show", "--kind", "person", "--name", "alice-ng", "--with-refs", "--store", st)
			r.OK(t)
			out := r.Out()
			task, journal, newer := strings.Index(out, "send the orbit budget to alice"), strings.Index(out, "journal: lunch with"), strings.Index(out, "she owns the orbit budget now")
			if journal < 0 || !strings.Contains(out, "referenced by 4 rows, newest first:") || !(task < journal && journal < newer) || !strings.Contains(out, "(me/journal)") {
				t.Errorf("[O D-792] `resource show --with-refs --store %s` does not list the journal note (me/journal) between the task and the 2021 note:\n%s", st, r)
			}
		}
	})
	// devco D-822 (bug hunt H-81): a meeting note names the person in
	// plain words, without ${res:…}; lore ask found it, --with-refs left
	// it out, and an agent answering everything about her missed her last
	// meeting. A daily feed naming her (imported) comes after what the
	// owner wrote, so it does not bury that meeting.
	s.Step("personal-store/show-with-refs-plain-name", func() {
		ada.Lore(ada.Home, "memory", "add", "--body", "Met alice-ng about fjord: talked about the pricing page round 202.", "--at", "2022-06-01", "--to", to, "--yes").OK(t)
		ada.Lore(ada.Home, "memory", "add", "--body", "Feed slack: alice-ng sent 3 emails about orbit.", "--at", "2023-01-01", "--source", "imported", "--to", to, "--yes").OK(t)
		r := ada.Lore(ada.Home, "resource", "show", "--kind", "person", "--name", "alice-ng", "--with-refs", "--store", "me")
		r.OK(t)
		out := r.Out()
		met, task, feed := strings.Index(out, "pricing page round 202"), strings.Index(out, "send the orbit budget to alice"), strings.Index(out, "sent 3 emails")
		if met < 0 || feed < 0 || !(task < met && met < feed) || !strings.Contains(out, "referenced by 6 rows, newest first, the rows a feed imported after the rest:") {
			t.Errorf("[O D-822] --with-refs: want the plain-name meeting after the task (newest first) and the feed row last:\n%s", r)
		}
		if strings.Contains(out, "runs orbit") {
			t.Errorf("[O D-822] bob-ray's note is listed under alice-ng:\n%s", r)
		}
	})
}
