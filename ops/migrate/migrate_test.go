package migrate

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/op"
)

// fixedMem is an arbitrary valid memory id.
const fixedMem = "mem_01a1a59b891c70b789144cd3e711fb92"

// fakeRewriter records every call so the tests can check order, the dry-run
// flag each step saw, and that the lock is always released.
type fakeRewriter struct {
	tables   []row.Table
	v1       map[row.Table][]ids.AnyID
	steps    []string
	setRows  int // rows the settings steps report
	live     []string
	lockErr  error
	failAt   string // "settings", "live", or a table name
	calls    []string
	dryRuns  []bool
	unlocked int
}

func (f *fakeRewriter) MigratedTables() []row.Table { return f.tables }

func (f *fakeRewriter) RewriteV1(_ context.Context, table row.Table, dryRun bool) ([]ids.AnyID, error) {
	f.calls, f.dryRuns = append(f.calls, "rewrite:"+table.String()), append(f.dryRuns, dryRun)
	if f.failAt == table.String() {
		return nil, errs.New(errs.Stale, "changed concurrently")
	}
	return f.v1[table], nil
}

func (f *fakeRewriter) MigrateSettings(_ context.Context, dryRun bool) (Settings, error) {
	f.calls, f.dryRuns = append(f.calls, "settings"), append(f.dryRuns, dryRun)
	if f.failAt == "settings" {
		return Settings{}, errs.New(errs.ConfigInvalid, "bad config")
	}
	return Settings{Steps: f.steps, Rows: f.setRows}, nil
}

func (f *fakeRewriter) MigrateLive(_ context.Context, dryRun bool) ([]string, error) {
	f.calls, f.dryRuns = append(f.calls, "live"), append(f.dryRuns, dryRun)
	if f.failAt == "live" {
		return nil, errs.New(errs.Internal, "git down")
	}
	return f.live, nil
}

func (f *fakeRewriter) EnableFeature(_ context.Context, name string) (EnableOut, error) {
	f.calls = append(f.calls, "enable "+name)
	return EnableOut{Feature: name, Added: true, Features: []string{name}}, nil
}

func (f *fakeRewriter) LockMigration(context.Context) (func() error, error) {
	f.calls = append(f.calls, "lock")
	if f.lockErr != nil {
		return nil, f.lockErr
	}
	return func() error { f.unlocked++; return nil }, nil
}

// runOp invokes migrate.run on f with a JSON input.
func runOp(t *testing.T, f *fakeRewriter, input string) (RunOut, error) {
	t.Helper()
	e := Ops(f)[0]
	in, err := e.Decode(op.JSONDecoder{Body: []byte(input)})
	if err != nil {
		t.Fatal(err)
	}
	res, err := e.Invoke(context.Background(), in)
	if err != nil {
		return RunOut{}, err
	}
	b, err := res.JSON()
	if err != nil {
		t.Fatal(err)
	}
	var out RunOut
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	return out, nil
}

// TestRunOrderAndOutput: the run takes the lock, converts settings, brings
// live rows, then rewrites each migrated table in the Rewriter's order; the
// output lists live rows then table/id rows, the steps, and the count. The
// dry-run flag reaches every step; the lock is released once. Empty input
// gives empty (never null) lists.
func TestRunOrderAndOutput(t *testing.T) {
	mem, err := ids.ParseAny(fixedMem)
	if err != nil {
		t.Fatal(err)
	}
	for _, dry := range []bool{false, true} {
		f := &fakeRewriter{
			tables: []row.Table{"memories", "rules"},
			v1:     map[row.Table][]ids.AnyID{"memories": {mem}},
			steps:  []string{"identity: moved"},
			live:   []string{"tasks/tsk_x"},
		}
		input := `{}`
		if dry {
			input = `{"dry_run":true}`
		}
		out, rerr := runOp(t, f, input)
		if rerr != nil {
			t.Fatal(rerr)
		}
		wantCalls := []string{"lock", "settings", "live", "rewrite:memories", "rewrite:rules"}
		if !slices.Equal(f.calls, wantCalls) {
			t.Fatalf("calls %v, want %v", f.calls, wantCalls)
		}
		for i, d := range f.dryRuns {
			if d != dry {
				t.Fatalf("step %d saw dry_run=%v, want %v", i, d, dry)
			}
		}
		if f.unlocked != 1 {
			t.Fatalf("unlocked %d times", f.unlocked)
		}
		wantRows := []string{"tasks/tsk_x", "memories/" + fixedMem}
		if out.DryRun != dry || out.Count != 2 || !slices.Equal(out.Rows, wantRows) || !slices.Equal(out.Steps, f.steps) {
			t.Fatalf("out %+v", out)
		}
	}
	empty, err := runOp(t, &fakeRewriter{}, `{}`)
	if err != nil || empty.Rows == nil || empty.Steps == nil || empty.Count != 0 {
		t.Fatalf("empty run: %+v %v", empty, err)
	}
}

