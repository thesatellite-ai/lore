package wire

// coord_standalone_merged_test.go — F9's merged stamp on cmd/lore
// (coord_standalone_merged.go): ports of saas/cmd/cli merged_record_test.go
// (TestRecordMergedBranch_TwoClonesWriteTheSameRow,
// TestStampMerged_DecidesOnTheFreshRow) and failclosed_coord_test.go
// (TestRecordMergedReportsFailures) onto stampMergedRow and
// mergewatch.Line (later.md 211, 632).

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/adapters/mergewatch"
	"github.com/thesatellite-ai/lore/adapters/signals"
	"github.com/thesatellite-ai/lore/adapters/signals/coord"
	"github.com/thesatellite-ai/lore/logic/merge"
	"github.com/thesatellite-ai/lore/logic/orgresolve"
	"github.com/thesatellite-ai/lore/model/errs"
	opstask "github.com/thesatellite-ai/lore/ops/task"
	"github.com/thesatellite-ai/lore/schema"
)

// seqRows returns one row version per Get (the last one repeats; a nil
// version is "no such row"), counts stagings, and fails Stage with err.
type seqRows struct {
	docs   []coord.Doc
	gets   int
	staged []coord.Doc
	err    error
}

func (r *seqRows) Get(context.Context, string) (coord.Doc, bool, error) {
	d := r.docs[min(r.gets, len(r.docs)-1)]
	r.gets++
	return d.Clone(), d != nil, nil
}

func (r *seqRows) Stage(_ context.Context, rows ...coord.RowWrite) ([]signals.Companion, error) {
	if r.err != nil {
		return nil, r.err
	}
	for _, w := range rows {
		r.staged = append(r.staged, w.Doc)
	}
	return nil, nil
}

// racingSignals runs the decision `rounds` times, as signals.Store does
// when the push loses a race and it decides again on fresh state; err
// fails the write after the last decision, and wrote records a success.
type racingSignals struct {
	rounds int
	err    error
	wrote  bool
}

func (s *racingSignals) Write(_ context.Context, decide signals.Decide) (signals.Result, error) {
	for i := 0; i < s.rounds; i++ {
		if _, err := decide(signals.View{}); err != nil {
			return signals.Result{}, err
		}
	}
	s.wrote = s.err == nil
	return signals.Result{}, s.err
}

// TestStampMerged_DecidesOnTheFreshRow: the stamp is decided inside the
// signals write, on a fresh read. A row someone else already stamped, or
// stamped while we lost a race, is not written again and is not reported
// as recorded by us; only merged_at changes (updated_at is kept, so two
// finders stage the same bytes).
func TestStampMerged_DecidesOnTheFreshRow(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 11, 3, 10, 41, 0, 0, time.UTC)
	open := coord.Doc{}
	open.SetStr("id", "tsk_1")
	open.SetStr("status", schema.TaskStatusDone)
	open.SetStr("updated_at", "2026-11-03T10:30:00.000000000Z")
	stamped := open.Clone()
	stamped.SetTime(fieldMergedAt, at)
	reopened := open.Clone()
	reopened.SetStr("status", schema.TaskStatusInProgress)
	cases := []struct {
		name         string
		docs         []coord.Doc
		rounds       int
		wantRecorded bool
	}{
		{"first finder stamps", []coord.Doc{open}, 1, true},
		{"already stamped: nothing to do", []coord.Doc{stamped}, 1, false},
		{"lost the race to another finder", []coord.Doc{open, stamped}, 2, false},
		{"lost an unrelated race, still unstamped", []coord.Doc{open, open}, 2, true},
		{"reopened task", []coord.Doc{reopened}, 1, false},
		{"row gone", []coord.Doc{nil}, 1, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			rows := &seqRows{docs: c.docs}
			ok, err := stampMergedRow(context.Background(), rows, &racingSignals{rounds: c.rounds}, "tsk_1", at)
			if err != nil || ok != c.wantRecorded {
				t.Fatalf("recorded=%v err=%v, want recorded=%v", ok, err, c.wantRecorded)
			}
			if !c.wantRecorded {
				return
			}
			last := rows.staged[len(rows.staged)-1]
			if !last.Time(fieldMergedAt).Equal(at) || last.Str("updated_at") != open.Str("updated_at") || last.Status() != schema.TaskStatusDone {
				t.Fatalf("staged %v: want merged_at %v, updated_at and status unchanged", last, at)
			}
		})
	}
}

