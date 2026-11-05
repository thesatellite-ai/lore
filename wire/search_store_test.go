package wire

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/op"
	opsmemory "github.com/thesatellite-ai/lore/ops/memory"
	opssearch "github.com/thesatellite-ai/lore/ops/search"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// TestSearchAttachedStoreHits (later 78, FLAG1, r2-source): on cmd/lore's
// core search a hit from an attached store carries _source (store and its
// id) in --json and the [store] label in text; a repo hit carries neither;
// archived and deleted store rows are left out; every id is listed once.
func TestSearchAttachedStoreHits(t *testing.T) {
	a := combinedApp(t, 1)
	for _, src := range []store.Source{store.SourceRepo, "s0"} {
		name, err := value.TextOf("prj-" + string(src))
		if err != nil {
			t.Fatal(err)
		}
		if _, err = a.Create(background(), src, schema.TableProjects, map[string]value.Value{"name": name}); err != nil {
			t.Fatal(err)
		}
	}
	repoHit := histAddMemory(t, a, "", "wombat repo row")
	storeHit := histAddMemory(t, a, "s0", "wombat store row")
	archived := histAddMemory(t, a, "s0", "wombat store archived")
	deleted := histAddMemory(t, a, "s0", "wombat store deleted")
	if _, err := run(t, a, opsmemory.OpArchive, `{"id":"`+archived+`"}`, true); err != nil {
		t.Fatal(err)
	}
	if _, err := run(t, a, opsmemory.OpDelete, `{"id":"`+deleted+`"}`, true); err != nil {
		t.Fatal(err)
	}

	e, ok := a.Registry().Lookup(opssearch.OpRun)
	if !ok {
		t.Fatal("no search.run")
	}
	res, err := a.Run(background(), e, op.JSONDecoder{Body: []byte(`{"query":"wombat"}`)}, op.Caller{})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := res.JSON()
	if err != nil {
		t.Fatal(err)
	}
	var out opssearch.RunOut
	if err = json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	seen := map[string]int{}
	for _, h := range out.Data.Hits {
		seen[h.ID]++
		switch h.ID {
		case repoHit:
			if h.Source.Store != "" {
				t.Errorf("repo hit has _source %+v", h.Source)
			}
		case storeHit:
			if h.Source.Store != "s0" || h.Source.StoreID.IsZero() {
				t.Errorf("store hit _source %+v, want store s0 with its id", h.Source)
			}
		default:
			t.Errorf("unexpected hit %s (archived %s, deleted %s)", h.ID, archived, deleted)
		}
	}
	if seen[repoHit] != 1 || seen[storeHit] != 1 || len(seen) != 2 || len(out.Data.Hits) != 2 {
		t.Fatalf("hits %v (raw %s)", seen, raw)
	}

	text, ok := res.Text()
	if !ok {
		t.Fatal("search has no text form")
	}
	for _, ln := range strings.Split(strings.TrimSpace(text), "\n") {
		switch {
		case strings.Contains(ln, storeHit):
			if !strings.Contains(ln, "  [s0]") {
				t.Errorf("store hit line has no [s0] label: %q", ln)
			}
		case strings.Contains(ln, repoHit):
			if strings.Contains(ln, "[s0]") {
				t.Errorf("repo hit line carries a store label: %q", ln)
			}
		}
	}
	if !strings.Contains(text, storeHit) || !strings.Contains(text, repoHit) {
		t.Fatalf("text misses a hit: %q", text)
	}
}

// TestSearchStoreLabel (later 78): "[store]" for a store root, "[store/folder]"
// inside a folder.
func TestSearchStoreLabel(t *testing.T) {
	if got := opssearch.StoreLabel(store.SourceRef{Store: "agency"}); got != "[agency]" {
		t.Errorf("root: %q", got)
	}
	if got := opssearch.StoreLabel(store.SourceRef{Store: "agency", Folder: "acme-hub"}); got != "[agency/acme-hub]" {
		t.Errorf("folder: %q", got)
	}
}
