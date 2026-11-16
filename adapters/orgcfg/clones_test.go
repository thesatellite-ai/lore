package orgcfg

// Ports of saas/cmd/cli failclosed_store_test.go (B11) to the clone
// inspector of cmd/lore's store show|doctor: a clone folder git cannot open
// is a problem (never "not cloned"); a never-cloned store is quiet; a ref
// listing that fails is reported by show and doctor (never "nothing
// unpushed", never "branch does not exist"). Plus the unpushed matrix.

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

const testNorm = "example.com/agency/notes"

// testGit runs git in dir with a fixed identity.
func testGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	full := append([]string{"-C", dir, "-c", "user.name=t", "-c", "user.email=t@example.com"}, args...)
	out, err := gitRun(context.Background(), full...)
	if err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
	return strings.TrimSpace(out)
}

// storeOrigin is a bare store repo whose main holds the marker.
func storeOrigin(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	origin := filepath.Join(root, "origin.git")
	testGit(t, root, "init", "-q", "--bare", "-b", "main", origin)
	seed := filepath.Join(root, "seed")
	testGit(t, root, "clone", "-q", origin, seed)
	if err := os.WriteFile(filepath.Join(seed, storeMarkerFile), []byte("version: 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	testGit(t, seed, "add", ".")
	testGit(t, seed, "commit", "-q", "-m", "init")
	testGit(t, seed, "push", "-q", "origin", "main")
	return origin
}

// failingRun fails every git call naming failArg, as a broken clone would.
func failingRun(failArg string) func(context.Context, ...string) (string, error) {
	return func(ctx context.Context, args ...string) (string, error) {
		if slices.Contains(args, failArg) {
			return "", errors.New("injected git failure: " + failArg)
		}
		return gitRun(ctx, args...)
	}
}

func hasPrefix(ps []string, prefix string) bool {
	for _, p := range ps {
		if strings.HasPrefix(p, prefix) {
			return true
		}
	}
	return false
}

func TestInspectNotClonedIsNotAProblem(t *testing.T) {
	info := Clones{Home: t.TempDir()}.Inspect(context.Background(), testNorm)
	if info.Cloned || len(info.Problems) != 0 {
		t.Fatalf("never cloned: want not cloned and no problems: %+v", info)
	}
}

func TestInspectReportsUnopenableClone(t *testing.T) {
	c := Clones{Home: t.TempDir()}
	dir := c.Dir(testNorm)
	for _, d := range []string{"objects", "refs"} {
		if err := os.MkdirAll(filepath.Join(dir, ".git", d), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, ".git", "HEAD"), []byte("garbage\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	info := c.Inspect(context.Background(), testNorm)
	if info.Cloned || !hasPrefix(info.Problems, ProblemOpen) {
		t.Fatalf("an unopenable clone must be a problem: %+v", info)
	}
}

func TestInspectAndDoctorReportUnpushedReadFailure(t *testing.T) {
	ctx := context.Background()
	origin := storeOrigin(t)
	c := Clones{Home: t.TempDir(), Run: failingRun("for-each-ref")}
	if d := (Clones{Home: c.Home}).Doctor(ctx, testNorm, origin, []string{"main"}); len(d.Problems) != 0 {
		t.Fatalf("fixture: a healthy store has problems: %v", d.Problems)
	}
	info := c.Inspect(ctx, testNorm)
	if !info.Cloned || !hasPrefix(info.Problems, ProblemUnpushedUnknown) {
		t.Fatalf("show must report the unpushed check it could not run: %+v", info)
	}
	d := c.Doctor(ctx, testNorm, origin, []string{"main"})
	if !hasPrefix(d.Problems, ProblemUnpushedUnknown) {
		t.Fatalf("doctor must report the unpushed check it could not run: %v", d.Problems)
	}
	if !hasPrefix(d.Problems, "branch main: ") || hasPrefix(d.Problems, "branch main does not exist") {
		t.Fatalf("doctor must report the failed branch check, not a missing branch: %v", d.Problems)
	}
}

// TestDoctorAndUnpushed: a healthy store passes; a missing branch, a local
// commit the store lacks and an unreachable store are each a problem; a
// local ref equal to or behind its tracking ref is not unpushed.
func TestDoctorAndUnpushed(t *testing.T) {
	ctx := context.Background()
	origin := storeOrigin(t)
	c := Clones{Home: t.TempDir()}
	if d := c.Doctor(ctx, testNorm, origin, []string{"main"}); len(d.Problems) != 0 || !d.Cloned || d.LastFetch.IsZero() {
		t.Fatalf("healthy store: %+v", d)
	}
	if d := c.Doctor(ctx, testNorm, origin, []string{"staging"}); !hasPrefix(d.Problems, "branch staging does not exist") {
		t.Fatalf("missing branch: %v", d.Problems)
	}
	dir := c.Dir(testNorm)
	track := testGit(t, dir, "rev-parse", "refs/remotes/origin/main")
	testGit(t, dir, "update-ref", "refs/heads/main", track) // equal: nothing waits
	if info := c.Inspect(ctx, testNorm); len(info.Unpushed) != 0 {
		t.Fatalf("equal ref listed as unpushed: %v", info.Unpushed)
	}
	tree := testGit(t, dir, "rev-parse", track+"^{tree}")
	ahead := testGit(t, dir, "commit-tree", "-p", track, "-m", "local", tree)
	testGit(t, dir, "update-ref", "refs/heads/main", ahead)
	testGit(t, dir, "update-ref", "refs/lore/live", ahead) // no tracking ref at all
	info := c.Inspect(ctx, testNorm)
	if !slices.Equal(info.Unpushed, []string{"refs/heads/main", "refs/lore/live"}) {
		t.Fatalf("unpushed = %v", info.Unpushed)
	}
	// devco D-483: waiting work is one warning per ref, naming how to
	// push or delete it, never a problem (doctor goes on and exits 0)
	testGit(t, dir, "update-ref", "refs/heads/backup-test", ahead) // a side branch only this clone has
	d := c.Doctor(ctx, testNorm, origin, []string{"main"})
	if hasPrefix(d.Problems, ProblemUnpushed) || len(d.Warnings) != 3 {
		t.Fatalf("unpushed work must be warnings, one per ref: problems %v warnings %v", d.Problems, d.Warnings)
	}
	wantSide := UnpushedWarning(dir, "refs/heads/backup-test", []string{"main"})
	if !slices.Contains(d.Warnings, wantSide) || !strings.Contains(wantSide, "push it with `git -C "+dir+" push origin backup-test`") ||
		!strings.Contains(wantSide, "delete it with `git -C "+dir+" branch -D backup-test`") {
		t.Fatalf("side branch warning %q not in %v", wantSide, d.Warnings)
	}
	if ref, ok := UnpushedRef(wantSide); !ok || ref != "refs/heads/backup-test" {
		t.Fatalf("UnpushedRef = %q %v", ref, ok)
	}
	// the store's own branch is lore's waiting work: never "delete it"
	if w := UnpushedWarning(dir, "refs/heads/main", []string{"main"}); !slices.Contains(d.Warnings, w) || strings.Contains(w, "branch -D") || !strings.Contains(w, "`lore sync`") {
		t.Fatalf("store branch warning %q (all %v)", w, d.Warnings)
	}
	testGit(t, dir, "update-ref", "-d", "refs/heads/backup-test")
	gone := filepath.Join(t.TempDir(), "gone.git")
	testGit(t, dir, "remote", "set-url", "origin", gone)
	if d := c.Doctor(ctx, testNorm, gone, []string{"main"}); !slices.Contains(d.Problems, ProblemUnreachable) {
		t.Fatalf("unreachable store: %v", d.Problems)
	}
}

// TestOpenClone: open returns the store's own clone (stores/<owner>/<repo>,
// files checked out, no worktree folder anywhere) for its default branch,
// and the pinned clone <repo>@<branch> for another branch; a missing
// branch is refused and makes nothing.
func TestOpenClone(t *testing.T) {
	ctx := context.Background()
	origin := storeOrigin(t)
	seed := filepath.Join(t.TempDir(), "seed")
	testGit(t, filepath.Dir(seed), "clone", "-q", origin, seed)
	testGit(t, seed, "push", "-q", "origin", "main:staging")
	c := Clones{Home: t.TempDir()}
	want := filepath.Join(c.Home, "stores", "agency", "notes")
	for range 2 {
		dir, commit, err := c.Open(ctx, testNorm, origin, "main")
		if err != nil {
			t.Fatal(err)
		}
		if dir != want || testGit(t, dir, "rev-parse", "HEAD") != commit {
			t.Fatalf("clone %s at %s, want %s", dir, commit, want)
		}
		if _, err = os.Stat(filepath.Join(dir, storeMarkerFile)); err != nil {
			t.Fatal(err)
		}
	}
	dir, commit, err := c.Open(ctx, testNorm, origin, "staging")
	if err != nil {
		t.Fatal(err)
	}
	if dir != want+"@staging" || testGit(t, dir, "symbolic-ref", "--short", "HEAD") != "staging" || testGit(t, dir, "rev-parse", "HEAD") != commit {
		t.Fatalf("staging clone %s at %s", dir, commit)
	}
	if _, _, err := c.Open(ctx, testNorm, origin, "nope"); err == nil {
		t.Fatal("a missing branch must be refused")
	}
	if _, err := os.Stat(want + "@nope"); err == nil {
		t.Fatal("a refused open made a clone")
	}
	if _, err := os.Stat(filepath.Join(c.Home, "worktrees")); err == nil {
		t.Fatal("store open made a worktrees folder")
	}
}
