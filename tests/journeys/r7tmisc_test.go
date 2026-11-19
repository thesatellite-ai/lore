//go:build journeys && !windows

package journeys

// r7tmisc_test.go — a year of org life, the small things people hit (devco
// track T, round 7). Lee runs the org, Ann joins later:
//   - T-1: lore init without --yes in a client repo that still holds lore
//     0.1.17 data asks first (E_NEEDS_CONFIRM) and writes nothing, like a
//     fresh client repo;
//   - T-4: once that repo is linked, a plain memory add goes to the client
//     store folder; the repo's own format does not matter;
//   - T-11: lore search in a repo with no lore data writes nothing anywhere;
//   - T-2: lore search with two bare words searches both;
//   - T-6: a member who has not synced since a team was made is told to
//     sync, never to make the team again;
//   - T-7: task reassign --assign @team names the command that gives the
//     task to a team (task edit --assign @team), never actor register;
//   - T-15: lore audit log says it lists the repo's own rows and names
//     where a team-store write is (member show --history).

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

const (
	r7tClientSlug = "github.com/client-co/alfred"
	r7tFolder     = "client_alfred"
	r7tPlainSlug  = "github.com/acme-co/appdir"
)

func TestJourneyR7TMisc(t *testing.T) {
	t.Parallel()
	tt := newTLTeam(t)
	w := tt.w
	lee := tt.person(tlLee)
	parent := filepath.Join(lee.Home, "code")

	w.Step("day1/org-with-client-repos", func() {
		tlStartTeam(t, lee, tt.repo[tlLee.Email].Dir, tlLeadSetup, tlOrg, tt.store.SSHURL(), tlLee, []string{tlFolders})
		s := lee.Clone(tt.store, "team-store")
		jWrite(t, filepath.Join(s.Dir, filepath.FromSlash(jManyOrgFile)), "name: "+tlOrg+"\nstores:\n"+
			"    - url: "+tt.store.SSHURL()+"\n      org_folder: org\nrepos:\n    - github.com/client-co/*\n")
		s.CommitAll("the org's client repos")
		s.Git("push", "-q", "origin", "HEAD:main")
		lee.Lore(parent, "org", "refresh", tlOrg).OK(t)
		lee.Lore(parent, "store", "folder", "add", "team", r7tFolder).OK(t)
		lee.Lore(parent, "sync").OK(t)
		tt.lore(tlLee, "init").OK(t)
		c := tt.repo[tlLee.Email]
		c.CommitAll("lore init")
		c.Git("push", "-q", "origin", "HEAD")
	})

	var alfred *teamharness.Clone
	w.Step("day2/old-client-repo-init-asks-first", func() {
		alfred = jPlainRepo(t, lee, w.Remote(r7tClientSlug), filepath.Join(parent, "alfred"))
		jManyOldNotes(t, alfred)
		before := alfred.Git("status", "--porcelain", "--ignored")
		hooks := alfred.Git("rev-parse", "--git-path", "hooks")
		lee.LoreIn(alfred, "init").Fails(t, 26, "client repo of "+tlOrg, "lore init --yes").Never(t, "adopted")
		if st := alfred.Git("status", "--porcelain", "--ignored"); st != before {
			t.Fatalf("a refused init changed the client repo:\n%s", st)
		}
		if strings.Contains(jReadOr(filepath.Join(alfred.Dir, strings.TrimSpace(hooks), "pre-commit")), "lore") {
			t.Fatal("a refused init wired lore's hooks into the client repo")
		}

		// T-4: linked, a plain memory goes to the client store folder
		lee.LoreIn(alfred, "org", "link", tlOrg, "--project", r7tFolder, "--yes").OK(t)
		lee.LoreIn(alfred, "memory", "add", "--body", "alfred bills on the first").OK(t).Says(t, r7tFolder).Never(t, "E_FORMAT_OLD")
		if st := alfred.Git("status", "--porcelain", "--ignored"); st != before {
			t.Fatalf("a memory add for the client store changed the client repo:\n%s", st)
		}
	})

	w.Step("day3/search-in-a-repo-with-no-lore-data-writes-nothing", func() {
		app := jPlainRepo(t, lee, w.Remote(r7tPlainSlug), filepath.Join(parent, "appdir"))
		lee.LoreIn(app, "search", "billing").OK(t).Says(t, "no matches").Never(t, "committed")
		lee.LoreIn(app, "search", "billing").OK(t).Never(t, "committed")
		if st := app.Git("status", "--porcelain", "--ignored"); st != "" {
			t.Fatalf("a search left files in a repo with no lore data:\n%s", st)
		}
		// T-2: two bare words are one query
		tt.lore(tlLee, "memory", "add", "--to", "me", "--body", "Zephyrine retries are idempotency-safe").OK(t)
		tt.lore(tlLee, "search", "Zephyrine", "retries").OK(t).Says(t, "Zephyrine").Never(t, "--name")
		tt.lore(tlLee, "team", "rename", "platform", "backend", "--yes").Refused(t, "--to").Never(t, "--name")
	})

	var line string
	w.Step("week1/ann-joins", func() {
		_, line = tlInvite(t, lee, tt.repo[tlLee.Email].Dir, tlAnn.Email)
		m := tt.person(tlAnn)
		tlJoin(t, m, tt.repo[tlAnn.Email].Dir, tlTeamSetup, line, tlAnn, []string{tlFolders})
		tt.lore(tlAnn, "whoami").OK(t).Says(t, tlOrg, tlAnn.Email)
	})

	w.Step("week2/a-team-ann-has-not-synced-yet", func() {
		tt.lore(tlLee, "team", "add", "--handle", "web", "--members", "@lee", "--yes").OK(t)
		tt.lore(tlLee, "sync").OK(t)
		tt.lore(tlAnn, "team", "show", "--handle", "@web").Refused(t, "lore sync").Never(t, "team add")
		tt.lore(tlAnn, "sync").OK(t)
		tt.lore(tlAnn, "team", "show", "--handle", "@web").OK(t).Says(t, "@web")
	})

	w.Step("week3/reassign-to-a-team", func() {
		o := struct{ ID string }{jID(t, lee.LoreIn(alfred, "task", "add", "--title", "wire the alfred invoice export", "--json").OK(t))}
		lee.LoreIn(alfred, "task", "reassign", "--id", o.ID, "--assign", "@web").Refused(t, "@web is a team", "lore task edit --id "+o.ID+" --assign @web").Never(t, "actor register")
		lee.LoreIn(alfred, "task", "edit", "--id", o.ID, "--assign", "@web").OK(t)
		lee.LoreIn(alfred, "task", "show", "--id", o.ID).OK(t).Says(t, "@web")
	})

	w.Step("month2/team-store-writes-are-in-the-audit-log", func() {
		lee.LoreIn(alfred, "memory", "add", "--to", tlOrg, "--body", "the org closes books on the 5th").OK(t)
		lee.LoreIn(alfred, "sync").OK(t)
		lee.LoreIn(alfred, "audit", "log", "--limit", "10").OK(t).Says(t, "this repo's own rows", "lore member show --who <person> --history")
		lee.LoreIn(alfred, "member", "show", "--who", tlLee.Email, "--history").OK(t).Says(t, tlOrg)
	})
}

// jReadOr is the file at p, or "" when it cannot be read.
func jReadOr(p string) string {
	b, err := os.ReadFile(p)
	if err != nil {
		return ""
	}
	return string(b)
}
