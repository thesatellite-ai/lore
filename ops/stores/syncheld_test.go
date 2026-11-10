package stores

import (
	"context"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/store"
)

// fakeSyncer answers each store's Sync from a fixed report.
type fakeSyncer struct {
	names []store.Source
	reps  map[store.Source]store.SyncReport
}

func (f fakeSyncer) Stores() []store.Source { return f.names }
func (f fakeSyncer) Sync(_ context.Context, n store.Source) (store.SyncReport, error) {
	return f.reps[n], nil
}

// TestSyncAllReportsRefusalOnce (owner decision 7): a row both sides
// edited in folder "platform" is refused by every folder of the store's
// clone; lore sync counts it once, under the folder that holds it, and
// every other folder says it is held by that folder's conflict (its push
// did not run), never "refused 1" of its own.
func TestSyncAllReportsRefusalOnce(t *testing.T) {
	t.Parallel()
	id, err := ids.ParseAny("mem_" + "01a1a6841fc0784d" + "92a62b0acf14ab01")
	if err != nil {
		t.Fatal(err)
	}
	conflict := store.Refusal{ID: id, Code: errs.Stale, Reason: "both sides changed the row", Folder: "platform"}
	held := conflict
	held.Held = true
	s := fakeSyncer{
		names: []store.Source{store.SourceRepo, "team", "team/org", "team/platform"},
		reps: map[store.Source]store.SyncReport{
			"team":          {Refused: []store.Refusal{held}},
			"team/org":      {Refused: []store.Refusal{held}},
			"team/platform": {Refused: []store.Refusal{conflict}},
		},
	}
	out, err := SyncAll(context.Background(), s, "")
	if !errs.Has(err, errs.Stale) || strings.Count(err.Error(), id.String()) != 1 {
		t.Fatalf("err = %v, want one E_STALE naming the row once", err)
	}
	if len(out.Stores) != 3 {
		t.Fatalf("stores %+v", out.Stores)
	}
	want := "held: waiting on team/platform's conflict (" + id.String() + ")"
	for _, r := range out.Stores {
		switch r.Store {
		case "team/platform":
			if r.Refused != 1 || r.Held != "" || len(r.Problems) != 1 {
				t.Errorf("the row's folder: %+v", r)
			}
		default:
			if r.Refused != 0 || len(r.Problems) != 0 || r.HeldBy != "team/platform" || r.Held != want {
				t.Errorf("%s: %+v, want held %q", r.Store, r, want)
			}
		}
	}
	// The row's folder not synced in this run (sync --store team/org):
	// the held line is the run's problem, so the exit still says so.
	out, err = SyncAll(context.Background(), s, "team/org")
	if !errs.Has(err, errs.Stale) || len(out.Stores) != 1 || out.Stores[0].Held != "held: waiting on folder platform's conflict ("+id.String()+")" {
		t.Fatalf("alone: %+v %v", out.Stores, err)
	}
}

// TestSyncAllHeldByTwoFolders: rows of two folders ("platform" and "web")
// were both edited on both sides: each is reported once by its own folder,
// and the third folder's held line names both of them with their rows,
// never only the first.
func TestSyncAllHeldByTwoFolders(t *testing.T) {
	t.Parallel()
	a, err := ids.ParseAny("mem_" + "01a1a6841fc0784d" + "92a62b0acf14ab01")
	if err != nil {
		t.Fatal(err)
	}
	b, err := ids.ParseAny("mem_" + "01a1a6841fc0784d" + "92a62b0acf14ab02")
	if err != nil {
		t.Fatal(err)
	}
	ca := store.Refusal{ID: a, Code: errs.Stale, Reason: "both sides changed the row", Folder: "platform"}
	cb := store.Refusal{ID: b, Code: errs.Stale, Reason: "both sides changed the row", Folder: "web"}
	ha, hb := ca, cb
	ha.Held, hb.Held = true, true
	s := fakeSyncer{
		names: []store.Source{"team/org", "team/platform", "team/web"},
		reps: map[store.Source]store.SyncReport{
			"team/org":      {Refused: []store.Refusal{hb, ha}},
			"team/platform": {Refused: []store.Refusal{ca, hb}},
			"team/web":      {Refused: []store.Refusal{ha, cb}},
		},
	}
	out, err := SyncAll(context.Background(), s, "")
	if !errs.Has(err, errs.Stale) || strings.Count(err.Error(), a.String()) != 1 || strings.Count(err.Error(), b.String()) != 1 {
		t.Fatalf("err = %v, want each row named once", err)
	}
	want := "held: waiting on team/platform's conflict (" + a.String() + ") and team/web's conflict (" + b.String() + ")"
	for _, r := range out.Stores {
		if r.Store == "team/org" && (r.Held != want || r.HeldBy != "team/platform, team/web") {
			t.Errorf("team/org: %+v, want held %q", r, want)
		}
	}
	// Alone, neither holding folder synced: both are named by folder.
	out, err = SyncAll(context.Background(), s, "team/org")
	alone := "held: waiting on folder platform's conflict (" + a.String() + ") and folder web's conflict (" + b.String() + ")"
	if !errs.Has(err, errs.Stale) || len(out.Stores) != 1 || out.Stores[0].Held != alone {
		t.Fatalf("alone: %+v %v, want %q", out.Stores, err, alone)
	}
}
