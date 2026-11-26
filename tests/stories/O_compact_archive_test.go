//go:build stories && !windows

package stories

// O_compact_archive_test.go — devco D-1003 (bug hunt H-257, store growth
// SG-6) on the owner-shaped machine (story O): compaction with an archive.
// The admin compacts the team store behind its flag; the old history is
// pushed to refs/lore-archive/epoch-0 first; a teammate with writes still
// waiting adopts the new history only after re-doing the rewrite from his
// own copy, and his writes go out on top; --verify proves the snapshot
// against the archive; --undo-epoch puts the old history back as epoch 2,
// which the teammate adopts the same way; a higher epoch the teammate forces
// onto the remote by hand is refused by the admin's lore, which stays on
// its own history (no checkpoint). Before the fix any higher epoch was taken as it came.

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

// oCaGit runs git against the team remote's bare repo.
func oCaGit(t *testing.T, r *teamharness.Remote, env []string, stdin string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"--git-dir", r.Dir}, args...)...)
	cmd.Env = append(os.Environ(), env...)
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// oCaForgeEpoch puts a parentless commit on the remote's main that holds
// main's tree with the epoch raised by one, authored by email: what a
// member with push access can do by hand.
func oCaForgeEpoch(t *testing.T, r *teamharness.Remote, email string) {
	t.Helper()
	marker := oCaGit(t, r, nil, "", "show", "main:lore-store.yaml")
	var lines []string
	epoch := 0
	for l := range strings.SplitSeq(marker, "\n") {
		if v, ok := strings.CutPrefix(l, "epoch: "); ok {
			epoch, _ = strconv.Atoi(strings.TrimSpace(v))
			continue
		}
		lines = append(lines, l)
	}
	lines = append(lines, "epoch: "+strconv.Itoa(epoch+1))
	blob := oCaGit(t, r, nil, strings.Join(lines, "\n")+"\n", "hash-object", "-w", "--stdin")
	idx := []string{"GIT_INDEX_FILE=" + filepath.Join(t.TempDir(), "index")}
	oCaGit(t, r, idx, "", "read-tree", "main")
	oCaGit(t, r, idx, "", "update-index", "--cacheinfo", "100644,"+blob+",lore-store.yaml")
	tree := oCaGit(t, r, idx, "", "write-tree")
	who := []string{"GIT_AUTHOR_NAME=member", "GIT_AUTHOR_EMAIL=" + email, "GIT_COMMITTER_NAME=member", "GIT_COMMITTER_EMAIL=" + email}
	forged := oCaGit(t, r, who, "", "commit-tree", tree, "-m", "lore: snapshot of the store history")
	oCaGit(t, r, nil, "", "update-ref", "refs/heads/main", forged)
}

