package rowref

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

// resEnv is a fakeEnv whose stores hold resource rows; listErr fails List.
type resEnv struct {
	fakeEnv
	rows    map[store.Source][]row.Row
	listErr error
}

func (r *resEnv) List(_ context.Context, src store.Source, table row.Table) ([]row.Row, error) {
	if r.listErr != nil {
		return nil, r.listErr
	}
	if table != schema.TableResources {
		return nil, nil
	}
	return r.rows[src], nil
}

func resRow(t *testing.T, name string, deleted bool) row.Row {
	t.Helper()
	rid, err := ids.New[ids.Resource]()
	if err != nil {
		t.Fatal(err)
	}
	id := rid.Any()
	v, err := value.TextOf(name)
	if err != nil {
		t.Fatal(err)
	}
	return row.Row{Table: schema.TableResources, ID: id, Deleted: deleted, Fields: map[string]value.Value{fieldResourceName: v}}
}

// TestParseResourceName (RES 3.11, later 452): --ref takes a resource
// name: one live resource of that name in any attached store becomes a
// resources pointer (an IPv6 name with ':' included); a deleted row does
// not count; two live rows (two levels or kinds) are E_RESOURCE_AMBIGUOUS;
// no row keeps the "not a row id" refusal; a store that cannot be listed
// refuses (fail closed). Row ids still win over names.
func TestParseResourceName(t *testing.T) {
	ctx := context.Background()
	hub := resRow(t, "hub_prod", false)
	v6 := resRow(t, "fd00::1", false)
	env := &resEnv{rows: map[store.Source][]row.Row{
		"agency":         {hub, resRow(t, "old_box", true), v6},
		store.SourceRepo: {resRow(t, "dup", false)},
	}}
	env.rows["agency"] = append(env.rows["agency"], resRow(t, "dup", false))

	for name, want := range map[string]ids.AnyID{"hub_prod": hub.ID, " hub_prod ": hub.ID, "fd00::1": v6.ID} {
		refs, err := Parse(ctx, env, []string{name})
		if err != nil || len(refs) != 1 || refs[0].Table != schema.TableResources || refs[0].ID != want {
			t.Fatalf("Parse(%q) = %+v, %v; want resources/%s", name, refs, err, want)
		}
	}
	if refs, err := Parse(ctx, env, []string{visibleID}); err != nil || len(refs) != 1 || refs[0].ID.String() != visibleID {
		t.Fatalf("a row id: %+v %v", refs, err)
	}
	bad := []struct {
		name, in string
		env      *resEnv
		code     errs.Code
	}{
		{"two live rows", "dup", env, errs.ResourceAmbiguous},
		{"only a deleted row", "old_box", env, errs.NotFound},
		{"no such name", "nowhere", env, errs.NotFound},
		{"colon, no such name", "acme/app:" + visibleID, env, errs.InvalidInput},
		{"store cannot be listed", "hub_prod", &resEnv{listErr: errs.New(errs.StoreUnreachable, "offline")}, errs.StoreUnreachable},
	}
	for _, c := range bad {
		t.Run(c.name, func(t *testing.T) {
			refs, err := Parse(ctx, c.env, []string{c.in})
			if refs != nil || errs.CodeOf(err) != c.code {
				t.Fatalf("Parse(%q) = %+v, %v; want %s", c.in, refs, err, c.code)
			}
		})
	}
}
