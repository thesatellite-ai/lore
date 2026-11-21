package wire

import (
	"encoding/json"
	"testing"

	"github.com/thesatellite-ai/lore/model/value"
	opsmemory "github.com/thesatellite-ai/lore/ops/memory"
	opssearch "github.com/thesatellite-ai/lore/ops/search"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// metaStoreSearch runs `lore search --query q [--store only]` on a and
// returns each hit's store ("" = the repo) by id.
func metaStoreSearch(t *testing.T, a *App, q, only string) map[string]string {
	t.Helper()
	in, err := json.Marshal(opssearch.RunIn{Query: q, Store: only})
	if err != nil {
		t.Fatal(err)
	}
	out, err := run(t, a, opssearch.OpRun, string(in), false)
	if err != nil {
		t.Fatal(err)
	}
	var res opssearch.RunOut
	if err = json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, h := range res.Data.Hits {
		got[h.ID] = h.Source.Store
	}
	return got
}

// metaStoreMemory adds a memory with metadata meta ("key=value") to the
// store to ("" = the repo) and returns its id.
func metaStoreMemory(t *testing.T, a *App, to, body, meta string) string {
	t.Helper()
	add := map[string]any{"body": body, "meta": []string{meta}}
	if to != "" {
		add["to"] = to
	}
	in, err := json.Marshal(add)
	if err != nil {
		t.Fatal(err)
	}
	out, err := run(t, a, opsmemory.OpAdd, string(in), true)
	if err != nil {
		t.Fatal(err)
	}
	var m struct {
		ID string `json:"id"`
	}
	if err = json.Unmarshal([]byte(out), &m); err != nil || m.ID == "" {
		t.Fatalf("memory add: %s %v", out, err)
	}
	return m.ID
}

// TestSearchMetaConditionReadsStores (devco D-796, owner P-13): a
// metadata condition (`day:2026-11-20`) finds the rows of an attached
// store, labelled with that store, as the same words do; --store keeps
// only that store's rows. Before, the condition read the repo's rows only,
// so a journal adopted into a store folder was never found by its day.
func TestSearchMetaConditionReadsStores(t *testing.T) {
	t.Parallel()
	a := combinedApp(t, 1)
	for _, s := range []store.Source{store.SourceRepo, "s0"} {
		name, err := value.TextOf("prj-" + string(s))
		if err != nil {
			t.Fatal(err)
		}
		if _, err = a.Create(background(), s, schema.TableProjects, map[string]value.Value{"name": name}); err != nil {
			t.Fatal(err)
		}
	}
	inStore := metaStoreMemory(t, a, "s0", "journal entry in the store", "day=2026-11-20")
	inRepo := metaStoreMemory(t, a, "", "journal entry in the repo", "day=2026-11-20")
	other := metaStoreMemory(t, a, "s0", "journal entry of another day", "day=2026-11-19")

	got := metaStoreSearch(t, a, "day:2026-11-20", "")
	if src, ok := got[inStore]; !ok || src != "s0" {
		t.Errorf("store row: found %v, store %q; want it, labelled s0 (hits %v)", ok, src, got)
	}
	if src, ok := got[inRepo]; !ok || src != "" {
		t.Errorf("repo row: found %v, store %q; want it, unlabelled (hits %v)", ok, src, got)
	}
	if _, ok := got[other]; ok {
		t.Errorf("a row of another day was found: %v", got)
	}

	only := metaStoreSearch(t, a, "day:2026-11-20", "s0")
	if _, ok := only[inStore]; !ok || len(only) != 1 {
		t.Errorf("--store s0: hits %v, want only the store row %s", only, inStore)
	}
}
