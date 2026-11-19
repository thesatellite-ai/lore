//go:build stories && !windows

package stories

// O_store_clone_test.go — a store clone is recognised as a store clone on
// the owner-shaped machine (story O; use pass 4, devco D-682, D-686,
// D-687):
//
//	D-682  `lore init` inside a store clone (top or a folder) adopted "the
//	       shared lore project": it committed a context.targets policy and
//	       actor rows into the store folder, wrote pointer files and a
//	       .gitignore into the clone and installed hooks there; setup list
//	       there said "a git repo lore is not set up in (lore init sets it
//	       up)". Now init refuses with the store's name and folder and
//	       writes nothing (the clone and its log are byte-identical), and
//	       setup list calls the folder a store clone.
//	D-686  whoami inside the ops store's clone named the company org (the
//	       ops store's remote matches the company's own pattern
//	       github.com/<owner>/*). A remote that is an attached store's url
//	       names the org of that store before any pattern.
//	D-687  `store doctor --setup <company>` listed the ops clone (another
//	       setup's store) with its warning, and `store doctor --store
//	       <org>` said "stores this repo and machine use: none" before
//	       checking that store.

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// oCloneState is everything lore could change in a store clone: HEAD and
// every ref, the work tree (untracked and ignored files too), the local git
// config, the hooks and the attributes file. Two equal states mean nothing
// was written.
func oCloneState(t testing.TB, s *Story, dir string) string {
	t.Helper()
	var b strings.Builder
	b.WriteString(s.Git(dir, "for-each-ref", "--format=%(refname) %(objectname)"))
	b.WriteString(s.Git(dir, "rev-parse", "HEAD"))
	b.WriteString(s.Git(dir, "status", "--porcelain", "--ignored", "--untracked-files=all"))
	b.WriteString(s.Git(dir, "config", "--local", "--list"))
	for _, sub := range []string{"hooks", "info"} {
		ents, _ := os.ReadDir(filepath.Join(dir, ".git", sub))
		names := make([]string, 0, len(ents))
		for _, e := range ents {
			names = append(names, e.Name())
		}
		sort.Strings(names)
		for _, n := range names {
			body, _ := os.ReadFile(filepath.Join(dir, ".git", sub, n))
			sum := sha256.Sum256(body)
			b.WriteString(sub + "/" + n + " " + hex.EncodeToString(sum[:8]) + "\n")
		}
	}
	return b.String()
}

