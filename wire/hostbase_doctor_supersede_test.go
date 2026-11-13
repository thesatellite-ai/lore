package wire

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/logic/listcap"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/op"
	opsdecision "github.com/thesatellite-ai/lore/ops/decision"
	opsrule "github.com/thesatellite-ai/lore/ops/rule"
	"github.com/thesatellite-ai/lore/schema"
)

// node is a chain node at minute m of a fixed day.
func node(id, project, next string, m int) supersedeNode {
	return supersedeNode{id: id, projectID: project, supersededBy: next, createdAt: time.Date(2026, 11, 6, 10, m, 0, 0, time.UTC)}
}

// TestPlanSupersedeRepairMatrix covers every shape of a chain link: none,
// correct (old → new), reversed (new → old: one fix, the new row cleared),
// equal instants (skipped, noted), two reversed links into one row (both
// skipped), a target with a real successor (skipped), a target whose own
// link is reversed too (a chain a → b → c written backwards: both fixed),
// an interrupted repair that left both directions (the old row kept, only
// the new row cleared), a link into another project or to an unknown row
// (ignored), and the plan of a repaired set is empty.
func TestPlanSupersedeRepairMatrix(t *testing.T) {
	t.Parallel()
	type want struct {
		fixes     []supersedeFix
		set       map[string]string
		skips     int
		conflicts int
	}
	cases := map[string]struct {
		nodes []supersedeNode
		want  want
	}{
		"no links":     {[]supersedeNode{node("a", "p", "", 1), node("b", "p", "", 2)}, want{set: map[string]string{}}},
		"correct link": {[]supersedeNode{node("a", "p", "b", 1), node("b", "p", "", 2)}, want{set: map[string]string{}}},
		"reversed":     {[]supersedeNode{node("a", "p", "", 1), node("b", "p", "a", 2)}, want{fixes: []supersedeFix{{"rule", "a", "b"}}, set: map[string]string{"a": "b", "b": ""}}},
		"same instant": {[]supersedeNode{node("a", "p", "", 1), node("b", "p", "a", 1)}, want{set: map[string]string{}, skips: 1}},
		"two into one": {[]supersedeNode{node("a", "p", "", 1), node("b", "p", "a", 2), node("c", "p", "a", 3)}, want{set: map[string]string{}, conflicts: 1}},
		"target has a real successor": {[]supersedeNode{node("a", "p", "d", 1), node("b", "p", "a", 3), node("d", "p", "", 2)},
			want{set: map[string]string{}, skips: 1}},
		"chain written backwards": {[]supersedeNode{node("a", "p", "", 1), node("b", "p", "a", 2), node("c", "p", "b", 3)},
			want{fixes: []supersedeFix{{"rule", "a", "b"}, {"rule", "b", "c"}}, set: map[string]string{"a": "b", "b": "c", "c": ""}}},
		"interrupted repair": {[]supersedeNode{node("a", "p", "b", 1), node("b", "p", "a", 2)},
			want{fixes: []supersedeFix{{"rule", "a", "b"}}, set: map[string]string{"a": "b", "b": ""}}},
		"other project":  {[]supersedeNode{node("a", "p", "", 1), node("b", "q", "a", 2)}, want{set: map[string]string{}}},
		"unknown target": {[]supersedeNode{node("b", "p", "zzz", 2)}, want{set: map[string]string{}}},
	}
	for name, c := range cases {
		got := planSupersedeRepair("rule", c.nodes)
		if !slices.Equal(got.fixes, c.want.fixes) {
			t.Errorf("%s: fixes %v, want %v", name, got.fixes, c.want.fixes)
		}
		if len(got.set) != len(c.want.set) {
			t.Errorf("%s: set %v, want %v", name, got.set, c.want.set)
		}
		for k, v := range c.want.set {
			if have, ok := got.set[k]; !ok || have != v {
				t.Errorf("%s: set[%s] = %q (%v), want %q", name, k, have, ok, v)
			}
		}
		if len(got.skips) != c.want.skips {
			t.Errorf("%s: skips %v, want %d", name, got.skips, c.want.skips)
		}
		if len(got.conflicts) != c.want.conflicts {
			t.Errorf("%s: conflicts %v, want %d", name, got.conflicts, c.want.conflicts)
		}
	}
	// two rows replacing one: one finding with both ids and the target,
	// the next step, and no fix
	two := planSupersedeRepair("rule", []supersedeNode{node("a", "p", "", 1), node("b", "p", "a", 2), node("c", "p", "a", 3)})
	if len(two.conflicts) != 1 || len(two.fixes) != 0 {
		t.Fatalf("two into one = %+v", two)
	}
	if got := conflictText(two.conflicts[0]); !strings.Contains(got, "rule rows b and c each claim to replace a") || !strings.Contains(got, "lore rule delete --id <id>") ||
		!strings.Contains(got, "lore doctor --fix") {
		t.Errorf("conflict finding = %q", got)
	}
	// applying a plan's set yields an empty plan (idempotent)
	n := []supersedeNode{node("a", "p", "", 1), node("b", "p", "a", 2), node("c", "p", "b", 3)}
	p := planSupersedeRepair("rule", n)
	for i := range n {
		if next, ok := p.set[n[i].id]; ok {
			n[i].supersededBy = next
		}
	}
	if again := planSupersedeRepair("rule", n); len(again.fixes)+len(again.skips) != 0 {
		t.Errorf("plan after repair = %+v, want empty", again)
	}
}