func TestStoryOCompactArchive(t *testing.T) {
	s := New(t, "O")
	team := s.Remote("github.com/" + oOwner + "/lore-team")
	platform := s.Seed(s.Remote("github.com/"+oOwner+"/platform"), func(dir string) { PlainRepo(t, dir) })
	web := s.Seed(s.Remote("github.com/"+oOwner+"/web"), func(dir string) { PlainRepo(t, dir) })
	ada := s.OwnerShaped(OwnerShape{Personal: oAdminPersonal, Org: oAdminOrg, OrgFolder: oOrg, Alias: oAlias, OrgOwners: []string{oOwner}})
	pc := ada.CloneTo(platform, ada.OrgDir("platform"))
	oSetupFlow(t, ada, pc.Dir, oSetup, "admin", oAdminOrg, oAlias,
		map[string]any{"org": oOrg, "store_url": team.SSHURL()}, []string{"~/code/" + oOrg + "/**"})
	// mo is a second admin, so both write the company folder (a member's
	// compaction is refused in the unit tests, TestForgedEpochIsRefused)
	ada.LoreIn(pc, "member", "add", "--email", oMemOrg.Email, "--name", oMemOrg.Name, "--role", "admin", "--yes").OK(t)
	ada.LoreIn(pc, "sync").OK(t)
	mo := s.OwnerShaped(OwnerShape{Personal: oMemPersonal, Org: oMemOrg, OrgFolder: oOrg, Alias: oAlias, OrgOwners: []string{oOwner}})
	mwc := mo.CloneTo(web, mo.OrgDir("web"))
	oJoinSteps(t, mo, mwc.Dir, oOrg, oMemOrg)
	oJoin(t, mo, mwc.Dir, oOrg, "git@"+oAlias+":"+oOwner+"/lore-team.git").OK(t)
	for i := range 5 {
		ada.Lore(pc.Dir, "memory", "add", "--body", "widget history note "+strconv.Itoa(i), "--to", oOrg+"/org").OK(t)
		ada.LoreIn(pc, "sync").OK(t)
	}
	mo.Lore(mwc.Dir, "sync", "--setup", oOrg).OK(t)

	s.Step("D-1003/behind-its-flag", func() {
		ada.Lore(pc.Dir, "store", "compact", "--store", oOrg, "--before", "30d", "--yes").Refused(t, "behind its flag", "compact_archive=on")
		ada.Lore(pc.Dir, "store", "set", "--store", oOrg, "--settings", "compact_archive=on").OK(t)
		ada.LoreIn(pc, "sync").OK(t) // the setting goes out before the org file push
		ada.SetEnv("LORE_COMPACT_V2", "1")
		// the org floor: a lore too old to verify an epoch must be read-only first
		ada.Lore(pc.Dir, "store", "compact", "--store", oOrg, "--before", "30d", "--yes").Refused(t, "min_lore_version is not set", "lore org config set --org "+oOrg+" --key min_lore_version --value 0.1.57")
		ada.Lore(pc.Dir, "org", "config", "set", "--org", oOrg, "--key", "min_lore_version", "--value", "0.1.57", "--push", "--yes").OK(t)
	})

	s.Step("D-1003/compact-archives-then-member-adopts", func() {
		s.AdvanceDays(40) // the history above is now older than --before 30d
		// mo's two writes wait for his next sync (push manual)
		mo.Lore(mwc.Dir, "memory", "add", "--body", "widget offline note one", "--to", oOrg+"/org").OK(t)
		mo.Lore(mwc.Dir, "memory", "add", "--body", "widget offline note two", "--to", oOrg+"/org").OK(t)
		ada.Lore(pc.Dir, "store", "compact", "--store", oOrg, "--before", "30d", "--yes").OK(t).
			Says(t, "archived as refs/lore-archive/epoch-0", "epoch 0 → 1")
		if out := oCaGit(t, team, nil, "", "for-each-ref", "refs/lore-archive/"); !strings.Contains(out, "refs/lore-archive/epoch-0") {
			t.Fatalf("[O D-1003] no archive ref on the team remote: %q", out)
		}
		mo.Lore(mwc.Dir, "sync", "--setup", oOrg).OK(t)
		mo.Lore(mwc.Dir, "--setup", oOrg, "store", "show", "--store", oOrg).OK(t).Says(t, "epoch     1")
		ada.LoreIn(pc, "sync").OK(t)
		ada.Lore(pc.Dir, "memory", "list", "--store", oOrg).OK(t).Says(t, "widget history note 0", "widget offline note one", "widget offline note two")
	})

	s.Step("D-1003/verify-against-the-archive", func() {
		mo.Lore(mwc.Dir, "--setup", oOrg, "store", "compact", "--store", oOrg, "--verify").OK(t).Says(t, "epoch 1 verified against its archive")
	})

	s.Step("D-1003/undo-puts-the-history-back", func() {
		ada.Lore(pc.Dir, "memory", "add", "--body", "widget note after the compaction", "--to", oOrg+"/org").OK(t)
		ada.LoreIn(pc, "sync").OK(t)
		ada.Lore(pc.Dir, "store", "compact", "--store", oOrg, "--undo-epoch", "0", "--yes").OK(t).Says(t, "undid the compaction", "epoch 1 → 2")
		mo.Lore(mwc.Dir, "sync", "--setup", oOrg).OK(t)
		mo.Lore(mwc.Dir, "--setup", oOrg, "store", "show", "--store", oOrg).OK(t).Says(t, "epoch     2")
		mo.Lore(mwc.Dir, "--setup", oOrg, "memory", "list", "--store", oOrg).OK(t).Says(t, "widget history note 4", "widget offline note two", "widget note after the compaction")
	})

	s.Step("D-1003/forged-epoch-refused", func() {
		before := oCaGit(t, team, nil, "", "rev-parse", "main")
		oCaForgeEpoch(t, team, oMemOrg.Email)
		ada.LoreIn(pc, "sync").Says(t, "cannot verify", "no compaction checkpoint", "lore store compact --store ", " --verify")
		ada.Lore(pc.Dir, "store", "show", "--store", oOrg).OK(t).Says(t, "epoch     2")
		ada.Lore(pc.Dir, "memory", "list", "--store", oOrg).OK(t).Says(t, "widget note after the compaction")
		oCaGit(t, team, nil, "", "update-ref", "refs/heads/main", before) // put the remote back for the story's end
	})
}
