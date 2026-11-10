package engine

import (
	"context"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/hlc"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/op"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// TestMoveResourceIntoClient: a resource moving into a client repo is
// refused (E_LEAK_BLOCKED, every line listed) while a ref field points at a
// store resource or an internal field has a value; --drop-internal removes
// the internal value with a warning; a sensitive value makes the move ask
// (RES 3.9, R2, BATCH 7).
func TestMoveResourceIntoClient(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	w := newProvWorld(t, fakeProv{})
	h := hlc.Next(hlc.HLC{}, w.eng.wall(), testActor)
	put := func(table row.Table, fields map[string]value.Value) ids.AnyID {
		t.Helper()
		id, err := w.eng.ids.NewID(mustPrefix(t, w, table))
		if err != nil {
			t.Fatal(err)
		}
		sr, err := store.FromRow(row.Row{Table: table, ID: id, HLC: h, Fields: fields})
		if err != nil {
			t.Fatal(err)
		}
		if err = w.data[agency].Open(store.OpenOptions{}).Write(ctx, []store.Change{{Row: sr}}); err != nil {
			t.Fatal(err)
		}
		return id
	}
	kind := put(schema.TableKinds, map[string]value.Value{"name": txt("service")})
	kref, err := value.RefOf(kind)
	if err != nil {
		t.Fatal(err)
	}
	field := func(name, typ string, extra map[string]value.Value) string {
		f := map[string]value.Value{"kind": kref, "name": txt(name), "type": txt(typ)}
		for k, v := range extra {
			f[k] = v
		}
		return put(schema.TableKindFields, f).String()
	}
	runsOn := field("runs_on", "ref", nil)
	notes := field("notes", "text", map[string]value.Value{"internal": value.BoolOf(true)})
	ip := field("ip", "text", map[string]value.Value{"sensitive": value.BoolOf(true)})
	host := put(schema.TableResources, map[string]value.Value{"kind": kref, "name": txt("hub_prod")})
	res := func(name, entries string) map[string]value.Value {
		j, jerr := value.JSONOf([]byte(entries))
		if jerr != nil {
			t.Fatal(jerr)
		}
		return map[string]value.Value{"kind": kref, "name": txt(name), "fields": j}
	}
	leaky := put(schema.TableResources, res("api", `{"`+runsOn+`":{"name":"runs_on","v":"`+host.String()+`"},"`+notes+`":{"name":"notes","v":"x"}}`))
	_, err = w.eng.Move(ctx, Ref{ID: leaky}, store.SourceRepo)
	if !errs.Has(err, errs.LeakBlocked) || !strings.Contains(err.Error(), "runs_on points at") || !strings.Contains(err.Error(), "notes (internal field)") {
		t.Fatalf("want E_LEAK_BLOCKED listing runs_on and notes, got %v", err)
	}

	fields := res("web", `{"`+notes+`":{"name":"notes","v":"x"},"`+ip+`":{"name":"ip","v":"203.0.113.10"}}`)
	web := put(schema.TableResources, fields)
	if _, _, err = w.eng.resourceIntoClient(ctx, store.SourceRepo, web, fields, false); !errs.Has(err, errs.ResourceInternal) {
		t.Fatalf("internal without --drop-internal: %v", err)
	}
	if _, _, err = w.eng.resourceIntoClient(ctx, store.SourceRepo, web, fields, true); !errs.Has(err, errs.NeedsConfirm) || !strings.Contains(err.Error(), "ip") {
		t.Fatalf("sensitive must ask: %v", err)
	}
	got, warns, err := w.eng.resourceIntoClient(context.WithValue(ctx, callKey{}, call{caller: op.Caller{Yes: true}}), store.SourceRepo, web, fields, true)
	if err != nil {
		t.Fatal(err)
	}
	if s := got["fields"].String(); strings.Contains(s, notes) || !strings.Contains(s, ip) || len(warns) != 1 || warns[0].Code != WarnResourceDropped {
		t.Fatalf("drop-internal: fields %s warnings %v", s, warns)
	}

	// 3.15: a value outside its field's allowed values blocks the move.
	prov := field("provider", "text", map[string]value.Value{"values": mustList(t, "aws", "gcp")})
	odd := res("odd", `{"`+prov+`":{"name":"provider","v":"azure"}}`)
	if _, _, err = w.eng.resourceIntoClient(ctx, store.SourceRepo, web, odd, false); !errs.Has(err, errs.ResourceQuarantined) || !strings.Contains(err.Error(), "not one of aws, gcp") {
		t.Fatalf("quarantined: %v", err)
	}

	// BATCH 3, end to end: the client repo gets its own kind row (origin =
	// the store kind) and the field rows the row uses, only the used
	// allowed value; the moved row names them; a second move reuses them.
	yes := context.WithValue(ctx, callKey{}, call{caller: op.Caller{Yes: true}})
	cdn := put(schema.TableResources, res("cdn", `{"`+prov+`":{"name":"provider","v":"aws"},"`+ip+`":{"name":"ip","v":"203.0.113.9"}}`))
	mv, err := w.eng.Move(yes, Ref{ID: cdn}, store.SourceRepo)
	if err != nil {
		t.Fatal(err)
	}
	ck, err := w.eng.List(ctx, store.SourceRepo, schema.TableKinds)
	if err != nil || len(ck) != 1 || originID(ck[0].Fields["origin"]) != kind.String() || ck[0].ID == kind {
		t.Fatalf("client kind: %v %v", ck, err)
	}
	// MQ15/R2-Q1: the store kind records the client kind in copies.
	if _, sk, lerr := w.eng.locateExcept(ctx, Ref{Table: schema.TableKinds, ID: kind}, store.SourceRepo); lerr != nil || strings.Join(listTexts(sk.Fields["copies"]), ",") != w.eng.placeOf(store.SourceRepo)+"="+ck[0].ID.String() {
		t.Fatalf("store kind copies: %v %v", sk.Fields["copies"], lerr)
	}
	if mv.Row.Fields["kind"].String() != ck[0].ID.String() || strings.Contains(mv.Row.Fields["fields"].String(), prov) {
		t.Fatalf("moved row still names store definitions: kind %s fields %s", mv.Row.Fields["kind"], mv.Row.Fields["fields"])
	}
	cf, err := w.eng.List(ctx, store.SourceRepo, schema.TableKindFields)
	if err != nil || len(cf) != 2 {
		t.Fatalf("client fields: %v %v", cf, err)
	}
	for _, f := range cf {
		if f.Fields["name"].String() == "provider" && strings.Join(listTexts(f.Fields["values"]), ",") != "aws" {
			t.Fatalf("values copied beyond use: %v", f.Fields["values"])
		}
	}
	cdn2 := put(schema.TableResources, res("cdn2", `{"`+prov+`":{"name":"provider","v":"gcp"}}`))
	if _, err = w.eng.Move(yes, Ref{ID: cdn2}, store.SourceRepo); err != nil {
		t.Fatal(err)
	}
	ck2, _ := w.eng.List(ctx, store.SourceRepo, schema.TableKinds)
	cf2, _ := w.eng.List(ctx, store.SourceRepo, schema.TableKindFields)
	if len(ck2) != 1 || len(cf2) != 2 {
		t.Fatalf("second move: %d kinds, %d fields (want reuse)", len(ck2), len(cf2))
	}
	if _, sk, _ := w.eng.locateExcept(ctx, Ref{Table: schema.TableKinds, ID: kind}, store.SourceRepo); len(listTexts(sk.Fields["copies"])) != 1 {
		t.Fatalf("second move recorded the copy twice: %v", sk.Fields["copies"])
	}
}

// mustList is a list-of-text value.
func mustList(t *testing.T, items ...string) value.Value {
	t.Helper()
	l, err := textList(items)
	if err != nil {
		t.Fatal(err)
	}
	return l
}
