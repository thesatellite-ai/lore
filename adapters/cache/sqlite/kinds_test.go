package sqlite

import (
	"slices"
	"testing"

	"github.com/thesatellite-ai/lore/model/canon"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// kindWorld is a "server" kind with ip, region and a ref to another
// server, plus one resource.
type kindWorld struct {
	kind              ids.ID[ids.Kind]
	ipF, regionF, upF ids.ID[ids.KindField]
	res               ids.ID[ids.Resource]
}

func (f *fixture) kindRow(id ids.ID[ids.Kind], name string) store.Row {
	return f.live(schema.TableKinds, id.Any(), map[string]value.Value{"name": text(f.t, name)})
}

func (f *fixture) fieldRow(id ids.ID[ids.KindField], kind ids.ID[ids.Kind], name, typ string, refKind ids.ID[ids.Kind]) store.Row {
	fields := map[string]value.Value{
		"kind_id": text(f.t, kind.String()), "name": text(f.t, name), "type": text(f.t, typ),
	}
	if !refKind.IsZero() {
		fields["ref_kind"] = text(f.t, refKind.String())
	}
	return f.live(schema.TableKindFields, id.Any(), fields)
}

// resource builds a resources row; vals maps fld id → (field name, raw JSON v).
func (f *fixture) resource(id ids.ID[ids.Resource], kind ids.ID[ids.Kind], name string, vals map[ids.ID[ids.KindField]][2]string) store.Row {
	var members []canon.Member
	for fid, nv := range vals {
		entry := must(canon.Object([]canon.Member{{Key: "name", Raw: canon.String(nv[0])}, {Key: "v", Raw: []byte(nv[1])}}))
		members = append(members, canon.Member{Key: fid.String(), Raw: entry})
	}
	obj := must(canon.Object(members))
	return f.live(schema.TableResources, id.Any(), map[string]value.Value{
		"kind": text(f.t, kind.String()), "name": text(f.t, name), "fields": must(value.JSONOf(obj)),
	})
}

func (f *fixture) kindWorld(c *Cache) kindWorld {
	k := kindWorld{
		kind: must(ids.New[ids.Kind]()), ipF: must(ids.New[ids.KindField]()),
		regionF: must(ids.New[ids.KindField]()), upF: must(ids.New[ids.KindField]()),
		res: must(ids.New[ids.Resource]()),
	}
	dangling := must(ids.New[ids.Resource]())
	f.apply(c, store.SourceRepo, "k1",
		f.kindRow(k.kind, "server"),
		f.fieldRow(k.ipF, k.kind, "ip", "ip", ids.ID[ids.Kind]{}),
		f.fieldRow(k.regionF, k.kind, "region", "text", ids.ID[ids.Kind]{}),
		f.fieldRow(k.upF, k.kind, "upstream", "ref", k.kind),
		f.resource(k.res, k.kind, "hub_prod", map[ids.ID[ids.KindField]][2]string{
			k.ipF: {"ip", `"203.0.113.10"`}, k.regionF: {"region", `"eu"`}, k.upF: {"upstream", `"` + dangling.String() + `"`},
		}))
	return k
}

// TestDynamicKind: kind rows become res_<name> with typed columns, an
// index on the ref field, the stable res_<knd id> view, and the resource's
// values read by fld id (the dangling upstream ref loads).
func TestDynamicKind(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	c := f.open("c.db")
	k := f.kindWorld(c)
	cols := columnsOf(t, c, "res_server")
	for _, want := range []string{"name", "ip", "region", "upstream"} {
		if !slices.Contains(cols, want) {
			t.Fatalf("res_server columns %v lack %s", cols, want)
		}
	}
	if n := count(t, c, `SELECT COUNT(*) FROM "res_server" WHERE id = ? AND ip = '203.0.113.10' AND region = 'eu' AND name = 'hub_prod'`, k.res.String()); n != 1 {
		t.Fatal("resource not projected into res_server")
	}
	view := schema.KindTablePrefix + k.kind.String()
	if n := count(t, c, `SELECT COUNT(*) FROM "`+view+`" WHERE id = ?`, k.res.String()); n != 1 {
		t.Fatalf("stable view %s does not show the resource", view)
	}
	n := count(t, c, `SELECT COUNT(*) FROM pragma_index_list('res_server') AS l, pragma_index_info(l.name) AS i WHERE i.name = 'upstream'`)
	if n == 0 {
		t.Fatal("ref field upstream has no index")
	}
	// The resource is also in the generic resources table.
	if n := count(t, c, `SELECT COUNT(*) FROM "resources" WHERE id = ?`, k.res.String()); n != 1 {
		t.Fatal("resource missing from resources")
	}
	// A tombstoned resource leaves the kind table.
	f.apply(c, store.SourceRepo, "k2", f.tomb(schema.TableResources, k.res.Any()))
	if n := count(t, c, `SELECT COUNT(*) FROM "res_server"`); n != 0 {
		t.Fatal("tombstoned resource still in res_server")
	}
}

// TestKindFieldRename: renaming a field row rebuilds the table with the new
// column and keeps the value (keyed by fld id); renaming the kind renames
// the table while the stable view keeps working.
func TestKindFieldRename(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	c := f.open("c.db")
	k := f.kindWorld(c)
	f.apply(c, store.SourceRepo, "k2", f.fieldRow(k.ipF, k.kind, "address", "ip", ids.ID[ids.Kind]{}))
	cols := columnsOf(t, c, "res_server")
	if slices.Contains(cols, "ip") || !slices.Contains(cols, "address") {
		t.Fatalf("after rename columns = %v", cols)
	}
	if n := count(t, c, `SELECT COUNT(*) FROM "res_server" WHERE address = '203.0.113.10'`); n != 1 {
		t.Fatal("renamed column lost its value")
	}
	f.apply(c, store.SourceRepo, "k3", f.kindRow(k.kind, "host"))
	if n := count(t, c, `SELECT COUNT(*) FROM sqlite_master WHERE name = 'res_server'`); n != 0 {
		t.Fatal("old kind table not dropped on kind rename")
	}
	view := schema.KindTablePrefix + k.kind.String()
	if n := count(t, c, `SELECT COUNT(*) FROM "res_host" JOIN "`+view+`" USING (id)`); n != 1 {
		t.Fatal("res_host or the stable view lost the resource")
	}
	// Deleting the kind removes its table and view; the resource stays raw.
	f.apply(c, store.SourceRepo, "k4", f.tomb(schema.TableKinds, k.kind.Any()))
	if n := count(t, c, `SELECT COUNT(*) FROM sqlite_master WHERE name IN ('res_host', ?)`, view); n != 0 {
		t.Fatal("deleted kind's table or view remains")
	}
	if n := count(t, c, `SELECT COUNT(*) FROM "resources"`); n != 1 {
		t.Fatal("resource lost with its kind")
	}
}

// TestBrokenKindSkipped: a kind whose field has an unknown type gets no
// table and does not stop the Apply.
func TestBrokenKindSkipped(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	c := f.open("c.db")
	kind := must(ids.New[ids.Kind]())
	f.apply(c, store.SourceRepo, "1",
		f.kindRow(kind, "gadget"),
		f.fieldRow(must(ids.New[ids.KindField]()), kind, "x", "hologram", ids.ID[ids.Kind]{}))
	if n := count(t, c, `SELECT COUNT(*) FROM sqlite_master WHERE name = 'res_gadget'`); n != 0 {
		t.Fatal("broken kind got a table")
	}
}

// TestQuarantineLeftOut: a resource whose value breaks its field's allowed
// values or type stays in resources but is left out of res_<kind> and
// search; widening the values brings it back (RES 3.15, R10).
func TestQuarantineLeftOut(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	c := f.open("c.db")
	kind, provF, sizeF := must(ids.New[ids.Kind]()), must(ids.New[ids.KindField]()), must(ids.New[ids.KindField]())
	good, odd, bent := must(ids.New[ids.Resource]()), must(ids.New[ids.Resource]()), must(ids.New[ids.Resource]())
	prov := func(vals ...string) store.Row {
		r := f.fieldRow(provF, kind, "provider", "text", ids.ID[ids.Kind]{})
		d := must(r.Decode())
		items := make([]value.Value, 0, len(vals))
		for _, v := range vals {
			items = append(items, text(t, v))
		}
		d.Fields["values"] = must(value.ListOf(value.KindText, items...))
		return must(store.FromRow(d))
	}
	f.apply(c, store.SourceRepo, "q1",
		f.kindRow(kind, "host"), prov("aws", "gcp"),
		f.fieldRow(sizeF, kind, "size", "number", ids.ID[ids.Kind]{}),
		f.resource(good, kind, "goodhost", map[ids.ID[ids.KindField]][2]string{provF: {"provider", `"aws"`}}),
		f.resource(odd, kind, "oddhost", map[ids.ID[ids.KindField]][2]string{provF: {"provider", `"azure"`}}),
		f.resource(bent, kind, "benthost", map[ids.ID[ids.KindField]][2]string{sizeF: {"size", `"big"`}}))
	if n := count(t, c, `SELECT COUNT(*) FROM "res_host"`); n != 1 {
		t.Fatalf("res_host has %d rows, want only the valid one", n)
	}
	if n := count(t, c, `SELECT COUNT(*) FROM "resources"`); n != 3 {
		t.Fatalf("resources has %d rows, want all 3 (quarantined rows stay visible)", n)
	}
	for _, q := range []string{"oddhost", "benthost"} {
		if hits, err := c.Search(t.Context(), schema.TableResources, q, 10); err != nil || len(hits) != 0 {
			t.Fatalf("search %s: %v %v (quarantined rows are left out)", q, hits, err)
		}
	}
	if hits, err := c.Search(t.Context(), schema.TableResources, "goodhost", 10); err != nil || len(hits) != 1 {
		t.Fatalf("search goodhost: %v %v", hits, err)
	}
	f.apply(c, store.SourceRepo, "q2", prov("aws", "gcp", "azure"))
	if n := count(t, c, `SELECT COUNT(*) FROM "res_host" WHERE id = ?`, odd.String()); n != 1 {
		t.Fatal("widened values did not bring oddhost back into res_host")
	}
}
