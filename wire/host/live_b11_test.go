package host

// B11 ports of the old binary's live overlay unit tests
// (saas/cmd/cli/live_core_test.go, live_companion_meta_test.go,
// live_failclosed_test.go, live_land_test.go, live_land_branch_test.go,
// live_ride_test.go): the same behaviours against wire/host's liveBackend
// and liveSession, which cmd/lore runs (LiveHook).

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/adapters/gitref"
	"github.com/thesatellite-ai/lore/adapters/live"
	"github.com/thesatellite-ai/lore/adapters/orgcfg"
	"github.com/thesatellite-ai/lore/internal/testenv"
	"github.com/thesatellite-ai/lore/logic/canonjson"
	"github.com/thesatellite-ai/lore/logic/clock"
	"github.com/thesatellite-ai/lore/model/hlc"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
	"github.com/thesatellite-ai/lore/store/memstore"
)

// Fixed ids of the overlay tests (valid lore ids, never real rows).
const (
	ltTaskA   = "tsk_01a1a6841fc0784d92a62b0acf148b26"
	ltTaskB   = "tsk_01a1a6841fc0784d92a62b0acf148b27"
	ltMemory  = "mem_01a1a6841fc0784d92a62b0acf148b28"
	ltMkyID   = "mky_01a1a6841fc0784d92a62b0acf148b29"
	ltActorID = "act_3686b9758592753ea25bf584466bc40e"
)

// ltBase is the wall time of the first stamp; later stamps add seconds.
var ltBase = time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)

