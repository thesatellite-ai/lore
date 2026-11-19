//go:build journeys && !windows

package journeys

// adminstore_test.go — an org with an admin-only store (devco:D-442). Lee
// keeps the company's servers, domains and accounts in a second store that
// only admins can read. Before `audience: admins`, that store sat in the
// shared lore-org.yaml like any other: Ann (a member, no access to it) got
// errors from setup, sync and doctor, and her client-repo checks failed
// closed. Now the store is marked admins-only with a command, Ann's lore
// never tries to read it (no error, no ops content, guards still work),
// Lee reads and writes both stores, and when Ann is made an admin her next
// sync gets the store.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	asOpsSlug    = "github.com/acme-co/lore-ops"
	asClientSlug = "github.com/client-co/portal"
	asOpsBody    = "the staging host is box-17 behind the bastion"
)

// asDeny makes m's git unable to read slug's store, the way GitHub answers
// a repo the login has no access to: the url goes to a folder that is not
// there, so git says it cannot read from the remote.
func asDeny(t testing.TB, m *Machine, slug string) {
	t.Helper()
	gc := filepath.Join(m.Home, ".gitconfig")
	nowhere := filepath.Join(m.w.Root, "no-access", strings.ReplaceAll(slug, "/", "_")) + ".git"
	for _, from := range []string{"git@" + strings.Replace(slug, "/", ":", 1) + ".git", "https://" + slug + ".git"} {
		if r := m.w.TryGit(m.Home, "config", "--file", gc, "--add", "url."+nowhere+".insteadOf", from); r.Code != 0 {
			t.Fatalf("deny %s: %s", slug, r.Stderr)
		}
	}
}

