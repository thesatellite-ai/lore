//go:build journeys && !windows

package journeys

// heldconflict_test.go — a store row both people edited by id from their
// repo, in an org folder the repo does not attach (B2-27). The edits went
// through the by-id fallback (B2-21), the second sync was held by the
// folder's conflict, but `lore sync conflicts` from the repo was empty and
// `lore sync resolve 1` said "no open conflict". Now the repo's conflicts
// list the row and resolve settles it.

import (
	"path/filepath"
	"testing"
)

// TestJourneyTeamLeadHeldConflict is a lead and a teammate retitling one
// task in an org folder neither repo attaches.
func TestJourneyTeamLeadHeldConflict(t *testing.T) {
	t.Parallel()
	tt := newTLTeam(t)
	w := tt.w
	lee := tt.person(tlLee)
	var id string

	w.Step("day1/a-task-in-a-folder-no-repo-attaches", func() {
		tlStartTeam(t, lee, tt.repo[tlLee.Email].Dir, tlLeadSetup, tlOrg, tt.store.SSHURL(), tlLee, []string{tlFolders})
		s := lee.Clone(tt.store, "team-store")
		jWrite(t, filepath.Join(s.Dir, filepath.FromSlash(jManyOrgFile)), "name: "+tlOrg+"\nstores:\n"+
			"    - url: "+tt.store.SSHURL()+"\n      org_folder: org\nown:\n    - github.com/acme-co/*\n")
		s.CommitAll("the org's own repos")
		s.Git("push", "-q", "origin", "HEAD:main")
		parent := filepath.Join(lee.Home, "code")
		lee.Lore(parent, "org", "refresh", tlOrg).OK(t)
		lee.Lore(parent, "store", "folder", "add", "team", "core").OK(t)
		lee.Lore(parent, "store", "folder", "add", "team", "sync").OK(t)
		tt.lore(tlLee, "init", "--non-interactive", "--name=app").OK(t)
		c := tt.repo[tlLee.Email]
		c.CommitAll("lore init")
		c.Git("push", "-q", "origin", "HEAD")
		tt.lore(tlLee, "org", "link", tlOrg, "--project", "core", "--own", "--yes").OK(t)
		r := tt.lore(tlLee, "task", "add", "wire the sync worker", "--to", tlOrg+"/sync", "--yes").OK(t)
		id = sbTaskID.FindString(r.Out())
		if id == "" {
			t.Fatalf("task add printed no id: %s", r)
		}
		tt.lore(tlLee, "sync", "run").OK(t)

		_, line := tlInvite(t, lee, tt.repo[tlLee.Email].Dir, tlAnn.Email)
		ann := tt.person(tlAnn)
		tlJoin(t, ann, tt.repo[tlAnn.Email].Dir, tlTeamSetup, line, tlAnn, []string{tlFolders})
		ann.Lore(filepath.Join(ann.Home, "code"), "org", "refresh", tlOrg).OK(t)
		tt.lore(tlAnn, "org", "link", tlOrg, "--project", "core", "--own", "--yes").OK(t)
		tt.lore(tlAnn, "task", "show", id).OK(t).Says(t, "wire the sync worker")
	})

	w.Step("day2/both-retitle-it-by-id", func() {
		w.AdvanceDays(1)
		tt.lore(tlAnn, "sync", "run").OK(t)
		tt.m[tlAnn.Email].SetEnv("LORE_OFFLINE", "1")
		tt.lore(tlAnn, "task", "show", id).OK(t)
		tt.lore(tlAnn, "task", "edit", id, "--title", "Ann's title", "--yes").OK(t)
		tt.lore(tlLee, "task", "edit", id, "--title", "Lee's title", "--yes").OK(t)
		tt.lore(tlLee, "sync", "run").OK(t)
		tt.m[tlAnn.Email].SetEnv("LORE_OFFLINE", "0")
		r := tt.lore(tlAnn, "sync", "run")
		r.Fails(t, 23, "E_STALE", id, "lore sync conflicts")
		cf := tt.lore(tlAnn, "sync", "conflicts").OK(t)
		t.Logf("sync conflicts from the repo:\n%s", cf)
		cf.Says(t, "#1 tasks/"+id, tlOrg+"/sync", `"Ann's title"`, `"Lee's title"`)
		tt.lore(tlAnn, "sync", "resolve", "1", "--take", "kept").OK(t).Says(t, "resolved #1", id)
		tt.lore(tlAnn, "sync", "run").OK(t)
		tt.lore(tlLee, "sync", "run").OK(t)
		for _, p := range []tlPerson{tlLee, tlAnn} {
			tt.lore(p, "task", "show", id).OK(t).Says(t, "Ann's title")
		}
		tt.lore(tlAnn, "sync", "conflicts").OK(t).Says(t, "no conflicts")
	})
}