// gitT runs git in dir with a fixed identity (through gitref.Exec, the
// production runner, so no test-only subprocess call).
func gitT(t *testing.T, dir string, args ...string) {
	t.Helper()
	inv := gitref.Invocation{Dir: dir, Args: args, Env: []string{"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com", "GIT_CONFIG_NOSYSTEM=1"}}
	if out, err := (gitref.Exec{}).Run(context.Background(), inv); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// ltFixture is a git repo with live mode on (no remote), a liveBackend over
// an empty in-memory branch store, and the run context its session hangs on.
type ltFixture struct {
	ctx   context.Context
	b     *liveBackend
	inner store.Backend
	s     *liveSession
}

func newLiveFixture(t *testing.T) ltFixture { return newLiveFixtureWith(t, "", nil) }

// newLiveFixtureWith is newLiveFixture with a ~/.lore/config.yaml body
// ("" = none) and a clock (nil = the wall clock); the repo holds a .lore/
// folder so the cwd project resolves (doctor, sync).
func newLiveFixtureWith(t *testing.T, configYAML string, clk clock.Clock) ltFixture {
	t.Helper()
	home := t.TempDir()
	testenv.SetHome(t, home)
	t.Setenv(EnvLive, "")
	t.Setenv(EnvSync, "")
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	loreHome := filepath.Join(home, ".lore")
	if configYAML != "" {
		if err := os.MkdirAll(loreHome, 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(orgcfg.ConfigPath(loreHome), []byte(configYAML), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	gitT(t, dir, "init", "-q", "-b", "main")
	gitT(t, dir, "commit", "-q", "--allow-empty", "-m", "init")
	if err := os.MkdirAll(filepath.Join(dir, ".lore"), 0o750); err != nil {
		t.Fatal(err)
	}
	h := &liveHost{cfg: LiveConfig{Home: loreHome, WorkDir: dir, Version: "0.1.18", Stderr: &strings.Builder{}, Clock: clk}}
	run := &liveRun{h: h, sessions: map[string]*liveSession{}}
	ctx := withRun(context.Background(), run)
	inner := memstore.NewData().Open(store.OpenOptions{})
	b := &liveBackend{inner: inner, top: dir, folder: live.RootFolder}
	s := b.session(ctx)
	if s == nil {
		t.Fatal("live mode must apply in a git repo with LORE_LIVE unset")
	}
	t.Cleanup(s.release)
	return ltFixture{ctx: ctx, b: b, inner: inner, s: s}
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
		tv, terr := value.TextOf(title)
		if terr != nil {
			t.Fatal(terr)
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
func (f ltFixture) liveHas(t *testing.T, table, id string) bool {
	t.Helper()
	_, files, err := f.b.liveFiles(f.ctx, f.s)
	if err != nil {
		t.Fatal(err)
	}
	_, ok := files[live.RowKey{Table: table, ID: id}]
	return ok
}

// TestLiveBackend_Placement: a new row of a live kind goes live, a memory
// stays with the code, an edit of a branch row stays in the branch data
// (F1), and Read lists both under a live cursor.
func TestLiveBackend_Placement(t *testing.T) {
	f := newLiveFixture(t)
	ctx := f.ctx
	a := ltRow(t, schema.TableTasks, ltTaskA, 1, "open work", false)
	if err := f.b.Write(ctx, []store.Change{{Row: a}}); err != nil {
		t.Fatal(err)
	}
	if !f.liveHas(t, schema.TableTasks, ltTaskA) {
		t.Fatal("a new task goes live")
	}
	if _, err := f.inner.Get(ctx, a.Table, a.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("a live row is not in the branch data: %v", err)
	}
	got, err := f.b.Get(ctx, a.Table, a.ID)
	if err != nil || got.HLC.Compare(a.HLC) != 0 || !strings.Contains(string(got.Doc), "open work") {
		t.Fatalf("Get reads the live copy: %v %s", err, got.Doc)
	}
	m := ltRow(t, schema.TableMemories, ltMemory, 1, "a lesson", false)
	if err = f.b.Write(ctx, []store.Change{{Row: m}}); err != nil {
		t.Fatal(err)
	}
	if f.liveHas(t, schema.TableMemories, ltMemory) {
		t.Fatal("a memory never goes live by default")
	}
	if _, err = f.inner.Get(ctx, m.Table, m.ID); err != nil {
		t.Fatalf("a memory is written to the branch data: %v", err)
	}
	br := ltRow(t, schema.TableTasks, ltTaskB, 1, "branch task", false)
	if err = f.inner.Write(ctx, []store.Change{{Row: br}}); err != nil {
		t.Fatal(err)
	}
	if err = f.b.Write(ctx, []store.Change{{Row: ltRow(t, schema.TableTasks, ltTaskB, 2, "branch task edited", false), Expect: br.HLC}}); err != nil {
		t.Fatal(err)
	}
	if f.liveHas(t, schema.TableTasks, ltTaskB) {
		t.Fatal("an edit of a branch row stays with the code")
	}
	rows, cur, err := f.b.Read(ctx, "")
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

// TestLiveBackend_ExpectRefusals: the lost-update guard, a write must name
// the version a reader sees; a tombstone is never resurrected by a create.
func TestLiveBackend_ExpectRefusals(t *testing.T) {
	f := newLiveFixture(t)
	ctx := f.ctx
	v1 := ltRow(t, schema.TableTasks, ltTaskA, 1, "v1", false)
	if err := f.b.Write(ctx, []store.Change{{Row: v1}}); err != nil {
		t.Fatal(err)
	}
	wrong := ltRow(t, schema.TableTasks, ltTaskA, 9, "stale", false)
	for _, c := range []struct {
		name   string
		expect hlc.HLC
	}{
		{"create over an existing live row", hlc.HLC{}},
		{"expect from another version", wrong.HLC},
	} {
		t.Run(c.name, func(t *testing.T) {
			err := f.b.Write(ctx, []store.Change{{Row: ltRow(t, schema.TableTasks, ltTaskA, 2, "lost update", false), Expect: c.expect}})
			if !errors.Is(err, store.ErrStale) {
				t.Fatalf("want E_STALE, got %v", err)
			}
		})
	}
	if err := f.b.Write(ctx, []store.Change{{Row: ltRow(t, schema.TableTasks, ltTaskA, 2, "v2", false), Expect: v1.HLC}}); err != nil {
		t.Fatalf("the current version is accepted: %v", err)
	}
	got, err := f.b.Get(ctx, v1.Table, v1.ID)
	if err != nil || !strings.Contains(string(got.Doc), "v2") {
		t.Fatalf("v2 is read back: %v %s", err, got.Doc)
	}
	if err = f.b.Write(ctx, []store.Change{{Row: ltRow(t, schema.TableTasks, ltTaskA, 3, "", true), Expect: got.HLC}}); err != nil {
		t.Fatal(err)
	}
	dead, err := f.b.Get(ctx, v1.Table, v1.ID)
	if err != nil || !dead.Deleted || strings.Contains(string(dead.Doc), "v2") {
		t.Fatalf("the tombstone keeps no field: %v %s", err, dead.Doc)
	}
	if err := f.b.Write(ctx, []store.Change{{Row: ltRow(t, schema.TableTasks, ltTaskA, 4, "back", false)}}); !errors.Is(err, store.ErrStale) {
		t.Fatalf("a create over a tombstone is refused: %v", err)
	}
}

// dirMem is an in-memory backend that knows a repository folder, the shape
// liveHost.wrap wraps (a folder backend).
type dirMem struct {
	store.Backend
	dir string
}

func (d dirMem) Dir() string { return d.dir }

// TestLiveWrap_OnlyTheRepoStoreWithLiveOn: LORE_LIVE=0 / LORE_SYNC=0 keep
// every row in .lore/data (the wrapper has no live session), and only the
// repo store is wrapped.
func TestLiveWrap_OnlyTheRepoStoreWithLiveOn(t *testing.T) {
	h := &liveHost{}
	inner := dirMem{Backend: memstore.NewData().Open(store.OpenOptions{}), dir: t.TempDir()}
	t.Setenv(EnvLive, "")
	t.Setenv(EnvSync, "")
	if _, ok := h.wrap(store.SourceRepo, inner).(*liveBackend); !ok {
		t.Fatal("the repo store is wrapped while live mode is on")
	}
	for _, env := range []string{EnvLive, EnvSync} {
		t.Run(env+"=0", func(t *testing.T) {
			t.Setenv(env, envOff)
			// Still wrapped (F16 on branch writes, noSessionLeakCheck), but
			// with no live overlay: every row stays in .lore/data.
			lb, ok := h.wrap(store.SourceRepo, inner).(*liveBackend)
			if !ok || !lb.off || lb.session(t.Context()) != nil {
				t.Fatalf("%s=0 keeps every row in .lore/data (no live session)", env)
			}
		})
	}
	if got := h.wrap("northwind", inner); got != store.Backend(inner) {
		t.Fatal("only the repo store is wrapped")
	}
}

// TestLiveBackend_MetaKeyRidesLiveAsCompanion (PASS3-A-1, METADATA 3.10): a
// metadata key row registered before a live write rides live as a companion
// of the content row that uses it.
func TestLiveBackend_MetaKeyRidesLiveAsCompanion(t *testing.T) {
	f := newLiveFixture(t)
	ctx := f.ctx
	k := ltRow(t, schema.TableMetaKeys, ltMkyID, 1, "newkey", false)
	if err := f.b.Write(ctx, []store.Change{{Row: k}}); err != nil {
		t.Fatal(err)
	}
	if f.liveHas(t, schema.TableMetaKeys, ltMkyID) {
		t.Fatal("a held key row is not written before a content row carries it")
	}
	if got, err := f.b.Get(ctx, k.Table, k.ID); err != nil || got.HLC.Compare(k.HLC) != 0 {
		t.Fatalf("a held key row reads back within the command: %v", err)
	}
	if err := f.b.Write(ctx, []store.Change{{Row: ltRow(t, schema.TableTasks, ltTaskA, 2, "uses newkey", false)}}); err != nil {
		t.Fatal(err)
	}
	_, files, err := f.b.liveFiles(ctx, f.s)
	if err != nil {
		t.Fatal(err)
	}
	lf, ok := files[live.RowKey{Table: schema.TableMetaKeys, ID: ltMkyID}]
	if !ok || !lf.Block.Companion {
		t.Fatalf("the key row rides live with the task, marked companion: present=%v companion=%v", ok, lf.Block.Companion)
	}
	if _, err = f.inner.Get(ctx, k.Table, k.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("the key row is not in the branch data: %v", err)
	}
	if len(f.s.companions) != 0 {
		t.Fatal("a carried companion is no longer held")
	}
}

// TestLiveBackend_LoneMetaKeyFlushesToBranch: a key row no content row
// carried lands in the branch data at the end of the run, never live.
func TestLiveBackend_LoneMetaKeyFlushesToBranch(t *testing.T) {
	f := newLiveFixture(t)
	ctx := f.ctx
	k := ltRow(t, schema.TableMetaKeys, ltMkyID, 1, "newkey", false)
	if err := f.b.Write(ctx, []store.Change{{Row: k}}); err != nil {
		t.Fatal(err)
	}
	if err := f.s.flushCompanions(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := f.inner.Get(ctx, k.Table, k.ID); err != nil {
		t.Fatalf("a key row no content row carried lands in the branch data: %v", err)
	}
	if f.liveHas(t, schema.TableMetaKeys, ltMkyID) {
		t.Fatal("a lone key row never goes live")
	}
}

// TestWithoutLoneSupporting: a companion row never lands alone; with a
// content row, or written live on purpose (actor register, Q12), it lands.
func TestWithoutLoneSupporting(t *testing.T) {
	comp := live.LandItem{Key: live.RowKey{Table: "actors", ID: "act_1"}, File: live.File{Block: live.Block{Companion: true}}}
	task := live.LandItem{Key: live.RowKey{Table: "tasks", ID: "tsk_1"}}
	onPurpose := live.LandItem{Key: live.RowKey{Table: "actors", ID: "act_2"}}
	if got := withoutLoneSupporting([]live.LandItem{comp}); len(got) != 0 {
		t.Fatalf("a lone companion must not land: %v", got)
	}
	if got := withoutLoneSupporting([]live.LandItem{comp, task}); len(got) != 2 {
		t.Fatalf("a companion lands with its content row: %v", got)
	}
	if got := withoutLoneSupporting([]live.LandItem{onPurpose}); len(got) != 1 {
		t.Fatalf("an actor written live on purpose lands: %v", got)
	}
}

// TestLandLines_NameRealBranch: every landing line names the branch the
// pass landed on (a repo whose default branch is not main once read
// "landed on main"); a zero outcome never claims main either.
func TestLandLines_NameRealBranch(t *testing.T) {
	const branch = "trunk"
	for _, st := range []live.LandStatus{live.LandDone, live.LandProtected, live.LandRetriesOut, live.LandRefused} {
		for _, left := range []int{0, 2} {
			got := strings.Join(landLines(landOutcome{Branch: branch, Status: st, Commit: "0123456789abcdef", Detail: "hook said no", Left: left}), "\n")
			if !strings.Contains(got, branch) {
				t.Errorf("status %s left %d: %q does not name %q", st, left, got, branch)
			}
			if strings.Contains(got, "main") {
				t.Errorf("status %s left %d: %q names main, but the branch is %q", st, left, got, branch)
			}
		}
	}
	if got := strings.Join(landLines(landOutcome{Status: live.LandDone, Commit: "0123456789abcdef", Left: 1}), "\n"); strings.Contains(got, "main") {
		t.Errorf("zero-branch outcome %q names main", got)
	}
}

// TestRideBranchSkipList (F6): no ride commit on a detached HEAD, on the
// default branch, or while a rebase / cherry-pick / revert / merge / bisect
// is in progress.
func TestRideBranchSkipList(t *testing.T) {
	testenv.SetHome(t, t.TempDir())
	ctx := context.Background()
	dir := t.TempDir()
	gitT(t, dir, "init", "-q", "-b", "main")
	gitT(t, dir, "commit", "-q", "--allow-empty", "-m", "init")
	repo, err := gitref.Open(ctx, dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	sess := func() *liveSession {
		return &liveSession{repo: repo, ref: &live.Ref{Repo: repo, RemoteName: "origin"}}
	}
	if _, ok := sess().rideBranch(ctx); ok {
		t.Fatal("no ride on the default branch")
	}
	gitT(t, dir, "checkout", "-q", "-b", "feat/x")
	if b, ok := sess().rideBranch(ctx); !ok || b != "feat/x" {
		t.Fatalf("a feature branch rides: %q %v", b, ok)
	}
	gitDir := filepath.Join(dir, ".git")
	for _, m := range rideInProgressMarkers {
		p := filepath.Join(gitDir, m)
		if err := os.WriteFile(p, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		if _, ok := sess().rideBranch(ctx); ok {
			t.Fatalf("no ride while %s exists", m)
		}
		if err := os.Remove(p); err != nil {
			t.Fatal(err)
		}
	}
	gitT(t, dir, "checkout", "-q", "--detach")
	if _, ok := sess().rideBranch(ctx); ok {
		t.Fatal("no ride on a detached HEAD")
	}
}

// badPurgedLists are _live/purged.json bodies that cannot be read as a set
// of ids. Each must stop every action that would otherwise treat the set as
// empty (and bring purged rows back).
var badPurgedLists = map[string]string{
	"not json":           `{"ids":[`,
	"ids is a string":    `{"ids":"mem_1"}`,
	"ids is an object":   `{"ids":{"mem_1":true}}`,
	"ids holds a number": `{"ids":["mem_1",7]}`,
	"top level is list":  `["mem_1"]`,
}

// liveTestRow returns a live tree file for one memories row.
func liveTestRow(t *testing.T, id string) []byte {
	t.Helper()
	f := live.NewVersion(nil, canonjson.Object{"_v": canonjson.String("1"), "_table": canonjson.String("memories"), "id": canonjson.String(id), "body": canonjson.String("x")}, time.Unix(1, 0).UTC(), "act_1")
	b, err := f.Encode()
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// TestRestorableTreeStopsOnAnUnreadablePurgeList: `lore live restore`
// fails closed; the purge list is checked before anything else is read.
func TestRestorableTreeStopsOnAnUnreadablePurgeList(t *testing.T) {
	for name, purged := range badPurgedLists {
		t.Run(name, func(t *testing.T) {
			tree := live.Tree{
				live.PurgedPath: []byte(purged),
				live.RowPath("acme", "memories", "mem_1"): liveTestRow(t, "mem_1"),
			}
			// The session has no ref or repo: reaching past the purge list
			// would panic, so the order is part of the check.
			keep, dropped, err := (&liveSession{}).restorableTree(context.Background(), tree)
			if err == nil || keep != 0 || dropped != nil {
				t.Fatalf("want an error and nothing to restore, got keep=%d dropped=%v err=%v", keep, dropped, err)
			}
		})
	}
}

// TestWhyMissingOnLiveReportsAnUnreadablePurgeList: the not-found
// explanation names a purged id, says nothing for an unlisted one, and
// reports an unreadable purge list instead of saying nothing.
func TestWhyMissingOnLiveReportsAnUnreadablePurgeList(t *testing.T) {
	cases := map[string]struct {
		purged string // "" = no purge list on the live ref
		want   string // "" = no explanation
	}{
		"purged":     {`{"ids":["mem_1"]}`, "deleted on purpose"},
		"not purged": {`{"ids":["mem_2"]}`, ""},
		"no list":    {"", ""},
		"unreadable": {`{"ids":"mem_1"}`, "could not be read"},
		"not json":   {`{`, "could not be read"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			f := newLiveFixture(t)
			// One row so the live ref exists in every case.
			req := live.WriteRequest{Folder: live.RootFolder, BinaryVersion: "0.1.18", Raw: map[string][]byte{
				live.RowPath(live.RootFolder, "memories", "mem_9"): liveTestRow(t, "mem_9"),
			}}
			if c.purged != "" {
				req.Raw[live.PurgedPath] = []byte(c.purged)
			}
			if _, err := f.s.ref.Write(f.ctx, req); err != nil {
				t.Fatal(err)
			}
			got := f.s.whyMissing(f.ctx, live.RootFolder, "mem_1")
			if c.want == "" && got != "" || c.want != "" && !strings.Contains(got, c.want) {
				t.Fatalf("got %q want %q", got, c.want)
			}
		})
	}
}

// TestMainPurgedRefusesAnUnreadableList: main's _purged.json decides what
// landing leaves out; when it cannot be read the landing plan must stop,
// never treat main as having purged nothing. A ref that does not exist yet
// holds no purge list.
func TestMainPurgedRefusesAnUnreadableList(t *testing.T) {
	cases := map[string]struct {
		body    string // "" = file absent
		wantErr bool
		want    []string
	}{
		"absent":           {"", false, nil},
		"valid":            {`{"ids":["mem_1","mem_2"],"_v":1}`, false, []string{"mem_1", "mem_2"}},
		"non-string entry": {`{"ids":["mem_1",3],"_v":1}`, false, []string{"mem_1"}},
		"ids is a string":  {`{"ids":"mem_1","_v":1}`, true, nil},
		"ids is an object": {`{"ids":{"mem_1":true},"_v":1}`, true, nil},
		"not json":         {`{"ids":[`, true, nil},
		// P3-LIVE-2: fail closed on the format version, as v0.1.17 did.
		"no version":      {`{"ids":["mem_1"]}`, true, nil},
		"version is text": {`{"ids":["mem_1"],"_v":"1"}`, true, nil},
		"version zero":    {`{"ids":["mem_1"],"_v":0}`, true, nil},
		"newer version":   {`{"ids":["mem_1"],"_v":2}`, true, nil},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			testenv.SetHome(t, t.TempDir())
			ctx := context.Background()
			dir := t.TempDir()
			gitT(t, dir, "init", "-q", "-b", "main")
			p := mainDataPath(live.RootFolder, purgedFileName)
			if c.body != "" {
				full := filepath.Join(dir, filepath.FromSlash(p))
				if err := os.MkdirAll(filepath.Dir(full), 0o750); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(full, []byte(c.body), 0o600); err != nil {
					t.Fatal(err)
				}
				gitT(t, dir, "add", "-A")
			}
			gitT(t, dir, "commit", "-q", "--allow-empty", "-m", "main")
			repo, err := gitref.Open(ctx, dir, nil)
			if err != nil {
				t.Fatal(err)
			}
			s := &liveSession{repo: repo}
			if none, nerr := s.mainPurged(ctx, "refs/remotes/origin/main", live.RootFolder); nerr != nil || len(none) != 0 {
				t.Fatalf("missing ref: %v %v", none, nerr)
			}
			set, err := s.mainPurged(ctx, "HEAD", live.RootFolder)
			if c.wantErr {
				if err == nil {
					t.Fatalf("want an error, got %v", set)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if len(set) != len(c.want) {
				t.Fatalf("got %v want %v", set, c.want)
			}
			for _, id := range c.want {
				if !set[id] {
					t.Fatalf("got %v want %v", set, c.want)
				}
			}
		})
	}
}

// TestLiveDoctorLines_NameRealBranch: the doctor's live lines name the
// repo's real default branch (config default_branch: trunk). A repo whose
// default branch is not main once read "copied to main" and "a protected
// main" while its rows landed elsewhere.
func TestLiveDoctorLines_NameRealBranch(t *testing.T) {
	const branch = "trunk"
	clk := clock.NewFake(ltBase)
	f := newLiveFixtureWith(t, "default_branch: "+branch+"\nlive:\n  max_age: 1h\n", clk)
	done := ltRow(t, schema.TableTasks, ltTaskA, 1, "finished work", false)
	if err := f.b.Write(f.ctx, []store.Change{{Row: done}}); err != nil {
		t.Fatal(err)
	}
	// The row is past live.max_age, and this machine was refused as a
	// protected branch (the "waiting" line then names the ride).
	clk.Advance(2 * time.Hour)
	if err := os.WriteFile(filepath.Join(f.s.stateDir, liveNoteProtected), []byte("protected"), 0o600); err != nil {
		t.Fatal(err)
	}
	base, err := liveDoctor{h: f.s.h}.Base(f.ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	var lines []string
	for _, l := range append(append([]string{}, base.Notes...), base.Warnings...) {
		if strings.HasPrefix(l, liveDoctorPrefix) && strings.Contains(l, "row(s)") {
			lines = append(lines, l)
		}
	}
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "exist only in live") || !strings.Contains(joined, "older than live.max_age") {
		t.Fatalf("want the only-in-live and older-than-max_age lines, got notes %q warnings %q", base.Notes, base.Warnings)
	}
	for _, l := range lines {
		if !strings.Contains(l, branch) {
			t.Errorf("%q does not name %q", l, branch)
		}
		if strings.Contains(l, "main") {
			t.Errorf("%q names main, but the default branch is %q", l, branch)
		}
	}
}

// TestLiveBackend_UnreadableLiveRowIsAnError: an undecodable row file on
// the live ref is never treated as absent (it would let a create or a
// stale branch copy win over the live row): Get and Read fail.
func TestLiveBackend_UnreadableLiveRowIsAnError(t *testing.T) {
	f := newLiveFixture(t)
	req := live.WriteRequest{Folder: live.RootFolder, BinaryVersion: "0.1.18", Raw: map[string][]byte{
		live.RowPath(live.RootFolder, schema.TableTasks, ltTaskA): []byte(`{not a row`),
	}}
	if _, err := f.s.ref.Write(f.ctx, req); err != nil {
		t.Fatal(err)
	}
	a := ltRow(t, schema.TableTasks, ltTaskA, 1, "x", false)
	if _, err := f.b.Get(f.ctx, a.Table, a.ID); err == nil || errors.Is(err, store.ErrNotFound) {
		t.Fatalf("Get of an unreadable live row must fail, not report not-found: %v", err)
	}
	if _, _, err := f.b.Read(f.ctx, ""); err == nil {
		t.Fatal("Read must fail on an unreadable live row")
	}
	if err := f.b.Write(f.ctx, []store.Change{{Row: a}}); err == nil {
		t.Fatal("a create over an unreadable live row must fail")
	}
}

// TestLiveBackend_ReadThroughEditMaterialises (t-old2, F1): on the default
// branch, a memory that is on the fetched origin/main but not in this
// checkout (its delete committed here, or not pulled yet) is read through;
// an edit that expects exactly that landed copy writes the branch file
// (Expect "none" on the branch) instead of E_STALE, and an edit from any
// other version stays stale.
func TestLiveBackend_ReadThroughEditMaterialises(t *testing.T) {
	f := newLiveFixture(t)
	dir := f.b.top
	m := ltRow(t, schema.TableMemories, ltMemory, 1, "keep me around", false)
	rel := mainDataPath(live.RootFolder, schema.TableMemories+"/"+ltMemory+rowFileExt)
	full := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, m.Doc, 0o600); err != nil {
		t.Fatal(err)
	}
	gitT(t, dir, "add", "-A")
	gitT(t, dir, "commit", "-q", "-m", "memory")
	gitT(t, dir, "update-ref", "refs/remotes/origin/main", "HEAD")
	gitT(t, dir, "rm", "-q", rel)
	gitT(t, dir, "commit", "-q", "-m", "rm")
	got, err := f.b.Get(f.ctx, m.Table, m.ID)
	if err != nil || got.HLC.Compare(m.HLC) != 0 {
		t.Fatalf("the landed copy is read through: %v", err)
	}
	stale := ltRow(t, schema.TableMemories, ltMemory, 3, "stale edit", false)
	if err := f.b.Write(f.ctx, []store.Change{{Row: stale, Expect: ltRow(t, schema.TableMemories, ltMemory, 9, "x", false).HLC}}); !errors.Is(err, store.ErrStale) {
		t.Fatalf("an edit from another version stays stale: %v", err)
	}
	edit := ltRow(t, schema.TableMemories, ltMemory, 2, "archived", false)
	if err := f.b.Write(f.ctx, []store.Change{{Row: edit, Expect: got.HLC}}); err != nil {
		t.Fatalf("an edit of the read-through copy writes, not E_STALE: %v", err)
	}
	if br, err := f.inner.Get(f.ctx, m.Table, m.ID); err != nil || br.HLC.Compare(edit.HLC) != 0 {
		t.Fatalf("the edit materialises the row in the branch data: %v", err)
	}
	if f.liveHas(t, schema.TableMemories, ltMemory) {
		t.Fatal("a memory edit stays with the code")
	}
}
