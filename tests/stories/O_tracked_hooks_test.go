//go:build stories && !windows

package stories

// O_tracked_hooks_test.go — devco D-709 on the owner-shaped machine (story
// O): an org repo whose hooks folder is committed with the repo
// (core.hooksPath=.githooks, tracked) and holds lore's blocks an older
// lore wrote. `lore sync` said it would not add blocks to the tracked
// folder; the commit's lore check then said "`lore sync` … sets up hook
// blocks": a loop. The cell: sync and the commit both name `lore fix
// --tracked-hooks`, never `lore sync`, for those blocks; run as printed it
// brings them current and the next commit is quiet.

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestStoryOTrackedHooksNameTheFlag(t *testing.T) {
	s := New(t, "O")
	team := s.Remote("github.com/" + oOwner + "/lore-team")
	hooked := s.Seed(s.Remote("github.com/"+oOwner+"/hooked"), func(dir string) {
		PlainRepo(t, dir)
		writeExec(t, filepath.Join(dir, ".githooks", "pre-commit"), "#!/bin/sh\necho team pre-commit\n")
	})

	ada := s.OwnerShaped(OwnerShape{Personal: oAdminPersonal, Org: oAdminOrg, OrgFolder: oOrg, Alias: oAlias, OrgOwners: []string{oOwner}})
	hc := ada.CloneTo(hooked, ada.OrgDir("hooked"))
	oSetupFlow(t, ada, hc.Dir, oSetup, "admin", oAdminOrg, oAlias,
		map[string]any{"org": oOrg, "store_url": team.SSHURL()}, []string{"~/code/" + oOrg + "/**"})
	hc.Git("config", "core.hooksPath", ".githooks")
	hooks := filepath.Join(hc.Dir, ".githooks")
	const setUpLine = "git is not set up for lore's row files"
	const syncLoop = "`lore sync` (or the next lore write here) sets up hook blocks"

	s.Step("D-709/blocks-committed-in-the-tracked-folder", func() {
		ada.Lore(hc.Dir, "init", "--yes").OK(t)
		ada.Lore(hc.Dir, "fix", "--tracked-hooks").Says(t, "hook blocks in "+hooks)
		hc.Git("add", "-A")
		hc.TryGit("commit", "-q", "-m", "lore and its hook blocks").OK(t)
	})

	var printed string
	s.Step("D-709/older-blocks-sync-and-commit-name-the-flag", func() {
		ents, err := os.ReadDir(hooks)
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range ents { // the blocks as an older lore wrote them
			p := filepath.Join(hooks, e.Name())
			b, rerr := os.ReadFile(p)
			if rerr != nil {
				t.Fatal(rerr)
			}
			writeExec(t, p, strings.ReplaceAll(string(b), "lore sync hook", "lore  sync hook"))
		}
		ada.Lore(hc.Dir, "sync").Says(t, "`lore fix --tracked-hooks`").Never(t, syncLoop)
		writeFile(t, filepath.Join(hc.Dir, "main.go"), "package main\n\nfunc main() { _ = 2 }\n")
		hc.Git("add", "-A")
		out := hc.TryGit("commit", "-q", "-m", "older blocks").OK(t).Out()
		if strings.Contains(out, syncLoop) {
			t.Fatalf("the commit sends the owner to lore sync, which refuses the tracked folder:\n%s", out)
		}
		i := strings.Index(out, setUpLine)
		if i < 0 {
			t.Fatalf("the commit does not say the blocks are stale:\n%s", out)
		}
		m := regexp.MustCompile("`(lore [^`]+)` sets up hook blocks in " + regexp.QuoteMeta(hooks)).FindStringSubmatch(out[i:])
		if m == nil || m[1] != "lore fix --tracked-hooks" {
			t.Fatalf("the commit's step for the hook blocks is %q, want lore fix --tracked-hooks:\n%s", m, out)
		}
		printed = m[1]
	})

	s.Step("D-709/printed-command-works-next-commit-quiet", func() {
		ada.Lore(hc.Dir, strings.Fields(printed)[1:]...).Says(t, "hook blocks in "+hooks) // exactly as printed
		hc.Git("add", "-A")
		hc.TryGit("commit", "-q", "-m", "current blocks").OK(t)
		writeFile(t, filepath.Join(hc.Dir, "main.go"), "package main\n\nfunc main() { _ = 3 }\n")
		hc.Git("add", "-A")
		if out := hc.TryGit("commit", "-q", "-m", "after").OK(t).Out(); strings.Contains(out, setUpLine) {
			t.Fatalf("after %s the commit still says the blocks are not set up:\n%s", printed, out)
		}
	})
}
