//go:build journeys && !windows

package journeys

// unreachstore_test.go — an org store a member cannot reach (devco:D-726).
// Lee adds a second store to the org for everyone (no audience), but the
// forge gives Ann's login no access to it. Before, every read of Ann's
// lore tried that store: whoami showed no `reads` line for her own repo's
// folder and complained it could not read the other store. Now the store
// she cannot reach is skipped for her with one quiet note in whoami and
// doctor naming what to ask an admin; her repo's folder reads, lists and
// syncs as normal, and Lee is unaffected.

import (
	"path/filepath"
	"testing"
)

const (
	urSecretSlug = "github.com/acme-co/lore-secret"
	urSecretBody = "the vendor contract renews in march"
)

func TestJourneyUnreachableOrgStore(t *testing.T) {
	t.Parallel()
	tt := newTLTeam(t)
	w := tt.w
	lee := tt.person(tlLee)
	sec := w.Remote(urSecretSlug)
	parent := filepath.Join(lee.Home, "code")
	line := ""

	w.Step("day1/lee-adds-a-second-store-for-everyone", func() {
		tlStartTeam(t, lee, tt.repo[tlLee.Email].Dir, tlLeadSetup, tlOrg, tt.store.SSHURL(), tlLee, []string{tlFolders})
		s := lee.Clone(tt.store, "team-store")
		jWrite(t, filepath.Join(s.Dir, filepath.FromSlash(jManyOrgFile)), "name: "+tlOrg+"\nstores:\n"+
			"    - url: "+tt.store.SSHURL()+"\n      org_folder: org\nown:\n    - github.com/acme-co/*\n")
		s.CommitAll("own repos")
		s.Git("push", "-q", "origin", "HEAD:main")
		lee.Lore(parent, "org", "refresh", tlOrg).OK(t)
		tt.lore(tlLee, "init", "--non-interactive", "--name=app").OK(t)
		c := tt.repo[tlLee.Email]
		c.CommitAll("lore init")
		c.Git("push", "-q", "origin", "HEAD")
		tt.lore(tlLee, "org", "link", tlOrg, "--project", "core", "--own", "--yes").OK(t)
		tt.lore(tlLee, "org", "store", "add", "--org", tlOrg, "--url", sec.SSHURL(), "--push", "--yes").OK(t)
		lee.Lore(parent, "store", "folder", "add", sec.SSHURL(), "vendors").OK(t)
		lee.Lore(parent, "memory", "add", "--body", urSecretBody, "--to", "lore-secret/vendors", "--yes").OK(t)
		tt.lore(tlLee, "sync").OK(t)
		_, line = tlInvite(t, lee, c.Dir, tlAnn.Email)
	})

	ann := tt.person(tlAnn)
	asDeny(t, ann, urSecretSlug)

	w.Step("day1/ann-joins-and-reads-her-folder", func() {
		tlJoin(t, ann, tt.repo[tlAnn.Email].Dir, tlTeamSetup, line, tlAnn, []string{tlFolders})
		a := tt.repo[tlAnn.Email]
		a.Git("pull", "-q", "--no-rebase", "origin", "main")
		tt.lore(tlAnn, "sync").OK(t).Never(t, "ERROR", "cannot read")
		r := tt.lore(tlAnn, "whoami").OK(t)
		r.Says(t, tlOrg+"/core (this repo's link)", "lore-secret: no access, skipped", "Ask an admin of "+tlOrg).Never(t, "ERROR", "cannot read")
		tt.lore(tlAnn, "memory", "add", "--body", "ann was here", "--yes").OK(t)
		tt.lore(tlAnn, "memory", "list").OK(t).Says(t, "ann was here").Never(t, "lore-secret", "ERROR")
		tt.lore(tlAnn, "context", "--topics").OK(t).Never(t, "ERROR", urSecretBody)
		tt.lore(tlAnn, "search", "vendor contract").OK(t).Never(t, urSecretBody)
		tt.lore(tlAnn, "doctor").Says(t, "lore-secret: no access, skipped")
		tt.lore(tlAnn, "sync").OK(t).Never(t, "ERROR", "cannot read")
	})

	w.Step("day1/ann-cannot-write-the-store-she-cannot-reach", func() {
		ann.Lore(filepath.Join(ann.Home, "code"), "memory", "add", "--body", "x", "--to", "lore-secret/vendors", "--yes").Refused(t, "E_STORE_UNREACHABLE").Says(t, "no access to lore-secret", "Ask an admin of "+tlOrg).Never(t, "go online")
	})

	w.Step("day1/ann-folder-scans-skip-the-store-quietly", func() {
		// every path that walks all org stores (folder scans, a link
		// picking its store) skips the refused one without a fetch and
		// says it once, never "could not read" or "offline"
		outside := filepath.Join(ann.Home, "code")
		ann.Lore(outside, "project", "list").OK(t).Says(t, "lore-secret: no access, skipped").Never(t, "could not read", "ERROR")
		tt.lore(tlAnn, "org", "link", "--org", tlOrg, "--project", "vendors", "--store", sec.SSHURL(), "--yes").
			Refused(t, "E_STORE_UNREACHABLE").Says(t, "no access to the store", "Ask an admin").Never(t, "offline", "retry when")
	})

	w.Step("day1/lee-unaffected", func() {
		lee.Lore(parent, "search", "vendor contract").OK(t).Says(t, urSecretBody)
		tt.lore(tlLee, "whoami").OK(t).Says(t, tlOrg+"/core (this repo's link)").Never(t, "no access")
		tt.lore(tlLee, "doctor").Never(t, "no access, skipped")
	})
}
