//go:build journeys && !windows

package journeys

// initlinked_test.go — `lore init` in a clone of a repo the org store
// already links by its remote (B1-18). Lee links a repo with no lore files
// to a store folder and writes a note there; on another clone of that repo
// `lore init` used to mint a new project id, so the link no longer named
// the repo and the folder's notes stopped reaching it. Now init takes the
// linked project id, says so, and the team's note is there.

import (
	"path/filepath"
	"regexp"
	"testing"

	"github.com/thesatellite-ai/lore/logic/orgresolve"
	"github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

const (
	ilSlug   = "github.com/acme-co/svc"
	ilFolder = "svc"
	ilNote   = "the svc queue drains every night at two"

	ilClientSlug   = "github.com/client-co/web"
	ilClientFolder = "client_web"
	ilClientNote   = "the client deploys on tuesdays only"
)

// ilProjectID matches init's "project_id: <id>" line.
var ilProjectID = regexp.MustCompile(`project_id: (prj_[0-9a-f]+)`)

func TestJourneyInitLinkedRepo(t *testing.T) {
	t.Parallel()
	tt := newTLTeam(t)
	w := tt.w
	lee := tt.person(tlLee)
	parent := filepath.Join(lee.Home, "code")
	remote := w.Remote(ilSlug)
	var hub, second *teamharness.Clone

	w.Step("day1/link-a-repo-with-no-lore-files", func() {
		tlStartTeam(t, lee, tt.repo[tlLee.Email].Dir, tlLeadSetup, tlOrg, tt.store.SSHURL(), tlLee, []string{tlFolders})
		// the org's own repos (as the org of the report had): github.com/acme-co/*
		s := lee.Clone(tt.store, "team-store")
		jWrite(t, filepath.Join(s.Dir, filepath.FromSlash(jManyOrgFile)), "name: "+tlOrg+"\nstores:\n"+
			"    - url: "+tt.store.SSHURL()+"\n      org_folder: org\nown:\n    - github.com/acme-co/*\n")
		s.CommitAll("the org's own repos")
		s.Git("push", "-q", "origin", "HEAD:main")
		lee.Lore(parent, "org", "refresh", tlOrg).OK(t)
		lee.Lore(parent, "store", "folder", "add", "team", ilFolder).OK(t)
		hub = jPlainRepo(t, lee, remote, filepath.Join(parent, "svc"))
		lee.LoreIn(hub, "org", "link", tlOrg, "--project", ilFolder, "--own", "--yes").OK(t)
		// H-2: a plain write in a repo linked by its remote (no --to) goes to
		// the linked folder; it used to fail on the read-only repo cache
		lee.LoreIn(hub, "memory", "add", "--body", ilNote).OK(t).Says(t, ilFolder)
		lee.LoreIn(hub, "memory", "list").OK(t).Says(t, ilNote)
	})

	w.Step("day2/init-in-another-clone-keeps-the-link", func() {
		w.AdvanceDays(1)
		second = lee.Clone(remote, "svc-two")
		lee.MoveClone(t, second, filepath.Join(parent, "svc-two"))
		r := lee.LoreIn(second, "init", "--non-interactive", "--name=svc").OK(t)
		want, err := orgresolve.RemoteProjectID(ilSlug)
		if err != nil {
			t.Fatal(err)
		}
		m := ilProjectID.FindStringSubmatch(r.Stdout)
		if m == nil || m[1] != want {
			t.Fatalf("lore init in a linked repo: project id %v, want the linked %s:\n%s", m, want, r)
		}
		r.Says(t, ilFolder) // init names the link it took the id from
		lee.LoreIn(second, "memory", "list").OK(t).Says(t, ilNote)
		lee.LoreIn(second, "whoami").OK(t).Never(t, "0 link(s) for this repo")
	})

	w.Step("day2/init-over-machine-local-leftovers-keeps-the-link", func() {
		// B1-18 round 2: a checkout copied from an older one keeps lore's
		// machine-local files (.lore/lock, .lore/state/, ignored by git) but
		// no row files; the link (the folder's _meta.json links entry) still
		// names the repo, and init takes its id
		third := lee.Clone(remote, "svc-three")
		lee.MoveClone(t, third, filepath.Join(parent, "svc-three"))
		third.WriteFile(".lore/lock", "")
		third.WriteFile(".lore/state/stamp", "")
		r := lee.LoreIn(third, "init", "--non-interactive", "--name=svc").OK(t)
		want, err := orgresolve.RemoteProjectID(ilSlug)
		if err != nil {
			t.Fatal(err)
		}
		if m := ilProjectID.FindStringSubmatch(r.Stdout); m == nil || m[1] != want {
			t.Fatalf("lore init over leftovers: project id %v, want the linked %s:\n%s", m, want, r)
		}
		lee.LoreIn(third, "memory", "list").OK(t).Says(t, ilNote)
	})

	w.Step("day3/a-client-repo-linked-by-its-remote", func() {
		w.AdvanceDays(1)
		lee.Lore(parent, "store", "folder", "add", "team", ilClientFolder).OK(t)
		client := w.Remote(ilClientSlug)
		web := jPlainRepo(t, lee, client, filepath.Join(parent, "client-web"))
		lee.LoreIn(web, "org", "link", tlOrg, "--project", ilClientFolder, "--yes").OK(t)
		// H-2 for a client link: the plain write lands in the client folder
		lee.LoreIn(web, "memory", "add", "--body", ilClientNote).OK(t).Says(t, ilClientFolder)
		// H-1: lore init in another clone of the client repo used to refuse
		// with E_LEAK_BLOCKED "cannot check: store unreachable and nothing
		// cached": init had dropped the store its own leak check reads
		other := lee.Clone(client, "client-web-two")
		lee.MoveClone(t, other, filepath.Join(parent, "client-web-two"))
		lee.LoreIn(other, "init", "--non-interactive", "--name=client-web").OK(t).Never(t, "E_LEAK_BLOCKED")
		lee.LoreIn(other, "memory", "list").OK(t).Says(t, ilClientNote)
	})
}
