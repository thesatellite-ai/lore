package live

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/adapters/gitremote"
	"github.com/thesatellite-ai/lore/adapters/gitremote/gitremotetest"
)

const week = 7 * 24 * time.Hour

func openRow(since, lastCopy time.Time) File {
	return File{Body: obj(map[string]string{"id": "tsk_1", "title": "x", "status": "todo"}), Block: Block{LiveSince: since, LastCopyAt: lastCopy, HLC: HLC{Wall: since}}}
}

func bodyOf(t *testing.T, f File) []byte {
	t.Helper()
	b, err := f.EncodeBody()
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// F5 ShouldLand matrix: finished / open / named × age under / over (from
// live_since or last_copy_at) × main equal / older / absent × conflict ×
// tombstone.
func TestShouldLandMatrix(t *testing.T) {
	young := openRow(t0, time.Time{})
	conflict := young
	conflict.Block.Conflict = true
	tomb := Tombstone("tsk_1", Block{HLC: HLC{Wall: t0}})
	older := []byte(`{"id":"tsk_1","title":"older"}`)
	cases := []struct {
		name string
		in   LandInputs
		want LandAction
	}{
		{"finished, main absent", LandInputs{File: young, Finished: true}, LandFinal},
		{"finished, main older", LandInputs{File: young, Finished: true, MainBody: older}, LandFinal},
		{"finished, main equal → nothing (twice is a no-op)", LandInputs{File: young, Finished: true, MainBody: bodyOf(t, young)}, LandStay},
		{"named open → copy", LandInputs{File: young, Named: true}, LandCopy},
		{"named finished → land", LandInputs{File: young, Named: true, Finished: true}, LandFinal},
		{"open, 6 days", LandInputs{File: young, Now: t0.Add(6 * 24 * time.Hour)}, LandStay},
		{"open, max age reached → copy", LandInputs{File: young, Now: t0.Add(week)}, LandCopy},
		{"open, max age but main equal", LandInputs{File: young, Now: t0.Add(week), MainBody: bodyOf(t, young)}, LandStay},
		{"open, copied 1 day ago", LandInputs{File: openRow(t0, t0.Add(week)), Now: t0.Add(week + 24*time.Hour), MainBody: older}, LandStay},
		{"open, copied max age ago → copy again", LandInputs{File: openRow(t0, t0.Add(week)), Now: t0.Add(2 * week), MainBody: older}, LandCopy},
		{"conflict never lands, even named and finished", LandInputs{File: conflict, Named: true, Finished: true}, LandStay},
		{"tombstone, main has file", LandInputs{File: tomb, MainBody: older}, LandFinal},
		{"tombstone, main lacks file, not purged", LandInputs{File: tomb}, LandFinal},
		{"tombstone already deleted and purged", LandInputs{File: tomb, MainPurged: true}, LandStay},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if c.in.MaxAge == 0 {
				c.in.MaxAge = week
			}
			if got := ShouldLand(c.in); got.Action != c.want {
				t.Fatalf("ShouldLand = %+v, want %s", got, c.want)
			}
		})
	}
}

// F5 Confirm: a row leaves live only when finished and main holds exactly
// it; a tombstone only once main deleted and purged it and max age passed.
func TestConfirmMatrix(t *testing.T) {
	f := openRow(t0, time.Time{})
	tomb := Tombstone("tsk_1", Block{HLC: HLC{Wall: t0}})
	cases := []struct {
		name string
		in   ConfirmInputs
		want bool
	}{
		{"finished and equal", ConfirmInputs{File: f, Finished: true, MainBody: bodyOf(t, f)}, true},
		{"finished, main older (reopened after landing)", ConfirmInputs{File: f, Finished: true, MainBody: []byte(`{"id":"tsk_1"}`)}, false},
		{"finished, main absent", ConfirmInputs{File: f, Finished: true}, false},
		{"open but equal (max-age copy) stays live", ConfirmInputs{File: f, MainBody: bodyOf(t, f)}, false},
		{"tombstone purged, max age passed", ConfirmInputs{File: tomb, MainPurged: true, Now: t0.Add(week), MaxAge: week}, true},
		{"tombstone purged, too early", ConfirmInputs{File: tomb, MainPurged: true, Now: t0.Add(time.Hour), MaxAge: week}, false},
		{"tombstone, main still has the file", ConfirmInputs{File: tomb, MainPurged: true, MainBody: []byte(`{}`), Now: t0.Add(week), MaxAge: week}, false},
		{"tombstone, not purged on main", ConfirmInputs{File: tomb, Now: t0.Add(week), MaxAge: week}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := Confirm(c.in); got != c.want {
				t.Fatalf("Confirm = %v, want %v", got, c.want)
			}
		})
	}
}

