package task_test

import (
	"context"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
	"github.com/thesatellite-ai/lore/wire"
)

// routeTasks sends a task with no --to to the org store and every other
// table to the repo, as F14 does in a client repo whose
// defaults.client_repos.task is the agency store.
func routeTasks(o *wire.Options) {
	o.DefaultTarget = func(_ context.Context, q wire.WriteQuery) (store.Source, error) {
		if q.Table == schema.TableTasks {
			return orgStore, nil
		}
		return "", nil
	}
}

// TestAddRoutedDefaultTasklist (BUGFIX-19): a task add with no --to that
// routing sends to the org store lands there with its on-demand default
// task list (and the caller's actor row) in the same store, instead of
// "one batch cannot write to two stores (repo, agency)"; a second add
// reuses that list; the repo gets no task list.
func TestAddRoutedDefaultTasklist(t *testing.T) {
	t.Parallel()
	h := opstest.New(t, repoClient(true), withOrg(t), routeTasks)
	id := add(h, "one", "")
	src, r := get(h, schema.TableTasks, id)
	if src != orgStore {
		t.Fatalf("task in %q, want %q", src, orgStore)
	}
	list := field(r, "tasklist_id")
	if list == "" {
		t.Fatal("no tasklist_id")
	}
	if lsrc, _ := get(h, schema.TableTasklists, list); lsrc != orgStore {
		t.Fatalf("default list in %q, want %q", lsrc, orgStore)
	}
	if n := dataFiles(t, h, schema.TableTasklists); n != 0 {
		t.Fatalf("the repo got %d task list(s)", n)
	}
	if _, r2 := get(h, schema.TableTasks, add(h, "two", "")); field(r2, "tasklist_id") != list {
		t.Fatalf("second add list %q, want %q", field(r2, "tasklist_id"), list)
	}
}
