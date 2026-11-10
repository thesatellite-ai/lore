package metalist

import (
	"context"
	"testing"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/logic/meta"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
)

// oldRow is a replaced row holding two metadata keys.
func oldRow(t *testing.T) row.Row {
	t.Helper()
	m := map[ids.ID[ids.MetaKey]]value.JSON{}
	for name, v := range map[string]string{"client": `"acme"`, "tier": `"gold"`} {
		id, err := ids.New[ids.MetaKey]()
		if err != nil {
			t.Fatal(err)
		}
		j, err := meta.Entry{Name: name, V: []byte(v)}.Encode()
		if err != nil {
			t.Fatal(err)
		}
		m[id] = j
	}
	return row.Row{Table: schema.TableDecisions, Meta: m}
}

// TestPlanCarry: 3.19 / CLI_REFERENCE 2.4 — every key is carried by the
// create itself when no --meta is given, none with --no-meta, and with
// --meta the create is left alone (After sets the rest).
func TestPlanCarry(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name      string
		explicit  engine.MetaFlags
		noMeta    bool
		wantTerms int
		wantCount int  // after Before
		wantCtx   bool // Before binds something on ctx
	}{
		{"carry all on the create", engine.MetaFlags{}, false, 2, 2, true},
		{"--no-meta carries nothing", engine.MetaFlags{}, true, 0, 0, false},
		// OV30-CARRY-REQ: with --meta the create still gets the carry (as an
		// engine.MetaCarry filled before the required check); the count is
		// known only after the create runs.
		{"--meta given: carry bound for the create", engine.MetaFlags{Meta: []string{"client=globex"}}, false, 2, 0, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cr, err := PlanCarry(oldRow(t), schema.TableDecisions, c.explicit, c.noMeta)
			if err != nil {
				t.Fatal(err)
			}
			if len(cr.terms) != c.wantTerms {
				t.Fatalf("terms %d, want %d", len(cr.terms), c.wantTerms)
			}
			for _, tm := range cr.terms {
				if tm.KeyID.IsZero() || tm.Value == "" {
					t.Errorf("term %+v: want a key id and a JSON value", tm)
				}
			}
			got := cr.Before(ctx)
			if cr.Count != c.wantCount || c.wantCtx != (got != ctx) {
				t.Errorf("Before: count %d (want %d), ctx changed %v", cr.Count, c.wantCount, got != ctx)
			}
		})
	}
	if _, err := PlanCarry(row.Row{}, schema.TableDecisions, engine.MetaFlags{}, false); err != nil {
		t.Errorf("a row without metadata: %v", err)
	}
}

// TestCarryLine: the "carried over N keys" note.
func TestCarryLine(t *testing.T) {
	for n, want := range map[int]string{0: "", 1: "carried over 1 key\n", 3: "carried over 3 keys\n"} {
		if got := (Carry{Count: n}).Line(); got != want {
			t.Errorf("%d: %q, want %q", n, got, want)
		}
	}
}
