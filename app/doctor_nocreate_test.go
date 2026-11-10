package app

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// repoSnapshot lists every path under dir (relative, sorted) plus the
// local git config, so a test can say "nothing changed".
func repoSnapshot(t *testing.T, dir string) string {
	t.Helper()
	var paths []string
	err := filepath.WalkDir(dir, func(p string, _ os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, rerr := filepath.Rel(dir, p)
		if rerr != nil {
			return rerr
		}
		paths = append(paths, rel)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(paths)
	cfg, err := os.ReadFile(filepath.Join(dir, ".git", "config"))
	if err != nil {
		t.Fatal(err)
	}
	return strings.Join(paths, "\n") + "\n--- .git/config\n" + string(cfg)
}

// TestDoctorWritesNothingInARepoWithoutLore (ONB 6.1 "doctor only
// reads", 1.2): `lore doctor`, `--all` and `--all --json` in a git repo
// lore is not set up in leave every file, the git config and the hooks
// as they were (no .lore/, no .git/lore/, no .gitattributes), exit 0 and
// say once that the store checks were skipped. After `lore init` the
// store checks run again (the note is gone). PATH holds only git
// (hermeticPath), so the lore binaries installed on the machine running
// the test cannot add a PATH warning and fail the exit-0 check.
func TestDoctorWritesNothingInARepoWithoutLore(t *testing.T) {
	t.Parallel()
	w := newCLIWorld(t).person().unscoped().hermeticPath(t)
	dir := w.root
	w.git(t, dir, "init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("notes\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	before := repoSnapshot(t, dir)
	for _, args := range [][]string{{"doctor"}, {"doctor", "--all"}, {"doctor", "--all", "--json"}} {
		code, out, errOut := w.lore(t, dir, args...)
		if code != 0 {
			t.Fatalf("lore %v: exit %d\n%s\n%s", args, code, out, errOut)
		}
		if after := repoSnapshot(t, dir); after != before {
			t.Fatalf("lore %v changed the repo:\nbefore:\n%s\nafter:\n%s", args, before, after)
		}
		if strings.Contains(errOut, "context.targets") {
			t.Fatalf("lore %v warned about context.targets in a repo with no lore:\n%s", args, errOut)
		}
		if args[len(args)-1] == "doctor" && !strings.Contains(out, "store checks were skipped") {
			t.Fatalf("plain doctor did not say the store checks were skipped:\n%s", out)
		}
	}
	if code, out, errOut := w.lore(t, dir, "init"); code != 0 {
		t.Fatalf("lore init: exit %d\n%s\n%s", code, out, errOut)
	}
	if _, out, _ := w.lore(t, dir, "doctor"); strings.Contains(out, "store checks were skipped") {
		t.Fatalf("doctor skipped the store checks in an initialised repo:\n%s", out)
	}
}
