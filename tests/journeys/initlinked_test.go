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
	"strings"
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

// TestJourneyLinkedFolderOfAnotherStore (L-4): a repo with no lore files is
// linked by its remote to a folder of the org's second store. A plain write
// lands in that store's folder, and so does a write with the --to that the
// L-4 refusal names (--to <org>/<folder>), so the hint leads to the right
// place. (Once the org entry stops listing that store, the link itself is
// no longer read on this machine: the repo then reads as not set up, and
// the refusal is not reached; it guards a link read from a store that is
// then not attached.)
func TestJourneyLinkedFolderOfAnotherStore(t *testing.T) {
	t.Parallel()
	tt := newTLTeam(t)
	w := tt.w
	lee := tt.person(tlLee)
	parent := filepath.Join(lee.Home, "code")
	two := w.Remote("github.com/acme-co/lore-extra")
	var repo *teamharness.Clone

	w.Step("day1/link-to-a-folder-of-a-second-store", func() {
		tlStartTeam(t, lee, tt.repo[tlLee.Email].Dir, tlLeadSetup, tlOrg, tt.store.SSHURL(), tlLee, []string{tlFolders})
		s := lee.Clone(tt.store, "team-store")
		jWrite(t, filepath.Join(s.Dir, filepath.FromSlash(jManyOrgFile)), "name: "+tlOrg+"\nstores:\n"+
			"    - url: "+tt.store.SSHURL()+"\n      org_folder: org\n    - url: "+two.SSHURL()+"\nown:\n    - github.com/acme-co/*\n")
		s.CommitAll("org file: a second store")
		s.Git("push", "-q", "origin", "HEAD:main")
		lee.Lore(parent, "org", "refresh", tlOrg).OK(t)
		lee.Lore(parent, "store", "folder", "add", two.SSHURL(), "extra").OK(t)
		repo = jPlainRepo(t, lee, w.Remote("github.com/acme-co/svc-extra"), filepath.Join(parent, "svc-extra"))
		lee.LoreIn(repo, "org", "link", tlOrg, "--project", "extra", "--store", two.SSHURL(), "--own", "--yes").OK(t)
	})

	w.Step("day2/plain-and-hinted-writes-land-in-the-second-store", func() {
		w.AdvanceDays(1)
		lee.LoreIn(repo, "memory", "add", "--body", "plain write to extra").OK(t)
		lee.LoreIn(repo, "memory", "add", "--body", "hinted write to extra", "--to", tlOrg+"/extra").OK(t)
		if f := two.FilesMatching("refs/heads/main", "extra/memories/"); len(f) != 2 {
			t.Fatalf("both writes must land in the second store's extra/ folder: %v", f)
		}
		if f := tt.store.FilesMatching("refs/heads/main", "extra/"); len(f) != 0 {
			t.Fatalf("a write went to the first store: %v", f)
		}
	})

	w.Step("day3/the-org-file-drops-the-second-store", func() {
		// L-7: the org's lore-org.yaml no longer lists the second store
		w.AdvanceDays(1)
		s := lee.Clone(tt.store, "team-store-again")
		jWrite(t, filepath.Join(s.Dir, filepath.FromSlash(jManyOrgFile)), "name: "+tlOrg+"\nstores:\n"+
			"    - url: "+tt.store.SSHURL()+"\n      org_folder: org\nown:\n    - github.com/acme-co/*\n")
		s.CommitAll("org file: drop the second store")
		s.Git("push", "-q", "origin", "HEAD:main")
		lee.Lore(parent, "org", "refresh", tlOrg).OK(t)
		// (1) the "no lore project" answer says why
		r := lee.LoreIn(repo, "memory", "add", "--body", "after the drop")
		if r.Code == 0 || !strings.Contains(r.Out(), "was linked in store") || !strings.Contains(r.Out(), "no longer lists it") || !strings.Contains(r.Out(), "lore-extra") {
			t.Fatalf("the refusal does not say the link's store was dropped: %s", r)
		}
		// (2) --to <org>/<folder> --yes must not make the folder in the
		// first store
		r = lee.LoreIn(repo, "memory", "add", "--body", "after the drop", "--to", tlOrg+"/extra", "--yes")
		if r.Code == 0 || !strings.Contains(r.Out(), "would create folder extra") || !strings.Contains(r.Out(), "lore-extra") {
			t.Fatalf("--to %s/extra --yes was not refused naming the linked store: %s", tlOrg, r)
		}
		if f := tt.store.FilesMatching("refs/heads/main", "extra/"); len(f) != 0 {
			t.Fatalf("the folder was created in the first store: %v", f)
		}
	})
}
