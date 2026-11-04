package main

// Unit tests of the core's live overlay (live_core.go) over a real git repo
// and an in-memory branch store: where each write goes, the lost-update
// guard (Expect against the copy a reader sees), tombstones, the fenced
// collector and the live-off no-op.

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/model/hlc"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/store"
	"github.com/thesatellite-ai/lore/store/memstore"

	"saas/pkg/aicoder/live"
)

// Fixed ids of the overlay tests (valid lore ids, never real rows).
const (
	ltTaskA   = "tsk_01a1a6841fc0784d92a62b0acf148b26"
	ltTaskB   = "tsk_01a1a6841fc0784d92a62b0acf148b27"
	ltMemory  = "mem_01a1a6841fc0784d92a62b0acf148b28"
	ltActorID = "act_3686b9758592753ea25bf584466bc40e"
)

// ltBase is the wall time of the first stamp; later stamps add seconds.
var ltBase = time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)

// newLiveFixture is a git repo with live mode on (no remote) and a
// liveBackend over an empty in-memory branch store.
func newLiveFixture(t *testing.T) (*liveBackend, store.Backend, *liveSession) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("HOME", t.TempDir())
	t.Setenv(envLive, "")
	t.Setenv(envSync, "")
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	for _, args := range [][]string{
		{"init", "-q", "-b", "main"},
		{"-c", "user.name=t", "-c", "user.email=t@example.com", "commit", "-q", "--allow-empty", "-m", "init"},
	} {
		if out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	inner := memstore.NewData().Open(store.OpenOptions{})
	b := &liveBackend{inner: inner, top: dir, folder: live.RootFolder}
	s := b.session(context.Background())
	if s == nil {
		t.Fatal("live mode must apply in a git repo with LORE_LIVE unset")
	}
	t.Cleanup(func() {
		s.release()
		for k, v := range liveSessions {
			if v == s {
				delete(liveSessions, k)
			}
		}
	})
	return b, inner, s
}

// ltRow builds a row of table with a title (or tombstone) stamped at ltBase + sec.
func ltRow(t *testing.T, table, id string, sec int, title string, deleted bool) store.Row {
	t.Helper()
	aid, err := ids.ParseAny(id)
	if err != nil {
		t.Fatal(err)
	}
	actor, err := ids.Parse[ids.Actor](ltActorID)
	if err != nil {
		t.Fatal(err)
	}
	r := row.Row{Table: row.Table(table), ID: aid, HLC: hlc.HLC{Wall: ltBase.Add(time.Duration(sec) * time.Second), Actor: actor}, Deleted: deleted}
	if !deleted {
		tv, err := value.TextOf(title)
		if err != nil {
			t.Fatal(err)
		}
		r.Fields = map[string]value.Value{"title": tv}
	}
	sr, err := store.FromRow(r)
	if err != nil {
		t.Fatal(err)
	}
	return sr
}

// liveHas reports whether the local live ref holds table/id.
func liveHas(t *testing.T, b *liveBackend, s *liveSession, table, id string) bool {
	t.Helper()
	_, files, err := b.liveFiles(context.Background(), s)
	if err != nil {
		t.Fatal(err)
	}
	_, ok := files[live.RowKey{Table: table, ID: id}]
	return ok
}

