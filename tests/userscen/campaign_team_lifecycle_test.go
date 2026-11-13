//go:build userscen

package userscen

// campaign_team_lifecycle_test.go — the reverse paths of the team area:
// add → show → edit → delete → trash restore → purge with a list after
// each step, run-twice behaviour, odd values (empty, 10k chars, unicode,
// RTL, spaces and slashes), and the asks-first commands writing nothing
// until confirmed.

import (
	"path/filepath"
	"strings"
	"testing"

	th "github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

// TestCampaignTeam_ContactReversePath: a contact through its whole life
// with odd values: add, list, edit (10k label, RTL), remove (to the
// trash), a second remove refused naming the trash, restore (same id),
// remove again and purge; the list after each step says where it is.
func TestCampaignTeam_ContactReversePath(t *testing.T) {
	o := newOrgScen(t)
	_, bc := o.joinOn(t, orgDay(t, "2026-10-01"), "bob", orgBobEmail, orgBobName, "bob")
	values := []struct {
		name, typ, value, label string
	}{
		{"phone with spaces", "phone", "+49 30 1234567", "مكتب 📞"},
		{"slack text with slash", "slack", "acme/#general 🚀", "work / chat"},
		{"github handle", "github", "bob-dev", ""},
		{"long label", "website", "https://example.com/bob", teamLong},
	}
	for _, v := range values {
		t.Run(v.name, func(t *testing.T) {
			args := []string{"actor", "contact", "add", "@bob", v.typ, v.value}
			if v.label != "" {
				args = append(args, "--label", v.label)
			}
			id := orgID(t, o.app.MustLore(args...).Says(t, "added for Bob Roe (@bob)"), "acn")
			got, ok := teamContactByValue(teamContacts(t, o.app, "@bob"), v.value)
			if !ok || got.ID != id || got.Type != v.typ || got.Label != v.label || got.ActorName != "Bob Roe (@bob)" {
				t.Fatalf("after add: %+v (ok %v)", got, ok)
			}
			// run twice: the same contact, no second row
			o.app.MustLore(args...).Says(t, id, "already there")
			n := 0
			for _, c := range teamContacts(t, o.app, "@bob") {
				if c.Value == v.value {
					n++
				}
			}
			if n != 1 {
				t.Fatalf("adding the same contact twice keeps one row: %d", n)
			}
			// edit to an RTL label, read back on bob's clone
			o.app.MustLore("actor", "contact", "edit", id, "--label", "שלום עולם").Says(t, "edited")
			if c, _ := teamContactByValue(teamContacts(t, bc, "@bob"), v.value); c.Label != "שלום עולם" {
				t.Fatalf("bob's clone reads the edit: %+v", c)
			}
			// remove → trash → restore → remove → purge
			o.app.MustLore("actor", "contact", "remove", id).Says(t, "restorable from the trash")
			if _, ok := teamContactByValue(teamContacts(t, o.app, "@bob"), v.value); ok {
				t.Fatal("a removed contact is not listed")
			}
			o.app.Lore("actor", "contact", "remove", id).Fails(t, th.ExitRefused, "E_IN_TRASH", "lore trash restore --id "+id)
			o.app.MustLore("trash", "list").Says(t, id, "actor_contacts")
			o.app.MustLore("trash", "restore", id).Says(t, "restored 1 row", id)
			if c, ok := teamContactByValue(teamContacts(t, o.app, "@bob"), v.value); !ok || c.ID != id || c.Label != "שלום עולם" {
				t.Fatalf("restore brings back the same contact: %+v (ok %v)", c, ok)
			}
			o.app.MustLore("actor", "contact", "remove", id)
			o.app.Lore("trash", "purge", id).Refused(t, "--confirm")
			o.app.MustLore("trash", "purge", id, "--confirm")
			o.app.MustLore("trash", "list").Never(t, id)
			o.app.Lore("trash", "restore", id).Refused(t)
			if _, ok := teamContactByValue(teamContacts(t, o.app, "@bob"), v.value); ok {
				t.Fatal("a purged contact is gone")
			}
		})
	}
	// everyone's list (the org's phone list) is the union, read on both clones
	if a, b := len(teamContacts(t, o.app)), len(teamContacts(t, bc)); a != b {
		t.Fatalf("both clones list the same contacts: %d vs %d", a, b)
	}
}

// TestCampaignTeam_WorkspaceReversePath: workspaces with odd names
// through add → show → edit → search → delete → restore → purge, a list
// after each step, and delete twice refused naming the trash.
func TestCampaignTeam_WorkspaceReversePath(t *testing.T) {
	o := newOrgScen(t)
	c := o.app
	names := []struct{ name, title, body string }{
		{"unicode and emoji", "Ünïcødé 🚀 launch", "the launch plan"},
		{"spaces and slashes", "client / acme  hub", "a/b/c paths"},
		{"rtl", "مشروع جديد", "نص عربي"},
		{"reserved words", "null", "undefined true false"},
		{"10k body", "long body", teamLong},
	}
	for _, n := range names {
		t.Run(n.name, func(t *testing.T) {
			id := orgID(t, c.MustLore("workspace", "add", "--title", n.title, "--body", n.body), "wsp")
			show := func() teamWorkspace {
				t.Helper()
				return th.DecodeJSON[struct {
					Data teamWorkspace `json:"data"`
				}](t, c.MustLore("workspace", "show", id, "--json")).Data
			}
			if w := show(); w.Name != n.title || w.Body != n.body {
				t.Fatalf("show reads what add wrote: %q / %d chars", w.Name, len(w.Body))
			}
			listed := func() bool {
				for _, w := range teamWorkspaces(t, c) {
					if w.ID == id {
						return true
					}
				}
				return false
			}
			if !listed() {
				t.Fatal("added workspace is listed")
			}
			c.MustLore("workspace", "edit", id, "--name", n.title+" v2")
			if w := show(); w.Name != n.title+" v2" || w.Body != n.body {
				t.Fatalf("edit --name keeps the body: %q", w.Name)
			}
			c.Lore("workspace", "delete", id).Fails(t, th.ExitNeedsConfirm, "E_NEEDS_CONFIRM")
			if !listed() {
				t.Fatal("an unconfirmed delete keeps the workspace")
			}
			c.MustLore("workspace", "delete", id, "--yes")
			if listed() {
				t.Fatal("a deleted workspace is not listed")
			}
			c.Lore("workspace", "delete", id, "--yes").Refused(t, "E_IN_TRASH", "lore trash restore --id "+id)
			c.MustLore("trash", "restore", id).Says(t, id)
			if !listed() || show().Name != n.title+" v2" {
				t.Fatal("restore brings back the edited workspace")
			}
			c.MustLore("workspace", "delete", id, "--purge", "--confirm")
			if listed() {
				t.Fatal("a purged workspace is gone")
			}
			c.Lore("workspace", "show", id).Fails(t, th.ExitRefused, "E_PURGED")
		})
	}
	// search: a word of a name and of a body; nothing matches
	keep := orgID(t, c.MustLore("workspace", "add", "--title", "Billing cleanup", "--body", "Invoices and refunds."), "wsp")
	c.MustLore("workspace", "search", "refunds").Says(t, keep)
	c.MustLore("workspace", "search", "billing").Says(t, keep)
	c.MustLore("workspace", "search", "zzzznothing").Says(t, "(no matches)")
}

// TestCampaignTeam_ProjectArchiveDeleteRestore: a project archived and
// unarchived twice (idempotent), deleted after a confirmation, restored
// from the trash with the same id; project show and list after each.
func TestCampaignTeam_ProjectArchiveDeleteRestore(t *testing.T) {
	o := newOrgScen(t)
	c := o.app
	id := teamProjectNamed(t, c, "app")
	c.MustLore("project", "show", id).Says(t, id, "app")
	// The verbs take the name too, as `project member` does.
	c.MustLore("project", "show", "app").Says(t, id, "app")
	for range 2 {
		c.MustLore("project", "archive", "app")
	}
	// v0.1.17: archived projects stay listed, marked [archived]
	archivedAt := func() string {
		t.Helper()
		for _, p := range teamProjects(t, c) {
			if p.ID == id {
				return p.ArchivedAt
			}
		}
		t.Fatalf("project %s not listed", id)
		return ""
	}
	if archivedAt() == "" {
		t.Fatal("archive sets archived_at")
	}
	c.MustLore("project", "list").Says(t, id, "[archived]")
	for range 2 {
		c.MustLore("project", "unarchive", "app")
	}
	if archivedAt() != "" {
		t.Fatal("unarchive clears archived_at")
	}
	c.MustLore("project", "list").Never(t, "[archived]")
	before := c.Status()
	c.Lore("project", "delete", id).Fails(t, th.ExitNeedsConfirm, "E_NEEDS_CONFIRM", "--yes")
	if c.Status() != before {
		t.Fatal("an unconfirmed project delete writes nothing")
	}
	c.MustLore("project", "delete", id, "--yes")
	for _, p := range teamProjects(t, c) {
		if p.ID == id {
			t.Fatal("a deleted project is not listed")
		}
	}
	c.MustLore("trash", "restore", id).Says(t, id)
	if teamProjectNamed(t, c, "app") != id {
		t.Fatal("restore brings back the same project id")
	}
}

// TestCampaignTeam_RolesAndContactTypesRetire: added with unicode labels,
// edited, retired twice (idempotent), out of the live lists, refused as a
// member role once retired; a built-in contact type can be retired too.
func TestCampaignTeam_RolesAndContactTypesRetire(t *testing.T) {
	o := newOrgScen(t)
	c := o.app
	c.MustLore("role", "add", "qa_lead", "--label", "QA lead ✓", "--description", "שלום").Says(t, "role qa_lead added")
	if !teamRoles(t, c)["qa_lead"] {
		t.Fatal("an added role is live")
	}
	c.MustLore("role", "edit", "qa_lead", "--label", "QA lead (night)").Says(t, "qa_lead")
	c.MustLore("project", "member", "add", "app", "@mira", "--role", "qa_lead")
	for range 2 {
		c.MustLore("role", "edit", "qa_lead", "--retire")
	}
	if teamRoles(t, c)["qa_lead"] {
		t.Fatal("a retired role is not live")
	}
	c.MustLore("role", "list").Never(t, "qa_lead")
	// a membership keeps a retired role it holds; nobody gets it anew
	c.MustLore("project", "member", "edit", "app", "@mira", "--role", "dev,qa_lead")
	c.Lore("project", "member", "add", "org", "@mira", "--role", "qa_lead").Fails(t, orgExitBadInput, "E_ROLE_UNKNOWN", "qa_lead (retired)")

	c.MustLore("contact-type", "add", "matrix_chat", "--kind", "handle", "--label", "Matrix 💬").Says(t, "matrix_chat added")
	c.MustLore("actor", "contact", "add", "@mira", "matrix_chat", "mira")
	for range 2 {
		c.MustLore("contact-type", "edit", "matrix_chat", "--retire")
	}
	c.MustLore("contact-type", "list").Never(t, "matrix_chat")
	c.Lore("actor", "contact", "add", "@mira", "matrix_chat", "mira2").Refused(t, "matrix_chat")
}

// TestCampaignTeam_DeactivateSomeoneWhoLeft: after offboard, deactivate
// names the person's status and the way back (they are on the roster,
// not unknown), and changes nothing; a second deactivate of someone
// deactivated says they already are.
func TestCampaignTeam_DeactivateSomeoneWhoLeft(t *testing.T) {
	o := newOrgScen(t)
	_, _ = o.joinOn(t, orgDay(t, "2026-10-01"), "bob", orgBobEmail, orgBobName, "bob")
	o.app.MustLore("actor", "offboard", "@bob", "--yes")
	before := o.notes.Rev("refs/heads/main")
	for range 2 {
		o.app.Lore("actor", "deactivate", "@bob").Fails(t, th.ExitRefused, "E_STATUS_CHANGE", "Bob Roe (@bob)", "left", "lore actor rejoin --actor @bob").
			Never(t, "no one is @bob")
	}
	if o.notes.Rev("refs/heads/main") != before {
		t.Fatal("a refused deactivate writes nothing")
	}
	// an active person still deactivates; a second time names the status
	cara, _ := o.joinOn(t, orgDay(t, "2026-10-01"), "cara", "cara@example.com", "Cara Coe", "cara")
	_ = cara
	o.app.MustLore("actor", "deactivate", "@cara").Says(t, "deactivated")
	o.app.Lore("actor", "deactivate", "@cara").Fails(t, th.ExitRefused, "E_STATUS_CHANGE", "Cara Coe (@cara) is already", "nothing to deactivate")
	o.app.Lore("actor", "deactivate", "@nobody").Fails(t, orgExitBadInput, "E_INVALID_INPUT", "no one is @nobody")
}

// TestCampaignTeam_AsksFirstWritesNothing: offboard, rejoin and project
// delete without --yes print the preview and E_NEEDS_CONFIRM and leave
// the store and the clone as they were (the asks-first commands have no
// --dry-run: the question is the dry run).
func TestCampaignTeam_AsksFirstWritesNothing(t *testing.T) {
	o := newOrgScen(t)
	bob, _ := o.joinOn(t, orgDay(t, "2026-10-01"), "bob", orgBobEmail, orgBobName, "bob")
	o.mira.SetNow(orgDay(t, "2026-11-10"))
	bob.SetNow(orgDay(t, "2026-11-10"))
	o.app.MustLore("project", "member", "add", "app", "@bob", "--role", "dev")
	snap := func() string { return o.notes.Rev("refs/heads/main") + "|" + o.app.Status() + "|" + o.app.Head() }
	before := snap()
	for range 2 {
		o.app.Lore("actor", "offboard", "@bob").Fails(t, th.ExitNeedsConfirm, "E_NEEDS_CONFIRM", "offboard Bob Roe (@bob)", "app", "rerun with --yes")
		o.app.Lore("actor", "offboard", "@bob", "--json").Fails(t, th.ExitNeedsConfirm, "E_NEEDS_CONFIRM")
	}
	if snap() != before {
		t.Fatal("an unconfirmed offboard writes nothing")
	}
	if a := showActor(t, o.app, "@bob"); a.Status != "active" || len(a.Memberships) != 1 || a.Memberships[0].EndedOn != "" {
		t.Fatalf("bob is untouched: %+v", a)
	}
	o.app.MustLore("actor", "offboard", "@bob", "--yes")
	o.mira.SetNow(orgDay(t, "2026-11-12"))
	before = snap()
	o.app.Lore("actor", "rejoin", "@bob", "--type", "contractor").Fails(t, th.ExitNeedsConfirm, "E_NEEDS_CONFIRM")
	if snap() != before {
		t.Fatal("an unconfirmed rejoin writes nothing")
	}
	if a := showActor(t, o.app, "@bob"); a.Status != "left" {
		t.Fatalf("bob stays left until the rejoin is confirmed: %+v", a)
	}
	o.app.MustLore("actor", "rejoin", "@bob", "--type", "contractor", "--yes")
	if a := showActor(t, o.app, "@bob"); a.Status != "active" || len(a.Periods) != 2 {
		t.Fatalf("rejoin confirmed: %+v", a)
	}
}

// TestCampaignTeam_PeriodReplacesTheRosterGuess: the stint read from
// active/left_at is a guess for someone still here; a hand-written stint
// that starts earlier replaces it (and says so), a second open one is
// refused, and the list after shows only the real one.
func TestCampaignTeam_PeriodReplacesTheRosterGuess(t *testing.T) {
	o := newOrgScen(t)
	o.app.MustLore("actor", "period", "add", "@mira", "--type", "employee", "--started-on", "2026-01-12").
		Says(t, "it replaces the stint read from the roster")
	l := orgJSON[struct {
		Periods []orgPeriod `json:"periods"`
	}](t, o.app.MustLore("actor", "period", "list", "@mira", "--json"))
	if len(l.Periods) != 1 || l.Periods[0].StartedOn != "2026-01-12" || l.Periods[0].ID == "" || l.Periods[0].Source == "migrated" {
		t.Fatalf("one real stint replaces the guess: %+v", l.Periods)
	}
	o.app.Lore("actor", "period", "add", "@mira", "--type", "contractor", "--started-on", "2026-03-01").Fails(t, th.ExitRefused, "E_PERIOD_OVERLAP")
}

// TestCampaignTeam_OrgCommandsTwice: org use, org set --commit and org
// rename run twice answer the same and change nothing the second time.
func TestCampaignTeam_OrgCommandsTwice(t *testing.T) {
	o := newOrgScen(t)
	c := o.app
	for range 2 {
		c.MustLore("org", "use", orgName).Says(t, "uses org agency on this machine", "nothing committed")
	}
	if st := c.Status(); st != "" {
		t.Fatalf("org use commits nothing: %q", st)
	}
	c.MustLore("org", "set", orgName, "--commit").Says(t, filepath.FromSlash(".lore/org.yaml"), "commit it with git")
	first, _ := c.ReadFile(".lore/org.yaml")
	c.MustLore("org", "set", orgName, "--commit")
	if again, _ := c.ReadFile(".lore/org.yaml"); again != first || !strings.Contains(first, orgName) {
		t.Fatalf("org set twice writes the same file: %q vs %q", first, again)
	}
	c.MustLore("org", "rename", orgName, "agency-two").Says(t, "is now agency-two")
	c.MustLore("actor", "show", "@mira").Says(t, orgMiraName)
	c.MustLore("org", "rename", "agency-two", orgName)
	c.MustLore("actor", "list").Says(t, orgMiraName)
}
