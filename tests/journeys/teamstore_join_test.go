//go:build journeys && !windows

package journeys

// teamstore_join_test.go — a member who is on a team when they join gets
// the team's store (devco:D-734). Lee's org keeps the @web team's notes in
// a second store (audience team:@web) and links the team's app repo to a
// folder of it. Ann (a member, not an admin) is invited onto @web and
// joins: join and sync clone and attach the team's store, and in the app
// repo whoami reads the link (reads line, links 1). Bob, a member outside @web, never gets it.
// Then the org file gains an audience kind this lore does not know (what a
// newer admin lore could write): Ann's lore keeps the store off her
// machine and says so in whoami and doctor with the upgrade, never
// silently (lore 0.1.18 read team:@web that way and said nothing).

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	tsWebSlug   = "github.com/acme-co/lore-web"
	tsWebFolder = "webapp"
	tsWebBody   = "the web app deploys from the green cluster"
)

func TestJourneyTeamStoreAtJoin(t *testing.T) {
	t.Parallel()
	tt := newTLTeam(t)
	w := tt.w
	lee := tt.person(tlLee)
	web := w.Remote(tsWebSlug)
	url := tt.store.SSHURL()
	annLine, bobLine := "", ""

	w.Step("day1/lee-keeps-a-store-for-web-and-links-the-app-to-it", func() {
		tlStartTeam(t, lee, tt.repo[tlLee.Email].Dir, tlLeadSetup, tlOrg, url, tlLee, []string{tlFolders})
		tt.lore(tlLee, "team", "add", "--handle", "@web", "--members", "@lee", "--yes").OK(t)
		tt.lore(tlLee, "sync").OK(t)
		tt.lore(tlLee, "org", "store", "add", "--org", tlOrg, "--url", web.SSHURL(), "--audience", "team:@web", "--push", "--yes").OK(t).
			Says(t, "team:@web")
		lee.Lore(filepath.Join(lee.Home, "code"), "store", "folder", "add", web.SSHURL(), tsWebFolder).OK(t)
		tt.lore(tlLee, "org", "link", "--org", tlOrg, "--store", web.SSHURL(), "--project", tsWebFolder, "--yes").OK(t)
		tt.lore(tlLee, "memory", "add", "--body", tsWebBody, "--yes").OK(t)
		tt.lore(tlLee, "sync").OK(t)
		tt.lore(tlLee, "whoami").OK(t).Says(t, "lore-web/"+tsWebFolder+" (this repo's link)", "1 link(s) for this repo")
		// Ann is on @web from the moment she accepts; Bob is not
		_, annLine = tlInvite(t, lee, tt.repo[tlLee.Email].Dir, tlAnn.Email, "--team", "@web")
		_, bobLine = tlInvite(t, lee, tt.repo[tlLee.Email].Dir, tlBob.Email)
	})

	ann := tt.person(tlAnn)
	webClone := func(m *Machine) string {
		return filepath.Join(m.Home, ".lore", "stores", "acme-co", "lore-web")
	}
	join := func(p tlPerson, m *Machine, line string) {
		tlJoin(t, m, tt.repo[p.Email].Dir, tlTeamSetup, line, p, []string{tlFolders})
		tt.repo[p.Email].Git("pull", "-q", "--no-rebase", "origin", "main")
	}

	w.Step("day1/ann-on-web-joins-and-gets-the-team-store", func() {
		join(tlAnn, ann, annLine)
		tt.lore(tlAnn, "sync").OK(t)
		if cfg := asConfig(t, ann); !strings.Contains(cfg, "lore-web") {
			t.Fatalf("Ann (on @web) has no lore-web in her org entry after join and sync:\n%s", cfg)
		}
		if _, err := os.Stat(webClone(ann)); err != nil {
			t.Fatalf("Ann has no clone of the @web store: %v", err)
		}
		tt.lore(tlAnn, "whoami").OK(t).Says(t, "lore-web/"+tsWebFolder+" (this repo's link)", "1 link(s) for this repo").Never(t, "cannot read that audience")
		tt.lore(tlAnn, "search", "green cluster").OK(t).Says(t, "green cluster")
		// a second sync keeps the store
		tt.lore(tlAnn, "sync").OK(t)
		if cfg := asConfig(t, ann); !strings.Contains(cfg, "lore-web") {
			t.Fatalf("a second sync dropped lore-web from Ann's org entry:\n%s", cfg)
		}
	})

	w.Step("day1/bob-outside-web-never-gets-it", func() {
		bob := tt.person(tlBob)
		asDeny(t, bob, tsWebSlug)
		join(tlBob, bob, bobLine)
		tt.lore(tlBob, "sync").OK(t).Never(t, "lore-web")
		tt.lore(tlBob, "whoami").OK(t).Never(t, "lore-web")
		if cfg := asConfig(t, bob); strings.Contains(cfg, "lore-web") {
			t.Fatalf("Bob (outside @web) has lore-web in his org entry:\n%s", cfg)
		}
		if _, err := os.Stat(webClone(bob)); err == nil {
			t.Fatalf("Bob has a clone of the @web store")
		}
	})

	// An audience kind this lore does not know reaches Ann's machine: the
	// store goes off it, and whoami and doctor say which store and that
	// this lore must be upgraded (D-734), never a silent skip.
	w.Step("day2/an-audience-this-lore-cannot-read-is-said", func() {
		w.AdvanceDays(1)
		ed := lee.Clone(tt.store, "org-edit")
		raw, ok := ed.ReadFile(jManyOrgFile)
		if !ok || !strings.Contains(raw, "audience: team:@web") {
			t.Fatalf("lore-org.yaml (%v):\n%s", ok, raw)
		}
		ed.WriteFile(jManyOrgFile, strings.Replace(raw, "audience: team:@web", "audience: group:@web", 1))
		ed.CommitAll("a newer lore's audience kind")
		ed.Git("push", "-q", "origin", "HEAD:main")
		tt.lore(tlAnn, "sync").OK(t)
		if cfg := asConfig(t, ann); strings.Contains(cfg, "lore-web") {
			t.Fatalf("Ann's lore kept a store of an audience it cannot read:\n%s", cfg)
		}
		tt.lore(tlAnn, "whoami").OK(t).Says(t, "store lore-web is for group:@web", "cannot read that audience", "lore upgrade")
		tt.lore(tlAnn, "doctor").Says(t, "is for group:@web", "cannot read that audience", "lore upgrade")
	})
}
