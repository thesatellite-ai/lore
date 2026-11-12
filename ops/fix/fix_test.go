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
	prepDry  bool
}

func (f *fakeEnv) RepoTop(context.Context) (string, error)    { return "", nil }
func (f *fakeEnv) FixRepos(context.Context) ([]string, error) { return f.roots, nil }
func (f *fakeEnv) FixProgress() io.Writer                     { return writerFunc(f.write) }
func (f *fakeEnv) FixPrepare(_ context.Context, dry bool, _ int) Prep {
	f.prepDry = dry
	return Prep{NoFetch: !dry, Fetched: 1}
}
func (f *fakeEnv) FixIn(_ context.Context, root string, dry bool, p Prep) RepoResult {
	f.mu.Lock()
	f.gotDry = append(f.gotDry, dry)
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
