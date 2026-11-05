package sqlite

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/model/hlc"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/store"
	"github.com/thesatellite-ai/lore/store/storetest"
)

// machineFixture is one machine store file per subtest; every Open sees
// the same rows (a read-only handle beside a read-write one).
type machineFixture struct{ path string }

func (f machineFixture) Open(t *testing.T, opts store.OpenOptions) store.Backend {
	t.Helper()
	m, err := OpenMachineStore(context.Background(), f.path, opts.ReadOnly)
	if err != nil {
		t.Fatalf("open machine store: %v", err)
	}
	return m
}

// TestMachineStoreContract: the machine store passes the backend contract
// every store passes (P3-LOCAL-1).
func TestMachineStoreContract(t *testing.T) {
	storetest.Run(t, func(t *testing.T) storetest.Fixture {
		return machineFixture{path: filepath.Join(t.TempDir(), "state", "machine.db")}
	})
}

// TestMachineStorePurgeAndNoRemote: purged ids are kept with the batch, a
// reopened file still has them, and Sync never moves anything.
func TestMachineStorePurgeAndNoRemote(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "machine.db")
	m, err := OpenMachineStore(ctx, path, false)
	if err != nil {
		t.Fatal(err)
	}
	id, err := ids.ParseAny("mem_0190a1b2c3d47e5f8718293a4b5c6d7e")
	if err != nil {
		t.Fatal(err)
	}
	actor, err := ids.Parse[ids.Actor]("act_0190a1b2c3d47e5f8718293a4b5c6d7f")
	if err != nil {
		t.Fatal(err)
	}
	h := hlc.Next(hlc.HLC{}, time.Unix(1_800_000_000, 0).UTC(), actor)
	r, err := store.FromRow(row.Row{Table: "memories", ID: id, HLC: h, CreatedBy: actor, UpdatedBy: actor, CreatedAt: h.Wall, UpdatedAt: h.Wall, Deleted: true})
	if err != nil {
		t.Fatal(err)
	}
	if err = m.WritePurge(ctx, []store.Change{{Row: r}}, []ids.AnyID{id}); err != nil {
		t.Fatalf("WritePurge: %v", err)
	}
	rep, err := m.Sync(ctx)
	if err != nil || rep.Sent != 0 || rep.Received != 0 {
		t.Fatalf("Sync = %+v, %v; want nothing moved", rep, err)
	}
	if err = m.Close(); err != nil {
		t.Fatal(err)
	}
	m2, err := OpenMachineStore(ctx, path, false)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = m2.Close() }()
	got, err := m2.Purged(ctx)
	if err != nil || !got[id.String()] {
		t.Fatalf("Purged after reopen = %v, %v; want %s", got, err, id)
	}
	if _, err = m2.Get(ctx, "memories", id); err != nil {
		t.Fatalf("tombstone lost: %v", err)
	}
}
