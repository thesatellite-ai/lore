package main

// Repair of --supersedes links written by older versions (link stored on
// the NEW row): the pure plan over an input matrix of chain shapes, and the
// repair on a real DB, run twice to show the second run changes nothing.

import (
	"context"
	"reflect"
	"sort"
	"testing"
	"time"

	"dbent/gen/ent"
	entDecision "dbent/gen/ent/decision"
)

func TestPlanSupersedeRepair(t *testing.T) {
	t.Parallel()
	const prj, other = "prj_a", "prj_b"
	at := func(min int) time.Time { return time.Date(2026, 1, 1, 0, min, 0, 0, time.UTC) }
	n := func(id string, min int, by string) supersedeNode {
		return supersedeNode{id: id, projectID: prj, supersededBy: by, createdAt: at(min)}
	}
	cases := []struct {
		name      string
		nodes     []supersedeNode
		wantFixes []supersedeFix
		wantSet   map[string]string
		wantSkips []string // ids skipped
	}{
		{name: "no links", nodes: []supersedeNode{n("a", 1, ""), n("b", 2, "")}, wantSet: map[string]string{}},
		{name: "correct link (old → new) is left alone", nodes: []supersedeNode{n("a", 1, "b"), n("b", 2, "")}, wantSet: map[string]string{}},
		{
			name: "reversed link (new → old) is turned round", nodes: []supersedeNode{n("a", 1, ""), n("b", 2, "a")},
			wantFixes: []supersedeFix{{supersedeMemory, "a", "b"}}, wantSet: map[string]string{"a": "b", "b": ""},
		},
		{
			name: "reversed chain of three", nodes: []supersedeNode{n("a", 1, ""), n("b", 2, "a"), n("c", 3, "b")},
			wantFixes: []supersedeFix{{supersedeMemory, "a", "b"}, {supersedeMemory, "b", "c"}},
			wantSet:   map[string]string{"a": "b", "b": "c", "c": ""},
		},
		{
			name: "interrupted repair (both directions) only clears the reversed one", nodes: []supersedeNode{n("a", 1, "b"), n("b", 2, "a")},
			wantFixes: []supersedeFix{{supersedeMemory, "a", "b"}}, wantSet: map[string]string{"a": "b", "b": ""},
		},
		{
			name: "interrupted chain repair finishes", nodes: []supersedeNode{n("a", 1, "b"), n("b", 2, "a"), n("c", 3, "b")},
			wantFixes: []supersedeFix{{supersedeMemory, "a", "b"}, {supersedeMemory, "b", "c"}},
			wantSet:   map[string]string{"a": "b", "b": "c", "c": ""},
		},
		{
			name: "two rows claim to replace one: both skipped", nodes: []supersedeNode{n("a", 1, ""), n("b", 2, "a"), n("c", 3, "a")},
			wantSet: map[string]string{}, wantSkips: []string{"b", "c"},
		},
		{
			name: "old row already has a real successor: skipped", nodes: []supersedeNode{n("a", 1, "c"), n("b", 2, "a"), n("c", 3, "")},
			wantSet: map[string]string{}, wantSkips: []string{"b"},
		},
		{
			name: "same creation time: direction unknown, skipped", nodes: []supersedeNode{n("a", 1, ""), n("b", 1, "a")},
			wantSet: map[string]string{}, wantSkips: []string{"b"},
		},
		{
			name:    "link into another project or to a missing row is ignored",
			nodes:   []supersedeNode{n("a", 1, ""), {id: "b", projectID: other, supersededBy: "a", createdAt: at(2)}, n("c", 3, "zzz")},
			wantSet: map[string]string{},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			p := planSupersedeRepair(supersedeMemory, tc.nodes)
			if !reflect.DeepEqual(p.fixes, tc.wantFixes) {
				t.Fatalf("fixes %+v, want %+v", p.fixes, tc.wantFixes)
			}
			if !reflect.DeepEqual(p.set, tc.wantSet) {
				t.Fatalf("set %v, want %v", p.set, tc.wantSet)
			}
			var skipped []string
			for _, s := range p.skips {
				skipped = append(skipped, s.id)
			}
			sort.Strings(skipped)
			if !reflect.DeepEqual(skipped, tc.wantSkips) {
				t.Fatalf("skips %+v, want ids %v", p.skips, tc.wantSkips)
			}
			// Applying the plan's final links and planning again finds nothing.
			after := append([]supersedeNode(nil), tc.nodes...)
			for i := range after {
				if next, ok := p.set[after[i].id]; ok {
					after[i].supersededBy = next
				}
			}
			if again := planSupersedeRepair(supersedeMemory, after); len(again.fixes) != 0 {
				t.Fatalf("plan after repair is not empty: %+v", again.fixes)
			}
		})
	}
}

