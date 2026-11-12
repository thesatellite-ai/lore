package wire

import (
	"context"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/adapters/orgcfg"
	"github.com/thesatellite-ai/lore/engine/attach"
	"github.com/thesatellite-ai/lore/model/hlc"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	opsorgcmd "github.com/thesatellite-ai/lore/ops/orgcmd"
	opsstores "github.com/thesatellite-ai/lore/ops/stores"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
	"github.com/thesatellite-ai/lore/wire/internal/isolate"
)

// storePurger is the git backend's purge write (engine.Purger's store
// half): tombstones plus the folder's _purged.json.
type storePurger interface {
	WritePurge(ctx context.Context, changes []store.Change, purged []ids.AnyID) error
}

// TestEveryStoreWriteKeepsALoreStore (item 32b, property style): every
// writer lore has into a store — org create (marker, README, org folder,
// lore-org.yaml, roster rows), store folder add, org link and unlink
// (_meta.json links), a row of every built-in table in the org folder, a
// project folder and the store's own folder, a tombstone, a purge
// (_purged.json) — leaves a tree
// the store check accepts: each path is rowfs.StoreFileAllowed and
// orgcfg.CheckStoreTree passes, on the remote and in the local clone. A
// refused path names the writer that must change, or the definition.
func TestEveryStoreWriteKeepsALoreStore(t *testing.T) {
	if isolate.Run(t) {
		return
	}
	w := newOrgWorld(t)
	ctx := context.Background()
	mira := w.home(t, "mira")
	h := orgHostAt(t, w.repo, mira)
	if _, err := h.Create(ctx, opsorgcmd.CreateIn{Org: "agency", Store: "agency", OrgFolder: rowfs.FolderOrg, Own: []string{"github.com/agency/*"},
		Key: "human:mira@agency.example", Name: "Mira", Handle: "mira"}, true); err != nil {
		t.Fatalf("org create: %v", err)
	}
	if _, err := h.Use(ctx, opsorgcmd.UseIn{Entry: "agency"}); err != nil {
		t.Fatalf("org use: %v", err)
	}
	app, err := New(StandaloneOptions(w.repo, mira, w.repo, "test"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = app.Close() }()
	for _, f := range []string{"web", "gone"} {
		if _, err := app.AddFolder(ctx, opsstores.FolderAddIn{Store: "agency", Folder: f}); err != nil {
			t.Fatalf("store folder add %s: %v", f, err)
		}
	}
	if _, err := h.Link(ctx, opsorgcmd.LinkIn{Org: "agency", Project: "web"}, true); err != nil {
		t.Fatalf("org link: %v", err)
	}
	if _, err := h.Link(ctx, opsorgcmd.LinkIn{Org: "agency", Project: "gone"}, true); err != nil {
		t.Fatalf("org link gone: %v", err)
	}
	for _, f := range []string{rowfs.FolderOrg, "web", "gone", rowfs.FolderRoot} {
		writeEveryTable(ctx, t, app, f)
	}
	if _, err := h.Unlink(ctx, opsorgcmd.UnlinkIn{Org: "agency", Project: "web"}, true); err != nil {
		t.Fatalf("org unlink: %v", err)
	}
	remote := filepath.Join(w.hosts, "agency/notes.git")
	clone := filepath.Join(filepath.Dir(mira), ".lore", "stores", "agency", "notes")
	if out, gerr := exec.Command("git", "-C", clone, "rev-parse", "--git-dir").CombinedOutput(); gerr != nil {
		t.Fatalf("store clone at %s: %v %s", clone, gerr, out)
	}
	for _, at := range []struct{ dir, rev string }{{remote, "main"}, {clone, "HEAD"}} {
		files := orgGit(t, at.dir, "ls-tree", "-r", "--name-only", at.rev)
		for _, p := range strings.Split(strings.TrimSpace(files), "\n") {
			if !rowfs.StoreFileAllowed(p) {
				t.Errorf("%s %s: lore wrote %s, which the store check refuses", at.dir, at.rev, p)
			}
		}
		if err := orgcfg.CheckStoreTree(ctx, at.dir, at.rev, "github.com/agency/notes", "main"); err != nil {
			t.Errorf("%s %s: %v\nfiles:\n%s", at.dir, at.rev, err, files)
		}
		for _, want := range []string{"org/" + rowfs.OrgFileName, "org/" + rowfs.MetaFileName, "web/" + rowfs.PurgedFileName, rowfs.StoreDataDir + "/" + schema.TableMemories + "/"} {
			if !strings.Contains(files, want) {
				t.Errorf("%s %s: no %s: the test no longer drives that writer\n%s", at.dir, at.rev, want, files)
			}
		}
	}
}

// writeEveryTable writes one row of every built-in synced table into
// folder of the agency store through the store backend (the same Write
// the engine calls), a tombstone over one of them, and purges another
// (_purged.json), then syncs so the remote holds it all.
func writeEveryTable(ctx context.Context, t *testing.T, app *App, folder string) {
	t.Helper()
	canon, err := canonical(attach.Store{URL: orgTestStoreURL, Folder: folder})
	if err != nil {
		t.Fatal(err)
	}
	b, err := app.openStore(ctx, canon, orgTestStoreURL)
	if err != nil {
		t.Fatalf("open %s: %v", canon, err)
	}
	defer func() {
		if cerr := b.Close(); cerr != nil {
			t.Errorf("close %s: %v", canon, cerr)
		}
	}()
	actor, err := ids.New[ids.Actor]()
	if err != nil {
		t.Fatal(err)
	}
	stamp := hlc.Next(hlc.HLC{}, time.Now(), actor)
	var changes []store.Change
	var last store.Row
	for _, def := range schema.Builtin() {
		if !def.Synced {
			continue
		}
		mem, err := ids.New[ids.Memory]()
		if err != nil {
			t.Fatal(err)
		}
		id, err := ids.ParseAny(string(def.Prefix) + mem.String()[3:])
		if err != nil {
			t.Fatalf("id for %s: %v", def.Name, err)
		}
		stamp = hlc.Next(stamp, time.Now(), actor)
		r, err := store.FromRow(row.Row{Table: def.Name, ID: id, HLC: stamp, CreatedAt: stamp.Wall, UpdatedAt: stamp.Wall})
		if err != nil {
			t.Fatalf("row of %s: %v", def.Name, err)
		}
		changes = append(changes, store.Change{Row: r})
		last = r
	}
	if err := b.Write(ctx, changes); err != nil {
		t.Fatalf("write every table into %s: %v", canon, err)
	}
	stamp = hlc.Next(stamp, time.Now(), actor)
	tomb, err := store.FromRow(row.Row{Table: last.Table, ID: last.ID, HLC: stamp, Deleted: true})
	if err != nil {
		t.Fatal(err)
	}
	p, ok := b.(storePurger)
	if !ok {
		t.Fatalf("the store backend of %s has no WritePurge", canon)
	}
	if err := p.WritePurge(ctx, []store.Change{{Row: tomb, Expect: last.HLC}}, []ids.AnyID{last.ID}); err != nil {
		t.Fatalf("purge in %s: %v", canon, err)
	}
	if _, err := b.Sync(ctx); err != nil {
		t.Fatalf("sync %s: %v", canon, err)
	}
}
