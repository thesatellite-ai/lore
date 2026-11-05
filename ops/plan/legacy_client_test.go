package plan_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/wire"
)

// internalRepo / clientRepo set the repo's class (wire.Options.RepoIsClient).
func internalRepo(o *wire.Options) {
	o.RepoIsClient = func(context.Context) (bool, error) { return false, nil }
}
func clientRepo(o *wire.Options) {
	o.RepoIsClient = func(context.Context) (bool, error) { return true, nil }
}

// TestPlanLegacyStatusClientWrites (PC-LEAK-1 edge, TSQ9): a v0.1.17 plan
// (status_str only, written while the repo was internal) keeps its status
// through every client-repo write: the engine's client step folds the
// internal status_str into status (only when status is empty) before it
// drops the field. Covers a create straight into a client repo, a delete
// and a restore, and an edit.
func TestPlanLegacyStatusClientWrites(t *testing.T) {
	for _, c := range []struct{ legacy, want string }{
		{"active", "in_progress"}, {"approved", "approved"}, {"free text", "draft"},
	} {
		// create straight into a client repo: the fold runs on the create
		ch := opstest.New(t, clientRepo)
		cid := ch.Seed(schema.TablePlans, map[string]string{"status_str": opstest.Quote(c.legacy)})
		assertPlanFile(t, ch, cid, c.want)

		// a legacy row already on disk, then trash + restore in a client repo
		ih := opstest.New(t, internalRepo)
		id := ih.Seed(schema.TablePlans, map[string]string{"status_str": opstest.Quote(c.legacy)})
		if got := readPlanFile(t, ih, id); !strings.Contains(got, `"status_str"`) || strings.Contains(got, `"status":`) {
			t.Fatalf("%s: setup: internal file should hold status_str only: %s", c.legacy, got)
		}
		h := ih.Reopen(clientRepo)
		h.MustYes("plan.delete", `{"id":"`+id+`"}`)
		h.Must("trash.restore", `{"target":"`+id+`"}`)
		if got := h.JSON(h.Must("plan.show", `{"id":"`+id+`"}`)); !strings.Contains(got, `"status":"`+c.want+`"`) {
			t.Errorf("%s: after restore in a client repo: %s, want status %s", c.legacy, got, c.want)
		}
		assertPlanFile(t, h, id, c.want)
	}
}

// assertPlanFile checks the stored row file: status set, status_str gone.
func assertPlanFile(t *testing.T, h *opstest.H, id, want string) {
	t.Helper()
	got := readPlanFile(t, h, id)
	if strings.Contains(got, `"status_str"`) || !strings.Contains(got, `"status": "`+want+`"`) && !strings.Contains(got, `"status":"`+want+`"`) {
		t.Errorf("%s: file %s, want status %s and no status_str", id, got, want)
	}
}

// readPlanFile is the plan's row file in the repo.
func readPlanFile(t *testing.T, h *opstest.H, id string) string {
	t.Helper()
	root, err := os.OpenRoot(h.Repo) // P2-LINT-5: reads under the chosen dir go through os.Root
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }() // read-only handle
	b, err := root.ReadFile(filepath.Join(".lore", "data", string(schema.TablePlans), id+".json"))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
