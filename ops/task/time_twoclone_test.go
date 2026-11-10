package task_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/logic/merge"
	opstask "github.com/thesatellite-ai/lore/ops/task"
	"github.com/thesatellite-ai/lore/schema"
)

// TestTimeEntriesTwoCopiesMerge (TIME 11, later 143): two machines that
// each log time on the same task from one base copy produce two row files;
// the row merge driver git runs on the pull keeps both entries (time
// entries merge list-by-id, never newest-wins), in either order, and a
// copy merged with itself changes nothing. The repo is an own repo: in a
// client repo time_entries (an internal field) never reaches the row file.
func TestTimeEntriesTwoCopiesMerge(t *testing.T) {
	t.Parallel()
	h := opstest.New(t, repoClient(false))
	id := add(h, "shared task", "")
	path := filepath.Join(h.Repo, ".lore", "data", schema.TableTasks, id+".json")
	rel := filepath.Join(".lore", "data", schema.TableTasks, id+".json")
	read := func() []byte {
		t.Helper()
		rt, err := os.OpenRoot(h.Repo) // the read stays inside the test's checkout
		if err != nil {
			t.Fatal(err)
		}
		b, err := rt.ReadFile(rel)
		if cerr := rt.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	base := read()
	h.Must(opstask.OpTimeLog, `{"id":"`+id+`","duration":"45m","note":"machine a"}`)
	ours := read()
	if err := os.WriteFile(path, base, 0o600); err != nil {
		t.Fatal(err)
	}
	b := h.ReopenCold(repoClient(false))
	b.Must(opstask.OpTimeLog, `{"id":"`+id+`","duration":"1h","note":"machine b"}`)
	theirs := read()
	if strings.Contains(string(theirs), "machine a") {
		t.Fatal("the second copy was not written from the base")
	}
	reg, err := schema.NewRegistry(schema.Builtin()...)
	if err != nil {
		t.Fatal(err)
	}
	d := merge.Driver{Registry: reg}
	for _, c := range []struct {
		name string
		x, y []byte
	}{{"a then b", ours, theirs}, {"b then a", theirs, ours}} {
		got, conflict, merr := d.Merge(base, c.x, c.y)
		if merr != nil || conflict {
			t.Fatalf("%s: conflict=%v err=%v", c.name, conflict, merr)
		}
		s := string(got)
		if !strings.Contains(s, "machine a") || !strings.Contains(s, "machine b") || strings.Count(s, `"tme_`) < 2 {
			t.Fatalf("%s: merged copy lost an entry:\n%s", c.name, s)
		}
	}
	same, conflict, err := d.Merge(base, ours, ours)
	if err != nil || conflict || strings.Count(string(same), "machine a") != 1 {
		t.Fatalf("self merge: conflict=%v err=%v\n%s", conflict, err, same)
	}
}