// addRow adds a row through the registry and returns its id.
func addRow(t *testing.T, a *App, opName, input string) string {
	t.Helper()
	out, err := run(t, a, opName, input, true)
	if err != nil {
		t.Fatal(err)
	}
	return idOf(t, out)
}

// successor reads a row's superseded_by_id ("" when none) and status.
func successor(t *testing.T, a *App, tbl, id string) (next, status string) {
	t.Helper()
	rid, err := ids.ParseAny(id)
	if err != nil {
		t.Fatal(err)
	}
	_, r, err := a.Get(background(), engine.Ref{Table: row.Table(tbl), ID: rid})
	if err != nil {
		t.Fatal(err)
	}
	if v, ok := r.Fields[supersedeFieldNext]; ok && v != nil && v.Kind() != value.KindNull {
		next = v.String()
	}
	if v, ok := r.Fields[supersedeFieldStatus]; ok && v != nil && v.Kind() != value.KindNull {
		status = v.String()
	}
	return next, status
}

// TestDoctorSupersedesRepair: reversed links on a rule and on an accepted
// decision are one warning listing both (plain doctor writes nothing);
// --fix without the caller's yes answers E_NEEDS_CONFIRM naming them and
// writes nothing; with yes both are repaired in one run (the old rows point
// at the new ones, the new rows' links cleared, the accepted decision moved
// to superseded) and a second run finds nothing. Break-verified: with the
// op.Confirmed check removed the refusal case writes.
func TestDoctorSupersedesRepair(t *testing.T) {
	t.Parallel()
	repo := gitRepo(t)
	a := openWith(t, repo, Options{})
	withProject(t, a)
	ctx := background()
	oldRule := addRow(t, a, opsrule.OpAdd, `{"body":"Always use tabs in Go files"}`)
	newRule := addRow(t, a, opsrule.OpAdd, `{"body":"Always run gofmt on Go files"}`)
	oldDec := addRow(t, a, opsdecision.OpAdd, `{"title":"Use SQLite","body":"one file database"}`)
	newDec := addRow(t, a, opsdecision.OpAdd, `{"title":"Use SQLite with WAL","body":"one file database in WAL mode"}`)
	if _, st := successor(t, a, schema.TableDecisions, oldDec); st == "" {
		t.Fatal("decision has no status")
	}
	setStatus, err := value.TextOf(schema.DecisionStatusAccepted)
	if err != nil {
		t.Fatal(err)
	}
	decID, err := ids.ParseAny(oldDec)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.Update(ctx, engine.Ref{Table: row.Table(schema.TableDecisions), ID: decID}, map[string]value.Value{supersedeFieldStatus: setStatus}); err != nil {
		t.Fatal(err)
	}
	// the old binary's shape: the link on the NEW row
	if err = a.setSuccessor(ctx, row.Table(schema.TableRules), newRule, oldRule, ""); err != nil {
		t.Fatal(err)
	}
	if err = a.setSuccessor(ctx, row.Table(schema.TableDecisions), newDec, oldDec, ""); err != nil {
		t.Fatal(err)
	}
	var warns, notes, fails, fixed []string
	runIt := func(c context.Context, fix bool) error {
		warns, notes, fails, fixed = nil, nil, nil, nil
		return a.doctorSupersedes(c, fix, func(s string) { warns = append(warns, s) }, func(s string) { notes = append(notes, s) }, func(s string) { fails = append(fails, s) },
			func(s string) { fixed = append(fixed, s) })
	}
	unchanged := func(stage string) {
		t.Helper()
		if n, _ := successor(t, a, schema.TableRules, newRule); n != oldRule {
			t.Errorf("%s: new rule link = %q, want the reversed %s kept", stage, n, oldRule)
		}
		if n, _ := successor(t, a, schema.TableRules, oldRule); n != "" {
			t.Errorf("%s: old rule link = %q, want none yet", stage, n)
		}
	}
	if err = runIt(ctx, false); err != nil {
		t.Fatal(err)
	}
	if len(warns) != 1 || !strings.Contains(warns[0], "2 --supersedes link(s)") || !strings.Contains(warns[0], "rule "+oldRule+" → "+newRule) ||
		!strings.Contains(warns[0], "decision "+oldDec+" → "+newDec) {
		t.Fatalf("plain doctor warns = %v", warns)
	}
	unchanged("plain doctor")

	err = runIt(op.WithConfirmed(ctx, false), true)
	if errs.CodeOf(err) != errs.NeedsConfirm || !strings.Contains(err.Error(), "Repair 2 --supersedes link(s)") {
		t.Fatalf("--fix without yes = %v, want E_NEEDS_CONFIRM naming 2 links", err)
	}
	unchanged("--fix without yes")

	if err = runIt(op.WithConfirmed(ctx, true), true); err != nil {
		t.Fatal(err)
	}
	// item 46: the repair that wrote is a fixed line (lore fix's doctor
	// fixes step lists it), not a note the step leaves out
	if len(fails) != 0 || len(fixed) != 1 || !strings.HasPrefix(fixed[0], "2 --supersedes links repaired") || !strings.Contains(fixed[0], "rule "+oldRule+" → "+newRule) {
		t.Fatalf("--fix --yes: fixed %v notes %v fails %v", fixed, notes, fails)
	}
	if n, _ := successor(t, a, schema.TableRules, oldRule); n != newRule {
		t.Errorf("old rule link = %q, want %s", n, newRule)
	}
	if n, _ := successor(t, a, schema.TableRules, newRule); n != "" {
		t.Errorf("new rule link = %q, want cleared", n)
	}
	if n, st := successor(t, a, schema.TableDecisions, oldDec); n != newDec || st != schema.DecisionStatusSuperseded {
		t.Errorf("old decision = %q %s, want %s superseded", n, st, newDec)
	}
	if err = runIt(op.WithConfirmed(ctx, true), true); err != nil || len(warns)+len(fails)+len(fixed) != 0 || hasLine(notes, "repaired") {
		t.Errorf("second run: err %v warns %v notes %v fails %v fixed %v, want nothing", err, warns, notes, fails, fixed)
	}
}

