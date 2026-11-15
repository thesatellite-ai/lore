package engine

import (
	"context"
	"testing"

	"github.com/thesatellite-ai/lore/logic/scan"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
	"github.com/thesatellite-ai/lore/store/memstore"
)

// TestGetRepoRefThroughAlias pins devco D-422: with no repo store attached
// and another store answering for it (WithRepoAlias, a store-only open of
// `<org>/<folder>` outside a repo), a read naming `repo` finds the row in
// that store. It used to index the stores by the unresolved name and
// panic on a nil backend (`repo add --project … --yes` from a plain folder).
func TestGetRepoRefThroughAlias(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	reg, err := schema.NewRegistry(schema.Builtin()...)
	if err != nil {
		t.Fatal(err)
	}
	sc, err := scan.NewRegexScanner([]scan.Rule{{ID: awsRule, Regex: `AKIA[0-9A-Z]{16}`, Keywords: []string{"akia"}, Source: scan.SourceBuiltin}})
	if err != nil {
		t.Fatal(err)
	}
	const folderStore store.Source = "acme/sync"
	data := memstore.NewData()
	eng, err := New(WithStore(Attached{Name: folderStore, Backend: data.Open(store.OpenOptions{})}),
		WithCache(newFakeCache()), WithRegistry(reg), WithScanner(sc), WithActor(testActor), WithAudit(&auditLog{}),
		WithRepoAlias(folderStore))
	if err != nil {
		t.Fatal(err)
	}
	w, err := eng.Create(ctx, folderStore, schema.TableProjects, map[string]value.Value{"name": txt("sync")})
	if err != nil {
		t.Fatal(err)
	}
	src, got, err := eng.Get(ctx, Ref{Store: store.SourceRepo, Table: schema.TableProjects, ID: w.Row.ID})
	if err != nil || src != folderStore || got.ID != w.Row.ID {
		t.Fatalf("Get repo ref through the alias = %s %v %v, want the row in %s", src, got.ID, err, folderStore)
	}
}
