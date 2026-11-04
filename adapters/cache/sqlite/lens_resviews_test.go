package sqlite

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// TestLensResourceViews (RES 3.10): res__all / res__kinds / res__edges /
// res__dangling span both sources; an edge into the other store resolves,
// the dangling upstream of kindWorld is listed, and a masked lens drops the
// edge of a sensitive ref field.
func TestLensResourceViews(t *testing.T) {
	f := newFixture(t)
	golang := f.open("golang.db")
	k := f.kindWorld(golang) // hub_prod, upstream → a resource nowhere
	repo := f.open("repo.db")
	svc, runsOn, secret := must(ids.New[ids.Kind]()), must(ids.New[ids.KindField]()), must(ids.New[ids.KindField]())
	secretRow := f.live(schema.TableKindFields, secret.Any(), map[string]value.Value{
		fieldKindID: text(t, svc.String()), fieldName: text(t, "backup_on"), "type": text(t, "ref"), fieldSensitive: value.BoolOf(true),
	})
	f.apply(repo, store.SourceRepo, "1",
		f.kindRow(svc, "service"),
		f.fieldRow(runsOn, svc, "runs_on", "ref", k.kind),
		secretRow,
		f.resource(must(ids.New[ids.Resource]()), svc, "hub_api", map[ids.ID[ids.KindField]][2]string{
			runsOn: {"runs_on", `"` + k.res.String() + `"`}, secret: {"backup_on", `"` + k.res.String() + `"`},
		}),
	)
	for _, c := range []*Cache{repo, golang} {
		if err := c.Close(); err != nil {
			t.Fatal(err)
		}
	}
	srcs := []LensSource{
		{Name: string(store.SourceRepo), Path: filepath.Join(f.dir, "repo.db")},
		{Name: "golang", Path: filepath.Join(f.dir, "golang.db")},
	}
	q := func(l *Lens, sql string) string { return dump(must(l.Query(context.Background(), sql))) }
	l := openMasked(t, srcs, false)
	if got, want := q(l, `SELECT kind, name, scope, archived FROM res__all ORDER BY name`),
		"kind|name|scope|archived|service|hub_api|repo|0|server|hub_prod|golang|0"; got != want {
		t.Fatalf("res__all = %s, want %s", got, want)
	}
	if got := q(l, `SELECT name, table_name, fields FROM res__kinds ORDER BY name`); got != "name|table_name|fields|server|res_server|3|service|res_service|2" {
		t.Fatalf("res__kinds = %s", got)
	}
	if got := q(l, `SELECT a.name, e.field, b.name FROM res__edges e JOIN res__all a ON a.id = e.src_id JOIN res__all b ON b.id = e.dst_id ORDER BY e.field`); got != "name|field|name|hub_api|backup_on|hub_prod|hub_api|runs_on|hub_prod" {
		t.Fatalf("res__edges = %s", got)
	}
	if got := q(l, `SELECT source, name, field FROM res__dangling`); got != "source|name|field|golang|hub_prod|upstream" {
		t.Fatalf("res__dangling = %s", got)
	}
	m := openMasked(t, srcs, true)
	if got := q(m, `SELECT field FROM res__edges WHERE source = 'repo'`); got != "field|runs_on" {
		t.Fatalf("masked res__edges = %s, want only runs_on", got)
	}
}

// TestResourceFullText (RES 3.12, R8): a resource is found by its name,
// its kind's name and a field value; a sensitive value is never indexed,
// even for a resource applied before its kind.
func TestResourceFullText(t *testing.T) {
	f := newFixture(t)
	c := f.open("repo.db")
	kind, host, label := must(ids.New[ids.Kind]()), must(ids.New[ids.KindField]()), must(ids.New[ids.KindField]())
	res := must(ids.New[ids.Resource]())
	f.apply(c, store.SourceRepo, "1", f.resource(res, kind, "vault_prod", map[ids.ID[ids.KindField]][2]string{
		host: {"host", `"` + maskProbeIP + `"`}, label: {"label", `"frankfurt primary"`},
	}))
	f.apply(c, store.SourceRepo, "2",
		f.kindRow(kind, "vault"),
		f.live(schema.TableKindFields, host.Any(), map[string]value.Value{
			fieldKindID: text(t, kind.String()), fieldName: text(t, "host"), "type": text(t, "ip"), fieldSensitive: value.BoolOf(true),
		}),
		f.fieldRow(label, kind, "label", "text", ids.ID[ids.Kind]{}),
	)
	for match, want := range map[string]int{`vault_prod`: 1, `vault`: 1, `frankfurt`: 1, `"` + maskProbeIP + `"`: 0, `"198"`: 0} {
		got := must(c.Search(context.Background(), schema.TableResources, match, 10))
		if len(got) != want {
			t.Errorf("search %s = %v, want %d hits", match, got, want)
		}
	}
}