// TestRecordMergedReportsFailures (later.md 632): a stage or write failure
// is returned (nothing written after a failed stage), and the line never
// says "✓ merged" for it; a success says "✓ merged" and nothing else.
func TestRecordMergedReportsFailures(t *testing.T) {
	t.Parallel()
	f := mergewatch.Found{TaskID: "tsk_1", Finding: mergewatch.Finding{Method: mergewatch.MethodTrailer, Commit: "0123456789abcdef", At: time.Date(2026, 11, 3, 10, 30, 0, 0, time.UTC)}}
	open := coord.Doc{}
	open.SetStr("id", "tsk_1")
	open.SetStr("status", schema.TaskStatusDone)
	cases := []struct {
		name               string
		stageErr, writeErr error
		wantWrote, wantOK  bool
	}{
		{"stage fails", errors.New("stage boom"), nil, false, false},
		{"write fails", nil, errors.New("push rejected"), false, false},
		{"recorded", nil, nil, true, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			sw := &racingSignals{rounds: 1, err: tc.writeErr}
			ok, err := stampMergedRow(t.Context(), &seqRows{docs: []coord.Doc{open}, err: tc.stageErr}, sw, f.TaskID, f.Finding.At)
			if (err == nil) != tc.wantOK || ok != tc.wantOK || sw.wrote != tc.wantWrote {
				t.Fatalf("ok=%v err=%v wrote=%v; want ok=%v wrote=%v", ok, err, sw.wrote, tc.wantOK, tc.wantWrote)
			}
			got := mergewatch.Line(f, err)
			if tc.wantOK != strings.HasPrefix(got, "✓ tsk_1 merged") {
				t.Fatalf("line %q: \"✓ merged\" must appear only when recorded", got)
			}
			if !tc.wantOK && (!strings.Contains(got, "could not be recorded") || !strings.Contains(got, "the next lore command retries")) {
				t.Fatalf("a failed record must say so and that it retries: %q", got)
			}
		})
	}
}

// pushingSignals runs the decision once and then writes each companion's
// rows locally (PushSelf), as the signals executor does after a confirmed
// push.
type pushingSignals struct{}

func (pushingSignals) Write(ctx context.Context, decide signals.Decide) (signals.Result, error) {
	ch, err := decide(signals.View{})
	if err != nil {
		return signals.Result{}, err
	}
	for _, c := range ch.Companions {
		rc, ok := c.(signals.RemoteCompanion)
		if !ok {
			return signals.Result{}, errors.New("unexpected companion")
		}
		if _, err = rc.Prepare(ctx); err != nil {
			return signals.Result{}, err
		}
		if err = rc.PushSelf(ctx); err != nil {
			return signals.Result{}, err
		}
	}
	return signals.Result{}, nil
}

// taskRowFile reads table tasks' row file of id in repo's data.
func taskRowFile(t *testing.T, repo, id string) []byte {
	t.Helper()
	fsys := os.DirFS(repo)
	var hit []byte
	err := fs.WalkDir(fsys, ".lore", func(p string, d fs.DirEntry, werr error) error {
		if werr != nil {
			return werr
		}
		if !d.IsDir() && d.Name() == id+".json" && strings.Contains(p, "/"+schema.TableTasks+"/") {
			b, rerr := fs.ReadFile(fsys, p)
			hit = b
			return rerr
		}
		return nil
	})
	if err != nil || hit == nil {
		t.Fatalf("no row file of %s under %s: %v", id, repo, err)
	}
	return hit
}

