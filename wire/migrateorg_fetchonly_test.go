package wire

import (
	"context"
	"sync"
	"testing"

	"github.com/thesatellite-ai/lore/engine/attach"
	"github.com/thesatellite-ai/lore/logic/fileuri"
	opsmigrate "github.com/thesatellite-ai/lore/ops/migrate"
	"github.com/thesatellite-ai/lore/store"
)

// syncSpy records whether a store sync was asked to push: a sync whose
// ctx lacks the fetch-only mark (opsmigrate.WithNoPush) sends the clone's
// waiting commits.
type syncSpy struct {
	store.Backend
	mu      sync.Mutex
	pushing int
}

func (s *syncSpy) Unwrap() store.Backend { return s.Backend }

func (s *syncSpy) Sync(ctx context.Context) (store.SyncReport, error) {
	if !opsmigrate.NoPushAsked(ctx) {
		s.mu.Lock()
		s.pushing++
		s.mu.Unlock()
	}
	return s.Backend.Sync(ctx)
}

// TestMigrateOrgSyncFetchesOnly (devco:D-521): `lore migrate run` brings
// each org store up to date before converting it with a fetch and merge
// only. Its earlier full sync pushed the commits already waiting in the
// store clone, so a run that had nothing to migrate moved the store's
// remote (only push and sync move a remote).
func TestMigrateOrgSyncFetchesOnly(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeV1Marker(t, dir)
	url := fileuri.URL(dir, "")
	hook := func(context.Context) ([]attach.Store, error) {
		return []attach.Store{{Name: "agency", URL: url, Folder: "acme"}}, nil
	}
	spy := &syncSpy{}
	a := openWith(t, gitRepo(t), Options{
		OrgStores:    hook,
		RepoIsClient: func(context.Context) (bool, error) { return false, nil },
		WrapBackend: func(_ context.Context, name store.Source, _ string, b store.Backend) store.Backend {
			if name != "agency" {
				return b
			}
			spy.Backend = b
			return spy
		},
	})
	withProject(t, a)
	if _, err := run(t, a, opsmigrate.OpRun, `{"no_push":true}`, true); err != nil {
		t.Fatal(err)
	}
	if spy.Backend == nil {
		t.Fatal("the org store was never opened")
	}
	if _, err := run(t, a, opsmigrate.OpRun, `{}`, true); err != nil {
		t.Fatal(err)
	}
	spy.mu.Lock()
	defer spy.mu.Unlock()
	if spy.pushing != 0 {
		t.Fatalf("migrate run synced the org store %d time(s) with a push before converting it; want fetch-only", spy.pushing)
	}
}
