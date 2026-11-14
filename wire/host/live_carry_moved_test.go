package host

// live_carry_moved_test.go — devco:J-6: a live row that names a row only
// this work tree holds carries it to the live ref and removes its file
// (F-ADDADD-1). The move is said in one line naming the file, and the table
// folder it emptied is removed, so the folder, `git status` and what the
// earlier command printed ("wrote <file>") agree.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/live"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

const ltPlan = "pln_01a1a6841fc0784d92a62b0acf148b2c"

func TestLiveBackend_CarriedRowMoveIsSaid(t *testing.T) {
	f := newLiveFixture(t)
	plan := ltRow(t, schema.TablePlans, ltPlan, 1, "payouts revamp", false)
	if err := f.inner.Write(f.ctx, []store.Change{{Row: plan}}); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(f.b.top, filepath.FromSlash(mainDataPath(live.RootFolder, schema.TablePlans+"/"+ltPlan+rowFileExt)))
	if err := os.MkdirAll(filepath.Dir(file), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, plan.Doc, 0o600); err != nil {
		t.Fatal(err)
	}
	r, err := ltRow(t, schema.TableTasks, ltTaskA, 2, "pay sellers daily", false).Decode()
	if err != nil {
		t.Fatal(err)
	}
	pv, err := value.TextOf(ltPlan)
	if err != nil {
		t.Fatal(err)
	}
	r.Fields["plan_id"] = pv
	task, err := store.FromRow(r)
	if err != nil {
		t.Fatal(err)
	}
	if err = f.b.Write(f.ctx, []store.Change{{Row: task}}); err != nil {
		t.Fatal(err)
	}
	if !f.liveHas(t, schema.TablePlans, ltPlan) {
		t.Fatal("the plan the live task names rides live with it")
	}
	if _, err = os.Stat(file); !os.IsNotExist(err) {
		t.Fatalf("the carried plan's file is still in the working folder: %v", err)
	}
	if _, err = os.Stat(filepath.Dir(file)); !os.IsNotExist(err) {
		t.Fatalf("the plans folder the move emptied is still there: %v", err)
	}
	out := f.s.h.cfg.Stderr.(*strings.Builder).String()
	if !strings.Contains(out, "lore: moved plans/"+ltPlan+" to this clone's ") || !strings.Contains(out, file+" is no longer in your working folder") {
		t.Fatalf("the move is not said:\n%s", out)
	}
}