// TestRecordMergedBranch_TwoClonesWriteTheSameRow (later.md 211, trial
// finding F5): a checkout without live rows records the stamp in its
// branch data through the core. Two clones holding the same done row that
// find the same merge at different wall-clock times end with the same
// merged_at, the merge commit's date, and their two copies merge on the
// next pull without a conflict (the core orders copies by _hlc).
func TestRecordMergedBranch_TwoClonesWriteTheSameRow(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	a, repoA, id := standaloneTask(t)
	if _, err := run(t, a, opstask.OpDone, `{"id":"`+id+`","no_lesson":true}`, true); err != nil {
		t.Fatal(err)
	}
	gitIn(t, repoA, b11GitEnv, "add", "-A")
	gitIn(t, repoA, b11GitEnv, "commit", "-q", "-m", "done")
	base := taskRowFile(t, repoA, id)
	repoB := filepath.Join(t.TempDir(), "b")
	gitIn(t, repoA, b11GitEnv, "clone", "-q", repoA, repoB)
	notClient := func(context.Context) (bool, error) { return false, nil }
	b := openWith(t, repoB, Options{RepoIsClient: notClient})
	at := time.Date(2026, 11, 3, 10, 15, 0, 0, time.UTC)
	stamp := func(app *App, repo string) []byte {
		t.Helper()
		rows := engineRows{a: app}
		ok, err := stampMergedRow(ctx, rows, pushingSignals{}, id, at)
		if err != nil || !ok {
			t.Fatalf("stamp: recorded=%v err=%v", ok, err)
		}
		d, found, err := rows.Get(ctx, id)
		if err != nil || !found || !d.Time(fieldMergedAt).Equal(at) {
			t.Fatalf("merged_at must be the merge commit's date %v: %v %v %v", at, d, found, err)
		}
		// A second check finds it stamped and writes nothing.
		if again, aerr := stampMergedRow(ctx, rows, pushingSignals{}, id, at); aerr != nil || again {
			t.Fatalf("second stamp: recorded=%v err=%v, want nothing", again, aerr)
		}
		return taskRowFile(t, repo, id)
	}
	ra := stamp(a, repoA)
	time.Sleep(2 * time.Millisecond) // the two clones run at different instants
	rb := stamp(b, repoB)
	reg, err := schema.NewRegistry(schema.Builtin()...)
	if err != nil {
		t.Fatal(err)
	}
	merged, conflict, err := merge.Driver{Registry: reg}.Merge(base, ra, rb)
	if err != nil || conflict {
		t.Fatalf("the two stamped copies must merge cleanly: conflict=%v err=%v\n%s", conflict, err, merged)
	}
	if !strings.Contains(string(merged), `"merged_at"`) || !strings.Contains(string(merged), at.Format("2006-01-02T15:04:05")) {
		t.Fatalf("merged copy lost merged_at: %s", merged)
	}
}

// TestCoordUnknownTaskLeavesTheHintToTheHost (later.md 218): an unknown
// task on a coordination command is E_NOT_FOUND with no hint of its own,
// so the host's F1 answer (the `lore sync peek <remote>/<branch> tasks`
// hint, or what live knows about the id) is the one the user sees. A
// mission id, which the host's F1 does not explain under the task noun,
// gets its own hint; any other coordination error keeps its own.
func TestCoordUnknownTaskLeavesTheHintToTheHost(t *testing.T) {
	t.Parallel()
	const hex = "01a1b809623a78b8b1212e4489503297"
	cases := []struct {
		id, hint string
	}{
		{"tsk_" + hex, ""},
		{"tsk_1", ""}, // an id that does not parse: still the host's
		{"msn_" + hex, "`lore mission list` shows the mission ids"},
	}
	for _, c := range cases {
		var e *errs.Error
		if err := coordErr(fmt.Errorf("%w: %s", coord.ErrUnknownTask, c.id)); !errors.As(err, &e) || e.Code != errs.NotFound || e.Hint != c.hint {
			t.Errorf("unknown %s: %#v, want E_NOT_FOUND with hint %q", c.id, err, c.hint)
		}
	}
	var e *errs.Error
	if err := coordErr(coord.ErrContended); !errors.As(err, &e) || e.Code != errs.Contended || e.Hint == "" {
		t.Fatalf("a contended claim keeps its hint: %v", err)
	}
}

