package main

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/store"

	"saas/pkg/aicoder/live"
)

// fakeStoreBackend records calls; Sync answers rep/err.
type fakeStoreBackend struct {
	caps     store.Capabilities
	writeErr error
	rep      store.SyncReport
	syncErr  error
	writes   int
	syncs    int
	dir      string
}

func (f *fakeStoreBackend) Read(context.Context, store.Cursor) ([]store.Row, store.Cursor, error) {
	return nil, "", nil
}

func (f *fakeStoreBackend) Get(context.Context, row.Table, ids.AnyID) (store.Row, error) {
	return store.Row{}, store.ErrNotFound
}

func (f *fakeStoreBackend) Write(context.Context, []store.Change) error {
	f.writes++
	return f.writeErr
}

func (f *fakeStoreBackend) Sync(context.Context) (store.SyncReport, error) {
	f.syncs++
	return f.rep, f.syncErr
}
func (f *fakeStoreBackend) Capabilities() store.Capabilities { return f.caps }
func (f *fakeStoreBackend) Close() error                     { return nil }
func (f *fakeStoreBackend) Dir() string                      { return f.dir }

// TestWrapStorePushOnlyRemoteStores: only a non-repo store that talks to a
// remote is wrapped; the repo store and a local folder store are not, and
// the wrapper unwraps to the backend it got.
func TestWrapStorePushOnlyRemoteStores(t *testing.T) {
	remote := &fakeStoreBackend{caps: store.Capabilities{Remote: true}, dir: "/x/clone/work"}
	local := &fakeStoreBackend{}
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
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := wrapStorePush(tc.src, tc.b)
			sb, ok := got.(*storePushBackend)
			if ok != tc.wrap {
				t.Fatalf("wrapped = %v, want %v", ok, tc.wrap)
			}
			if !ok {
				if got != tc.b {
					t.Fatal("an unwrapped store must be returned as is")
				}
				return
			}
			if sb.Unwrap() != tc.b || sb.stateDir != "/x/clone" {
				t.Fatalf("unwrap/stateDir wrong: %q", sb.stateDir)
			}
		})
	}
}

// TestStorePushWrite is the write matrix: a refused write is the error and
// never pushes; a committed write pushes when autopush is on, and a failed,
// offline or refused push is a note that names `lore sync`, never an error
// (the commit exists; LORE_TEAM_SPEC §3.2 / STORE_SERVER_SPEC 6.1).
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
		{"remote refused rows: note", true, nil, store.SyncReport{Refused: []store.Refusal{{Reason: "x"}, {Reason: "y"}}}, nil, false, 1, "refused 2 row(s)"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := &fakeStoreBackend{caps: store.Capabilities{Remote: true}, writeErr: tc.writeErr, rep: tc.rep, syncErr: tc.syncErr}
			var notes bytes.Buffer
			b := &storePushBackend{inner: f, name: "northwind", autopush: func(context.Context) bool { return tc.autopush }, notes: &notes}
			err := b.Write(context.Background(), nil)
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

// TestStorePushRefresh: the first read of a command syncs the store when
// live.ShouldFetch says so, at most once per command, and stamps only a
// sync that reached the remote; reads never fail on an unreachable store.
func TestStorePushRefresh(t *testing.T) {
	now := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name      string
		autopush  bool
		kind      live.FetchKind
		stampAgo  time.Duration // 0 = no stamp
		rep       store.SyncReport
		syncErr   error
		wantSyncs int
		wantStamp bool
	}{
		{"never fetched", true, live.FetchCached, 0, store.SyncReport{}, nil, 1, true},
		{"fetched within ttl", true, live.FetchCached, 10 * time.Second, store.SyncReport{}, nil, 0, false},
		{"stamp older than ttl", true, live.FetchCached, 2 * time.Minute, store.SyncReport{}, nil, 1, true},
		{"stamp in the future (clock jumped back)", true, live.FetchCached, -time.Hour, store.SyncReport{}, nil, 1, true},
		{"autopush off: never on its own", false, live.FetchCached, 0, store.SyncReport{}, nil, 0, false},
		{"lore sync: always, even autopush off", false, live.FetchAlways, 10 * time.Second, store.SyncReport{}, nil, 1, true},
		{"offline: reads local, no stamp", true, live.FetchCached, 0, store.SyncReport{Offline: true}, nil, 1, false},
		{"sync error: reads local, no stamp", true, live.FetchCached, 0, store.SyncReport{}, errors.New("x"), 1, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if tc.stampAgo != 0 {
				if err := live.WriteFetchStamp(dir, now.Add(-tc.stampAgo)); err != nil {
					t.Fatal(err)
				}
			}
			before := live.FetchStamp(dir)
			f := &fakeStoreBackend{caps: store.Capabilities{Remote: true}, rep: tc.rep, syncErr: tc.syncErr}
			b := &storePushBackend{inner: f, name: "northwind", stateDir: dir, notes: &bytes.Buffer{},
				fetch: func(context.Context) live.FetchInputs {
					return live.FetchInputs{Now: now, TTL: time.Minute, Kind: tc.kind, Autopush: tc.autopush, HasRemote: true}
				}}
			ctx := context.Background()
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
			if tc.wantStamp && !after.Equal(now) {
				t.Fatalf("stamp = %v, want %v", after, now)
			}
		})
	}
}