func TestJourneyAdminOnlyStore(t *testing.T) {
	t.Parallel()
	tt := newTLTeam(t)
	w := tt.w
	lee := tt.person(tlLee)
	ops := w.Remote(asOpsSlug)
	parent := filepath.Join(lee.Home, "code")
	line := ""

	w.Step("day1/lee-adds-an-admin-only-ops-store", func() {
		tlStartTeam(t, lee, tt.repo[tlLee.Email].Dir, tlLeadSetup, tlOrg, tt.store.SSHURL(), tlLee, []string{tlFolders})
		s := lee.Clone(tt.store, "team-store")
		jWrite(t, filepath.Join(s.Dir, filepath.FromSlash(jManyOrgFile)), "name: "+tlOrg+"\nstores:\n"+
			"    - url: "+tt.store.SSHURL()+"\n      org_folder: org\nown:\n    - github.com/acme-co/*\nrepos:\n    - github.com/client-co/*\n")
		s.CommitAll("own and client repos")
		s.Git("push", "-q", "origin", "HEAD:main")
		lee.Lore(parent, "org", "refresh", tlOrg).OK(t)
		tt.lore(tlLee, "init", "--non-interactive", "--name=app").OK(t)
		c := tt.repo[tlLee.Email]
		c.CommitAll("lore init")
		c.Git("push", "-q", "origin", "HEAD")
		tt.lore(tlLee, "org", "link", tlOrg, "--project", "core", "--own", "--yes").OK(t)
		tt.lore(tlLee, "org", "store", "add", "--org", tlOrg, "--url", ops.SSHURL(), "--audience", "admins", "--push", "--yes").OK(t).
			Says(t, "admins")
		lee.Lore(parent, "store", "folder", "add", ops.SSHURL(), "infra").OK(t)
		lee.Lore(parent, "memory", "add", "--body", asOpsBody, "--to", "lore-ops/infra", "--yes").OK(t)
		lee.Lore(parent, "sync").OK(t)
		if len(ops.FilesMatching("refs/heads/main", "memories/")) != 1 || len(tt.store.FilesMatching("refs/heads/main", "memories/")) != 0 {
			t.Fatalf("the ops note is not (only) in the ops store: ops %v, team %v", ops.FilesMatching("refs/heads/main", "memories/"), tt.store.FilesMatching("refs/heads/main", "memories/"))
		}
		_, line = tlInvite(t, lee, c.Dir, tlAnn.Email)
	})

	ann := tt.person(tlAnn)
	asDeny(t, ann, asOpsSlug)

	w.Step("day1/ann-joins-without-an-error", func() {
		tlJoin(t, ann, tt.repo[tlAnn.Email].Dir, tlTeamSetup, line, tlAnn, []string{tlFolders})
		r := ann.Lore(filepath.Join(ann.Home, "code"), "org", "refresh", tlOrg).OK(t)
		r.Never(t, "lore-ops", "cannot read", "ERROR")
		a := tt.repo[tlAnn.Email]
		a.Git("pull", "-q", "--no-rebase", "origin", "main")
		tt.lore(tlAnn, "sync").OK(t).Never(t, "lore-ops", "could not", "problem")
		tt.lore(tlAnn, "doctor").Never(t, "E_LINK_UNKNOWN", "store_access", "treated as a client repo").
			Says(t, "lore-ops is for the org's admins", "you are not an admin of "+tlOrg)
		tt.lore(tlAnn, "search", "staging host").OK(t).Never(t, "box-17")
		ann.Lore(filepath.Join(ann.Home, "code"), "search", "staging host").OK(t).Never(t, "box-17")
		if cfg := asConfig(t, ann); strings.Contains(cfg, "lore-ops") {
			t.Fatalf("Ann's org entry lists the admins' store:\n%s", cfg)
		}
	})

	w.Step("day1/ann-client-repo-guards-work", func() {
		portal := jPlainRepo(t, ann, w.Remote(asClientSlug), filepath.Join(ann.Home, "code", "portal"))
		r := ann.LoreIn(portal, "whoami").OK(t)
		r.Says(t, "client").Never(t, "lore-ops")
		ann.LoreIn(portal, "check", "leaks").OK(t).Never(t, "lore-ops", "cannot")
	})

	w.Step("day1/no-repo-links-to-the-admins-store", func() {
		tt.lore(tlLee, "org", "link", tlOrg, "--project", "infra", "--store", ops.SSHURL(), "--own", "--yes").
			Refused(t, "admins-only", "--audience everyone")
	})

	w.Step("day1/lee-sees-both-stores", func() {
		lee.Lore(parent, "search", "staging host").OK(t).Says(t, "box-17")
		lee.Lore(parent, "store", "list").OK(t).Says(t, "lore-ops", "admins")
		tt.lore(tlLee, "doctor").Never(t, "is for the org's admins", "E_LINK_UNKNOWN")
	})

	// Ann is made an admin (and GitHub gives her login access to the ops
	// repo): her next sync brings the store in, with no command of her own.
	w.Step("day2/ann-made-admin-gets-the-store", func() {
		w.AdvanceDays(1)
		tt.lore(tlLee, "actor", "period", "edit", "@ann", "--org-role", "admin", "--yes").OK(t)
		tt.lore(tlLee, "sync").OK(t)
		asAllow(t, ann, asOpsSlug)
		tt.lore(tlAnn, "sync").OK(t)
		if cfg := asConfig(t, ann); !strings.Contains(cfg, "lore-ops") {
			t.Fatalf("Ann's org entry lacks the admins' store after her sync as an admin:\n%s", cfg)
		}
		tt.lore(tlAnn, "sync").OK(t)
		ann.Lore(filepath.Join(ann.Home, "code"), "search", "staging host").OK(t).Says(t, "box-17")
		tt.lore(tlAnn, "doctor").Never(t, "is for the org's admins")
	})

	// The store is opened to everyone again: no audience key is left.
	w.Step("day2/lee-opens-the-store-to-everyone", func() {
		tt.lore(tlLee, "org", "store", "audience", "--org", tlOrg, "--url", ops.SSHURL(), "--audience", "everyone", "--push", "--yes").OK(t).
			Says(t, "now for everyone", "pushed")
		if got, _ := tt.store.Read("refs/heads/main", jManyOrgFile); strings.Contains(got, "audience") {
			t.Fatalf("lore-org.yaml still has an audience:\n%s", got)
		}
		tt.lore(tlLee, "org", "store", "audience", "--org", tlOrg, "--url", tt.store.SSHURL(), "--audience", "admins", "--yes").
			Refused(t, "org folder", "cannot be for admins only")
	})
}

// asConfig is m's ~/.lore/config.yaml.
func asConfig(t testing.TB, m *Machine) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(m.Home, ".lore", "config.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// asAllow undoes asDeny: m's git reads slug's store again.
func asAllow(t testing.TB, m *Machine, slug string) {
	t.Helper()
	gc := filepath.Join(m.Home, ".gitconfig")
	nowhere := filepath.Join(m.w.Root, "no-access", strings.ReplaceAll(slug, "/", "_")) + ".git"
	if r := m.w.TryGit(m.Home, "config", "--file", gc, "--remove-section", "url."+nowhere); r.Code != 0 {
		t.Fatalf("allow %s: %s", slug, r.Stderr)
	}
}