var testLayout = Layout{
	DataPath: func(folder, rel string) string {
		if folder == RootFolder {
			return ".lore/data/" + rel
		}
		return folder + "/.lore/data/" + rel
	},
	AddPurged: func(existing []byte, ids []string) ([]byte, error) {
		var v struct {
			IDs []string `json:"ids"`
		}
		if len(existing) > 0 {
			if err := json.Unmarshal(existing, &v); err != nil {
				return nil, err
			}
		}
		set := map[string]bool{}
		for _, id := range append(v.IDs, ids...) {
			set[id] = true
		}
		v.IDs = v.IDs[:0]
		for id := range set {
			v.IDs = append(v.IDs, id)
		}
		sort.Strings(v.IDs)
		return json.Marshal(v)
	},
}

// landItem is the done task tsk_1 with the given title, landing for good.
func landItem(title string) LandItem {
	const id = "tsk_1"
	f := File{Body: obj(map[string]string{"id": id, "title": title, "status": "done"})}
	return LandItem{Folder: RootFolder, Key: RowKey{Table: "tasks", ID: id}, File: f, Action: LandFinal}
}

// E3 / F5: landing writes the row without its block on origin/main, never
// moves or pushes the local main, and a second landing of the same row is
// a no-op.
func TestLandOnMainNeverTouchesLocalMain(t *testing.T) {
	w := gitremotetest.NewWorld(t)
	a := w.Clone("a")
	r := newRef(t, a, a.Remote)
	gitremotetest.Git(t, a.Dir, "commit", "-q", "--allow-empty", "-m", "local only")
	localMain := gitremotetest.Git(t, a.Dir, "rev-parse", "refs/heads/main")
	res, err := r.Land(context.Background(), LandRequest{Branch: "main", Items: []LandItem{landItem("ship it")}, Layout: testLayout})
	if err != nil || res.Status != LandDone || res.Rows != 1 {
		t.Fatalf("land: %+v %v", res, err)
	}
	got, ok, _ := w.Origin.ReadFile(context.Background(), "refs/heads/main", ".lore/data/tasks/tsk_1.json")
	if !ok || strings.Contains(string(got), BlockKey) || !strings.Contains(string(got), "ship it") {
		t.Fatalf("main file: %s", got)
	}
	if subj := gitremotetest.Git(t, w.OriginDir, "log", "-1", "--format=%s", "main"); subj != "lore: land 1 row (_root)" {
		t.Fatalf("fixed message: %q", subj)
	}
	if gitremotetest.Git(t, w.OriginDir, "log", "--format=%s", "main") == "" || strings.Contains(gitremotetest.Git(t, w.OriginDir, "log", "--format=%s", "main"), "local only") {
		t.Fatal("the user's unpushed main commit must not be pushed")
	}
	if now := gitremotetest.Git(t, a.Dir, "rev-parse", "refs/heads/main"); now != localMain {
		t.Fatal("local main must not move")
	}
	again, err := r.Land(context.Background(), LandRequest{Branch: "main", Items: []LandItem{landItem("ship it")}, Layout: testLayout})
	if err != nil || again.Status != LandNothing {
		t.Fatalf("landing twice must commit nothing: %+v %v", again, err)
	}
}

