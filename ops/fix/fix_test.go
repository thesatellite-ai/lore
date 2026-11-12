package fix

import (
	"context"
	"fmt"
	"io"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/op"
	"github.com/thesatellite-ai/lore/ops/doctor"
)

// TestTextOneRepo pins the one-repo text: each step on its line, the
// "Everything is current." line when no step changed, the status last.
func TestTextOneRepo(t *testing.T) {
	cur := Out{Repo: "/w/app", Status: doctor.StatusHealthy, Steps: []doctor.FixStep{
		{Step: doctor.StepMigrate, Where: "/w/app/.lore/data", Result: doctor.ResultCurrent},
		{Step: doctor.StepDoctorFix, Result: doctor.ResultCurrent},
	}}
	txt := cur.Text()
	for _, want := range []string{"lore fix: /w/app\n", "migrate (/w/app/.lore/data): current", "doctor fixes: current", LineCurrent, "Status: HEALTHY\n"} {
		if !strings.Contains(txt, want) {
			t.Fatalf("missing %q in\n%s", want, txt)
		}
	}
	if !strings.HasSuffix(txt, "Status: HEALTHY\n") {
		t.Fatalf("status is not last:\n%s", txt)
	}
	changed := cur
	changed.Steps = []doctor.FixStep{{Step: doctor.StepMigrate, Result: "Upgraded 2 rows", Changed: true}}
	if strings.Contains(changed.Text(), LineCurrent) {
		t.Fatalf("a run that changed something says current:\n%s", changed.Text())
	}
	dry := changed
	dry.DryRun = true
	if !strings.Contains(dry.Text(), LineDryRun) {
		t.Fatalf("dry run text:\n%s", dry.Text())
	}
}

// TestAllNamesFailures pins --all: one block per repo, the failed repos
// named on the last line, and a broken repo or failed step counted.
func TestAllNamesFailures(t *testing.T) {
	repos := []RepoResult{
		{Repo: "/w/a", Status: doctor.StatusHealthy, Text: "lore fix: /w/a\nStatus: HEALTHY\n"},
		{Repo: "/w/b", Error: "lore fix exited 2 without a result", Exit: 2},
		{Repo: "/w/c", Status: doctor.StatusDegraded, Exit: 1, Steps: []doctor.FixStep{{Step: doctor.StepRender, Failed: true}}},
	}
	out := Out{Repos: repos, Failed: []string{"/w/b", "/w/c"}, Exit: 1}
	txt := out.Text()
	for _, want := range []string{"== /w/a\n", "== /w/b\n  FAILED: lore fix exited 2", "== /w/c\n", "failed: /w/b, /w/c"} {
		if !strings.Contains(txt, want) {
			t.Fatalf("missing %q in\n%s", want, txt)
		}
	}
	if repos[0].Failed() || !repos[1].Failed() || !repos[2].Failed() {
		t.Fatal("Failed() misclassifies")
	}
	if (RepoResult{Repo: "/w/d", Status: doctor.StatusDegraded, Exit: 1}).Failed() {
		t.Fatal("a degraded repo whose steps ran is not a failure")
	}
}

// TestRunAllStreamsAsReposFinish pins owner bug 14's streaming: a fast
// repo's block is emitted while a slow repo is still running (the slow
// run waits until the fast block is out), blocks are emitted one at a
// time, and the results come back in roots' order.
func TestRunAllStreamsAsReposFinish(t *testing.T) {
	fastOut := make(chan struct{})
	var emitted []string
	run := func(ctx context.Context, root string) RepoResult {
		if root == "/w/slow" {
			select {
			case <-fastOut:
			case <-time.After(5 * time.Second):
				return RepoResult{Error: "the fast block was not emitted while this repo ran"}
			}
		}
		return RepoResult{Status: doctor.StatusHealthy, Text: "Status: HEALTHY\n"}
	}
	emit := func(r RepoResult) {
		emitted = append(emitted, r.Repo)
		if r.Repo == "/w/fast" {
			close(fastOut)
		}
	}
	got := RunAll(context.Background(), []string{"/w/slow", "/w/fast"}, 2, run, emit)
	if len(emitted) != 2 || emitted[0] != "/w/fast" || emitted[1] != "/w/slow" {
		t.Fatalf("emitted %v, want the fast repo first", emitted)
	}
	if got[0].Repo != "/w/slow" || got[1].Repo != "/w/fast" || got[0].Error != "" {
		t.Fatalf("results %+v", got)
	}
	if got[0].WallMS <= 0 {
		t.Fatal("no wall time on a block")
	}
}