// TestRunCountsEveryRow: count is every row the run wrote or moved, the
// Rows lines plus the rows the settings steps report (Mode B, personal
// flatten, org roster, policy, tags), in a dry run and a real one alike;
// steps that move no rows (identity, markers) add nothing. Found by the
// docs example: a Mode B migration of two rows printed `count: 0`.
func TestRunCountsEveryRow(t *testing.T) {
	mem, err := ids.ParseAny(fixedMem)
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name    string
		v1      []ids.AnyID
		live    []string
		steps   []string
		setRows int
		want    int
	}{
		{"nothing", nil, nil, nil, 0, 0},
		{"settings only, no rows", nil, nil, []string{"identity: moved"}, 0, 0},
		{"mode b rows only", nil, nil, []string{"mode-b: memories/x", "mode-b: rules/y", "mode-b: stores.yaml"}, 2, 2},
		{"rewrites and live", []ids.AnyID{mem}, []string{"tasks/tsk_x"}, nil, 0, 2},
		{"every kind", []ids.AnyID{mem}, []string{"tasks/tsk_x", "tasks/tsk_y"}, []string{"a", "b"}, 5, 8},
	}
	for _, c := range cases {
		for _, dry := range []bool{false, true} {
			f := &fakeRewriter{tables: []row.Table{"memories"}, v1: map[row.Table][]ids.AnyID{"memories": c.v1}, live: c.live, steps: c.steps, setRows: c.setRows}
			input := `{}`
			if dry {
				input = `{"dry_run":true}`
			}
			out, rerr := runOp(t, f, input)
			if rerr != nil {
				t.Fatal(rerr)
			}
			if out.Count != c.want || len(out.Rows) != len(c.v1)+len(c.live) || len(out.Steps) != len(c.steps) {
				t.Fatalf("%s (dry %v): %+v, want count %d", c.name, dry, out, c.want)
			}
		}
	}
}

// TestRunLockedAndFailures: a held lock is E_MIGRATE_LOCKED and nothing
// else runs; a failing step returns its own error code, stops the later
// steps, and still releases the lock.
func TestRunLockedAndFailures(t *testing.T) {
	locked := &fakeRewriter{tables: []row.Table{"memories"}, lockErr: errs.New(errs.MigrateLocked, "held by pid 1")}
	if _, err := runOp(t, locked, `{}`); !errs.Has(err, errs.MigrateLocked) {
		t.Fatalf("want E_MIGRATE_LOCKED, got %v", err)
	}
	if !slices.Equal(locked.calls, []string{"lock"}) {
		t.Fatalf("a locked run went on: %v", locked.calls)
	}
	for _, c := range []struct {
		at        string
		code      errs.Code
		lastCall  string
		callCount int
	}{
		{"settings", errs.ConfigInvalid, "settings", 2},
		{"live", errs.Internal, "live", 3},
		{"memories", errs.Stale, "rewrite:memories", 4},
	} {
		f := &fakeRewriter{tables: []row.Table{"memories", "rules"}, failAt: c.at}
		_, err := runOp(t, f, `{}`)
		if !errs.Has(err, c.code) {
			t.Fatalf("%s: want %s, got %v", c.at, c.code, err)
		}
		if len(f.calls) != c.callCount || f.calls[len(f.calls)-1] != c.lastCall {
			t.Fatalf("%s: calls %v", c.at, f.calls)
		}
		if f.unlocked != 1 {
			t.Fatalf("%s: the lock was not released", c.at)
		}
	}
	var plain = errors.New("x")
	if errs.Has(plain, errs.MigrateLocked) {
		t.Fatal("errs.Has matched a plain error")
	}
}

// TestRunSpec: migrate.run is an admin write under /v1/migrate/run that
// declares every code its steps return.
func TestRunSpec(t *testing.T) {
	s := Ops(&fakeRewriter{})[0].Spec()
	if s.Name != OpRun || s.Permission != op.PermAdmin || s.Access != op.AccessWrite || s.RESTNoun != restNoun {
		t.Fatalf("spec %+v", s)
	}
	for _, c := range []errs.Code{errs.MigrateLocked, errs.Stale, errs.ConfigInvalid, errs.Internal} {
		if !slices.Contains(s.Errors, c) {
			t.Errorf("spec does not declare %s", c)
		}
	}
}
