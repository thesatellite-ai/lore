package engine

import (
	"context"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
	"github.com/thesatellite-ai/lore/store/memstore"
)

// webStore is a second writable store beside the repo store: the folder a
// --store names in these tests (shown as acme/web).
const webStore store.Source = "acme"

// TestNarrowKeepsTheCommandToTheNamedStore is devco D-617: under
// WithNarrow a row named by id in another store is refused, and so is a
// run whose first write lands outside the named store; nothing is written
// there. A run whose first write lands in the named store writes on.
func TestNarrowKeepsTheCommandToTheNamedStore(t *testing.T) {
	t.Parallel()
	f := setup(t, WithStore(Attached{Name: webStore, Backend: memstore.NewData().Open(store.OpenOptions{})}))
	inRepo := f.memory(t, "a repo note")
	bg := context.Background()
	webProject, err := f.eng.Create(bg, webStore, schema.TableProjects, map[string]value.Value{"name": txt("web")})
	if err != nil {
		t.Fatal(err)
	}
	fields := f.memFields("a web note")
	fields["project_id"] = txt(webProject.Row.ID.String())
	inWeb, err := f.eng.Create(bg, webStore, schema.TableMemories, fields)
	if err != nil {
		t.Fatal(err)
	}
	web := Narrow{Named: "acme/web", Sources: []store.Source{webStore}, Shown: map[store.Source]string{webStore: "acme/web"}}

	// by id: the repo's row is not in acme/web
	err = f.eng.refuseNarrowedIDs(WithNarrow(bg, web), []string{inRepo.ID.String()})
	if !errs.Has(err, errs.NotFound) || !strings.Contains(err.Error(), inRepo.ID.String()+" is not in acme/web; it is in repo (use --store repo)") {
		t.Fatalf("by id: err = %v, want E_NOT_FOUND naming where the row is", err)
	}
	if err := f.eng.refuseNarrowedIDs(WithNarrow(bg, web), []string{inWeb.Row.ID.String(), "code_repo"}); err != nil {
		t.Fatalf("a row of the named store (and a name) refused: %v", err)
	}

	// the first write outside the named store is refused, nothing written
	_, err = f.eng.Update(WithNarrow(bg, web), Ref{ID: inRepo.ID}, map[string]value.Value{"body": txt("changed")})
	if !errs.Has(err, errs.NotFound) || !strings.Contains(err.Error(), "is not in acme/web; it is in repo") {
		t.Fatalf("write: err = %v, want the D-617 refusal", err)
	}
	if _, r, gerr := f.eng.Get(bg, Ref{ID: inRepo.ID}); gerr != nil || r.Fields["body"].String() != "a repo note" {
		t.Fatalf("the refused write changed the row: %v %v", r.Fields["body"], gerr)
	}

	// a first write in the named store decides the run: later writes follow
	ctx := WithNarrow(bg, web)
	if _, err := f.eng.Update(ctx, Ref{ID: inWeb.Row.ID}, map[string]value.Value{"body": txt("web edit")}); err != nil {
		t.Fatalf("write in the named store: %v", err)
	}
	if _, err := f.eng.Update(ctx, Ref{ID: inRepo.ID}, map[string]value.Value{"body": txt("follow-up")}); err != nil {
		t.Fatalf("a later write of the same run: %v", err)
	}

	// no narrowing: unchanged behaviour
	if _, err := f.eng.Update(bg, Ref{ID: inRepo.ID}, map[string]value.Value{"body": txt("plain")}); err != nil {
		t.Fatalf("plain write: %v", err)
	}
}

// TestNarrowFindsTheNamedStoresCopy is devco D-617's multi-store case: a
// row with a copy in several attached stores (the caller's actor row is
// written once per store under one id) is found in the store --store
// names, even when another store's copy is newer; it is neither refused
// nor read or written in the other store.
func TestNarrowFindsTheNamedStoresCopy(t *testing.T) {
	t.Parallel()
	f := setup(t, WithStore(Attached{Name: webStore, Backend: memstore.NewData().Open(store.OpenOptions{})}))
	bg := context.Background()
	id, err := ids.ParseAny("act_01a1a59b891c70b789144cd3e711fb93")
	if err != nil {
		t.Fatal(err)
	}
	// the web copy first, so the repo copy is the newer one an
	// unqualified lookup picks
	for _, src := range []store.Source{webStore, store.SourceRepo} {
		fields := map[string]value.Value{"kind": txt("human"), "display_name": txt("rafa"), "stable_key": txt("human:rafa@example.test")}
		if _, cerr := f.eng.CreateWithID(bg, src, schema.TableActors, id, fields); cerr != nil {
			t.Fatalf("actor in %s: %v", src, cerr)
		}
	}
	if src, _, gerr := f.eng.Get(bg, Ref{ID: id}); gerr != nil || src != store.SourceRepo {
		t.Fatalf("unnarrowed lookup = %s %v, want the newer repo copy", src, gerr)
	}
	web := Narrow{Named: "acme/web", Sources: []store.Source{webStore}, Shown: map[store.Source]string{webStore: "acme/web"}}
	ctx := WithNarrow(bg, web)
	if err := f.eng.refuseNarrowedIDs(ctx, []string{id.String()}); err != nil {
		t.Fatalf("a row with a copy in the named store refused: %v", err)
	}
	if src, _, gerr := f.eng.Get(ctx, Ref{ID: id}); gerr != nil || src != webStore {
		t.Fatalf("narrowed lookup = %s %v, want the acme/web copy", src, gerr)
	}
	if _, err := f.eng.Update(ctx, Ref{ID: id}, map[string]value.Value{"display_name": txt("rafa w")}); err != nil {
		t.Fatalf("write of the named store's copy: %v", err)
	}
	if _, r, _ := f.eng.Get(bg, Ref{Store: store.SourceRepo, ID: id}); r.Fields["display_name"].String() != "rafa" {
		t.Fatalf("the repo copy was written: %v", r.Fields["display_name"])
	}
}
