package wire

// Port of saas/cmd/cli store_push_core_test.go and store_push_once_test.go
// (B11) to cmd/lore's org store push (storepush_standalone.go): only a
// remote store clone is wrapped; a write is committed then pushed at once
// (LORE_TEAM_SPEC §3.2), a failed, offline or refused push is one note per
// command naming `lore sync`, never an error (FLAG4); the first read of a
// command fetches when live.fetch_ttl says so and stamps only a sync that
// reached the remote.

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	gitbackend "github.com/thesatellite-ai/lore/adapters/backend/git"
	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/adapters/live"
	"github.com/thesatellite-ai/lore/logic/orgconf"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/store"
)

// fakePushBackend records calls; Sync answers rep/err.
type fakePushBackend struct {
	caps     store.Capabilities
	writeErr error
	rep      store.SyncReport
	syncErr  error
	writes   int
	syncs    int
	dir      string
}

func (f *fakePushBackend) Read(context.Context, store.Cursor) ([]store.Row, store.Cursor, error) {
	return nil, "", nil
}

func (f *fakePushBackend) Get(context.Context, row.Table, ids.AnyID) (store.Row, error) {
	return store.Row{}, store.ErrNotFound
}

func (f *fakePushBackend) Write(context.Context, []store.Change) error {
	f.writes++
	return f.writeErr
}

func (f *fakePushBackend) Sync(context.Context) (store.SyncReport, error) {
	f.syncs++
	return f.rep, f.syncErr
}
func (f *fakePushBackend) Capabilities() store.Capabilities { return f.caps }
func (f *fakePushBackend) Close() error                     { return nil }
func (f *fakePushBackend) Dir() string                      { return f.dir }

// pushRes is an org resolution with no config: autopush on, the default
// fetch ttl.
func pushRes(t *testing.T) *OrgResolution {
	t.Helper()
	return NewOrgResolution(t.TempDir(), t.TempDir(), nil)
}

func TestStorePushWrapsOnlyRemoteStores(t *testing.T) {
	t.Parallel()
	remote := &fakePushBackend{caps: store.Capabilities{Remote: true}, dir: "/x/stores/acme/notes"}
	local := &fakePushBackend{}
	cases := []struct {
		name string
		src  store.Source
		b    store.Backend
		wrap bool
	}{
		{"org store clone", "northwind", remote, true},
		{"store folder target", "northwind/acme", remote, true},
		{"repo store, even with a remote", store.SourceRepo, remote, false},
		{"local folder store", "notes", local, false},
	}
	wrap := storePushWrap(pushRes(t))
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := wrap(background(), tc.src, "", tc.b)
			sp, ok := got.(*storePush)
			if ok != tc.wrap {
				t.Fatalf("wrapped = %v, want %v", ok, tc.wrap)
			}
			if !ok {
				if got != tc.b {
					t.Fatal("an unwrapped store must be returned as is")
				}
				return
			}
			if sp.Unwrap() != tc.b || sp.stateDir != filepath.FromSlash("/x/stores/acme/notes/.git/lore") {
				t.Fatalf("unwrap/stateDir wrong: %q", sp.stateDir)
			}
		})
	}
}

func TestStorePushWrite(t *testing.T) {
	boom := errors.New("boom")
	cases := []struct {
		name      string
		autopush  bool
		writeErr  error
		rep       store.SyncReport
		syncErr   error
		wantErr   bool
		wantSyncs int
		wantNote  string
	}{
		{"pushed at once", true, nil, store.SyncReport{Sent: 1}, nil, false, 1, ""},
		{"autopush off: commit waits silently", false, nil, store.SyncReport{}, nil, false, 0, ""},
		{"write refused: error, no push", true, boom, store.SyncReport{}, nil, true, 0, ""},
		{"write refused, autopush off", false, boom, store.SyncReport{}, nil, true, 0, ""},
		{"push error: note", true, nil, store.SyncReport{}, boom, false, 1, "pushing failed"},
		{"offline: note", true, nil, store.SyncReport{Offline: true}, nil, false, 1, "unreachable"},
		{"remote refused rows: note", true, nil, store.SyncReport{Refused: []store.Refusal{{Reason: "x"}, {Reason: "y"}}}, nil, false, 1, "refused the push (`lore sync status`"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if !tc.autopush {
				t.Setenv(envAutopushSwitch, autopushOff)
			}
			f := &fakePushBackend{caps: store.Capabilities{Remote: true}, writeErr: tc.writeErr, rep: tc.rep, syncErr: tc.syncErr}
			var notes bytes.Buffer
			b := &storePush{inner: f, name: "northwind", res: pushRes(t), notes: &notes}
			err := b.Write(background(), nil)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tc.wantErr)
			}
			if f.writes != 1 || f.syncs != tc.wantSyncs {
				t.Fatalf("writes=%d syncs=%d, want 1/%d", f.writes, f.syncs, tc.wantSyncs)
			}
			got := notes.String()
			if tc.wantNote == "" {
				if got != "" {
					t.Fatalf("unexpected note %q", got)
				}
				return
			}
			for _, w := range []string{tc.wantNote, "northwind", "lore sync"} {
				if !strings.Contains(got, w) {
					t.Fatalf("note %q lacks %q", got, w)
				}
			}
		})
	}
}

