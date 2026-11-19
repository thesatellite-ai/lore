package task_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/op"
	opsmemory "github.com/thesatellite-ai/lore/ops/memory"
	opstask "github.com/thesatellite-ai/lore/ops/task"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// taskListCounter is an Env that counts the listings of the tasks table.
type taskListCounter struct {
	opsmemory.Env
	lists *int
}

// List implements opsmemory.Env.
func (e taskListCounter) List(ctx context.Context, src store.Source, table row.Table) ([]row.Row, error) {
	if table == schema.TableTasks {
		*e.lists++
	}
	return e.Env.List(ctx, src, table)
}

// importCountingLists imports n keyed tasks through an Env that counts the
// task listings, and returns the count.
func importCountingLists(t *testing.T, n int) int {
	t.Helper()
	h := opstest.New(t, repoClient(false))
	var b strings.Builder
	b.WriteString("id,title,key\n")
	for i := range n {
		fmt.Fprintf(&b, "K-%d,keyed task %d,key-%d\n", i, i, i)
	}
	b.WriteString("K-dup,the first key again,key-0\n")
	file := writeFile(t, "keyed.csv", b.String())
	actor, err := ids.Parse[ids.Actor](opstest.Actor)
	if err != nil {
		t.Fatal(err)
	}
	lists := 0
	e := opstask.ImportOps(taskListCounter{Env: h.App, lists: &lists})[0]
	res, err := h.App.Run(context.Background(), e, op.JSONDecoder{Body: []byte(`{"file":` + opstest.Quote(file) + `}`)}, op.Caller{Actor: actor, Yes: true})
	if err != nil {
		t.Fatal(err)
	}
	got := importIDs(h, res)
	if len(got) != n+1 || got[0] != got[n] {
		t.Fatalf("import ids %v: want %d, the repeated key on the first task", got, n+1)
	}
	return lists
}

// TestImportListsTasksOncePerFile (devco:T-3): add's --key rule listed
// every task of every store for each imported row, so a task import of
// 650 rows into a repo with 5,000 open tasks listed them 650 times (over
// 6 minutes). The listings no longer grow with the rows imported.
func TestImportListsTasksOncePerFile(t *testing.T) {
	t.Parallel()
	few, many := importCountingLists(t, 3), importCountingLists(t, 24)
	if many != few {
		t.Errorf("an import of 24 keyed rows listed the tasks %d times, one of 3 rows %d times: the listings grow with the rows", many, few)
	}
}
