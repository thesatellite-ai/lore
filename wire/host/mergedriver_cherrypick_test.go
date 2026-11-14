package host

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// cpGit runs git in dir with a fixed made-up identity; it fails the test
// on an error unless mayFail (a merge that stops on a conflict).
func cpGit(t *testing.T, dir string, mayFail bool, args ...string) {
	t.Helper()
	cmd := exec.Command(gitProgram, append([]string{"-C", dir, "-c", "user.name=Ann Able", "-c", "user.email=ann@example.test", "-c", "commit.gpgsign=false", "-c", "init.defaultBranch=main"}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1")
	if out, err := cmd.CombinedOutput(); err != nil && !mayFail {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
}

// cherryPickRepo builds the Q-6 history with real git: main has no copy
// of the row; feat adds it (the original row); release is cut from main
// and cherry-picks feat's commit (the same blob, no merge-base copy);
// each branch then commits its own edit and main takes feat. It returns
// the repo and release's tip: the other side of the merge the driver is
// then run for. No MERGE_HEAD is left: git writes it only after the
// drivers ran.
func cherryPickRepo(t *testing.T, rowPath string, original, onRelease, onFeat []byte) (dir, release string) {
	t.Helper()
	dir = t.TempDir()
	write := func(b []byte) {
		p := filepath.Join(dir, filepath.FromSlash(rowPath))
		if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, b, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	cpGit(t, dir, false, "init", "-q")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("app\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cpGit(t, dir, false, "add", "-A")
	cpGit(t, dir, false, "commit", "-q", "-m", "init")
	cpGit(t, dir, false, "checkout", "-q", "-b", "feat")
	write(original)
	cpGit(t, dir, false, "add", "-A")
	cpGit(t, dir, false, "commit", "-q", "-m", "feat: add the row")
	cpGit(t, dir, false, "checkout", "-q", "-b", "release", "main")
	cpGit(t, dir, false, "cherry-pick", "feat")
	write(onRelease)
	cpGit(t, dir, false, "commit", "-q", "-am", "release edit")
	cpGit(t, dir, false, "checkout", "-q", "feat")
	write(onFeat)
	cpGit(t, dir, false, "commit", "-q", "-am", "feat edit")
	cpGit(t, dir, false, "checkout", "-q", "main")
	cpGit(t, dir, false, "merge", "-q", "feat")
	out, err := exec.Command(gitProgram, "-C", dir, "rev-parse", "release").Output()
	if err != nil {
		t.Fatal(err)
	}
	return dir, strings.TrimSpace(string(out))
}

// TestMergeDriverCherryPickKeepsBothFields (devco Q-6): a row cherry-picked
// onto a release branch, edited there in one field and on the source
// branch in another, lost the release edit when both were merged: git
// gives no base (add/add), so the newer side's stale copy of the field it
// never touched won. The driver now takes the copy both histories share as
// the base: both edits are kept, and only a clash on the same field keeps
// the newer edit and names the replaced value.
//
// git merge names the other side in GITHEAD_<sha>; a cherry-pick or a
// rebase names none, so the driver finds the commit holding theirs.
func TestMergeDriverCherryPickKeepsBothFields(t *testing.T) {
	for _, viaEnv := range []bool{true, false} {
		t.Run(map[bool]string{true: "git merge", false: "cherry-pick or rebase"}[viaEnv], func(t *testing.T) {
			testCherryPickMerge(t, viaEnv)
		})
	}
}

func testCherryPickMerge(t *testing.T, viaEnv bool) {
	const rowPath = ".lore/data/memories/mem_01936f3300007d4e8f5a6b7c8d9e0f1a.json"
	original := memRow(t, 0, mdActorA, "core", "budget 110 kB")
	enter := func(t *testing.T, dir, release string) {
		t.Helper()
		t.Chdir(dir)
		if viaEnv {
			t.Setenv(gitHeadEnv+release, "release")
		}
	}

	t.Run("different fields: both kept", func(t *testing.T) {
		onRelease := memRow(t, 5, mdActorA, "core", "budget 105 kB (release)")
		onFeat := memRow(t, 6, mdActorB, "episodic", "budget 110 kB") // newer, body untouched
		dir, release := cherryPickRepo(t, rowPath, original, onRelease, onFeat)
		enter(t, dir, release)
		out, stderr, err := runDriver(t, rowPath, nil, onFeat, onRelease)
		if err != nil {
			t.Fatalf("merge must be clean: %v (%s)", err, stderr)
		}
		kind, body := decodeMem(t, out)
		if kind != "episodic" || body != "budget 105 kB (release)" {
			t.Fatalf("kind=%q body=%q: want feat's kind AND release's body (stderr %q)", kind, body, stderr)
		}
		if strings.Contains(stderr, "replaced") {
			t.Fatalf("no field clashed, nothing may be named replaced: %q", stderr)
		}
	})

	t.Run("same field: newer wins and the replaced value is named", func(t *testing.T) {
		onRelease := memRow(t, 5, mdActorA, "core", "budget 105 kB (release)")
		onFeat := memRow(t, 6, mdActorB, "core", "budget 100 kB")
		dir, release := cherryPickRepo(t, rowPath, original, onRelease, onFeat)
		enter(t, dir, release)
		out, stderr, err := runDriver(t, rowPath, nil, onFeat, onRelease)
		if err != nil {
			t.Fatalf("merge must be clean: %v (%s)", err, stderr)
		}
		if _, body := decodeMem(t, out); body != "budget 100 kB" {
			t.Fatalf("body=%q, want the newer edit", body)
		}
		if !strings.Contains(stderr, `replaced body "budget 105 kB (release)"`) {
			t.Fatalf("stderr must name the replaced value: %q", stderr)
		}
	})
}