// TestRunAllRespectsJobs: never more than jobs repos run at once, and
// jobs 1 runs them one after another.
func TestRunAllRespectsJobs(t *testing.T) {
	for _, jobs := range []int{1, 3, 8} {
		var running, peak atomic.Int32
		roots := make([]string, 20)
		for i := range roots {
			roots[i] = fmt.Sprintf("/w/r%02d", i)
		}
		RunAll(context.Background(), roots, jobs, func(ctx context.Context, root string) RepoResult {
			n := running.Add(1)
			for {
				p := peak.Load()
				if n <= p || peak.CompareAndSwap(p, n) {
					break
				}
			}
			time.Sleep(5 * time.Millisecond)
			running.Add(-1)
			return RepoResult{}
		}, nil)
		if p := peak.Load(); p > int32(jobs) || (jobs > 1 && p < 2) {
			t.Fatalf("jobs %d: peak %d at once", jobs, p)
		}
	}
}

// fakeEnv is an Env for --all: FixIn sleeps per repo, FixProgress writes
// to a buffer.
type fakeEnv struct {
	doctor.Env
	roots    []string
	progress strings.Builder
	mu       sync.Mutex
	gotDry   []bool
	gotYes   []bool
	prepDry  bool
	skipped  []Skip
	sweep    []string
	sweeps   int
}

func (f *fakeEnv) RepoTop(context.Context) (string, error) { return "", nil }
func (f *fakeEnv) FixRepos(context.Context) (Found, error) {
	return Found{Roots: f.roots, Skipped: f.skipped}, nil
}
func (f *fakeEnv) FixProgress() io.Writer { return writerFunc(f.write) }
func (f *fakeEnv) FixCacheSweep(context.Context) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sweeps++
	return f.sweep
}
func (f *fakeEnv) FixPrepare(_ context.Context, dry bool, _ int) Prep {
	f.prepDry = dry
	return Prep{NoFetch: !dry, Fetched: 1}
}
func (f *fakeEnv) FixIn(_ context.Context, root string, dry bool, p Prep) RepoResult {
	f.mu.Lock()
	f.gotDry = append(f.gotDry, dry)
	f.gotYes = append(f.gotYes, p.Yes)
	f.mu.Unlock()
	if strings.HasSuffix(root, "bad") {
		return RepoResult{Repo: root, Error: "lore fix exited 2: panic: boom", Exit: 2}
	}
	return RepoResult{Repo: root, Status: doctor.StatusHealthy, Text: "lore fix: " + root + "\nStatus: HEALTHY\n"}
}
func (f *fakeEnv) write(p []byte) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.progress.Write(p)
}

type writerFunc func([]byte) (int, error)

func (w writerFunc) Write(p []byte) (int, error) { return w(p) }

