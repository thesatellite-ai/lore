package sqlite

import (
	"context"
	"path/filepath"
	"strings"
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
	t.Parallel()
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
	t.Parallel()
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

// TestLensKindViewAllSources (devco D-758, owner P-8): a resource held in
// a store whose own cache lacks its kind (the kind lives in the repo) is a
// row of res_<kind>_all and of the res_<kind id> view, its field values
// read from the resource; a sensitive field stays masked in that arm, and
// a source holding no resource of the kind adds nothing.
func TestLensKindViewAllSources(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	repo, acme, empty := f.open("repo.db"), f.open("acme.db"), f.open("empty.db")
	kind, ip, token := must(ids.New[ids.Kind]()), must(ids.New[ids.KindField]()), must(ids.New[ids.KindField]())
	const secret = "tok-acme-zz91"
	f.apply(repo, store.SourceRepo, "1",
		f.kindRow(kind, "server"),
		f.fieldRow(ip, kind, "ip", "text", ids.ID[ids.Kind]{}),
		f.live(schema.TableKindFields, token.Any(), map[string]value.Value{
			fieldKindID: text(t, kind.String()), fieldName: text(t, "token"), "type": text(t, "text"), fieldSensitive: value.BoolOf(true),
		}),
		f.resource(must(ids.New[ids.Resource]()), kind, "db_one", map[ids.ID[ids.KindField]][2]string{ip: {"ip", `"10.0.0.9"`}}),
	)
	f.apply(acme, "acme", "1", f.resource(must(ids.New[ids.Resource]()), kind, "api_two", map[ids.ID[ids.KindField]][2]string{
		ip: {"ip", `"10.0.0.5"`}, token: {"token", `"` + secret + `"`},
	}))
	f.apply(empty, "empty", "1", f.memory(must(ids.New[ids.Memory]()).Any(), must(ids.New[ids.Project]()).Any(), "no resources here"))
	for _, c := range []*Cache{repo, acme, empty} {
		if err := c.Close(); err != nil {
			t.Fatal(err)
		}
	}
	srcs := []LensSource{
		{Name: string(store.SourceRepo), Path: filepath.Join(f.dir, "repo.db")},
		{Name: "acme", Path: filepath.Join(f.dir, "acme.db")},
		{Name: "empty", Path: filepath.Join(f.dir, "empty.db")},
	}
	q := func(l *Lens, sql string) string { return dump(must(l.Query(context.Background(), sql))) }
	l := openMasked(t, srcs, false)
	if got, want := q(l, `SELECT source, name, ip FROM res_server_all ORDER BY name`), "source|name|ip|acme|api_two|10.0.0.5|repo|db_one|10.0.0.9"; got != want {
		t.Fatalf("res_server_all = %s, want %s", got, want)
	}
	if got, want := q(l, `SELECT name FROM "res_`+kind.String()+`_all" ORDER BY name`), "name|api_two|db_one"; got != want {
		t.Fatalf("res_<kind id>_all = %s, want %s", got, want)
	}
	if got, want := q(l, `SELECT name FROM res_server`), "name|db_one"; got != want {
		t.Fatalf("res_server (nearest copy) = %s, want %s", got, want)
	}
	m := openMasked(t, srcs, true)
	if got := q(m, `SELECT name, token FROM res_server_all WHERE source = 'acme'`); strings.Contains(got, secret) || !strings.Contains(got, "api_two") {
		t.Fatalf("masked res_server_all = %s: the sensitive token must be masked in the arm", got)
	}
}
