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
	if _, _, err = w.eng.resourceIntoClient(ctx, store.SourceRepo, web, fields, false); !errs.Has(err, errs.LeakBlocked) {
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
}
