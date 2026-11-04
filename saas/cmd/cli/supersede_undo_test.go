package main

// The --supersedes undo path: when marking the old row fails after the new
// row was saved, finishSupersedes deletes the new row again, so the
// database is exactly as it was before the add, and the command reports the
// marking error instead of success. The failure is injected into the mark
// step itself (an update hook), with the old row present.

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	entgo "entgo.io/ent"

	"dbent/gen/ent"
	"saas/pkg/aicoder/errcodes"
)

// errInjectedMark is the failure the hook puts into the mark step.
var errInjectedMark = errors.New("injected: mark step failed")

// failRuleUpdates makes every single-row rule update fail (the mark
// step of a rule --supersedes; memories are written by the 0.1.18 core, which has no --supersedes); creates and deletes still work.
func failRuleUpdates(c *ent.Client) {
	c.Rule.Use(func(next ent.Mutator) ent.Mutator {
		return ent.MutateFunc(func(ctx context.Context, m ent.Mutation) (ent.Value, error) {
			if m.Op().Is(entgo.OpUpdateOne) {
				return nil, errInjectedMark
			}
			return next.Mutate(ctx, m)
		})
	})
}

// ruleState is every rule row's id and link, the state a failed
// --supersedes must leave untouched.
func ruleState(t *testing.T, c *ent.Client) map[string]string {
	t.Helper()
	rows, err := c.Rule.Query().All(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	out := make(map[string]string, len(rows))
	for _, r := range rows {
		out[r.ID] = derefStr(r.SupersededByID) + "|" + r.Body
	}
	return out
}

func TestFinishSupersedesUndoAfterFailedMark(t *testing.T) {
	t.Parallel()
	const project = "prj_01a10237c9a279ee94a32b8450dfb6f9"
	for _, tc := range []struct {
		name     string
		undoFail bool
	}{
		{name: "undo removes the new row: DB as before"},
		{name: "undo fails too: error names the row to delete", undoFail: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			c := openTestClient(t)
			old, err := c.Rule.Create().SetProjectID(project).SetBody("the printer is downstairs").SetSourceKind("manual").Save(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if err := checkSupersedes(ctx, c, supersedeRule, project, old.ID); err != nil {
				t.Fatal(err)
			}
			before := ruleState(t, c)

			// The add saves the new row, then the mark step fails.
			repl, err := c.Rule.Create().SetProjectID(project).SetBody("the printer is upstairs").SetSourceKind("manual").Save(ctx)
			if err != nil {
				t.Fatal(err)
			}
			failRuleUpdates(c)
			undoCalls := 0
			undo := func(ctx context.Context) error {
				undoCalls++
				if tc.undoFail {
					return errors.New("injected: undo failed")
				}
				return c.Rule.DeleteOneID(repl.ID).Exec(ctx)
			}
			err = finishSupersedes(ctx, c, supersedeRule, old.ID, repl.ID, undo)

			var ce *errcodes.CLIError
			if !errors.As(err, &ce) || ce.Code != errcodes.Internal || !errors.Is(err, errInjectedMark) {
				t.Fatalf("want E_INTERNAL wrapping the mark failure, got %v", err)
			}
			if undoCalls != 1 {
				t.Fatalf("undo ran %d times, want 1", undoCalls)
			}
			after := ruleState(t, c)
			if tc.undoFail {
				if !strings.Contains(ce.Hint, "lore rule delete "+repl.ID) {
					t.Fatalf("hint must name the leftover row: %q", ce.Hint)
				}
				if _, left := after[repl.ID]; !left || after[old.ID] != before[old.ID] {
					t.Fatalf("undo failure: old row must be untouched, new row left for the user: %v", after)
				}
				return
			}
			if !reflect.DeepEqual(after, before) {
				t.Fatalf("DB after a failed --supersedes:\n got  %v\n want %v", after, before)
			}
		})
	}
}
