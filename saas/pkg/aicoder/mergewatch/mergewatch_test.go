package mergewatch

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"saas/pkg/aicoder/gitremote/gitremotetest"
)

const target = "refs/remotes/origin/main"

type fixture struct {
	t    *testing.T
	w    *gitremotetest.World
	dev  *gitremotetest.Clone // makes the work
	lead *gitremotetest.Clone // merges it
	obs  *gitremotetest.Clone // checks
}

func newFixture(t *testing.T) *fixture {
	w := gitremotetest.NewWorld(t)
	return &fixture{t: t, w: w, dev: w.Clone("dev"), lead: w.Clone("lead"), obs: w.Clone("obs")}
}

func (f *fixture) git(c *gitremotetest.Clone, args ...string) string {
	return gitremotetest.Git(f.t, c.Dir, args...)
}

func (f *fixture) commit(c *gitremotetest.Clone, file, msg string) string {
	if err := os.WriteFile(filepath.Join(c.Dir, file), []byte(file+msg), 0o644); err != nil {
		f.t.Fatal(err)
	}
	f.git(c, "add", ".")
	f.git(c, "commit", "-q", "-m", msg)
	return f.git(c, "rev-parse", "HEAD")
}

// work makes one commit on branch work (with or without the trailer) and
// pushes it.
func (f *fixture) work(trailer bool) string {
	f.git(f.dev, "checkout", "-q", "-b", "work")
	msg := "invite mutation"
	if trailer {
		msg += "\n\nLore-Task: tsk_30"
	}
	sha := f.commit(f.dev, "api.go", msg)
	f.git(f.dev, "push", "-q", "origin", "work")
	return sha
}

func (f *fixture) check(cand Candidate, a Adapter) (Finding, bool, []Step) {
	f.git(f.obs, "fetch", "-q", "origin")
	ff, ok, steps, err := Checker{Repo: f.obs.Repo, Target: target, Adapter: a}.Check(context.Background(), cand)
	if err != nil {
		f.t.Fatal(err)
	}
	return ff, ok, steps
}

// TestMergeShapes is the F9 git fixture matrix: merge commit, fast-forward,
// squash with / without trailer, rebase, unmerged, reverted.
func TestMergeShapes(t *testing.T) {
	cases := []struct {
		name      string
		trailer   bool
		merge     func(f *fixture, sha string) string // returns the carrier commit
		useCommit bool
		found     bool
		method    Method
	}{
		{"merge commit, by ancestry", false, func(f *fixture, _ string) string {
			f.git(f.lead, "fetch", "-q", "origin", "work")
			f.git(f.lead, "merge", "-q", "--no-ff", "--no-edit", "FETCH_HEAD")
			f.git(f.lead, "push", "-q", "origin", "main")
			return f.git(f.lead, "rev-parse", "HEAD")
		}, true, true, MethodAncestry},
		{"fast-forward, by ancestry", false, func(f *fixture, sha string) string {
			f.git(f.lead, "fetch", "-q", "origin", "work")
			f.git(f.lead, "merge", "-q", "--ff-only", "FETCH_HEAD")
			f.git(f.lead, "push", "-q", "origin", "main")
			return sha
		}, true, true, MethodAncestry},
		{"squash keeping the trailer", true, squash(true), true, true, MethodTrailer},
		{"squash dropping the trailer", true, squash(false), true, false, ""},
		{"rebase keeping the trailer", true, func(f *fixture, _ string) string {
			f.commit(f.lead, "other.go", "unrelated on main")
			f.git(f.lead, "push", "-q", "origin", "main")
			f.git(f.lead, "fetch", "-q", "origin", "work")
			f.git(f.lead, "cherry-pick", "FETCH_HEAD")
			f.git(f.lead, "push", "-q", "origin", "main")
			return f.git(f.lead, "rev-parse", "HEAD")
		}, true, true, MethodTrailer},
		{"unmerged", true, func(*fixture, string) string { return "" }, true, false, ""},
		{"reverted after the merge stays merged", false, func(f *fixture, _ string) string {
			f.git(f.lead, "fetch", "-q", "origin", "work")
			f.git(f.lead, "merge", "-q", "--no-ff", "--no-edit", "FETCH_HEAD")
			carrier := f.git(f.lead, "rev-parse", "HEAD")
			f.git(f.lead, "revert", "--no-edit", "-m", "1", "HEAD")
			f.git(f.lead, "push", "-q", "origin", "main")
			return carrier
		}, true, true, MethodAncestry},
		{"no done_commit, trailer only", true, squash(true), false, true, MethodTrailer},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			sha := f.work(tc.trailer)
			carrier := tc.merge(f, sha)
			cand := Candidate{TaskID: "tsk_30"}
			if tc.useCommit {
				cand.DoneCommit = sha
			}
			got, ok, steps := f.check(cand, nil)
			if ok != tc.found {
				t.Fatalf("found=%v want %v; steps %+v", ok, tc.found, steps)
			}
			if !ok {
				return
			}
			if got.Method != tc.method || got.Commit != carrier {
				t.Fatalf("finding %+v, want method %s carrier %s; steps %+v", got, tc.method, carrier, steps)
			}
			want := f.git(f.obs, "show", "-s", "--format=%cI", carrier)
			if got.At.Format(time.RFC3339) != mustUTC(t, want) {
				t.Fatalf("merged_at must be the carrier's committer date %s, got %s", want, got.At)
			}
			// Deterministic: a second check finds the same value.
			again, _, _ := f.check(cand, nil)
			if again != got {
				t.Fatalf("two checks disagree: %+v vs %+v", got, again)
			}
		})
	}
}