// TestMigrateConvertsOldSupersedes is the owner's adopted v0.1.17 data
// (2026-11-12): links lore 0.1.16 and older wrote on the new row are moved
// onto the old row by `lore migrate run`'s supersedes step (dry run counts,
// run writes, rerun nothing), so doctor no longer reports them; two rules
// both replacing one stay as they are and doctor names both ids.
func TestMigrateConvertsOldSupersedes(t *testing.T) {
	t.Parallel()
	repo := gitRepo(t)
	a := openWith(t, repo, Options{})
	withProject(t, a)
	ctx := background()
	oldRule := addRow(t, a, opsrule.OpAdd, `{"body":"Always use tabs in Go files"}`)
	newRule := addRow(t, a, opsrule.OpAdd, `{"body":"Always run gofmt on Go files"}`)
	shared := addRow(t, a, opsrule.OpAdd, `{"body":"Review every change"}`)
	twinA := addRow(t, a, opsrule.OpAdd, `{"body":"Review every change twice"}`)
	twinB := addRow(t, a, opsrule.OpAdd, `{"body":"Review every change in pairs"}`)
	for _, l := range [][2]string{{newRule, oldRule}, {twinA, shared}, {twinB, shared}} {
		if err := a.setSuccessor(ctx, row.Table(schema.TableRules), l[0], l[1], ""); err != nil {
			t.Fatal(err)
		}
	}
	steps, n, err := a.migrateSupersedes(ctx, true)
	if err != nil || n != 1 || len(steps) != 1 || !strings.Contains(steps[0], "would move 1 --supersedes link") || !strings.Contains(steps[0], "rule "+oldRule+" → "+newRule) {
		t.Fatalf("dry run = %q, %d, %v", steps, n, err)
	}
	if next, _ := successor(t, a, schema.TableRules, oldRule); next != "" {
		t.Fatal("the dry run wrote")
	}
	if steps, n, err = a.migrateSupersedes(ctx, false); err != nil || n != 1 || !strings.Contains(steps[0], "moved 1 --supersedes link") {
		t.Fatalf("run = %q, %d, %v", steps, n, err)
	}
	if next, _ := successor(t, a, schema.TableRules, oldRule); next != newRule {
		t.Errorf("old rule link = %q, want %s", next, newRule)
	}
	if next, _ := successor(t, a, schema.TableRules, newRule); next != "" {
		t.Errorf("new rule link = %q, want cleared", next)
	}
	if steps, n, err = a.migrateSupersedes(ctx, false); err != nil || n != 0 || len(steps) != 0 {
		t.Fatalf("rerun = %q, %d, %v; want nothing", steps, n, err)
	}
	var warns []string
	if err = a.doctorSupersedes(ctx, false, func(s string) { warns = append(warns, s) }, func(string) {}, func(s string) { t.Error(s) }, func(s string) { t.Error(s) }); err != nil {
		t.Fatal(err)
	}
	if len(warns) != 1 || !strings.Contains(warns[0], twinA) || !strings.Contains(warns[0], twinB) || !strings.Contains(warns[0], "claim to replace "+shared) ||
		strings.Contains(warns[0], "point from the new row") {
		t.Fatalf("doctor after the run = %q, want only the two-into-one finding naming both ids", warns)
	}
	for _, id := range []string{twinA, twinB} {
		if next, _ := successor(t, a, schema.TableRules, id); next != shared {
			t.Errorf("%s link = %q; the two-into-one rows must be left as they are", id, next)
		}
	}
}

