//go:build journeys && !windows

package journeys

// storedoctor_warn_test.go — a branch only this machine's store clone
// holds (a backup someone made there) is a warning store doctor names,
// never the one line of the run (devco D-483). Before, `lore store
// doctor` stopped with E_INVALID_INPUT "1 store(s) need attention: …
// unpushed work: refs/heads/<branch>" and printed none of its checks, and
// `lore setup list` showed "⚠ 1" with nothing naming the warning.
//
//	git branch backup-test in the org store clone
//	store doctor ─▶ every store line, "⚠ … warn: unpushed work:
//	                refs/heads/backup-test" with push and delete commands,
//	                the summary; exit 0
//	setup list   ─▶ a ⚠ cell is followed by what it counts

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The side branch of the store-doctor journey (placeholder).
const sdwBranch = "backup-test"

func TestJourneyStoreDoctorWarnsSideBranch(t *testing.T) {
	t.Parallel()
	tt := newTLTeam(t)
	w := tt.w
	lee := tt.person(tlLee)
	app := tt.repo[tlLee.Email].Dir

	w.Step("day1/a-backup-branch-in-the-store-clone-is-a-warning", func() {
		tlStartTeam(t, lee, app, tlLeadSetup, tlOrg, tt.store.SSHURL(), tlLee, []string{tlFolders})
		var clone string
		_ = filepath.WalkDir(filepath.Join(lee.Home, ".lore", "stores"), func(p string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() || d.Name() != "lore-store.yaml" || clone != "" {
				return nil
			}
			if _, gerr := os.Stat(filepath.Join(filepath.Dir(p), ".git")); gerr == nil { // a git clone, not the personal folder store
				clone = filepath.Dir(p)
			}
			return nil
		})
		if clone == "" {
			t.Fatalf("no store clone under %s", filepath.Join(lee.Home, ".lore", "stores"))
		}
		lee.Lore(app, "store", "doctor").OK(t).Never(t, "warn:", "need attention")
		w.Git(clone, "branch", sdwBranch)
		lee.Lore(app, "store", "doctor").OK(t).
			Says(t, "stores this repo and machine use:", "⚠ ", "warn: unpushed work: refs/heads/"+sdwBranch,
				"push origin "+sdwBranch, "branch -D "+sdwBranch, "summary: ", "1 warn, 0 fail").
			Never(t, "E_INVALID_INPUT", "need attention")
		lee.Lore(app, "store", "doctor", "--json").OK(t).Says(t, `"warnings"`, `"summary"`, `"warn": 1`).Never(t, `"error"`)
	})

	w.Step("day1/setup-list-names-what-a-warning-cell-counts", func() {
		lee.Lore(app, "doctor", "--all")
		list := lee.Lore(app, "setup", "list").OK(t)
		if strings.Contains(list.Stdout, "⚠ ") && !strings.Contains(list.Stdout, "lore setup show --name ") {
			t.Fatalf("a ⚠ cell with nothing naming the warning:\n%s", list.Stdout)
		}
	})
}
