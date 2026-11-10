package mergewatch

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/adapters/gitref"
	"github.com/thesatellite-ai/lore/logic/orgconf"
)

// TestSweep is the lazy check's loop: the repo filter (with and without an
// adapter), a found merge, a miss, a git failure kept per task, and a
// never-fetched target that ends the sweep silently.
func TestSweep(t *testing.T) {
	f := newFixture(t)
	sha := f.work(false)
	f.git(f.lead, "fetch", "-q", "origin", "work")
	f.git(f.lead, "merge", "-q", "--no-ff", "--no-edit", "FETCH_HEAD")
	f.git(f.lead, "push", "-q", "origin", "main")
	f.git(f.obs, "fetch", "-q", "origin")
	ctx := context.Background()
	cands := []Candidate{
		{TaskID: "tsk_here", DoneCommit: sha, Repo: "api"},
		{TaskID: "tsk_other", DoneCommit: sha, Repo: "web"},
		{TaskID: "tsk_unknown_repo", DoneCommit: sha},
		{TaskID: "tsk_miss", Repo: "api"},
		{TaskID: "tsk_bad", Repo: "api"},
	}
	// A git failure on one task's trailer search (tsk_bad), real git for
	// everything else.
	repo, err := gitref.Open(ctx, f.obs.Dir, failGit{bad: "tsk_bad"})
	if err != nil {
		t.Fatal(err)
	}
	ck := Checker{Repo: repo, Target: target}
	found, probs := ck.Sweep(ctx, "api", cands)
	if got := foundIDs(found); got != "tsk_here,tsk_unknown_repo" {
		t.Fatalf("found %s, want the merged tasks of this repo and of an unknown repo", got)
	}
	if found[0].Finding.Method != MethodAncestry {
		t.Fatalf("method %s", found[0].Finding.Method)
	}
	if len(probs) != 1 || probs[0].TaskID != "tsk_bad" || !strings.Contains(ProblemLine(probs[0]), "tsk_bad") {
		t.Fatalf("a failing check is one problem naming its task: %+v", probs)
	}
	// here unknown: every candidate is checked.
	if found, _ := ck.Sweep(ctx, "", cands); foundIDs(found) != "tsk_here,tsk_other,tsk_unknown_repo" {
		t.Fatalf("here unknown: %s", foundIDs(found))
	}
	// An adapter asks about every repo's tasks.
	ck.Adapter = &fakeAdapter{}
	if found, _ := ck.Sweep(ctx, "api", cands); foundIDs(found) != "tsk_here,tsk_other,tsk_unknown_repo" {
		t.Fatalf("with an adapter: %s", foundIDs(found))
	}
	// Nothing fetched: nothing found, no problem.
	none := Checker{Repo: f.obs.Repo, Target: Target("origin", "nope")}
	if found, probs := none.Sweep(ctx, "api", cands); found != nil || probs != nil {
		t.Fatalf("unfetched target: %v %v", found, probs)
	}
	if found, probs := ck.Sweep(ctx, "api", nil); found != nil || probs != nil {
		t.Fatalf("no candidates: %v %v", found, probs)
	}
}

// failGit fails the trailer search of the task bad and runs real git for
// every other call.
type failGit struct{ bad string }

func (g failGit) Run(ctx context.Context, inv gitref.Invocation) ([]byte, error) {
	if strings.Contains(strings.Join(inv.Args, " "), g.bad) {
		return nil, errors.New("git log: simulated failure")
	}
	return gitref.Exec{}.Run(ctx, inv)
}

func foundIDs(fs []Found) string {
	var ids []string
	for _, f := range fs {
		ids = append(ids, f.TaskID)
	}
	return strings.Join(ids, ",")
}

func TestTargetAndAdapterFor(t *testing.T) {
	if got := Target("origin", "main"); got != target {
		t.Fatalf("Target = %q", got)
	}
	cases := []struct {
		in   orgconf.MergeCheckAdapter
		want bool
	}{{orgconf.MergeCheckGitHub, true}, {orgconf.MergeCheckNone, false}, {"", false}, {"gitlab", false}}
	for _, c := range cases {
		a, ok := AdapterFor(c.in, "/r")
		if ok != c.want || (a != nil) != c.want {
			t.Fatalf("AdapterFor(%q) = %v %v", c.in, a, ok)
		}
		if ok && a.(GitHub).Dir != "/r" {
			t.Fatalf("dir not kept: %+v", a)
		}
	}
}

// TestLine pins the merged line, recorded and not (v0.1.17 reportMerged).
func TestLine(t *testing.T) {
	at := time.Date(2026, 11, 2, 11, 52, 0, 0, time.FixedZone("x", 3600))
	f := Found{TaskID: "tsk_1", Finding: Finding{Method: MethodTrailer, Commit: "9c3e1f0aa", At: at}}
	if got := Line(f, nil); got != "✓ tsk_1 merged 10:52 (found by: trailer, 9c3e1f0)" {
		t.Fatalf("recorded line %q", got)
	}
	got := Line(f, errors.New("push refused"))
	if want := "lore: tsk_1 merged 10:52 (found by: trailer, 9c3e1f0) but it could not be recorded: push refused; the next lore command retries"; got != want {
		t.Fatalf("unrecorded line %q", got)
	}
}

func TestMergedUpdatedAt(t *testing.T) {
	early := time.Date(2026, 1, 1, 0, 0, 0, 0, time.FixedZone("x", 7200))
	late := early.Add(time.Hour)
	cases := []struct{ old, merged, want time.Time }{
		{early, late, late.UTC()},
		{late, early, late.UTC()},
		{early, early, early.UTC()},
		{time.Time{}, early, early.UTC()},
	}
	for _, c := range cases {
		got := MergedUpdatedAt(c.old, c.merged)
		if !got.Equal(c.want) || got.Location() != time.UTC {
			t.Fatalf("MergedUpdatedAt(%v, %v) = %v, want %v UTC", c.old, c.merged, got, c.want)
		}
	}
}
