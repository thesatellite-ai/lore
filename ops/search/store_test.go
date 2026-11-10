package search_test

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/ops/search"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// storeSearchWord is the placeholder word every seeded row holds.
const storeSearchWord = "zebraquux"

// seedBoth writes one row of t into the repo store and one into the
// attached store "s1", each with every full-text field holding the
// placeholder word, and returns their ids.
func seedBoth(t *testing.T, h *opstest.H, tg search.Target) (repoID, storeID string) {
	t.Helper()
	reg, err := schema.NewRegistry(schema.Builtin()...)
	if err != nil {
		t.Fatal(err)
	}
	def, ok := reg.Table(tg.Table)
	if !ok {
		t.Fatalf("no table %s", tg.Table)
	}
	set := func(src store.Source, where string) map[string]string {
		m := map[string]string{}
		for _, f := range def.FTS {
			if fd, ok := def.Field(f); ok && len(fd.Values) > 0 {
				continue // a closed set (decisions.status) keeps its sample
			}
			m[f] = strconv.Quote(storeSearchWord + " placeholder " + where)
		}
		// an optional project_id (playbooks) is the seeded project, the
		// one a default search reads
		if _, ok := def.Field("project_id"); ok {
			m["project_id"] = strconv.Quote(h.Project)
		}
		// a polymorphic target (comments) points at a memory of its store
		if _, ok := def.Field(schema.FieldEntityID); ok {
			m[schema.FieldEntityTable] = strconv.Quote(string(schema.TableMemories))
			m[schema.FieldEntityID] = strconv.Quote(h.SeedTo(src, schema.TableMemories, map[string]string{"body": `"target"`}))
		}
		return m
	}
	return h.Seed(tg.Table, set(store.SourceRepo, "repo")), h.SeedTo("s1", tg.Table, set("s1", "store"))
}

// TestNounSearchStore: every noun's search takes --store (json "store"):
// it keeps only the hits of that attached store, "repo" only the repo's,
// no flag both, and an unattached name is E_NOT_FOUND naming it. The noun
// searches are read from the registry, so a new noun search without the
// filter fails here.
func TestNounSearchStore(t *testing.T) {
	h := opstest.WithStores(t, []string{"s1"})
	byEntity := map[string]search.Target{}
	for _, tg := range search.Targets() {
		byEntity[tg.Entity] = tg
	}
	var nounSearches []string
	for _, e := range h.App.Registry().Entries() {
		name := e.Spec().Name
		if noun, ok := strings.CutSuffix(name, ".search"); ok && name != search.OpRun {
			if _, known := byEntity[noun]; !known {
				t.Errorf("%s: no search target for noun %q", name, noun)
				continue
			}
			nounSearches = append(nounSearches, name)
		}
	}
	if len(nounSearches) < 23 {
		t.Fatalf("found %d noun searches, want at least 23: %v", len(nounSearches), nounSearches)
	}
	for _, name := range nounSearches {
		tg := byEntity[strings.TrimSuffix(name, ".search")]
		repoID, storeID := seedBoth(t, h, tg)
		for _, c := range []struct {
			in        string
			repo, sto bool
		}{
			{`{"query":"` + storeSearchWord + `","limit":1000}`, true, true},
			{`{"query":"` + storeSearchWord + `","limit":1000,"store":"s1"}`, false, true},
			{`{"query":"` + storeSearchWord + `","limit":1000,"store":"repo"}`, true, false},
		} {
			got := h.JSON(h.Must(name, c.in))
			if strings.Contains(got, repoID) != c.repo || strings.Contains(got, storeID) != c.sto {
				t.Errorf("%s %s: repo hit %v (want %v), store hit %v (want %v)\n%s",
					name, c.in, strings.Contains(got, repoID), c.repo, strings.Contains(got, storeID), c.sto, got)
			}
		}
		if msg := h.Fails(name, `{"query":"`+storeSearchWord+`","store":"nosuch"}`, errs.NotFound); !strings.Contains(msg, `"nosuch"`) {
			t.Errorf("%s: unknown store message %q does not name it", name, msg)
		}
	}
}

// TestGlobalSearchStore: `lore search --store` keeps only that store's
// hits (repo hits carry no _source), "repo" only the repo's, and an
// unattached name is E_NOT_FOUND naming it.
func TestGlobalSearchStore(t *testing.T) {
	h := opstest.WithStores(t, []string{"s1"})
	repoID := h.Seed(schema.TableMemories, map[string]string{"body": `"` + storeSearchWord + ` repo"`})
	storeID := h.SeedTo("s1", schema.TableMemories, map[string]string{"body": `"` + storeSearchWord + ` store"`})
	hits := func(in string) map[string]string {
		var out search.RunOut
		if err := json.Unmarshal([]byte(h.JSON(h.Must(search.OpRun, in))), &out); err != nil {
			t.Fatal(err)
		}
		m := map[string]string{}
		for _, x := range out.Data.Hits {
			m[x.ID] = x.Source.Store
		}
		return m
	}
	all := hits(`{"query":"` + storeSearchWord + `"}`)
	if _, ok := all[repoID]; !ok || all[storeID] != "s1" || len(all) != 2 {
		t.Fatalf("no filter: %v", all)
	}
	only := hits(`{"query":"` + storeSearchWord + `","store":"s1"}`)
	if len(only) != 1 || only[storeID] != "s1" {
		t.Fatalf("--store s1: %v", only)
	}
	repo := hits(`{"query":"` + storeSearchWord + `","store":"` + string(store.SourceRepo) + `"}`)
	if _, ok := repo[repoID]; !ok || len(repo) != 1 {
		t.Fatalf("--store repo: %v", repo)
	}
	if msg := h.Fails(search.OpRun, `{"query":"`+storeSearchWord+`","store":"nosuch"}`, errs.NotFound); !strings.Contains(msg, `"nosuch"`) {
		t.Fatalf("unknown store message %q does not name it", msg)
	}
}
