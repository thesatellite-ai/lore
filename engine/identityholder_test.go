package engine

import (
	"context"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// TestIdentityHolderAcceptsLinkedRepoRef (devco D-736, cleanlink3): a
// cleaned linked repo has no repos row in any store; its id lives in the
// link. A scopes row written to the folder (agency here) with repo_id →
// that id was refused E_NOT_FOUND "repo_id → rep_…". With the identity
// holder naming agency the write goes through; any other dangling repo id
// is still refused, and a holder naming a store not attached here does
// not count.
func TestIdentityHolderAcceptsLinkedRepoRef(t *testing.T) {
	ctx := context.Background()
	w := newF15World(t, false)
	own, err := ids.Parse[ids.Repo]("rep_01936f0000007a1b8c2d3e4f5a6b7c11")
	if err != nil {
		t.Fatal(err)
	}
	other, err := ids.Parse[ids.Repo]("rep_01936f0000007a1b8c2d3e4f5a6b7c22")
	if err != nil {
		t.Fatal(err)
	}
	scope := func(rid ids.ID[ids.Repo], path string) error {
		ref, rerr := value.RefOf(rid.Any())
		if rerr != nil {
			t.Fatal(rerr)
		}
		_, err := w.eng.Create(ctx, agency, schema.TableScopes, map[string]value.Value{"repo_id": ref, schema.FieldScopePath: txt(path)})
		return err
	}
	if err := scope(own, "src/a"); !errs.Has(err, errs.NotFound) {
		t.Fatalf("no holder: want E_NOT_FOUND for the repo id no store holds, got %v", err)
	}
	holder := store.Source(agency)
	w.eng.SetIdentityHolder(func(_ context.Context, table row.Table, id ids.AnyID) (store.Source, bool) {
		return holder, table == schema.TableRepos && id.String() == own.String()
	})
	if err := scope(own, "src/b"); err != nil {
		t.Fatalf("the linked repo's own id is held by the folder, the scope write must pass: %v", err)
	}
	if err := scope(other, "src/c"); !errs.Has(err, errs.NotFound) {
		t.Fatalf("another repo id still dangles: want E_NOT_FOUND, got %v", err)
	}
	holder = "not_attached"
	if err := scope(own, "src/d"); !errs.Has(err, errs.NotFound) {
		t.Fatalf("a holder that is not attached here holds nothing: want E_NOT_FOUND, got %v", err)
	}
}