// TestSupersedeWarningOf50Links: doctor's finding for 50 reversed links
// keeps all 50 in the line (--json's warnings[] and findings[]), and the
// text a person reads names 5, then "… and 45 more (`lore doctor --json`
// lists all)", then the rest of the sentence with the fix command (the
// owner saw 84 links on one line several thousand characters long).
func TestSupersedeWarningOf50Links(t *testing.T) {
	t.Parallel()
	fixes := make([]supersedeFix, 50)
	for i := range fixes {
		fixes[i] = supersedeFix{kind: "rule", oldID: fmt.Sprintf("rul_%032x", 2*i), newID: fmt.Sprintf("rul_%032x", 2*i+1)}
	}
	line := supersedeWarning(fixes)
	if !strings.Contains(line, fixes[49].oldID) || !strings.HasPrefix(line, "50 --supersedes link(s)") {
		t.Fatalf("the finding lost links: %s", line)
	}
	shown := listcap.Text(line, "`lore doctor --json` lists all")
	if strings.Contains(shown, fixes[5].oldID) || !strings.Contains(shown, fixes[4].newID+" … and 45 more (`lore doctor --json` lists all); each read as old → new") ||
		!strings.HasSuffix(shown, "run `lore doctor --fix` to move each link onto the old row (lore migrate run and lore store adopt convert them too)") {
		t.Fatalf("text not capped as expected:\n%s", shown)
	}
	if len(shown) > 1000 {
		t.Fatalf("text still %d bytes", len(shown))
	}
}

// TestMigrateCommitSubject (item 46): a store clone's format-upgrade
// commit is titled as a migration and names the command that ran it, not
// the generic `lore: write mem_… (+4831 more)`.
func TestMigrateCommitSubject(t *testing.T) {
	t.Parallel()
	ctx := background()
	want := fmt.Sprintf("lore: migrate 4831 rows to format %d (lore migrate run)", row.FormatVersion)
	if got := migrateCommitSubject(ctx, 4831); got != want {
		t.Errorf("migrate run subject = %q, want %q", got, want)
	}
	want = fmt.Sprintf("lore: migrate 1 row to format %d (lore fix)", row.FormatVersion)
	if got := migrateCommitSubject(withMigrateVia(ctx, migrateViaFix), 1); got != want {
		t.Errorf("lore fix subject = %q, want %q", got, want)
	}
}
