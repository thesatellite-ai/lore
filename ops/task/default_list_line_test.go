package task_test

import (
	"context"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/op"
	opstask "github.com/thesatellite-ai/lore/ops/task"
)

// TestAddNamesDefaultList (U-3): a task added without --tasklist says
// which list it landed on, the first time (the list is created) and every
// time after (the list is reused); a task with --tasklist prints neither.
func TestAddNamesDefaultList(t *testing.T) {
	t.Parallel()
	h := opstest.New(t)
	first := opstest.Text(h.Must(opstask.OpAdd, `{"title":"One"}`))
	if !strings.Contains(first, `created tasklist tlt_`) {
		t.Fatalf("first add: %q", first)
	}
	second := opstest.IDPattern(opstest.Text(h.Must(opstask.OpAdd, `{"title":"Two"}`)))
	if want := "  on tasklist tlt_ID \"default\" (the default; --tasklist picks another)\n"; !strings.HasSuffix(second, want) {
		t.Fatalf("second add:\n got %q\nwant suffix %q", second, want)
	}
	list := h.ID(h.Must("tasklist.add", `{"title":"Sprint"}`))
	named := opstest.Text(h.Must(opstask.OpAdd, `{"title":"Three","tasklist":"`+list+`"}`))
	if strings.Contains(named, "tasklist") {
		t.Fatalf("--tasklist add names a list: %q", named)
	}
}

// TestEmptyListFlagsRefused (U-3): on the CLI, `--tasklist=` (and the
// mission and plan flags) given empty on task add / edit is E_INVALID_INPUT
// naming the flag, never "not given": task add once put such a task on
// the auto-created default list without a word.
func TestEmptyListFlagsRefused(t *testing.T) {
	t.Parallel()
	h := opstest.New(t)
	id := h.ID(h.Must(opstask.OpAdd, `{"title":"Edit me"}`))
	actor, err := ids.Parse[ids.Actor](opstest.Actor)
	if err != nil {
		t.Fatal(err)
	}
	run := func(name string, args []string, flags map[string][]string) error {
		e, ok := h.App.Registry().Lookup(name)
		if !ok {
			t.Fatalf("no op %s", name)
		}
		_, err := h.App.Run(context.Background(), e, op.ArgsDecoder{Args: args, Flags: flags}, op.Caller{Actor: actor})
		return err
	}
	for _, flag := range []string{"tasklist", "mission", "plan"} {
		for _, c := range []struct {
			name string
			args []string
		}{{opstask.OpAdd, []string{"New"}}, {opstask.OpEdit, []string{id}}} {
			err := run(c.name, c.args, map[string][]string{flag: {""}})
			if !errs.Has(err, errs.InvalidInput) || !strings.Contains(err.Error(), "--"+flag+" is empty; give a "+flag+" id") {
				t.Errorf("%s --%s=: %v", c.name, flag, err)
			}
		}
	}
	// Nothing was written by the refused adds: only the one task exists.
	if got := strings.Count(opstest.Text(h.Must(opstask.OpList, ``)), "tsk_"); got != 1 {
		t.Fatalf("%d tasks after refused adds, want 1", got)
	}
}