// TestStorePushNoteOnce (FLAG4): one command that writes twice to a store
// whose push fails prints the note once and does not retry the push.
func TestStorePushNoteOnce(t *testing.T) {
	t.Parallel()
	f := &fakePushBackend{caps: store.Capabilities{Remote: true}, syncErr: errors.New("boom")}
	var notes bytes.Buffer
	b := &storePush{inner: f, name: "agency", res: pushRes(t), notes: &notes}
	for range 2 {
		if err := b.Write(background(), nil); err != nil {
			t.Fatal(err)
		}
	}
	if f.writes != 2 || f.syncs != 1 {
		t.Fatalf("writes=%d syncs=%d, want 2/1", f.writes, f.syncs)
	}
	if n := strings.Count(notes.String(), "pushing failed"); n != 1 {
		t.Fatalf("note printed %d times:\n%s", n, notes.String())
	}
}

func TestStorePushRefresh(t *testing.T) {
	cases := []struct {
		name      string
		autopush  bool
		stampAgo  time.Duration // 0 = no stamp
		rep       store.SyncReport
		syncErr   error
		wantSyncs int
		wantStamp bool
	}{
		{"never fetched", true, 0, store.SyncReport{}, nil, 1, true},
		{"fetched within ttl", true, 10 * time.Second, store.SyncReport{}, nil, 0, false},
		{"stamp older than ttl", true, orgconf.DefaultFetchTTL + time.Minute, store.SyncReport{}, nil, 1, true},
		{"stamp in the future (clock jumped back)", true, -time.Hour, store.SyncReport{}, nil, 1, true},
		{"autopush off: never on its own", false, 0, store.SyncReport{}, nil, 0, false},
		{"offline: reads local, no stamp", true, 0, store.SyncReport{Offline: true}, nil, 1, false},
		{"sync error: reads local, no stamp", true, 0, store.SyncReport{}, errors.New("x"), 1, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if !tc.autopush {
				t.Setenv(envAutopushSwitch, autopushOff)
			}
			dir := t.TempDir()
			if tc.stampAgo != 0 {
				if err := live.WriteFetchStamp(dir, time.Now().Add(-tc.stampAgo)); err != nil {
					t.Fatal(err)
				}
			}
			before := live.FetchStamp(dir)
			f := &fakePushBackend{caps: store.Capabilities{Remote: true}, rep: tc.rep, syncErr: tc.syncErr}
			b := &storePush{inner: f, name: "northwind", res: pushRes(t), stateDir: dir, notes: &bytes.Buffer{}}
			ctx := background()
			start := time.Now()
			if _, _, err := b.Read(ctx, ""); err != nil {
				t.Fatal(err)
			}
			if _, err := b.Get(ctx, "tasks", ids.AnyID{}); !errors.Is(err, store.ErrNotFound) {
				t.Fatalf("get passes the inner answer through: %v", err)
			}
			if _, _, err := b.Read(ctx, ""); err != nil {
				t.Fatal(err)
			}
			if f.syncs != tc.wantSyncs {
				t.Fatalf("syncs = %d, want %d (once per command at most)", f.syncs, tc.wantSyncs)
			}
			after := live.FetchStamp(dir)
			if stamped := !after.Equal(before); stamped != tc.wantStamp {
				t.Fatalf("stamped = %v, want %v (before %v after %v)", stamped, tc.wantStamp, before, after)
			}
			if tc.wantStamp && (after.Before(start.Add(-time.Second)) || after.After(time.Now().Add(time.Second))) {
				t.Fatalf("stamp = %v, want this command's time", after)
			}
		})
	}
}

