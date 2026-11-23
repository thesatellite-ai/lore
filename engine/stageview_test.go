package engine

import (
	"context"
	"testing"

	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// TestStagedRowsReachTheMetaRegistry (devco:D-931, H-192): a dry run that
// runs in a check-only stage (meta edit --type, meta rehome, meta
// rebuild) checks its later metadata writes against the key rows it
// staged before. The registry read only the stores, so a value converted
// after its key was retyped was checked against the old type, and a key
// the stage added was unknown. Staged key rows (a create, an edit) now
// reach the registry while the stage is open, and nothing else sees them.
func TestStagedRowsReachTheMetaRegistry(t *testing.T) {
	t.Parallel()
	f := setup(t)
	bg := context.Background()
	size, err := f.eng.Create(bg, store.SourceRepo, schema.TableMetaKeys, map[string]value.Value{"name": txt("size"), "scope": txt("repo"), "type": txt("text")})
	if err != nil {
		t.Fatal(err)
	}
	ctx := Staging(WithCheckOnly(bg))
	defer Discard(ctx)
	if _, err = f.eng.Update(ctx, Ref{Store: store.SourceRepo, Table: schema.TableMetaKeys, ID: size.Row.ID}, map[string]value.Value{"type": txt("number")}); err != nil {
		t.Fatal(err)
	}
	if _, err = f.eng.Create(ctx, store.SourceRepo, schema.TableMetaKeys, map[string]value.Value{"name": txt("tier"), "scope": txt("repo"), "type": txt("text")}); err != nil {
		t.Fatal(err)
	}
	types := func(ctx context.Context) map[string]string {
		t.Helper()
		reg, rerr := f.eng.metaRegistry(ctx)
		if rerr != nil {
			t.Fatal(rerr)
		}
		out := map[string]string{}
		for _, d := range reg {
			out[d.name] = d.typ.String()
		}
		return out
	}
	if got := types(ctx); got["size"] != "number" || got["tier"] != "text" {
		t.Errorf("inside the stage the registry reads %v; want size number (the staged edit) and tier text (the staged create)", got)
	}
	if got := types(bg); got["size"] != "text" || got["tier"] != "" {
		t.Errorf("outside the stage the registry reads %v; want the stored rows only (size text, no tier)", got)
	}
	if _, _, err = CommitEach(ctx); err != nil {
		t.Fatal(err)
	}
	if got := types(bg); got["size"] != "text" || got["tier"] != "" {
		t.Errorf("after the check-only commit the registry reads %v; want nothing written", got)
	}
}