func mustUTC(t *testing.T, s string) string {
	tm, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatal(err)
	}
	return tm.UTC().Format(time.RFC3339)
}

func squash(keep bool) func(f *fixture, _ string) string {
	return func(f *fixture, _ string) string {
		f.git(f.lead, "fetch", "-q", "origin", "work")
		f.git(f.lead, "merge", "-q", "--squash", "FETCH_HEAD")
		msg := "invite mutation (#412)"
		if keep {
			msg += "\n\nLore-Task: tsk_30"
		}
		f.git(f.lead, "commit", "-q", "-m", msg)
		f.git(f.lead, "push", "-q", "origin", "main")
		return f.git(f.lead, "rev-parse", "HEAD")
	}
}

// TestTrailerIsExact: a trailer for tsk_300 must not count for tsk_30.
func TestTrailerIsExact(t *testing.T) {
	f := newFixture(t)
	f.commit(f.lead, "x.go", "other\n\nLore-Task: tsk_300")
	f.commit(f.lead, "y.go", "mention Lore-Task: tsk_30 in prose")
	f.git(f.lead, "push", "-q", "origin", "main")
	if _, ok, steps := f.check(Candidate{TaskID: "tsk_30"}, nil); ok {
		t.Fatalf("only an exact trailer line counts: %+v", steps)
	}
}

type fakeAdapter struct {
	commit string
	ok     bool
	err    error
	calls  int
}

func (a *fakeAdapter) MergedPR(_ context.Context, n int) (string, time.Time, bool, error) {
	a.calls++
	return a.commit, time.Date(2026, 11, 2, 11, 52, 0, 0, time.UTC), a.ok, a.err
}

// TestAdapter: check 3 runs only after 1 and 2 miss, and only with a PR.
func TestAdapter(t *testing.T) {
	f := newFixture(t)
	f.work(false)
	a := &fakeAdapter{commit: "abc1234", ok: true}
	got, ok, _ := f.check(Candidate{TaskID: "tsk_30", DonePR: 412}, a)
	if !ok || got.Method != MethodAdapter || got.Commit != "abc1234" {
		t.Fatalf("adapter finding: %+v %v", got, ok)
	}
	a2 := &fakeAdapter{}
	if _, ok, _ := f.check(Candidate{TaskID: "tsk_30"}, a2); ok || a2.calls != 0 {
		t.Fatal("no PR → the adapter is not asked")
	}
	a3 := &fakeAdapter{err: errors.New("gh: not logged in")}
	if _, ok, steps := f.check(Candidate{TaskID: "tsk_30", DonePR: 1}, a3); ok || !strings.Contains(steps[len(steps)-1].Result, "not logged in") {
		t.Fatalf("an adapter error is a miss with a reason: %+v", steps)
	}
}

// TestGitHubAdapterDecodes pins the gh JSON shape.
func TestGitHubAdapterDecodes(t *testing.T) {
	cases := map[string]bool{
		`{"mergedAt":"2026-11-02T11:52:00Z","mergeCommit":{"oid":"9c3e1f0"}}`: true,
		`{"mergedAt":null,"mergeCommit":null}`:                                false,
	}
	for body, want := range cases {
		g := GitHub{Run: func(_ context.Context, _ string, name string, args ...string) ([]byte, error) {
			if name != "gh" || strings.Join(args, " ") != "pr view 412 --json mergedAt,mergeCommit" {
				t.Fatalf("unexpected call %s %v", name, args)
			}
			return []byte(body), nil
		}}
		_, _, ok, err := g.MergedPR(context.Background(), 412)
		if err != nil || ok != want {
			t.Fatalf("%s: ok=%v err=%v", body, ok, err)
		}
	}
}

// TestNoTarget: nothing fetched is an error, not a silent "not merged".
func TestNoTarget(t *testing.T) {
	f := newFixture(t)
	_, _, _, err := Checker{Repo: f.obs.Repo, Target: "refs/remotes/origin/nope"}.Check(context.Background(), Candidate{TaskID: "x"})
	if !errors.Is(err, ErrNoTarget) {
		t.Fatalf("want ErrNoTarget, got %v", err)
	}
}

// TestAddTrailer pins the commit-msg rewrite.
func TestAddTrailer(t *testing.T) {
	cases := []struct{ in, want string }{
		{"fix login\n", "fix login\n\nLore-Task: tsk_1\n"},
		{"feat: add x\n", "feat: add x\n\nLore-Task: tsk_1\n"},
		{"fix login\n\nlong body\n", "fix login\n\nlong body\n\nLore-Task: tsk_1\n"},
		{"fix\n\nbody\n\nSigned-off-by: A <a@b>\n", "fix\n\nbody\n\nSigned-off-by: A <a@b>\nLore-Task: tsk_1\n"},
		{"fix\n\nLore-Task: tsk_1\n", "fix\n\nLore-Task: tsk_1\n"},
		{"fix\n# Please enter the commit message\n# lines starting with #\n", "fix\n\nLore-Task: tsk_1\n# Please enter the commit message\n# lines starting with #\n"},
		{"", ""},
		{"\n# only comments\n", "\n# only comments\n"},
	}
	for _, tc := range cases {
		if got := string(AddTrailer([]byte(tc.in), "tsk_1")); got != tc.want {
			t.Fatalf("AddTrailer(%q) = %q, want %q", tc.in, got, tc.want)
		}
		// Idempotent.
		once := AddTrailer([]byte(tc.in), "tsk_1")
		if twice := AddTrailer(once, "tsk_1"); string(twice) != string(once) {
			t.Fatalf("not idempotent for %q", tc.in)
		}
	}
}