// TestStorePushRefreshMergesFetchedCommits: a clone whose fetch stamp is
// fresh but whose tracking ref holds a teammate's commit its branch never
// merged (the fetch moved only the tracking ref: a plain `git fetch` in the
// clone, or another reader that fetches the same clone and stamps it)
// syncs at the command's first read, so the teammate's row is found.
// Before, the fresh stamp skipped the sync and a teammate's task was "no
// such task" until the stamp aged past live.fetch_ttl (team docs example
// team_ops: bob's `lore task claim` of alice's task). Break-verified by
// dropping the hasUnpulled check in refresh (the Get answers not found).
func TestStorePushRefreshMergesFetchedCommits(t *testing.T) {
	t.Parallel()
	ctx := background()
	root := t.TempDir()
	bare := filepath.Join(root, "notes.git")
	seed := filepath.Join(root, "seed")
	spGit(t, root, "init", "-q", "--bare", "-b", "main", bare)
	spGit(t, root, "init", "-q", "-b", "main", seed)
	id, err := ids.New[ids.Store]()
	if err != nil {
		t.Fatal(err)
	}
	marker, err := rowfs.Marker{ID: id}.Encode()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(seed, rowfs.MarkerFile), marker, 0o600); err != nil {
		t.Fatal(err)
	}
	spGit(t, seed, "add", rowfs.MarkerFile)
	spGit(t, seed, "commit", "-q", "-m", "init")
	spGit(t, seed, "push", "-q", bare, "main")
	open := func(name string) *gitbackend.Backend {
		b, err := gitbackend.OpenClone(ctx, bare, filepath.Join(root, name, "clones", "host", "notes"), rowfs.FolderRoot, store.OpenOptions{})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = b.Close() })
		return b
	}
	alice, bob := open("alice"), open("bob")
	r := cloneTestRow(t)
	if err := alice.Write(ctx, []store.Change{{Row: r}}); err != nil {
		t.Fatal(err)
	}
	if _, err := alice.Sync(ctx); err != nil {
		t.Fatal(err)
	}
	// bob's clone fetches alice's commit into its tracking ref only, and
	// its stamp says it fetched just now.
	spGit(t, bob.Dir(), "fetch", "-q", "origin")
	state := t.TempDir()
	if err := live.WriteFetchStamp(state, time.Now()); err != nil {
		t.Fatal(err)
	}
	b := &storePush{inner: bob, name: "notes", res: pushRes(t), stateDir: state, notes: &bytes.Buffer{}}
	if _, err := b.Get(ctx, r.Table, r.ID); err != nil {
		t.Fatalf("alice's row, fetched but not merged, was not read: %v", err)
	}
	if n, err := bob.Unpulled(ctx); err != nil || n != 0 {
		t.Fatalf("after the read bob's clone is still %d commit(s) behind (%v)", n, err)
	}
}

// TestStorePushFailureKeepsStamp (item 8, STORE layout 2026-11-12): a
// push that fails leaves the fetch stamp as it was — it is the clone's one
// "last fetched" (.git/lore/fetch-stamp), which the org link index's
// max-age rule reads too, so clearing it made a mere offline push turn the
// link status unknown — and says the next command pushes (refresh retries
// while commits wait, whatever the stamp); a push that lands keeps it.
func TestStorePushFailureKeepsStamp(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		rep    store.SyncReport
		err    error
		failed bool
	}{
		{"pushed", store.SyncReport{Sent: 1}, nil, false},
		{"offline", store.SyncReport{Offline: true}, nil, true},
		{"error", store.SyncReport{}, errors.New("boom"), true},
		{"refused", store.SyncReport{Refused: []store.Refusal{{Reason: "x"}}}, nil, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			if err := live.WriteFetchStamp(dir, time.Now()); err != nil {
				t.Fatal(err)
			}
			f := &fakePushBackend{caps: store.Capabilities{Remote: true}, rep: tc.rep, syncErr: tc.err}
			var notes bytes.Buffer
			b := &storePush{inner: f, name: "me", res: pushRes(t), notes: &notes, stateDir: dir, fetched: true}
			if err := b.Write(background(), nil); err != nil {
				t.Fatal(err)
			}
			if live.FetchStamp(dir).IsZero() {
				t.Fatal("the push cleared the clone's fetch stamp")
			}
			if tc.failed && !strings.Contains(notes.String(), "run `lore sync` when the store is reachable (or the next lore write to it pushes it; a read never pushes)") {
				t.Fatalf("note %q", notes.String())
			}
		})
	}
}
