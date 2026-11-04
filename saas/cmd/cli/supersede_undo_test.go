package main

// The --supersedes undo path: when marking the old row fails after the new
// row was saved, finishSupersedes deletes the new row again, so the rows
// are exactly as they were before the add, and the command reports the
// marking error instead of success. Rules are core-owned (0.1.18): the rows
// are real core rows, and the failure is injected into the mark step's core
// write itself (the rules folder is made read-only), with the old row
// present.

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"saas/pkg/aicoder/errcodes"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// Folder modes of the injected failure: read-only blocks the core's file
// write (it writes a temp file beside the row), writable puts it back.
const (
	rulesDirReadOnly fs.FileMode = 0o555
	rulesDirWritable fs.FileMode = 0o750
)

// coreRuleState is every live rule's id and link, read through the core:
// the state a failed --supersedes must leave untouched.
func coreRuleState(t *testing.T) map[string]string {
	t.Helper()
	rows, err := coreApp.List(context.Background(), store.SourceRepo, schema.TableRules)
	if err != nil {
		t.Fatal(err)
	}
	out := make(map[string]string, len(rows))
	for _, r := range rows {
		if r.Deleted {
			continue
		}
		next := ""
		if v, ok := r.Fields[supersedeFieldNext]; ok && v != nil && v.Kind() != value.KindNull {
			next = v.String()
		}
		body := ""
		if v, ok := r.Fields["body"]; ok && v != nil {
			body = v.String()
		}
		out[r.ID.String()] = next + "|" + body
	}
	return out
}

func TestFinishSupersedesUndoAfterFailedMark(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores the read-only folder that injects the failure")
	}
	for _, tc := range []struct {
		name     string
		undoFail bool
	}{
		{name: "undo removes the new row: rows as before"},
		{name: "undo fails too: error names the row to delete", undoFail: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			cf := newCoreFixture(t)
			old := cf.addID("rule", "add", "--body", "the printer is downstairs")
			// The add saves the new row (the mark step comes after it).
			repl := cf.addID("rule", "add", "--body", "the printer is upstairs")
			cf.mount()
			project := cf.projectID()
			c := openTestClient(t) // the callers' lore.db client; supersede reads and writes the core
			if err := checkSupersedes(ctx, c, supersedeRule, project, old); err != nil {
				t.Fatal(err)
			}
			before := coreRuleState(t)
			delete(before, repl) // the state before the add

			dir := filepath.Join(cf.dir, ".lore", "data", string(schema.TableRules))
			chmod := func(m fs.FileMode) {
				if err := os.Chmod(dir, m); err != nil {
					t.Fatal(err)
				}
			}
			chmod(rulesDirReadOnly)
			t.Cleanup(func() { chmod(rulesDirWritable) })
			undoCalls := 0
			undo := func(ctx context.Context) error {
				undoCalls++
				if tc.undoFail {
					return errors.New("injected: undo failed")
				}
				chmod(rulesDirWritable)
				rid, err := ids.ParseAny(repl)
				if err != nil {
					return err
				}
				_, err = coreApp.Delete(ctx, engine.Ref{Table: schema.TableRules, ID: rid})
				return err
			}
			err := finishSupersedes(ctx, c, supersedeRule, old, repl, undo)

			var ce *errcodes.CLIError
			if !errors.As(err, &ce) || ce.Code != errcodes.Internal || !errors.Is(err, fs.ErrPermission) {
				t.Fatalf("want E_INTERNAL wrapping the mark failure, got %v", err)
			}
			if undoCalls != 1 {
				t.Fatalf("undo ran %d times, want 1", undoCalls)
			}
			chmod(rulesDirWritable)
			after := coreRuleState(t)
			if tc.undoFail {
				if !strings.Contains(ce.Hint, "lore rule delete "+repl) {
					t.Fatalf("hint must name the leftover row: %q", ce.Hint)
				}
				if _, left := after[repl]; !left || after[old] != before[old] {
					t.Fatalf("undo failure: old row must be untouched, new row left for the user: %v", after)
				}
				return
			}
			if !reflect.DeepEqual(after, before) {
				t.Fatalf("rules after a failed --supersedes:\n got  %v\n want %v", after, before)
			}
		})
	}
}
