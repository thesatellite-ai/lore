package wire

import (
	"context"
	"encoding/json"
	"os"
	"strings"
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

// TestReadNamingStoreOutsideARepo pins devco D-421: outside any git work
// tree a read takes the store the write took (`rule list --to <store>`,
// or --store) and lists what the write put there; a read naming no store
// in a folder no org covers is refused with the flag to name, never with
// "run git init here first".
func TestReadNamingStoreOutsideARepo(t *testing.T) {
	t.Parallel()
	ctx := background()
	orgURL := folderStoreAt(t, orgStoreID, rowfs.VisibilityInternal)
	hook := func(context.Context) ([]attach.Store, error) {
		return []attach.Store{{Name: rosterStore, URL: orgURL, Folder: "org"}}, nil
	}
	plain, home := t.TempDir(), t.TempDir()
	app := func() *App {
		a, err := New(Options{RepoDir: plain, Home: home, FlagActor: testEmail, OrgStores: hook, Wall: time.Now})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = a.Close() })
		return a
	}
	a := app()
	add, _ := a.Registry().Lookup("rule.add")
	const body = "every estimate is reviewed by two people"
	if _, err := a.Run(ctx, add, op.JSONDecoder{Body: []byte(`{"body":"` + body + `","to":"` + string(rosterStore) + `"}`)}, op.Caller{Yes: true}); err != nil {
		t.Fatalf("rule add --to %s: %v", rosterStore, err)
	}
	list, ok := a.Registry().Lookup("rule.list")
	if !ok {
		t.Fatal("no rule.list")
	}
	res, err := app().Run(ctx, list, op.JSONDecoder{Body: []byte(`{"store":"` + string(rosterStore) + `"}`)}, op.Caller{})
	if err != nil {
		t.Fatalf("rule list --to %s outside a repo: %v", rosterStore, err)
	}
	if out, jerr := res.JSON(); jerr != nil || !strings.Contains(string(out), body) {
		t.Fatalf("rule list --to %s does not list the rule: %s %v", rosterStore, out, jerr)
	}
	_, err = app().Run(ctx, list, op.JSONDecoder{Body: []byte(`{}`)}, op.Caller{})
	// the hint names the read's own flag, --store, never a write's --to
	// (devco D-674), and never git init
	if hint := errs.EnvelopeOf(err).Error.Hint; !errs.Has(err, errs.NotAStore) || !strings.Contains(hint, "--store <org>") || strings.Contains(hint, "--to") || strings.Contains(hint, "git init") {
		t.Fatalf("rule list with no store, no org: %v (hint %q), want E_NOT_A_STORE naming --store (not --to), not git init", err, hint)
	}
	if entries, rerr := os.ReadDir(plain); rerr != nil || len(entries) != 0 {
		t.Fatalf("the folder it ran in got files: %v %v", entries, rerr)
	}
}

// TestEditByIDOutsideARepo: an edit naming its row by id from a folder
// that is no repo finds the org folder holding the row (devco D-433:
// `memory edit --id …` gave E_NOT_A_STORE "run git init here first").
func TestEditByIDOutsideARepo(t *testing.T) {
	t.Parallel()
	ctx := background()
	orgURL := folderStoreAt(t, orgStoreID, rowfs.VisibilityInternal)
	hook := func(context.Context) ([]attach.Store, error) {
		return []attach.Store{{Name: rosterStore, URL: orgURL, Folder: "org"}}, nil
	}
	plain, home := t.TempDir(), t.TempDir()
	app := func() *App {
		a, err := New(Options{RepoDir: plain, Home: home, FlagActor: testEmail, OrgStores: hook, Wall: time.Now})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = a.Close() })
		return a
	}
	a := app()
	add, _ := a.Registry().Lookup("memory.add")
	res, err := a.Run(ctx, add, op.JSONDecoder{Body: []byte(`{"body":"the staging box reboots on sundays","to":"` + string(rosterStore) + `"}`)}, op.Caller{Yes: true})
	if err != nil {
		t.Fatalf("memory add --to %s: %v", rosterStore, err)
	}
	out, _ := res.JSON()
	var added struct {
		ID string `json:"id"`
	}
	if err = json.Unmarshal(out, &added); err != nil || added.ID == "" {
		t.Fatalf("memory add output %s: %v", out, err)
	}
	edit, _ := a.Registry().Lookup("memory.edit")
	const body = "the staging box reboots on mondays"
	if _, err = app().Run(ctx, edit, op.JSONDecoder{Body: []byte(`{"id":"` + added.ID + `","body":"` + body + `"}`)}, op.Caller{Yes: true}); err != nil {
		t.Fatalf("memory edit --id outside a repo: %v", err)
	}
	list, _ := a.Registry().Lookup("memory.list")
	res, err = app().Run(ctx, list, op.JSONDecoder{Body: []byte(`{"store":"` + string(rosterStore) + `"}`)}, op.Caller{})
	if err != nil {
		t.Fatalf("memory list --store %s: %v", rosterStore, err)
	}
	if got, _ := res.JSON(); !strings.Contains(string(got), body) {
		t.Fatalf("the edit did not land: %s", got)
	}
	if entries, rerr := os.ReadDir(plain); rerr != nil || len(entries) != 0 {
		t.Fatalf("the folder it ran in got files: %v %v", entries, rerr)
	}
}