func TestStoryOStoreCloneIsAStoreClone(t *testing.T) {
	s := New(t, "O")
	team := s.Remote("github.com/" + oOwner + "/lore-team")
	ops := s.Remote("github.com/" + oOwner + "/lore-ops")
	platform := s.Seed(s.Remote("github.com/"+oOwner+"/platform"), func(dir string) { PlainRepo(t, dir) })
	tools := s.Seed(s.Remote("github.com/opsland/tools"), func(dir string) { PlainRepo(t, dir) })

	ada := s.OwnerShaped(OwnerShape{Personal: oAdminPersonal, Org: oAdminOrg, OrgFolder: oOrg, Alias: oAlias, OrgOwners: []string{oOwner}})
	pc := ada.CloneTo(platform, ada.OrgDir("platform"))
	oSetupFlow(t, ada, pc.Dir, oSetup, "admin", oAdminOrg, oAlias,
		map[string]any{"org": oOrg, "store_url": team.SSHURL()}, []string{"~/code/" + oOrg + "/**"})
	ada.LoreIn(pc, "store", "folder", "add", "team", oFolder).OK(t)
	// the company claims every repo of its GitHub owner, the ops store's too
	ada.LoreIn(pc, "org", "config", "set", "--org", oOrg, "--key", "own", "--value", "github.com/"+oOwner+"/*", "--yes").OK(t)
	ada.LoreIn(pc, "sync").OK(t)
	// a second admin setup: the ops org, whose store sits under the same owner
	const opsOrg, opsSetup = "acmeops", "acme-ops"
	tc := ada.CloneTo(tools, ada.Code("acmeops", "tools"))
	oSetupFlow(t, ada, tc.Dir, opsSetup, "admin", oAdminOrg, oAlias,
		map[string]any{"org": opsOrg, "store_url": ops.SSHURL()}, []string{"~/code/acmeops/**"})
	ada.LoreIn(tc, "sync").OK(t)

	teamClone := oStoreClone(ada, oOwner, "lore-team")
	opsClone := oStoreClone(ada, oOwner, "lore-ops")

	// D-682: init refuses at the clone's top and in a folder, with --yes
	// too, and writes nothing into the clone or the store's log
	s.Step("D-682/init-refused-in-store-clone", func() {
		for _, dir := range []string{teamClone, filepath.Join(teamClone, oFolder)} {
			for _, args := range [][]string{{"init"}, {"init", "--yes"}} {
				before := oCloneState(t, s, teamClone)
				refsBefore := s.RefsOf(team)
				r := ada.Lore(dir, args...).Refused(t, "lore init is for code repos", "store "+oOrg)
				r.Never(t, "adopted the shared lore project")
				if after := oCloneState(t, s, teamClone); after != before {
					t.Fatalf("lore %v in %s changed the store clone:\nbefore:\n%s\nafter:\n%s", args, dir, before, after)
				}
				s.NoPushSince(t, team, refsBefore, "init in the store clone")
			}
		}
		r := ada.Lore(filepath.Join(teamClone, oFolder), "init").Refused(t)
		r.Says(t, "(folder "+oFolder+")", "lore org link --org "+oOrg+" --project "+oFolder)
		// every command the refusal prints works where it says: the folder
		// read in the clone, the link in a code repo
		ada.Lore(filepath.Join(teamClone, oFolder), "task", "list").OK(t)
		ada.LoreIn(pc, "org", "link", "--org", oOrg, "--project", oFolder, "--yes").OK(t)
		// the same class: a writing render committed into the store, org
		// link writes a code repo's link; render --dry-run still previews
		// the folder there (D-432)
		before := oCloneState(t, s, teamClone)
		ada.Lore(filepath.Join(teamClone, oFolder), "render", "--dry-run").OK(t)
		if after := oCloneState(t, s, teamClone); after != before {
			t.Fatalf("render --dry-run in the store clone changed it:\nbefore:\n%s\nafter:\n%s", before, after)
		}
		for _, args := range [][]string{{"render"}, {"org", "link", "--org", oOrg, "--project", oFolder, "--yes"}} {
			before := oCloneState(t, s, teamClone)
			ada.Lore(filepath.Join(teamClone, oFolder), args...).Refused(t, "is for code repos", "nothing written")
			if after := oCloneState(t, s, teamClone); after != before {
				t.Fatalf("lore %v in the store clone changed it:\nbefore:\n%s\nafter:\n%s", args, before, after)
			}
		}
	})

	// D-682 (leftover): a read in a store folder that has no project row
	// yet commits nothing. render --dry-run there committed "lore: project
	// add prj_…: <folder>" into the store: the store-here open made the
	// folder's project row on first use, also for a preview. The matrix
	// cell "fresh store folder × read ops": each read leaves the clone
	// byte-identical (HEAD, refs, status, config, hooks) and pushes nothing;
	// the first write then makes the row in its own run.
	s.Step("D-682/fresh-store-folder-reads-write-nothing", func() {
		const fresh = "fresh"
		ada.LoreIn(pc, "store", "folder", "add", "team", fresh).OK(t)
		ada.LoreIn(pc, "sync").OK(t)
		ada.Lore(teamClone, "sync").OK(t)
		dir := filepath.Join(teamClone, fresh)
		if _, err := os.Stat(dir); err != nil {
			t.Fatalf("the new folder %s is not in the clone: %v", fresh, err)
		}
		reads := [][]string{
			{"render", "--dry-run"}, {"render", "--check"}, {"render", "--report"},
			{"task", "list"}, {"memory", "list"}, {"rule", "list"},
			{"search", "--query", "deploy"}, {"context", "--topic", "deploy"},
			{"sql", "--query", "select count(*) from tasks_all"},
			{"whoami"}, {"doctor"}, {"setup", "list"},
		}
		for _, args := range reads {
			before, refsBefore := oCloneState(t, s, teamClone), s.RefsOf(team)
			r := ada.Lore(dir, args...)
			oCheck(t, "cell/fresh-folder-read@"+strings.Join(args, " "), oCloneState(t, s, teamClone) == before,
				"lore %v in a fresh store folder changed the clone (exit %d):\n%s\nlog: %s", args, r.Code, r, s.Git(teamClone, "log", "-3", "--format=%s"))
			r.Never(t, "project add")
			s.NoPushSince(t, team, refsBefore, "lore "+strings.Join(args, " ")+" in a fresh store folder")
		}
		// the first write makes the folder's row, in its own run
		ada.Lore(dir, "memory", "add", "--body", "deploys go through the queue").OK(t)
		if log := s.Git(teamClone, "log", "-5", "--format=%s"); !strings.Contains(log, "project add") {
			t.Fatalf("the first write in the fresh folder made no project row:\n%s", log)
		}
	})

	// D-682: setup list inside the clone calls it a store clone and never
	// offers lore init there
	s.Step("D-682/setup-list-says-store-clone", func() {
		for _, dir := range []string{teamClone, filepath.Join(teamClone, oFolder)} {
			r := ada.Lore(dir, "setup", "list").OK(t)
			r.Says(t, "This folder: the clone of store "+oOrg)
			r.Never(t, "lore init sets it up", "a git repo lore is not set up in")
		}
	})

	// D-686: the ops clone is the ops org's store, though its remote
	// matches the company's own pattern
	s.Step("D-686/whoami-in-ops-clone-names-ops-org", func() {
		r := ada.Lore(opsClone, "whoami").OK(t)
		r.Says(t, "org        "+opsOrg)
		r.Never(t, "pattern github.com/"+oOwner+"/* of "+oOrg, team.SSHURL())
		r = ada.Lore(teamClone, "whoami").OK(t)
		r.Says(t, "org        "+oOrg)
	})

	// the ownershape cells "inside a store clone" × init / whoami / setup
	// list: each place a person stands in a clone (company top, company
	// folder, ops top) answers as that store's clone and writes nothing
	s.Step("cells/inside-store-clone", func() {
		for _, pl := range []struct{ name, dir, org string }{
			{"company-top", teamClone, oOrg},
			{"company-folder", filepath.Join(teamClone, oFolder), oOrg},
			{"ops-top", opsClone, opsOrg},
		} {
			top := teamClone
			if pl.org == opsOrg {
				top = opsClone
			}
			before := oCloneState(t, s, top)
			r := ada.Lore(pl.dir, "init")
			oCheck(t, "cell/init@"+pl.name, r.Code != 0 && strings.Contains(r.Out(), "the clone of store "+pl.org), "init in %s: %s", pl.name, r)
			r = ada.Lore(pl.dir, "whoami")
			oCheck(t, "cell/whoami@"+pl.name, r.Code == 0 && strings.Contains(r.Out(), "org        "+pl.org+"  "), "whoami in %s: %s", pl.name, r)
			r = ada.Lore(pl.dir, "setup", "list")
			oCheck(t, "cell/setuplist@"+pl.name, r.Code == 0 && strings.Contains(r.Out(), "This folder: the clone of store "+pl.org) && !strings.Contains(r.Out(), "lore init sets it up"), "setup list in %s: %s", pl.name, r)
			oCheck(t, "cell/unchanged@"+pl.name, oCloneState(t, s, top) == before, "init, whoami and setup list in %s changed the clone", pl.name)
		}
	})

	// D-687: store doctor names only the setup's or the store's own clone,
	// and its first section is the store it checks
	s.Step("D-687/store-doctor-scoped", func() {
		r := ada.Lore(ada.Home, "store", "doctor", "--setup", oSetup)
		r.Says(t, "lore-team")
		r.Never(t, "lore-ops")
		r = ada.Lore(ada.Home, "store", "doctor", "--store", oOrg)
		r.Says(t, "lore-team")
		r.Never(t, "lore-ops", "stores this repo and machine use:\n  none")
	})
}