// supersedeLinks reads id → superseded_by_id ("" for none) and decision
// statuses, the whole state a repair may touch.
func supersedeLinks(t *testing.T, c *ent.Client) map[string]string {
	t.Helper()
	ctx := context.Background()
	out := map[string]string{}
	for _, kind := range supersedeKinds {
		nodes, err := loadSupersedeNodes(ctx, c, kind)
		if err != nil {
			t.Fatal(err)
		}
		for _, n := range nodes {
			out[n.id] = n.supersededBy
			if kind == supersedeDecision {
				out[n.id+"#status"] = string(n.decisionStatus)
			}
		}
	}
	return out
}

func TestRepairSupersedesOldShapeRowsTwice(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	c := openTestClient(t)
	const project = "prj_01a10237c9a279ee94a32b8450dfb6f9"
	t0 := time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC)

	// Old shape, as an older `memory add --supersedes` wrote it: the NEW
	// memory carries the link to the old one; the old one has none.
	oldMem, err := c.Memory.Create().SetProjectID(project).SetBody("the printer is downstairs").SetSourceKind("manual").SetCreatedAt(t0).Save(ctx)
	if err != nil {
		t.Fatal(err)
	}
	newMem, err := c.Memory.Create().SetProjectID(project).SetBody("the printer is upstairs").SetSourceKind("manual").SetCreatedAt(t0.Add(time.Hour)).SetSupersededByID(oldMem.ID).Save(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// Decisions: an accepted old one (moves to superseded) and a
	// deprecated old one (status kept).
	mkDec := func(title string, at time.Time, st entDecision.Status, by string) *ent.Decision {
		cr := c.Decision.Create().SetProjectID(project).SetTitle(title).SetBody(title).SetStatus(st).SetSourceKind("manual").SetCreatedAt(at)
		if by != "" {
			cr.SetSupersededByID(by)
		}
		d, err := cr.Save(ctx)
		if err != nil {
			t.Fatal(err)
		}
		return d
	}
	oldDec := mkDec("Payments: PayPal", t0, entDecision.StatusAccepted, "")
	newDec := mkDec("Payments: Stripe", t0.Add(time.Hour), entDecision.StatusAccepted, oldDec.ID)
	deprDec := mkDec("Queue: SQS", t0, entDecision.StatusDeprecated, "")
	replDec := mkDec("Queue: NATS", t0.Add(time.Hour), entDecision.StatusAccepted, deprDec.ID)

	found, err := repairSupersedes(ctx, c, false)
	if err != nil || len(found.fixes) != 3 {
		t.Fatalf("report: %+v %v", found, err)
	}
	if got := supersedeLinks(t, c); got[newMem.ID] != oldMem.ID {
		t.Fatalf("a report-only run changed rows: %v", got)
	}

	res, err := repairSupersedes(ctx, c, true)
	if err != nil || len(res.fixes) != 3 || len(res.skips) != 0 {
		t.Fatalf("repair: %+v %v", res, err)
	}
	want := map[string]string{
		oldMem.ID: newMem.ID, newMem.ID: "",
		oldDec.ID: newDec.ID, oldDec.ID + "#status": string(entDecision.StatusSuperseded),
		newDec.ID: "", newDec.ID + "#status": string(entDecision.StatusAccepted),
		deprDec.ID: replDec.ID, deprDec.ID + "#status": string(entDecision.StatusDeprecated),
		replDec.ID: "", replDec.ID + "#status": string(entDecision.StatusAccepted),
	}
	first := supersedeLinks(t, c)
	if !reflect.DeepEqual(first, want) {
		t.Fatalf("after repair:\n got  %v\n want %v", first, want)
	}

	// Second run: nothing to do, nothing changed.
	again, err := repairSupersedes(ctx, c, true)
	if err != nil || len(again.fixes) != 0 || len(again.skips) != 0 {
		t.Fatalf("second repair: %+v %v", again, err)
	}
	if second := supersedeLinks(t, c); !reflect.DeepEqual(second, first) {
		t.Fatalf("second repair changed rows:\n got  %v\n want %v", second, first)
	}
}
