//go:build journeys && !windows

package journeys

// ci_backstop_test.go — `lore check leaks --ci` in an org's own repo, the
// CI job's backstop for a commit or push that skipped lore's hooks.
//
// devco R-13 (regression of devco D-14): after a teammate wrote a row in
// the own repo, --ci failed on the actor copies lore itself wrote there
// (stable_key, handle, the origin stamp an own repo keeps) and hinted
// `lore migrate run`, which keeps them. Now the CI clone passes.
// devco R-12: --ci checked ids and structure only, so a key committed and
// pushed with --no-verify passed CI. Now it names the file, rule and hash
// (never the value) with the `lore secrets allow --file` line.

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestJourneyTeamLeadCIBackstop is a lead, a teammate who writes a row in
// the own repo, and the CI clone that checks every push.
func TestJourneyTeamLeadCIBackstop(t *testing.T) {
	t.Parallel()
	tt := newTLTeam(t)
	w := tt.w
	lee := tt.person(tlLee)
	key := "AKIA" + strings.Repeat("Q", 16) // built at run time, never a literal key

	w.Step("day1/a-teammate-writes-in-the-own-repo", func() {
		tlStartTeam(t, lee, tt.repo[tlLee.Email].Dir, tlLeadSetup, tlOrg, tt.store.SSHURL(), tlLee, []string{tlFolders})
		s := lee.Clone(tt.store, "team-store")
		jWrite(t, filepath.Join(s.Dir, filepath.FromSlash(jManyOrgFile)), "name: "+tlOrg+"\nstores:\n"+
			"    - url: "+tt.store.SSHURL()+"\n      org_folder: org\nown:\n    - github.com/acme-co/*\n")
		s.CommitAll("the org's own repos")
		s.Git("push", "-q", "origin", "HEAD:main")
		lee.Lore(filepath.Join(lee.Home, "code"), "org", "refresh", tlOrg).OK(t)
		tt.lore(tlLee, "init", "--non-interactive", "--name=app").OK(t)
		c := tt.repo[tlLee.Email]
		c.CommitAll("lore init")
		c.Git("push", "-q", "origin", "HEAD")
		tt.lore(tlLee, "org", "link", tlOrg, "--project", "core", "--own", "--yes").OK(t)
		_, line := tlInvite(t, lee, c.Dir, tlAnn.Email)
		ann := tt.person(tlAnn)
		tlJoin(t, ann, tt.repo[tlAnn.Email].Dir, tlTeamSetup, line, tlAnn, []string{tlFolders})
		ann.Lore(filepath.Join(ann.Home, "code"), "org", "refresh", tlOrg).OK(t)
		a := tt.repo[tlAnn.Email]
		a.Git("pull", "-q", "--no-rebase", "origin", "main")
		tt.lore(tlAnn, "memory", "add", "--body", "the pump driver needs a reset after flashing", "--to", "repo", "--yes").OK(t)
		a.CommitAll("lore: a note")
		a.Git("push", "-q", "origin", "HEAD:main")
	})

	w.Step("day1/the-ci-clone-accepts-lore-s-own-actor-copies", func() {
		ci := lee.Clone(tt.app, "app-ci")
		r := lee.LoreIn(ci, "check", "leaks", "--ci")
		t.Logf("check leaks --ci:\n%s", r)
		r.OK(t).Never(t, "lore migrate run", "actor copy")
	})

	w.Step("day2/a-key-pushed-past-the-hooks-fails-ci", func() {
		w.AdvanceDays(1)
		a := tt.repo[tlAnn.Email]
		jWrite(t, filepath.Join(a.Dir, "deploy.env.example"), "AWS_ACCESS_KEY_ID="+key+"\n")
		a.Git("add", "deploy.env.example")
		a.Git("-c", "core.hooksPath=/dev/null", "commit", "-q", "--no-verify", "-m", "deploy example")
		a.Git("-c", "core.hooksPath=/dev/null", "push", "-q", "--no-verify", "origin", "HEAD:main")
		ci := lee.Clone(tt.app, "app-ci2")
		r := lee.LoreIn(ci, "check", "leaks", "--ci")
		t.Logf("check leaks --ci after the push:\n%s", r)
		r.Fails(t, 19, "E_LEAK_BLOCKED", "deploy.env.example:1", "lore secrets allow --file deploy.env.example").Never(t, key)
	})
}
