//go:build userscen

package userscen

// campaign_team_sync_test.go — the team area across machines: two
// teammates' clones of the org's app and the org's store as the bare
// remote. A profile write on one clone is read on the other; a write made
// offline is kept and reaches the teammate after `lore sync`; two people
// writing the roster at the same moment both land.

import (
	"bytes"
	"strings"
	"sync"
	"testing"

	th "github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

// TestCampaignTeam_TwoClonesSeeProfileWrites: each kind of people write
// made on mira's clone is read on bob's, and bob's on mira's, with live
// fetch on (fetch_ttl 0s): contacts, profile fields, roles, contact
// types, periods, status, memberships, workspaces of the org project.
func TestCampaignTeam_TwoClonesSeeProfileWrites(t *testing.T) {
	o := newOrgScen(t)
	_, bc := o.join(t, "bob", orgBobEmail, orgBobName, "bob")
	writes := []struct {
		name  string
		by    *th.Clone
		args  []string
		check []string // what `actor show @bob` (or the list) prints after, on the other clone
		read  []string // the read run on the other clone
	}{
		{"mira adds bob's phone", o.app, []string{"actor", "contact", "add", "@bob", "phone", "+1 555 0100"}, []string{"+1 555 0100"}, []string{"actor", "contact", "list", "@bob"}},
		{"bob edits his title", bc, []string{"actor", "edit", "@bob", "--title", "Backend • API", "--timezone", "Asia/Kolkata"}, []string{"Backend • API", "Asia/Kolkata"}, []string{"actor", "show", "@bob"}},
		{"mira adds a role", o.app, []string{"role", "add", "sre", "--label", "SRE"}, []string{"sre"}, []string{"role", "list"}},
		{"bob adds a contact type", bc, []string{"contact-type", "add", "signal", "--kind", "phone", "--label", "Signal"}, []string{"signal"}, []string{"contact-type", "list"}},
		{"mira records bob's past stint", o.app, []string{"actor", "period", "add", "@bob", "--type", "intern", "--started-on", "2019-01-01", "--ended-on", "2019-06-30"}, []string{"2019-01-01 → 2019-06-30"}, []string{"actor", "period", "list", "@bob"}},
		{"mira puts bob on leave", o.app, []string{"actor", "status", "@bob", "on_leave", "--from", "2026-11-08", "--until", "2026-11-30"}, []string{"on_leave"}, []string{"actor", "show", "@bob"}},
		{"bob is back", bc, []string{"actor", "status", "@bob", "active"}, []string{"Bob Roe"}, []string{"actor", "list", "--status", "active"}},
		{"mira adds bob to app", o.app, []string{"project", "member", "add", "app", "@bob", "--role", "dev,sre"}, []string{"@bob", "dev, sre"}, []string{"project", "member", "list", "app"}},
		{"bob changes his roles", bc, []string{"project", "member", "edit", "app", "@bob", "--role", "sre"}, []string{"@bob  sre"}, []string{"project", "member", "list", "app"}},
	}
	for _, w := range writes {
		w.by.MustLore(w.args...)
		w.by.MustLore("sync") // C5: a write reaches the team with its author's sync
		other := bc
		if w.by == bc {
			other = o.app
		}
		other.MustLore(w.read...).Says(t, w.check...)
	}
	// the two clones agree on the whole roster document
	a := showActor(t, o.app, "@bob")
	b := showActor(t, bc, "@bob")
	if a.ID != b.ID || a.Status != b.Status || a.Profile != b.Profile || len(a.Contacts) != len(b.Contacts) || len(a.Periods) != len(b.Periods) || len(a.Memberships) != len(b.Memberships) {
		t.Fatalf("both clones show the same bob:\n%+v\n%+v", a, b)
	}
}

// TestCampaignTeam_OfflineWriteLandsAfterSync: bob writes his own profile
// while the store is unreachable; the write is kept on his machine (no
// error lost, nothing half-written), mira does not see it yet, and after
// bob is back online one `lore sync` brings it to her.
func TestCampaignTeam_OfflineWriteLandsAfterSync(t *testing.T) {
	o := newOrgScen(t)
	bob, bc := o.join(t, "bob", orgBobEmail, orgBobName, "bob")
	before := o.notes.Rev("refs/heads/main")
	bob.Offline()
	r := bc.Lore("actor", "contact", "add", "@bob", "github", "bob-offline")
	if r.Code != 0 && !strings.Contains(r.Out(), "E_OFFLINE") {
		t.Fatalf("offline: a write either succeeds locally or says E_OFFLINE: %s", r)
	}
	if o.notes.Rev("refs/heads/main") != before {
		t.Fatal("nothing reaches the store while bob is offline")
	}
	o.app.MustLore("actor", "contact", "list", "@bob").Never(t, "bob-offline")
	bob.Online()
	if r.Code != 0 {
		// the offline refusal kept nothing: write it again online
		bc.MustLore("actor", "contact", "add", "@bob", "github", "bob-offline")
	} else {
		bc.MustLore("sync")
	}
	o.app.MustLore("actor", "contact", "list", "@bob").Says(t, "bob-offline")
	bc.MustLore("actor", "contact", "list", "@bob").Says(t, "bob-offline")
	n := 0
	for _, c := range teamContacts(t, o.app, "@bob") {
		if c.Value == "bob-offline" {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("the offline write lands once: %d", n)
	}
}

// TestCampaignTeam_ConcurrentRosterWrites: mira and bob write the roster
// at the same moment from their own clones, several times; every write
// lands, both clones read all of them, and nobody is shown twice.
func TestCampaignTeam_ConcurrentRosterWrites(t *testing.T) {
	o := newOrgScen(t)
	_, bc := o.join(t, "bob", orgBobEmail, orgBobName, "bob")
	const rounds = 3
	type job struct {
		c    *th.Clone
		args []string
	}
	var jobs []job
	for i := range rounds {
		n := string(rune('a' + i))
		jobs = append(jobs,
			job{o.app, []string{"actor", "contact", "add", "@mira", "slack", "mira-" + n}},
			job{bc, []string{"actor", "contact", "add", "@bob", "slack", "bob-" + n}},
		)
	}
	var wg sync.WaitGroup
	errs := make(chan string, len(jobs))
	for _, j := range jobs {
		wg.Add(1)
		go func(j job) {
			defer wg.Done()
			cmd := j.c.Command(j.args...)
			var out bytes.Buffer
			cmd.Stdout, cmd.Stderr = &out, &out
			if err := cmd.Run(); err != nil {
				errs <- strings.Join(j.args, " ") + ": " + err.Error() + "\n" + out.String()
			}
		}(j)
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		t.Errorf("a concurrent roster write failed: %s", e)
	}
	o.app.MustLore("sync")
	bc.MustLore("sync")
	o.app.MustLore("sync")
	for _, c := range []*th.Clone{o.app, bc} {
		all := teamContacts(t, c)
		for i := range rounds {
			n := string(rune('a' + i))
			for _, v := range []string{"mira-" + n, "bob-" + n} {
				k := 0
				for _, x := range all {
					if x.Value == v {
						k++
					}
				}
				if k != 1 {
					t.Errorf("%s: contact %s listed %d times", c.Dir, v, k)
				}
			}
		}
		if l := listActors(t, c); l.Count != 2 {
			t.Errorf("%s: two people, two roster rows: %v", c.Dir, displayNames(l))
		}
	}
}
