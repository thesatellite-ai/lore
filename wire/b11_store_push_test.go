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
	"strings"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/adapters/live"
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
	remote := &fakePushBackend{caps: store.Capabilities{Remote: true}, dir: "/x/clone/work"}
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
			if sp.Unwrap() != tc.b || sp.stateDir != "/x/clone" {
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
		{"stamp older than ttl", true, 2 * time.Minute, store.SyncReport{}, nil, 1, true},
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

// TestStorePushFailureClearsStamp (item 8): a push that fails clears the
// fetch stamp, so the next command's first read syncs (and pushes) at
// once; a push that lands keeps it.
func TestStorePushFailureClearsStamp(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		rep     store.SyncReport
		err     error
		cleared bool
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
			if got := live.FetchStamp(dir).IsZero(); got != tc.cleared {
				t.Fatalf("stamp cleared = %v, want %v", got, tc.cleared)
			}
			if tc.cleared && !strings.Contains(notes.String(), "the next lore command that reaches the store pushes it, or run `lore sync`") {
				t.Fatalf("note %q", notes.String())
			}
		})
	}
}
