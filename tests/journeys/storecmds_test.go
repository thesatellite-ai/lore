//go:build journeys && !windows

package journeys

// storecmds_test.go — three commands a person runs away from any code
// repo (owner run 2026-11-12, D-ae, D-af). Lee's laptop has a global git
// identity (his personal email) and his setup a work identity: every store
// clone lore makes gets the work identity in its own .git/config, with one
// line saying so; doctor flags a clone set back to the personal email and
// --fix repairs it; `lore sync` from the plain parent folder syncs every
// store of the setup (one line each), keeps going when one store cannot be
// reached and exits non-zero naming it; inside a store checkout it syncs
// that store; `lore org refresh` without a name takes the only org, then,
// with a second org entry, the active setup's org, and from a folder no
// setup covers it names the orgs and the command.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The names of this journey (placeholders).
const (
	jCmdsWorkEmail = "lee.work@example.test"
	jCmdsBetaOrg   = "betaorg"
	jCmdsBetaSlug  = "github.com/beta-co/lore-beta"
	jCmdsMeSlug    = "github.com/lee-dev/lore-me"
)

func TestJourneyStoreCommands(t *testing.T) {
	t.Parallel()
	tt := newTLTeam(t)
	w := tt.w
	lee := tt.person(tlLee) // global git identity: lee@example.test
	work := tlPerson{Name: tlLee.Name, Email: jCmdsWorkEmail, Login: tlLee.Login}
	parent := filepath.Join(lee.Home, "code")
	app := tt.repo[tlLee.Email]
	two := w.Remote(jManyTwoSlug)
	me := w.Remote(jCmdsMeSlug)
	teamClone := lee.LorePath("stores", "acme-co", "lore-team")
	twoClone := lee.LorePath("stores", "acme-co", "notes-two")
	twoTilde := "~/.lore/stores/acme-co/notes-two" // twoClone as the identity line shows it (under ~)
	localEmail := func(dir string) string {
		t.Helper()
		return strings.TrimSpace(w.TryGit(dir, "config", "--local", "--get", "user.email").Stdout)
	}

	w.Step("day1/team-with-two-stores-and-a-work-identity", func() {
		// the setup's personal store is a git store of Lee's own (lore-me)
		tlApply(t, lee, app.Dir, tlLeadSetup, "kind", map[string]any{"kind": "admin", "name": tlLeadSetup})
		tlApply(t, lee, app.Dir, tlLeadSetup, "identity", map[string]any{"name": work.Name, "email": work.Email})
		tlApply(t, lee, app.Dir, tlLeadSetup, "github", map[string]any{"github_account": work.Login, "host_alias": "github.com"})
		tlApply(t, lee, app.Dir, tlLeadSetup, "personal_store", map[string]any{"personal_store": "create", "store_url": me.SSHURL()})
		tlApply(t, lee, app.Dir, tlLeadSetup, "team", map[string]any{"org": tlOrg, "store_url": tt.store.SSHURL()})
		tlLastSteps(t, lee, app.Dir, tlLeadSetup, []string{tlFolders})
		lee.Lore(app.Dir, "sync").OK(t) // C5: the new team store reaches the remote with Lee's sync
		s := lee.Clone(tt.store, "team-store")
		jWrite(t, filepath.Join(s.Dir, filepath.FromSlash(jManyOrgFile)), "name: "+tlOrg+"\nstores:\n"+
			"    - url: "+tt.store.SSHURL()+"\n      org_folder: org\n"+
			"    - url: "+two.SSHURL()+"\n")
		s.CommitAll("list every store")
		s.Git("push", "-q", "origin", "HEAD:main")
		// the only org entry: no name needed
		lee.Lore(parent, "org", "refresh").OK(t).Says(t, "org "+tlOrg+" (the only org entry on this machine)", "store "+two.SSHURL())
	})

	w.Step("day1/a-new-clone-gets-the-setup-identity", func() {
		r := lee.Lore(parent, "store", "folder", "add", two.SSHURL(), "client_a").OK(t)
		r.Says(t, "store clone "+twoTilde+": git user set to "+tlLee.Name+" <"+jCmdsWorkEmail+">")
		if n := strings.Count(r.Out(), ": git user set to "); n != 1 {
			t.Errorf("the identity line was said %d times, want once: %s", n, r)
		}
		for _, dir := range []string{teamClone, twoClone} {
			if got := localEmail(dir); got != jCmdsWorkEmail {
				t.Errorf("%s: user.email in .git/config = %q, want the setup identity %s", dir, got, jCmdsWorkEmail)
			}
		}
		if got := strings.TrimSpace(w.TryGit(lee.Home, "config", "--file", filepath.Join(lee.Home, ".gitconfig"), "user.email").Stdout); got != tlLee.Email {
			t.Errorf("the global git identity changed to %q", got)
		}
		// lore where lists the entry it wrote
		lee.Lore(parent, "where").OK(t).Says(t, filepath.Join(twoClone, ".git", "config")+" [user.email]")
		// a second command says nothing more
		lee.Lore(parent, "store", "list").OK(t).Never(t, ": git user set to ")
	})

	w.Step("day2/doctor-flags-a-clone-with-the-personal-identity", func() {
		w.AdvanceDays(1)
		// doctor's store checks run in a lore project (in a repo nobody ran
		// lore init in it stops at one note, D-20)
		lee.Lore(app.Dir, "init", "--non-interactive", "--name=app").OK(t)
		lee.OpenClone(tt.store, teamClone).Git("config", "user.email", tlLee.Email)
		lee.Lore(app.Dir, "doctor").Says(t, "store_identity", teamClone+" commits as "+tlLee.Email,
			"git -C "+teamClone+" config user.email "+jCmdsWorkEmail)
		lee.Lore(app.Dir, "doctor", "--fix").Says(t, "store_identity", "now names "+tlLee.Name+" <"+jCmdsWorkEmail+">")
		if got := localEmail(teamClone); got != jCmdsWorkEmail {
			t.Errorf("doctor --fix left user.email %q in %s", got, teamClone)
		}
		lee.Lore(app.Dir, "doctor").Never(t, "store_identity")
		// a clone whose entry was removed: a read leaves it so (owner bug
		// 6); the next sync or write gives it again, said as that command
		lee.OpenClone(two, twoClone).Git("config", "--unset", "user.email")
		lee.Lore(parent, "store", "folder", "list").OK(t).Never(t, ": git user set to ")
		if got := localEmail(twoClone); got != "" {
			t.Errorf("a read wrote user.email %q into the store clone", got)
		}
		lee.Lore(parent, "sync").OK(t).Says(t, "lore sync: store clone "+twoTilde+": git user set to ")
		if got := localEmail(twoClone); got != jCmdsWorkEmail {
			t.Errorf("the reopened clone's user.email = %q", got)
		}
	})

	w.Step("day3/sync-outside-a-repo-syncs-every-store", func() {
		w.AdvanceDays(1)
		r := lee.Lore(parent, "sync").OK(t)
		r.Says(t, "store "+tlOrg+": up to date", "store notes-two: up to date", "store me: up to date")
		var out struct {
			Data struct {
				Pass   string `json:"pass"`
				Stores []struct {
					Store, URL, Status string
				} `json:"stores"`
			} `json:"data"`
		}
		tlJSON(t, lee.Lore(parent, "sync", "--json").OK(t), &out)
		if out.Data.Pass != "setup" || len(out.Data.Stores) != 3 {
			t.Fatalf("sync --json outside a repo: pass %q, %d stores, want setup and 3 (team, notes-two, me): %+v", out.Data.Pass, len(out.Data.Stores), out.Data)
		}
		// inside a store checkout: that store only, by its short name
		lee.Lore(filepath.Join(twoClone, "client_a"), "sync").OK(t).Says(t, "store notes-two: up to date").Never(t, "store "+tlOrg)
		// a row changed in the store by another clone is pulled
		id := jID(t, lee.Lore(filepath.Join(twoClone, "client_a"), "task", "add", "a task in notes-two", "--json").OK(t))
		lee.Lore(filepath.Join(twoClone, "client_a"), "sync").OK(t) // C5: the task reaches the store with Lee's sync
		other := lee.Clone(two, "notes-two-other")
		p := filepath.Join(other.Dir, "client_a", "tasks", id+".json")
		body := jRead(t, p)
		jWrite(t, p, strings.Replace(body, "a task in notes-two", "a task in notes-two, renamed elsewhere", 1))
		other.CommitAll("renamed elsewhere")
		other.Git("push", "-q", "origin", "HEAD:main")
		lee.Lore(parent, "sync").OK(t).Says(t, "store notes-two: pulled (sent 0, received 1", "store "+tlOrg+": up to date")
	})

	w.Step("day3/the-personal-store-outside-any-setup-folder", func() {
		// ~ is in no setup's folders and no repo; the org is known
		// (LORE_ORG), so lead is the active setup by its org and its
		// personal store (a git store, lore-me) is listed and synced with
		// the org's stores (owner bug 9: it was left out)
		id := jID(t, lee.Lore(lee.LorePath("stores", "lee-dev", "lore-me"), "task", "add", "a note of my own", "--json").OK(t))
		lee.SetEnv("LORE_ORG", tlOrg)
		defer lee.SetEnv("LORE_ORG", "")
		lee.Lore(lee.Home, "store", "list").OK(t).Says(t, "notes-two", "me ", me.SSHURL())
		var list struct {
			Stores []struct {
				Name, URL string
				Personal  bool
			} `json:"stores"`
		}
		tlJSON(t, lee.Lore(lee.Home, "store", "list", "--json").OK(t), &list)
		var gotMe bool
		for _, e := range list.Stores {
			gotMe = gotMe || (e.Name == "me" && e.Personal && e.URL == me.SSHURL())
		}
		if !gotMe {
			t.Errorf("store list --json from ~ has no personal store me (%s): %+v", me.SSHURL(), list.Stores)
		}
		lee.Lore(lee.Home, "sync").OK(t) // C5: sends what this machine's writes left waiting
		lee.Lore(lee.Home, "sync").OK(t).Says(t, "store "+tlOrg+": up to date", "store notes-two: up to date", "store me: up to date")
		// a row of lore-me changed by another clone is pulled
		other := lee.Clone(me, "lore-me-other")
		var p string
		_ = filepath.WalkDir(other.Dir, func(f string, d os.DirEntry, err error) error {
			if err == nil && !d.IsDir() && d.Name() == id+".json" {
				p = f
			}
			return nil
		})
		if p == "" {
			t.Fatalf("no row file %s.json in the lore-me clone", id)
		}
		jWrite(t, p, strings.Replace(jRead(t, p), "a note of my own", "a note of my own, edited elsewhere", 1))
		other.CommitAll("edited elsewhere")
		other.Git("push", "-q", "origin", "HEAD:main")
		lee.Lore(lee.Home, "sync").OK(t).Says(t, "store me: pulled (sent 0, received 1")
		var out struct {
			Data struct {
				Stores []struct{ Store, Status string } `json:"stores"`
			} `json:"data"`
		}
		tlJSON(t, lee.Lore(lee.Home, "sync", "--json").OK(t), &out)
		if len(out.Data.Stores) != 3 || out.Data.Stores[2].Store != "me" {
			t.Errorf("sync --json from ~: want team, notes-two and me: %+v", out.Data.Stores)
		}
		// lore where names the lore-me clone as store me
		lee.Lore(lee.Home, "where").OK(t).Says(t, lee.LorePath("stores", "lee-dev", "lore-me")+" (store me)")
	})

	w.Step("day4/one-store-unreachable-the-others-still-sync", func() {
		w.AdvanceDays(1)
		bare := filepath.Join(w.Root, "hosts", "github.com", "acme-co", "notes-two.git")
		if err := os.Rename(bare, bare+".away"); err != nil {
			t.Fatal(err)
		}
		r := lee.Lore(parent, "sync").Refused(t)
		// the repo is gone from its address (a rename): H-11 says so and
		// names set-url, never "offline: check the network"
		r.Says(t, "store "+tlOrg+": up to date", "store notes-two: FAILED: no git repository at "+two.SSHURL(), "store me: up to date",
			"1 of 3 stores failed: notes-two", "notes-two: if the store moved or was renamed: lore store set-url --name notes-two --url <new-url>").
			Never(t, "check the network")
		var e struct {
			Error struct{ Code string } `json:"error"`
		}
		if err := json.Unmarshal([]byte(lee.Lore(parent, "sync", "--json").Refused(t).Stdout), &e); err != nil || e.Error.Code != "E_STORE_UNREACHABLE" {
			t.Errorf("sync --json with an unreachable store: code %q (%v), want E_STORE_UNREACHABLE", e.Error.Code, err)
		}
		if err := os.Rename(bare+".away", bare); err != nil {
			t.Fatal(err)
		}
		lee.Lore(parent, "sync").OK(t).Says(t, "store notes-two: up to date")
	})

	w.Step("day5/org-refresh-with-two-orgs", func() {
		w.AdvanceDays(1)
		beta := w.Remote(jCmdsBetaSlug)
		lee.Lore(app.Dir, "store", "init", "beta", beta.SSHURL(), "--yes").OK(t)
		lee.Lore(parent, "org", "create", jCmdsBetaOrg, "--store", beta.SSHURL(), "--key", "human:"+jCmdsWorkEmail, "--name", tlLee.Name, "--yes").OK(t)
		// the active setup of ~/code is lead, whose org is acmeteam
		lee.Lore(parent, "org", "refresh").OK(t).Says(t, "org "+tlOrg+" (the org of the active setup "+tlLeadSetup+")")
		// no setup covers ~: refused, naming both orgs and the command
		lee.Lore(lee.Home, "org", "refresh").Refused(t).Says(t, "this machine has 2 org entries ("+tlOrg+", "+jCmdsBetaOrg+")",
			"lore org refresh --org "+tlOrg)
		lee.Lore(lee.Home, "org", "refresh", jCmdsBetaOrg).OK(t).Never(t, "the only org")
	})
}
