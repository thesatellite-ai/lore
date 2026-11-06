package task_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/model/errs"
	opstask "github.com/thesatellite-ai/lore/ops/task"
	"github.com/thesatellite-ai/lore/schema"
)

// dataFiles counts the row files of table in the repo's .lore/data.
func dataFiles(t *testing.T, h *opstest.H, table string) int {
	t.Helper()
	ents, err := os.ReadDir(filepath.Join(h.Repo, ".lore", "data", table))
	if os.IsNotExist(err) {
		return 0
	}
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, e := range ents {
		if strings.HasSuffix(e.Name(), ".json") {
			n++
		}
	}
	return n
}

// TestRefusedAddWritesNoDefaultList (G-ENGINE-2 finding): the first task
// add of a project creates the default task list on demand; when that add
// is refused afterwards (a mission or plan that does not exist, a
// client-repo pointer at an org mission, a secret in the body with no
// answer) neither the list nor the task is written. The next good add
// writes both, and a second add reuses the list. (The caller's actor row
// joins the same batch through wire withActorRows; the op harness writes
// no actor row, so that half is wire TestRefusedCreateWritesNoActorRow and
// the cmd/lore smoke.)
func TestRefusedAddWritesNoDefaultList(t *testing.T) {
	// The fixture key is built at run time (CLAUDE.md convention 8).
	secret := "AKIA" + strings.Repeat("Q", 4) + "7XRT2MNB5KZ3"
	cases := []struct {
		name   string
		client bool
		in     func(h *opstest.H) string
		code   errs.Code
	}{
		{"unknown mission", false, func(*opstest.H) string {
			return `{"title":"x","mission":"msn_01936f0000007a1b8c2d3e4f5a6b7c8d"}`
		}, errs.NotFound},
		{"unknown plan", false, func(*opstest.H) string {
			return `{"title":"x","plan":"pln_01936f0000007a1b8c2d3e4f5a6b7c8d"}`
		}, errs.NotFound},
		{"client repo to an org mission", true, func(h *opstest.H) string {
			m := addID(h, h.Must("mission.add", `{"title":"internal goal","to":"`+orgStore+`"}`))
			return `{"title":"x","mission":"` + m + `"}`
		}, errs.LeakBlocked},
		{"secret without an answer", false, func(*opstest.H) string {
			return `{"title":"x","body":"key ` + secret + `"}`
		}, errs.NeedsConfirm},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := opstest.New(t, repoClient(c.client), withOrg(t))
			in := c.in(h)
			lists, tasks := dataFiles(t, h, schema.TableTasklists), dataFiles(t, h, schema.TableTasks)
			if lists != 0 {
				t.Fatalf("fixture: the repo already has %d task lists", lists)
			}
			h.Fails(opstask.OpAdd, in, c.code)
			if got := dataFiles(t, h, schema.TableTasklists); got != 0 {
				t.Errorf("a refused add left %d task list(s)", got)
			}
			if got := dataFiles(t, h, schema.TableTasks); got != tasks {
				t.Errorf("a refused add left %d task(s)", got-tasks)
			}
			id := add(h, "good", "")
			if _, r := get(h, schema.TableTasks, id); field(r, "tasklist_id") == "" || dataFiles(t, h, schema.TableTasklists) != 1 {
				t.Fatalf("the next good add: tasklist %q, %d lists", field(r, "tasklist_id"), dataFiles(t, h, schema.TableTasklists))
			}
			if add(h, "second", "") == "" || dataFiles(t, h, schema.TableTasklists) != 1 {
				t.Fatal("a second add made another default list")
			}
		})
	}
}