func TestLiveBackend_Placement(t *testing.T) {
	ctx := context.Background()
	b, inner, s := newLiveFixture(t)
	// A new row of a live kind goes live, not to the branch data.
	a := ltRow(t, tableTasks, ltTaskA, 1, "open work", false)
	if err := b.Write(ctx, []store.Change{{Row: a}}); err != nil {
		t.Fatal(err)
	}
	if !liveHas(t, b, s, tableTasks, ltTaskA) {
		t.Fatal("a new task goes live")
	}
	if _, err := inner.Get(ctx, a.Table, a.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("a live row is not in the branch data: %v", err)
	}
	got, err := b.Get(ctx, a.Table, a.ID)
	if err != nil || got.HLC.Compare(a.HLC) != 0 || !strings.Contains(string(got.Doc), "open work") {
		t.Fatalf("Get reads the live copy: %v %s", err, got.Doc)
	}
	// A row of a table that is not a live kind stays with the code.
	m := ltRow(t, "memories", ltMemory, 1, "a lesson", false)
	if err := b.Write(ctx, []store.Change{{Row: m}}); err != nil {
		t.Fatal(err)
	}
	if liveHas(t, b, s, "memories", ltMemory) {
		t.Fatal("a memory never goes live")
	}
	if _, err := inner.Get(ctx, m.Table, m.ID); err != nil {
		t.Fatalf("a memory is written to the branch data: %v", err)
	}
	// A branch row's edit stays in the branch data (F1).
	br := ltRow(t, tableTasks, ltTaskB, 1, "branch task", false)
	if err := inner.Write(ctx, []store.Change{{Row: br}}); err != nil {
		t.Fatal(err)
	}
	if err := b.Write(ctx, []store.Change{{Row: ltRow(t, tableTasks, ltTaskB, 2, "branch task edited", false), Expect: br.HLC}}); err != nil {
		t.Fatal(err)
	}
	if liveHas(t, b, s, tableTasks, ltTaskB) {
		t.Fatal("an edit of a branch row stays with the code")
	}
	// Read returns live and branch rows, under a live cursor.
	rows, cur, err := b.Read(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, r := range rows {
		seen[r.ID.String()] = true
	}
	if !seen[ltTaskA] || !seen[ltTaskB] || !seen[ltMemory] || !strings.HasPrefix(string(cur), liveCursorPrefix) {
		t.Fatalf("Read lists live and branch rows: %v cursor %q", seen, cur)
	}
}

// The lost-update guard: a write must name the version a reader sees.
func TestLiveBackend_ExpectRefusals(t *testing.T) {
	ctx := context.Background()
	b, _, _ := newLiveFixture(t)
	v1 := ltRow(t, tableTasks, ltTaskA, 1, "v1", false)
	if err := b.Write(ctx, []store.Change{{Row: v1}}); err != nil {
		t.Fatal(err)
	}
	wrong := ltRow(t, tableTasks, ltTaskA, 9, "stale", false)
	cases := []struct {
		name   string
		expect hlc.HLC
	}{
		{"create over an existing live row", hlc.HLC{}},
		{"expect from another version", wrong.HLC},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := b.Write(ctx, []store.Change{{Row: ltRow(t, tableTasks, ltTaskA, 2, "lost update", false), Expect: c.expect}})
			if !errors.Is(err, store.ErrStale) {
				t.Fatalf("want E_STALE, got %v", err)
			}
		})
	}
	if err := b.Write(ctx, []store.Change{{Row: ltRow(t, tableTasks, ltTaskA, 2, "v2", false), Expect: v1.HLC}}); err != nil {
		t.Fatalf("the current version is accepted: %v", err)
	}
	got, err := b.Get(ctx, v1.Table, v1.ID)
	if err != nil || !strings.Contains(string(got.Doc), "v2") {
		t.Fatalf("v2 is read back: %v %s", err, got.Doc)
	}
	// A tombstone is never resurrected by a create.
	if err := b.Write(ctx, []store.Change{{Row: ltRow(t, tableTasks, ltTaskA, 3, "", true), Expect: got.HLC}}); err != nil {
		t.Fatal(err)
	}
	dead, err := b.Get(ctx, v1.Table, v1.ID)
	if err != nil || !dead.Deleted || strings.Contains(string(dead.Doc), "v2") {
		t.Fatalf("the tombstone keeps no field: %v %s", err, dead.Doc)
	}
	if err := b.Write(ctx, []store.Change{{Row: ltRow(t, tableTasks, ltTaskA, 4, "back", false)}}); !errors.Is(err, store.ErrStale) {
		t.Fatalf("a create over a tombstone is refused: %v", err)
	}
}

// A fenced coordination write collects its live rows: nothing reaches the
// live ref until the companion's Finalize.
func TestLiveBackend_CollectorStagesOnly(t *testing.T) {
	ctx := context.Background()
	b, inner, s := newLiveFixture(t)
	br := ltRow(t, tableTasks, ltTaskB, 1, "branch task", false)
	if err := inner.Write(ctx, []store.Change{{Row: br}}); err != nil {
		t.Fatal(err)
	}
	before, _, _ := s.ref.Repo.ResolveRef(ctx, s.ref.Name)
	col := &liveCollector{rows: map[live.RowKey]live.File{}}
	claim := ltRow(t, tableTasks, ltTaskB, 2, "claimed", false)
	if err := b.Write(withLiveCollector(ctx, col), []store.Change{{Row: claim, Expect: br.HLC}}); err != nil {
		t.Fatal(err)
	}
	after, _, _ := s.ref.Repo.ResolveRef(ctx, s.ref.Name)
	if before != after {
		t.Fatal("a collected write moves no ref")
	}
	if _, ok := col.rows[live.RowKey{Table: tableTasks, ID: ltTaskB}]; !ok || col.s != s {
		t.Fatal("the claim of a branch task is collected as a live row (it joins the signals push)")
	}
	if got, _ := inner.Get(ctx, br.Table, br.ID); got.HLC.Compare(br.HLC) != 0 {
		t.Fatal("the branch copy is untouched until the push is confirmed")
	}
}

func TestCoreWrapBackend_LiveOffIsANoOp(t *testing.T) {
	inner := memstore.NewData().Open(store.OpenOptions{})
	t.Setenv(envLive, envValueOff)
	if got := coreWrapBackend(context.Background(), store.SourceRepo, "", inner); got != inner {
		t.Fatal("LORE_LIVE=0 keeps every row in .lore/data")
	}
	t.Setenv(envLive, "")
	if got := coreWrapBackend(context.Background(), "northwind", "acme", inner); got != inner {
		t.Fatal("only the repo store is wrapped")
	}
}
