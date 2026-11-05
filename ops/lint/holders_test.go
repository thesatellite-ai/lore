package lint

import (
	"testing"

	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// TestHolderFindings (SECRETS 5.2, OV13b): a secret reference whose
// machine holder's label (after the person's handle) no longer matches the
// machine row's label is stale_holder_label; an actor holder's label must
// equal the actor's handle. A reference without a label, a holder lore has
// no row for, a machine row without a label and a matching label are not
// findings; each stale (row, holder, label) is reported once.
func TestHolderFindings(t *testing.T) {
	mch, mchNoLabel := id(t, ids.PrefixMachine, 1), id(t, ids.PrefixMachine, 2)
	act := id(t, ids.PrefixActor, 3)
	const fullMachineID = "mch_0cd630f0aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	base := map[row.Table][]sourced{
		schema.TableMachines: {
			{src: store.SourceRepo, r: row.Row{Table: schema.TableMachines, ID: mch, Fields: map[string]value.Value{
				schema.FieldMachineLabel: tx(t, "studio_mac"), schema.FieldMachineID: tx(t, fullMachineID)}}},
			{src: store.SourceRepo, r: row.Row{Table: schema.TableMachines, ID: mchNoLabel, Fields: map[string]value.Value{}}},
		},
		schema.TableActors: {
			{src: store.SourceRepo, r: row.Row{Table: schema.TableActors, ID: act, Fields: map[string]value.Value{fieldActorHandle: tx(t, "mira")}}},
		},
	}
	ref := func(holder, label string) string {
		s := "${secret:gopass://acme/db?holder=" + holder
		if label != "" {
			s += "&label=" + label
		}
		return s + "}"
	}
	unknown := id(t, ids.PrefixMachine, 9).String()
	cases := []struct {
		name string
		body string
		want int
	}{
		{name: "machine label current (with handle)", body: ref(mch.String(), "mira.studio_mac"), want: 0},
		{name: "machine label current (no handle)", body: ref(mch.String(), "studio_mac"), want: 0},
		{name: "machine label changed", body: ref(mch.String(), "mira.old_laptop"), want: 1},
		{name: "full machine id holder, label changed", body: ref(fullMachineID, "mira.old"), want: 1},
		{name: "no label", body: ref(mch.String(), ""), want: 0},
		{name: "unknown machine", body: ref(unknown, "mira.x"), want: 0},
		{name: "machine row has no label", body: ref(mchNoLabel.String(), "mira.x"), want: 0},
		{name: "actor holder, handle current", body: ref(act.String(), "mira"), want: 0},
		{name: "actor holder, handle changed", body: ref(act.String(), "mira_old"), want: 1},
		{name: "same stale ref twice in one row", body: ref(mch.String(), "a.b") + " and " + ref(mch.String(), "a.b"), want: 1},
		{name: "two different stale refs", body: ref(mch.String(), "a.b") + " " + ref(act.String(), "x"), want: 2},
		{name: "alternates: one stale", body: "${secret:gopass://a?holder=" + mch.String() + "&label=mira.studio_mac|env://X?holder=" + act.String() + "&label=old}", want: 1},
		{name: "no reference", body: "plain text", want: 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			all := map[row.Table][]sourced{}
			for k, v := range base {
				all[k] = v
			}
			all[schema.TableMemories] = []sourced{{src: store.SourceRepo, r: row.Row{Table: schema.TableMemories, ID: id(t, ids.PrefixMemory, 4),
				Fields: map[string]value.Value{"body": tx(t, c.body)}}}}
			got := holderFindings(all)
			if len(got) != c.want {
				t.Fatalf("%d findings, want %d: %+v", len(got), c.want, got)
			}
			for _, f := range got {
				if f.Code != StaleHolderLabel || f.Fix == "" {
					t.Fatalf("finding %+v", f)
				}
			}
		})
	}
	// no machines or actors with labels at all: nothing to compare
	if got := holderFindings(map[row.Table][]sourced{schema.TableMemories: {{src: store.SourceRepo, r: row.Row{Table: schema.TableMemories,
		ID: id(t, ids.PrefixMemory, 5), Fields: map[string]value.Value{"body": tx(t, ref(mch.String(), "a.b"))}}}}}); len(got) != 0 {
		t.Fatalf("no labels known: %+v", got)
	}
}