// U-2: a brand-new repo whose remote has no default branch yet has nothing
// to land on: no error (the old "fetch: remote ref not found ...
// refs/heads/main"), nothing pushed, the rows stay live.
func TestLandRemoteWithoutDefaultBranchIsQuiet(t *testing.T) {
	w := gitremotetest.NewWorld(t)
	a := w.Clone("a")
	r := newRef(t, a, a.Remote)
	res, err := r.Land(context.Background(), LandRequest{Branch: "trunk", Items: []LandItem{landItem("x")}, Layout: testLayout})
	if err != nil || res.Status != LandNoRemote {
		t.Fatalf("want no-remote and no error: %+v %v", res, err)
	}
	if got := w.OriginRef("refs/heads/trunk"); got != "" {
		t.Fatalf("nothing may be pushed: trunk = %q", got)
	}
}

// F5 "main moved during push": a non-fast-forward rebuilds on the new tip.
func TestLandRetriesWhenMainMoves(t *testing.T) {
	w := gitremotetest.NewWorld(t)
	a, b := w.Clone("a"), w.Clone("b")
	fault := gitremote.NewFaultRemote(a.Remote)
	fault.BeforePush(1, func(ctx context.Context) error {
		gitremotetest.Git(t, b.Dir, "commit", "-q", "--allow-empty", "-m", "teammate")
		gitremotetest.Git(t, b.Dir, "push", "-q", "origin", "main")
		return nil
	})
	r := newRef(t, a, fault)
	res, err := r.Land(context.Background(), LandRequest{Branch: "main", Items: []LandItem{landItem("x")}, Layout: testLayout})
	if err != nil || res.Status != LandDone {
		t.Fatalf("land: %+v %v", res, err)
	}
	if log := gitremotetest.Git(t, w.OriginDir, "log", "--format=%s", "main"); !strings.Contains(log, "teammate") {
		t.Fatal("the teammate's commit must survive")
	}
}

// F5 → F6: a protection refusal is reported, nothing lands.
func TestLandProtectedMain(t *testing.T) {
	w := gitremotetest.NewWorld(t)
	a := w.Clone("a")
	fault := gitremote.NewFaultRemote(a.Remote)
	fault.Protect("refs/heads/main")
	r := newRef(t, a, fault)
	before := w.OriginRef("refs/heads/main")
	res, err := r.Land(context.Background(), LandRequest{Branch: "main", Items: []LandItem{landItem("x")}, Layout: testLayout})
	if err != nil || res.Status != LandProtected {
		t.Fatalf("want protected: %+v %v", res, err)
	}
	if w.OriginRef("refs/heads/main") != before {
		t.Fatal("protected main must not change")
	}
}

// Side effect 11: a tombstone lands as a file delete plus a _purged.json
// id; a tombstone of a row main never had only adds the id once.
func TestBuildLandingTombstone(t *testing.T) {
	files := map[string][]byte{".lore/data/tasks/tsk_1.json": []byte(`{"id":"tsk_1"}`)}
	read := func(p string) ([]byte, bool, error) { b, ok := files[p]; return b, ok, nil }
	tomb := LandItem{Folder: RootFolder, Key: RowKey{Table: "tasks", ID: "tsk_1"}, File: Tombstone("tsk_1", Block{}), Action: LandFinal}
	ch, changed, err := BuildLanding(context.Background(), read, []LandItem{tomb}, testLayout)
	if err != nil {
		t.Fatal(err)
	}
	if len(ch.Delete) != 1 || !strings.Contains(string(ch.Put[".lore/data/_purged.json"]), "tsk_1") || len(changed) != 1 {
		t.Fatalf("delete + purge: %+v %v", ch, changed)
	}
	files = map[string][]byte{".lore/data/_purged.json": []byte(`{"ids":["tsk_1"]}`)}
	ch, changed, _ = BuildLanding(context.Background(), read, []LandItem{tomb}, testLayout)
	if !ch.Empty() || len(changed) != 0 {
		t.Fatalf("already deleted and purged: nothing to do: %+v", ch)
	}
}

func TestLandMessageCountsAndFolders(t *testing.T) {
	m := LandMessage([]LandItem{
		{Folder: "web", Key: RowKey{Table: "tasks", ID: "tsk_b"}},
		{Folder: RootFolder, Key: RowKey{Table: "tasks", ID: "tsk_a"}},
	})
	if !strings.HasPrefix(m, "lore: land 2 rows (_root, web)\n\ntsk_a\ntsk_b") {
		t.Fatalf("message: %q", m)
	}
}
