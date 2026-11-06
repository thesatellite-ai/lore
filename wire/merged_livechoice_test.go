package wire

// merged_livechoice_test.go — t-e2e6 (99e8ae9b): the merged stamp of a repo
// task (the lazy merged check and `lore task merged` by hand) asks the repo
// store for the live ref (store.LiveChoice on, table tasks), as v0.1.17's
// recordMergedLive did; every other write of the same task keeps the
// default routing. What the live backend does with that choice (live with a
// session, a branch write with LORE_LIVE off) is pinned in wire/host
// TestLiveBackend_MergedStampChoice.

import (
	"context"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/adapters/mergewatch"
	opscoord "github.com/thesatellite-ai/lore/ops/coord"
	opstask "github.com/thesatellite-ai/lore/ops/task"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// taskWrite is one task row the repo backend was asked to write, with the
// live choice its ctx carried.
type taskWrite struct {
	id     string
	merged bool
	mode   store.LiveMode
}

// choiceRecorder records every task row written to the repo store.
type choiceRecorder struct {
	store.Backend
	mu     *sync.Mutex
	writes *[]taskWrite
}

func (r choiceRecorder) Write(ctx context.Context, changes []store.Change) error {
	mode := store.LiveChoiceFrom(ctx).For(schema.TableTasks)
	r.mu.Lock()
	for _, ch := range changes {
		if string(ch.Row.Table) == schema.TableTasks {
			*r.writes = append(*r.writes, taskWrite{id: ch.Row.ID.String(), merged: strings.Contains(string(ch.Row.Doc), `"`+fieldMergedAt+`"`), mode: mode})
		}
	}
	r.mu.Unlock()
	return r.Backend.Write(ctx, changes)
}

// Unwrap implements Unwrapper.
func (r choiceRecorder) Unwrap() store.Backend { return r.Backend }

// TestMergedStampAsksForTheLiveRef: task add and task done keep the default
// routing; the stamp written by `task merged` and by the lazy check
// (stampMerged) carries LiveOn for tasks, and only the stamp does.
func TestMergedStampAsksForTheLiveRef(t *testing.T) {
	t.Parallel()
	var mu sync.Mutex
	var writes []taskWrite
	repo := gitRepo(t)
	// Coordination writes (task merged's fenced update) push the signals
	// ref, so the repo needs a reachable origin.
	bare := t.TempDir()
	for _, args := range [][]string{{"init", "-q", "--bare", bare}, {"-C", repo, "remote", "add", "origin", bare}} {
		if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	a := openWith(t, repo, Options{
		RepoIsClient: func(context.Context) (bool, error) { return false, nil },
		WrapBackend: func(_ context.Context, name store.Source, _ string, b store.Backend) store.Backend {
			if name != store.SourceRepo {
				return b
			}
			return choiceRecorder{Backend: b, mu: &mu, writes: &writes}
		},
	})
	withProject(t, a)
	doneTask := func(title string) string {
		t.Helper()
		id := idOfTask(t, a, title)
		if _, err := run(t, a, opstask.OpDone, `{"id":"`+id+`","no_lesson":true}`, true); err != nil {
			t.Fatal(err)
		}
		return id
	}
	since := func(from int) []taskWrite {
		mu.Lock()
		defer mu.Unlock()
		return append([]taskWrite(nil), writes[from:]...)
	}
	mark := func() int { mu.Lock(); defer mu.Unlock(); return len(writes) }

	byHand := doneTask("merged by hand")
	lazy := doneTask("merged, seen by the lazy check")
	for _, w := range since(0) {
		if w.mode != store.LiveDefault {
			t.Fatalf("task add/done of %s carried live choice %v; only the merged stamp asks for live", w.id, w.mode)
		}
	}

	from := mark()
	if _, err := run(t, a, opscoord.OpTaskMerged, `{"id":"`+byHand+`"}`, true); err != nil {
		t.Fatal(err)
	}
	checkStamp(t, "task merged", byHand, since(from))

	from = mark()
	recorded, err := standaloneCoord{a: a}.stampMerged(background(), mergewatch.Found{TaskID: lazy, Finding: mergewatch.Finding{At: time.Date(2026, 11, 6, 9, 0, 0, 0, time.UTC)}})
	if err != nil || !recorded {
		t.Fatalf("stampMerged = %v, %v; want recorded", recorded, err)
	}
	checkStamp(t, "lazy merged check", lazy, since(from))
}

// checkStamp: writes holds the merged stamp of id, and every task write in
// it carries LiveOn.
func checkStamp(t *testing.T, how, id string, writes []taskWrite) {
	t.Helper()
	stamped := false
	for _, w := range writes {
		if w.mode != store.LiveOn {
			t.Fatalf("%s: task write %s carried %v, want LiveOn", how, w.id, w.mode)
		}
		if w.id == id && w.merged {
			stamped = true
		}
	}
	if !stamped {
		t.Fatalf("%s: no merged_at write of %s reached the repo store: %+v", how, id, writes)
	}
}

// idOfTask adds a task and returns its id.
func idOfTask(t *testing.T, a *App, title string) string {
	t.Helper()
	out, err := run(t, a, opstask.OpAdd, `{"title":"`+title+`"}`, true)
	if err != nil {
		t.Fatal(err)
	}
	i := strings.Index(out, "tsk_")
	if i < 0 || len(out) < i+36 {
		t.Fatalf("task add output has no id: %s", out)
	}
	return out[i : i+36]
}
