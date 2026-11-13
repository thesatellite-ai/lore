package wire

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/engine/attach"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/op"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// TestWriteToStoreOutsideARepo pins B-7: outside any git work tree a write
// naming its store (`lore rule add --to <org>/org`) lands in that store
// folder, as `lore member add` does, and leaves the folder it ran in
// untouched; a write with no --to there still needs a repo, and its hint
// never suggests `git init` in the home folder.
func TestWriteToStoreOutsideARepo(t *testing.T) {
	t.Parallel()
	ctx := background()
	orgURL := folderStoreAt(t, orgStoreID, rowfs.VisibilityInternal)
	hook := func(context.Context) ([]attach.Store, error) {
		return []attach.Store{{Name: rosterStore, URL: orgURL, Folder: "org"}}, nil
	}
	plain := t.TempDir()
	a, err := New(Options{RepoDir: plain, Home: t.TempDir(), FlagActor: testEmail, OrgStores: hook, Wall: time.Now})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })
	e, ok := a.Registry().Lookup("rule.add")
	if !ok {
		t.Fatal("no rule.add")
	}
	body := `{"body":"every estimate is reviewed by two people","to":"` + string(rosterStore) + `"}`
	if _, err := a.Run(ctx, e, op.JSONDecoder{Body: []byte(body)}, op.Caller{Yes: true}); err != nil {
		t.Fatalf("rule add --to %s outside a repo: %v", rosterStore, err)
	}
	eng, err := a.Engine(ctx)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := eng.List(ctx, store.Source(rosterStore), schema.TableRules)
	if err != nil || len(rows) != 1 {
		t.Fatalf("rules in %s: %d %v, want 1", rosterStore, len(rows), err)
	}
	if entries, rerr := os.ReadDir(plain); rerr != nil || len(entries) != 0 {
		t.Fatalf("the folder it ran in got files: %v %v", entries, rerr)
	}

	// no --to: still a repo command, refused with E_NOT_A_STORE
	b, err := New(Options{RepoDir: plain, Home: t.TempDir(), FlagActor: testEmail, OrgStores: hook, Wall: time.Now})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = b.Close() })
	if _, err := b.Run(ctx, e, op.JSONDecoder{Body: []byte(`{"body":"no target"}`)}, op.Caller{Yes: true}); !errs.Has(err, errs.NotAStore) {
		t.Fatalf("rule add with no --to outside a repo: %v, want E_NOT_A_STORE", err)
	}
	// a store the setup does not have: named, with where to look
	if _, err := b.Run(ctx, e, op.JSONDecoder{Body: []byte(`{"body":"x","to":"nosuch/org"}`)}, op.Caller{Yes: true}); !errs.Has(err, errs.NotFound) {
		t.Fatalf("rule add --to nosuch/org outside a repo: %v, want E_NOT_FOUND", err)
	}
}
