//go:build teame2e

package teame2e

// te-c store replay race (later 461, OV14 tests; METADATA 3.12 "store
// replay through the merge rules"): two machines change the metadata of
// the same store row at the same moment. The loser of the push race
// fetches and replays its write through the one row merge (logic/merge's
// nested meta strategy), so both keys survive and no marker or conflict
// entry appears; the v0.1.17 store replay merged meta as one value and
// kept only one side.

import (
	"strings"
	"testing"

	th "github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

func TestTeC_StoreReplayRaceMergesMetaPerKey(t *testing.T) {
	a := newAgency(t, th.Flag("to", "memory", "add"), th.Cmd("meta", "set"))
	to := orgNorthwind + "/" + folderAcme
	a.aliceApp.MustLore("meta", "add", "cost", "--type", "number", "--to", to)
	a.aliceApp.MustLore("meta", "add", "team", "--type", "text", "--to", to)
	id := mustID(t, a.aliceApp.MustLore("memory", "add", "--body", "the replay race memory", "--to", to), "mem")
	a.dev1App.MustLore("memory", "show", id).Says(t, "the replay race memory")

	for _, r := range a.w.Simultaneously(
		a.aliceApp.Prepare("meta", "set", id, "cost=4"),
		a.dev1App.Prepare("meta", "set", id, "team=core"),
	) {
		r.OK(t)
	}
	for _, c := range []*th.Clone{a.aliceApp, a.dev1App} {
		c.MustLore("sync")
	}
	for name, c := range map[string]*th.Clone{"alice": a.aliceApp, "dev1": a.dev1App} {
		out := c.MustLore("meta", "get", id).Stdout
		if !strings.Contains(out, "cost") || !strings.Contains(out, "4") || !strings.Contains(out, "team") || !strings.Contains(out, "core") {
			t.Errorf("%s: both racing metadata writes must survive the replay: %s", name, out)
		}
		if strings.Contains(out, "<<<<<<<") || strings.Contains(strings.ToLower(out), "conflict") {
			t.Errorf("%s: two different keys never conflict: %s", name, out)
		}
	}
	if a.storeHas(t, folderAcme, "<<<<<<<") {
		t.Fatal("the store must hold no conflict markers after the replay")
	}
}