// TestAllStreamsAndSummarises: --all prints every block on the progress
// writer (a failed repo with its exact error), the final text is the
// summary line (counts, the failed repo, wall time, jobs), the dry flag
// reaches every repo, and a failure exits 1.
func TestAllStreamsAndSummarises(t *testing.T) {
	env := &fakeEnv{roots: []string{"/w/a", "/w/bad", "/w/c"}}
	out, err := Run(context.Background(), env, RunIn{All: true, DryRun: true, Jobs: 2})
	if err != nil {
		t.Fatal(err)
	}
	prog := env.progress.String()
	for _, want := range []string{"== /w/a\n", "== /w/bad\n  FAILED: lore fix exited 2: panic: boom", "== /w/c\n"} {
		if !strings.Contains(prog, want) {
			t.Fatalf("progress lacks %q:\n%s", want, prog)
		}
	}
	txt := out.Text()
	if strings.Contains(txt, "== ") || !strings.Contains(txt, "3 repo(s), 2 ok, 1 failed: /w/bad") || !strings.Contains(txt, "2 at a time") || !strings.Contains(txt, "wall") {
		t.Fatalf("final text:\n%s", txt)
	}
	if out.Exit != 1 || !env.prepDry || len(env.gotDry) != 3 || slices.Contains(env.gotDry, false) {
		t.Fatalf("exit %d prepDry %v dry %v", out.Exit, env.prepDry, env.gotDry)
	}
	def, _ := Run(context.Background(), &fakeEnv{roots: []string{"/w/a"}}, RunIn{All: true})
	if def.Jobs != DefaultJobs {
		t.Fatalf("default jobs %d", def.Jobs)
	}
}

// TestAllPassesYesAndNotesSkipped (owner items 24, 26): lore fix --all
// --yes hands the confirmation to every repo's run (Prep.Yes), without it
// none gets it, and the folders FixRepos passed over (not git repos) are
// noted in the text and the JSON, never counted as failed.
func TestAllPassesYesAndNotesSkipped(t *testing.T) {
	for _, yes := range []bool{true, false} {
		env := &fakeEnv{roots: []string{"/w/a", "/w/c"}, skipped: []Skip{{Path: "/home/p", Why: "has a .lore folder but is not a git repo"}}}
		ctx := op.WithConfirmed(context.Background(), yes)
		out, err := Run(ctx, env, RunIn{All: true, Jobs: 2})
		if err != nil {
			t.Fatal(err)
		}
		if len(env.gotYes) != 2 || slices.Contains(env.gotYes, !yes) {
			t.Fatalf("yes %v: the repos got %v", yes, env.gotYes)
		}
		txt := out.Text()
		if out.Exit != 0 || len(out.Failed) != 0 || !strings.Contains(txt, "skipped /home/p: has a .lore folder but is not a git repo\n") ||
			!strings.Contains(txt, "2 repo(s), 2 ok, 0 failed (none failed); 1 folder(s) skipped") {
			t.Fatalf("exit %d failed %v text:\n%s", out.Exit, out.Failed, txt)
		}
		if len(out.Skipped) != 1 || out.Skipped[0].Path != "/home/p" {
			t.Fatalf("skipped %+v", out.Skipped)
		}
	}
}

// TestAllSweepsCachesOnce (owner item 35 follow-up): the machine-wide cache
// sweep runs once, in the --all parent (each repo's own run checks only its
// own caches); a damaged cache it finds is an error line and exits 1.
func TestAllSweepsCachesOnce(t *testing.T) {
	env := &fakeEnv{roots: []string{"/w/a", "/w/c"}}
	out, err := Run(context.Background(), env, RunIn{All: true, Jobs: 2})
	if err != nil || env.sweeps != 1 || out.Exit != 0 || len(out.Errors) != 0 {
		t.Fatalf("sound caches: sweeps %d exit %d errors %v err %v", env.sweeps, out.Exit, out.Errors, err)
	}
	line := "cache /h/.lore/cache/repo/x-0123456789ab.db is damaged; delete it"
	env = &fakeEnv{roots: []string{"/w/a"}, sweep: []string{line}}
	out, err = Run(context.Background(), env, RunIn{All: true})
	if err != nil || env.sweeps != 1 || out.Exit != 1 || !strings.Contains(out.Text(), "error: "+line+"\n") ||
		!strings.Contains(out.Text(), "1 cache error(s)") {
		t.Fatalf("damaged cache: sweeps %d exit %d text:\n%s", env.sweeps, out.Exit, out.Text())
	}
}

