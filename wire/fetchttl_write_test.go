package wire

import (
	"bytes"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/adapters/live"
	"github.com/thesatellite-ai/lore/logic/orgconf"
	"github.com/thesatellite-ai/lore/store"
)

// TestStoreFetchReadsReuseWritesRefetch (owner item M-4): a store clone
// fetched 2 minutes ago is fresh for a read (live.fetch_ttl defaults to
// 5 minutes, so reads stop going to the network on every command) and due
// for a writing command (orgconf.WriteFetchTTL, 60 s: a claim of a task a
// teammate just added must find it); a write within 60 s of the last fetch
// does not fetch either.
func TestStoreFetchReadsReuseWritesRefetch(t *testing.T) {
	for _, tc := range []struct {
		name      string
		write     bool
		stampAgo  time.Duration
		wantSyncs int
	}{
		{"read, fetched 2m ago", false, 2 * time.Minute, 0},
		{"read, fetched past the default ttl", false, orgconf.DefaultFetchTTL + time.Minute, 1},
		{"write, fetched 2m ago", true, 2 * time.Minute, 1},
		{"write, fetched 30s ago", true, 30 * time.Second, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := live.WriteFetchStamp(dir, time.Now().Add(-tc.stampAgo)); err != nil {
				t.Fatal(err)
			}
			f := &fakePushBackend{caps: store.Capabilities{Remote: true}}
			b := &storePush{inner: f, name: "northwind", res: pushRes(t), stateDir: dir, notes: &bytes.Buffer{}}
			ctx := background()
			if tc.write {
				ctx = WithWriteFetch(ctx)
			}
			if _, _, err := b.Read(ctx, ""); err != nil {
				t.Fatal(err)
			}
			if f.syncs != tc.wantSyncs {
				t.Fatalf("syncs = %d, want %d", f.syncs, tc.wantSyncs)
			}
		})
	}
}
