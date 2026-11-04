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

	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/schema"
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

// The repair on real core rows (rules, memories, hotfixes, patterns and
// decisions are core-owned in 0.1.18): rows are added through the binary,
// the old shape (the NEW row carries the link to the old one, as an older
// `--supersedes` wrote it) is written through the core, and the repair runs
// twice to show the second run changes nothing. Memories are back in the
// fixture (they had been swapped for rules while the core had no memory
// writes).
func TestRepairSupersedesOldShapeRowsTwice(t *testing.T) {
	ctx := context.Background()
	cf := newCoreFixture(t)
	oldMem := cf.addID("memory", "add", "--body", "the printer is downstairs")
	newMem := cf.addID("memory", "add", "--body", "the printer is upstairs")
	oldRule := cf.addID("rule", "add", "--body", "deploy on fridays")
	newRule := cf.addID("rule", "add", "--body", "never deploy on fridays")
	// Decisions: an accepted old one (moves to superseded) and a
	// deprecated old one (status kept).
	oldDec := cf.addID("decision", "add", "--title", "Payments: PayPal", "--body", "Payments: PayPal")
	newDec := cf.addID("decision", "add", "--title", "Payments: Stripe", "--body", "Payments: Stripe")
	deprDec := cf.addID("decision", "add", "--title", "Queue: SQS", "--body", "Queue: SQS", "--status", string(entDecision.StatusDeprecated))
	replDec := cf.addID("decision", "add", "--title", "Queue: NATS", "--body", "Queue: NATS")
	cf.mount()
	c := openTestClient(t) // the callers' lore.db client; the repair reads and writes the core
	for _, l := range []struct {
		table          row.Table
		newer, olderID string
	}{
		{schema.TableMemories, newMem, oldMem}, {schema.TableRules, newRule, oldRule},
		{schema.TableDecisions, newDec, oldDec}, {schema.TableDecisions, replDec, deprDec},
	} {
		if err := coreSetSuccessor(ctx, l.table, l.newer, l.olderID, ""); err != nil {
			t.Fatal(err)
		}
	}

	found, err := repairSupersedes(ctx, c, false)
	if err != nil || len(found.fixes) != 4 {
		t.Fatalf("report: %+v %v", found, err)
	}
	if got := supersedeLinks(t, c); got[newMem] != oldMem {
		t.Fatalf("a report-only run changed rows: %v", got)
	}

	res, err := repairSupersedes(ctx, c, true)
	if err != nil || len(res.fixes) != 4 || len(res.skips) != 0 {
		t.Fatalf("repair: %+v %v", res, err)
	}
	want := map[string]string{
		oldMem: newMem, newMem: "",
		oldRule: newRule, newRule: "",
		oldDec: newDec, oldDec + "#status": string(entDecision.StatusSuperseded),
		newDec: "", newDec + "#status": string(entDecision.StatusAccepted),
		deprDec: replDec, deprDec + "#status": string(entDecision.StatusDeprecated),
		replDec: "", replDec + "#status": string(entDecision.StatusAccepted),
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