// TestMergedCheckWarnsWhenStoreUnreachable (later.md 606, E-FATES-4, port
// of v0.1.17 merge_unchecked_test.go): the lazy merged check must not look
// as if it ran on the linked store when that store could not be opened:
// one warning line says its merge state was not checked and why, and the
// check goes on over the rest (this repo's own rows, as v0.1.17's
// lazyMergedCheck ran before groupMergedCheck). An unlinked checkout opens
// nothing and says nothing; a store that opens says nothing either.
func TestMergedCheckWarnsWhenStoreUnreachable(t *testing.T) {
	t.Parallel()
	unreachable := errors.New("fetch git@example.com:acme/store.git: connection refused\nexit status 128")
	linked := orgresolve.Result{Links: []orgresolve.Link{{Entry: "acme"}}}
	cases := []struct {
		name                     string
		sit                      orgresolve.Result
		openErr                  error
		wantRepoOnly, wantOpened bool
	}{
		{"no links: nothing to open, silent", orgresolve.Result{}, unreachable, false, false},
		{"client role without links: the store is opened", orgresolve.Result{Role: orgresolve.RoleClient}, unreachable, true, true},
		{"linked, store opens: silent", linked, nil, false, true},
		{"linked, store unreachable: one warning, the repo's rows still checked", linked, unreachable, true, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var out strings.Builder
			opened := false
			repoOnly := linkedStoreScope(&out, &coordWorld{sit: tc.sit}, func() error {
				opened = true
				return tc.openErr
			})
			got := out.String()
			if repoOnly != tc.wantRepoOnly || opened != tc.wantOpened {
				t.Fatalf("repo only=%v opened=%v, want %v %v", repoOnly, opened, tc.wantRepoOnly, tc.wantOpened)
			}
			if !tc.wantRepoOnly {
				if got != "" {
					t.Fatalf("want silence, got %q", got)
				}
				return
			}
			if strings.Count(got, "\n") != 1 || !strings.HasSuffix(got, "\n") {
				t.Fatalf("want exactly one warning line, got %q", got)
			}
			for _, want := range []string{mergeUncheckedPrefix, "open the linked store", "connection refused exit status 128", "not merged yet"} {
				if !strings.Contains(got, want) {
					t.Fatalf("warning %q lacks %q", got, want)
				}
			}
		})
	}
}

// TestMergeCandidatesOfTheRepoWhenStoreUnreachable (E-FATES-4): with the
// linked store out of reach the check still has this repo's own done,
// unmerged tasks to look at, and leaves the store's rows alone (they are
// neither checked nor stamped this time); with the store opened the
// group's rows are candidates too. An open task is never a candidate.
func TestMergeCandidatesOfTheRepoWhenStoreUnreachable(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repo, _, _, _ := folderStoreRepo(t)
	a := newTestApp(t, repo)
	withProject(t, a)
	add := func(input string) string {
		t.Helper()
		out, err := run(t, a, opstask.OpAdd, input, true)
		if err != nil {
			t.Fatal(err)
		}
		var shown struct {
			Data struct {
				ID string `json:"id"`
			} `json:"data"`
		}
		if err := json.Unmarshal([]byte(out), &shown); err != nil || shown.Data.ID == "" {
			t.Fatalf("task add output has no id: %s (%v)", out, err)
		}
		return shown.Data.ID
	}
	own, group := add(`{"title":"fix rounding"}`), add(`{"title":"ship the store side","to":"northwind"}`)
	w := &coordWorld{a: a, sit: orgresolve.Result{Links: []orgresolve.Link{{Entry: "acme"}}}}
	w.spot.store = true // the view of a linked checkout: the store's rows are the group's
	if got := a.mergeCandidates(ctx, w, true); len(got) != 0 {
		t.Fatalf("an open task is a candidate: %v", got)
	}
	for _, id := range []string{own, group} {
		if _, err := run(t, a, opstask.OpDone, `{"id":"`+id+`","no_lesson":true}`, true); err != nil {
			t.Fatal(err)
		}
	}
	ids := func(cs []mergewatch.Candidate) []string {
		var out []string
		for _, c := range cs {
			out = append(out, c.TaskID)
		}
		sort.Strings(out)
		return out
	}
	if got := ids(a.mergeCandidates(ctx, w, true)); !slices.Equal(got, []string{own}) {
		t.Fatalf("store out of reach: candidates %v, want only the repo's %s", got, own)
	}
	want := []string{own, group}
	sort.Strings(want)
	if got := ids(a.mergeCandidates(ctx, w, false)); !slices.Equal(got, want) {
		t.Fatalf("store opened: candidates %v, want %v", got, want)
	}
}
