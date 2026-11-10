package metalist

import (
	"context"
	"testing"

	"github.com/thesatellite-ai/lore/logic/auditfill"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/schema"
)

func actorT(t *testing.T, s string) ids.ID[ids.Actor] {
	t.Helper()
	a, err := ids.Parse[ids.Actor](s)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

// TestAuditFilters (INTEG-AUDIT-1): --created-by / --updated-by keep the
// rows whose envelope names the actor, on any table; an old row without
// audit keys answers to "unknown"; a bad value is E_INVALID_ID.
func TestAuditFilters(t *testing.T) {
	t.Parallel()
	a := actorT(t, "act_01936f0000007a1b8c2d3e4f5a6b7c8d")
	b := actorT(t, "act_01936f0000007b1b8c2d3e4f5a6b7c8e")
	rows := []row.Row{
		{Table: schema.TableDecisions, CreatedBy: a, UpdatedBy: a}, // 0: made by A
		{Table: schema.TableTasks, CreatedBy: a, UpdatedBy: b},     // 1: made by A, edited by B
		{Table: schema.TableRules, CreatedBy: b, UpdatedBy: b},     // 2: made by B
		{Table: schema.TableMemories},                              // 3: from before audit fields
	}
	for _, c := range []struct {
		name string
		f    Audit
		want []int
	}{
		{"none", Audit{}, []int{0, 1, 2, 3}},
		{"created A", Audit{CreatedBy: a.String()}, []int{0, 1}},
		{"updated B", Audit{UpdatedBy: b.String()}, []int{1, 2}},
		{"created A updated B", Audit{CreatedBy: a.String(), UpdatedBy: b.String()}, []int{1}},
		{"unknown", Audit{CreatedBy: auditfill.UnknownName}, []int{3}},
	} {
		got, err := c.f.Keep(rows)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if len(got) != len(c.want) {
			t.Fatalf("%s: kept %v, want %v", c.name, got, c.want)
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Fatalf("%s: kept %v, want %v", c.name, got, c.want)
			}
		}
		res, err := Select(context.Background(), nil, Flags{Audit: c.f}, rows)
		if err != nil || len(res.Order) != len(c.want) {
			t.Fatalf("%s: Select = %v %v, want %v", c.name, res.Order, err, c.want)
		}
	}
	if _, err := (Audit{UpdatedBy: "alice"}).Keep(rows); !errs.Has(err, errs.InvalidID) {
		t.Fatalf("bad actor: %v", err)
	}
}
