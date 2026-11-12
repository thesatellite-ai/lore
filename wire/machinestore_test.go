package wire

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/cache/sqlite"
	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/logic/refs"
	"github.com/thesatellite-ai/lore/logic/scan"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
	"github.com/thesatellite-ai/lore/store/memstore"
)

// tableTestMachine is a machine-local table of this test only (the real
// ones land with their nouns' schema defs, P3-LOCAL-3).
const tableTestMachine row.Table = "test_machine_rows"

// nopAudit drops every event.
type nopAudit struct{}

func (nopAudit) Record(context.Context, engine.Event) error { return nil }

// TestOpenMachineAttachesTheHomeFile: with a machine-local table in the
// registry openMachine attaches <home>/state/machine.db and its cache
// <home>/cache/machine-store.db; a row written through the engine lands in that
// file (seen again by a fresh open of it) and never in the repo store.
// Without a machine-local table nothing is opened (P3-LOCAL-2).
func TestOpenMachineAttachesTheHomeFile(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	home := t.TempDir()
	var shared []schema.TableDef // the built-ins without their machine-local tables
	for _, d := range schema.Builtin() {
		if !d.MachineLocal {
			shared = append(shared, d)
		}
	}
	plain, err := schema.NewRegistry(shared...)
	if err != nil {
		t.Fatal(err)
	}
	if o, cl, oerr := openMachine(ctx, home, plain, newCaches(), refs.Expander{}); oerr != nil || o != nil || cl != nil {
		t.Fatalf("openMachine without a machine-local table = %v, %v, %v; want nothing", o != nil, cl != nil, oerr)
	}
	if _, serr := os.Stat(filepath.Join(home, filepath.FromSlash(MachineStoreFile))); !os.IsNotExist(serr) {
		t.Fatalf("machine store file created without a machine-local table: %v", serr)
	}

	reg, err := plain.With(schema.Table(string(tableTestMachine), ids.PrefixSession, schema.MachineLocal(),
		schema.Field("tool", value.KindText, schema.Required())))
	if err != nil {
		t.Fatal(err)
	}
	c := newCaches()
	t.Cleanup(func() { _ = c.Close() })
	attachOpt, closer, err := openMachine(ctx, home, reg, c, refs.Expander{})
	if err != nil || attachOpt == nil || closer == nil {
		t.Fatalf("openMachine = %v, %v, %v", attachOpt != nil, closer != nil, err)
	}
	repoCache := filepath.Join(t.TempDir(), "repo.db")
	if err = c.open(ctx, store.SourceRepo, repoCache, reg, refs.Expander{}); err != nil {
		t.Fatal(err)
	}
	sc, err := scan.NewRegexScanner(nil)
	if err != nil {
		t.Fatal(err)
	}
	actor, err := ids.Parse[ids.Actor](testActor)
	if err != nil {
		t.Fatal(err)
	}
	repo := memstore.NewData()
	eng, err := engine.New(engine.WithStore(engine.Attached{Name: store.SourceRepo, Backend: repo.Open(store.OpenOptions{})}),
		attachOpt, engine.WithCache(c), engine.WithRegistry(reg), engine.WithScanner(sc), engine.WithActor(actor), engine.WithAudit(nopAudit{}))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = eng.Refresh(ctx, store.SourceMachine); err != nil {
		t.Fatalf("Refresh machine: %v", err)
	}
	tool, err := value.TextOf("codex")
	if err != nil {
		t.Fatal(err)
	}
	wr, err := eng.Create(ctx, store.SourceRepo, tableTestMachine, map[string]value.Value{"tool": tool})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err = closer.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(filepath.Join(home, cacheDir, machineCacheFile)); err != nil {
		t.Fatalf("machine cache file: %v", err)
	}
	again, err := sqlite.OpenMachineStore(ctx, filepath.Join(home, filepath.FromSlash(MachineStoreFile)), true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = again.Close() })
	if _, err = again.Get(ctx, tableTestMachine, wr.Row.ID); err != nil {
		t.Fatalf("row not in %s: %v", MachineStoreFile, err)
	}
	if _, err = repo.Open(store.OpenOptions{}).Get(ctx, tableTestMachine, wr.Row.ID); err == nil {
		t.Fatal("machine-local row written into the repo store")
	}
}