// retryEnv is fakeEnv whose repo "/w/shared" leaves a repair that could
// not fix on its first run (another repo held the store it writes) and
// fixes it on the next.
type retryEnv struct {
	fakeEnv
	calls map[string][]Prep
}

func (f *retryEnv) FixIn(_ context.Context, root string, _ bool, p Prep) RepoResult {
	f.mu.Lock()
	f.calls[root] = append(f.calls[root], p)
	n := len(f.calls[root])
	f.mu.Unlock()
	if root == "/w/shared" && n == 1 {
		return RepoResult{Repo: root, Status: doctor.StatusDegraded, Exit: 1, Text: "lore fix: " + root + "\nStatus: DEGRADED\n",
			Steps: []doctor.FixStep{{Step: doctor.StepDoctorFix, Result: doctor.ResultCurrent,
				Detail: []string{doctor.NotFixedPrefix + "actor_stable_key: not merged act_a into act_b: the store lock is held"}}}}
	}
	return RepoResult{Repo: root, Status: doctor.StatusHealthy, Text: "lore fix: " + root + "\nStatus: HEALTHY\n",
		Steps: []doctor.FixStep{{Step: doctor.StepDoctorFix, Result: "fixed 1", Changed: true}}}
}

// TestAllRetriesFailedRepairAlone (owner item 38): under lore fix --all
// --yes a repo whose doctor fixes step names a repair that could not fix
// (the shared-key merge, beside other repos writing the same store) runs
// once more after the others, alone, still confirmed and fetching its own
// store clones; its second block replaces the first and says so. A dry
// run and --jobs 1 never run a repo twice.
func TestAllRetriesFailedRepairAlone(t *testing.T) {
	env := &retryEnv{fakeEnv: fakeEnv{roots: []string{"/w/a", "/w/shared", "/w/c"}}, calls: map[string][]Prep{}}
	out, err := Run(op.WithConfirmed(context.Background(), true), env, RunIn{All: true, Jobs: 3})
	if err != nil {
		t.Fatal(err)
	}
	if got := env.calls["/w/shared"]; len(got) != 2 || !got[1].Yes || got[1].NoFetch || !got[0].NoFetch {
		t.Fatalf("/w/shared runs %+v; want two, the second confirmed and fetching for itself", got)
	}
	if len(env.calls["/w/a"]) != 1 || len(env.calls["/w/c"]) != 1 {
		t.Fatalf("other repos ran again: %+v", env.calls)
	}
	var shared RepoResult
	for _, r := range out.Repos {
		if r.Repo == "/w/shared" {
			shared = r
		}
	}
	if !shared.Retried || shared.Status != doctor.StatusHealthy || out.OK != 3 || out.Exit != 0 {
		t.Fatalf("result %+v ok %d exit %d; want the second run's healthy block", shared, out.OK, out.Exit)
	}
	if !strings.Contains(env.progress.String(), "== /w/shared"+retriedHeader+"\n") {
		t.Fatalf("progress does not mark the second run:\n%s", env.progress.String())
	}
	for _, in := range []RunIn{{All: true, Jobs: 3, DryRun: true}, {All: true, Jobs: 1}} {
		env := &retryEnv{fakeEnv: fakeEnv{roots: []string{"/w/shared"}}, calls: map[string][]Prep{}}
		if _, err := Run(context.Background(), env, in); err != nil {
			t.Fatal(err)
		}
		if n := len(env.calls["/w/shared"]); n != 1 {
			t.Fatalf("%+v ran the repo %d times", in, n)
		}
	}
}

// raceEnv is fakeEnv whose repos fail their first run beside the others
// in the ways a lost race shows (a store lock held during a doctor
// repair, a step failing on E_STORE_LOCKED, the whole run failing on
// E_DB_LOCKED), and whose "/w/stuck" repair fails alone too.
type raceEnv struct {
	fakeEnv
	calls map[string]int
}

