package rowref

import (
	"context"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/ops/internal/resname"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// resEnv is a fakeEnv whose stores hold resource rows; listErr fails List.
type resEnv struct {
	fakeEnv
	rows    map[store.Source][]row.Row
	kinds   []row.Row // the kinds table, held by the repo store
	listErr error
}

func (r *resEnv) List(_ context.Context, src store.Source, table row.Table) ([]row.Row, error) {
	if r.listErr != nil {
		return nil, r.listErr
	}
	if table == schema.TableKinds && src == store.SourceRepo {
		return r.kinds, nil
	}
	if table != schema.TableResources {
		return nil, nil
	}
	return r.rows[src], nil
}

func resRow(t *testing.T, name string, deleted bool) row.Row {
	t.Helper()
	return kindRow(t, name, deleted, "")
}

// hostKind is the kind id the kind-aware rows of the tests carry.
const hostKind = "knd_0e45506e64357c94a87e70ef1c6bbba9"

// kindRow is a resources row of kind id kind (none when empty), or with
// kind == "kind" the kinds row naming hostKind "host".
func kindRow(t *testing.T, name string, deleted bool, kind string) row.Row {
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
	r := row.Row{Table: schema.TableResources, ID: id, Deleted: deleted, Fields: map[string]value.Value{resname.FieldName: v}}
	if kind != "" {
		kv, err := value.TextOf(kind)
		if err != nil {
			t.Fatal(err)
		}
		r.Fields[resname.FieldKind] = kv
	}
	return r
}

// TestParseResourceQualified (RES 3.4, BUGFIX-10/11): --ref takes the same
// names as every resource lookup through the shared resolver: <kind>/<name>
// and <place>:<kind>/<name> pick one row; a bare name at two levels is
// E_RESOURCE_AMBIGUOUS whose hint names both qualified names; a wrong
// place or kind is not found.
func TestParseResourceQualified(t *testing.T) {
	ctx := context.Background()
	kid, err := ids.ParseAny(hostKind)
	if err != nil {
		t.Fatal(err)
	}
	kname, err := value.TextOf("host")
	if err != nil {
		t.Fatal(err)
	}
	inRepo := kindRow(t, "hub_db", false, hostKind)
	inAgency := kindRow(t, "hub_db", false, hostKind)
	solo := kindRow(t, "hub_web", false, hostKind)
	env := &resEnv{
		rows:  map[store.Source][]row.Row{store.SourceRepo: {inRepo, solo}, "agency": {inAgency}},
		kinds: []row.Row{{Table: schema.TableKinds, ID: kid, Fields: map[string]value.Value{"name": kname}}},
	}
	for in, want := range map[string]ids.AnyID{
		"repo:host/hub_db":   inRepo.ID,
		"agency:host/hub_db": inAgency.ID,
		"host/hub_web":       solo.ID,
		"repo:host/hub_web":  solo.ID,
	} {
		refs, err := Parse(ctx, env, []string{in})
		if err != nil || len(refs) != 1 || refs[0].ID != want {
			t.Errorf("Parse(%q) = %+v, %v; want %s", in, refs, err, want)
		}
	}
	_, err = Parse(ctx, env, []string{"hub_db"})
	if e := errs.EnvelopeOf(err).Error; !errs.Has(err, errs.ResourceAmbiguous) || !strings.Contains(e.Hint, "repo:host/hub_db") || !strings.Contains(e.Hint, "agency:host/hub_db") {
		t.Errorf("bare name at two levels: %v hint %q", err, e.Hint)
	}
	for _, in := range []string{"agency:host/hub_web", "repo:domain/hub_web", "domain/hub_web"} {
		if _, err := Parse(ctx, env, []string{in}); errs.CodeOf(err) != errs.NotFound && errs.CodeOf(err) != errs.InvalidInput {
			t.Errorf("Parse(%q): %v, want not found", in, err)
		}
	}
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

	for _, c := range []struct {
		name string
		want ids.AnyID
	}{
		{"hub_prod", hub.ID},
		{" hub_prod ", hub.ID}, // surrounding spaces are trimmed
		{"fd00::1", v6.ID},
	} {
		refs, err := Parse(ctx, env, []string{c.name})
		if err != nil || len(refs) != 1 || refs[0].Table != schema.TableResources || refs[0].ID != c.want {
			t.Fatalf("Parse(%q) = %+v, %v; want resources/%s", c.name, refs, err, c.want)
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