func (f *raceEnv) FixIn(_ context.Context, root string, _ bool, _ Prep) RepoResult {
	f.mu.Lock()
	f.calls[root]++
	n := f.calls[root]
	f.mu.Unlock()
	notFixed := []doctor.FixStep{{Step: doctor.StepDoctorFix, Result: doctor.ResultCurrent,
		Detail: []string{doctor.NotFixedPrefix + "actor_stable_key: not merged act_a into act_b: the store lock is held"}}}
	switch {
	case root == "/w/stuck":
		return RepoResult{Repo: root, Status: doctor.StatusDegraded, Exit: 1, Text: "lore fix: " + root + "\nStatus: DEGRADED\n", Steps: notFixed}
	case n > 1:
		return RepoResult{Repo: root, Status: doctor.StatusHealthy, Text: "lore fix: " + root + "\nStatus: HEALTHY\n"}
	case root == "/w/repair":
		return RepoResult{Repo: root, Status: doctor.StatusDegraded, Exit: 1, Text: "lore fix: " + root + "\nStatus: DEGRADED\n", Steps: notFixed}
	case root == "/w/step":
		return RepoResult{Repo: root, Status: doctor.StatusDegraded, Exit: 1, Text: "lore fix: " + root + "\nStatus: DEGRADED\n",
			Steps: []doctor.FixStep{{Step: doctor.StepMigrate, Failed: true, Result: "[E_STORE_LOCKED] the store lock is held by another lore process"}}}
	case root == "/w/run":
		return RepoResult{Repo: root, Error: "lore fix exited 2: [E_DB_LOCKED] another lore process is holding the DB write lock", Exit: 2}
	case root == "/w/slow":
		return RepoResult{Repo: root, Error: "lore fix did not finish in 10m0s and was stopped", Exit: 2}
	}
	return RepoResult{Repo: root, Status: doctor.StatusHealthy, Text: "lore fix: " + root + "\nStatus: HEALTHY\n"}
}

// TestAllRetriesEveryLostRaceAlone (owner item 44): every repo whose first
// run under --all lost a race with the others (a repair not finished, a
// step or the whole run failing on a lock) runs once more alone after
// them, and its second block replaces the first in the result; the header
// says the second run happened (no instruction left to follow), or that
// the repair is still unfinished alone. A run that timed out is not run
// again, and a repo that was fine runs once.
func TestAllRetriesEveryLostRaceAlone(t *testing.T) {
	env := &raceEnv{fakeEnv: fakeEnv{roots: []string{"/w/a", "/w/repair", "/w/run", "/w/slow", "/w/step", "/w/stuck"}}, calls: map[string]int{}}
	out, err := Run(context.Background(), env, RunIn{All: true, Jobs: 4})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]int{"/w/a": 1, "/w/repair": 2, "/w/run": 2, "/w/slow": 1, "/w/step": 2, "/w/stuck": 2}
	for root, n := range want {
		if env.calls[root] != n {
			t.Fatalf("%s ran %d times, want %d (all: %v)", root, env.calls[root], n, env.calls)
		}
	}
	prog := env.progress.String()
	for _, root := range []string{"/w/repair", "/w/run", "/w/step"} {
		if !strings.Contains(prog, "== "+root+retriedHeader+"\n") {
			t.Fatalf("%s: no second-run header in\n%s", root, prog)
		}
	}
	if !strings.Contains(prog, "== /w/stuck"+retriedStillHeader+"\n") {
		t.Fatalf("/w/stuck: no still-unfinished header in\n%s", prog)
	}
	if strings.Contains(prog, "run again alone") {
		t.Fatalf("a header still reads as an instruction:\n%s", prog)
	}
	for _, r := range out.Repos {
		if want[r.Repo] == 2 && !r.Retried {
			t.Fatalf("%s: the result keeps the first block", r.Repo)
		}
	}
	if !slices.Equal(out.Failed, []string{"/w/slow"}) || out.OK != 5 {
		t.Fatalf("failed %v ok %d; want only the timed-out repo failed", out.Failed, out.OK)
	}
}
