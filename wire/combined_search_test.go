package wire

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/model/value"
	opssearch "github.com/thesatellite-ai/lore/ops/search"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// combinedApp attaches n folder stores s0..s<n-1> to a fresh repo.
func combinedApp(t *testing.T, n int) *App {
	t.Helper()
	repo := gitRepo(t)
	var yaml strings.Builder
	yaml.WriteString("stores:\n")
	for i := range n {
		id := "sto_01a1a5aca7f57659be7320c5f4b7" + strconv.FormatInt(int64(0x2000+i), 16)
		yaml.WriteString("  - name: s" + strconv.Itoa(i) + "\n    url: " + folderStoreAt(t, id, rowfs.VisibilityInternal) + "\n")
	}
	writeStoresYAML(t, repo, yaml.String())
	a := openWith(t, repo, Options{RepoIsClient: func(context.Context) (bool, error) { return false, nil }})
	if _, err := a.Engine(background()); err != nil {
		t.Fatal(err)
	}
	return a
}

// TestCombinedCacheSearchAndDoctor (CFG3, later 156.1): past
// MaxStoreCaches attached stores share one cache file, yet every search
// hit still names the store it came from (a repo hit has no _source, a
// store hit names its own store, never a neighbour sharing the file), and
// doctor notes the combined cache; at MaxStoreCaches stores there is no
// combined cache and no note.
func TestCombinedCacheSearchAndDoctor(t *testing.T) {
	for _, n := range []int{MaxStoreCaches, MaxStoreCaches + 1} {
		t.Run(strconv.Itoa(n)+" stores", func(t *testing.T) {
			a := combinedApp(t, n)
			combined := n > MaxStoreCaches
			if a.Combined() != combined {
				t.Fatalf("Combined() = %v", a.Combined())
			}
			want := map[string]string{} // body word -> store ("" = repo)
			for _, s := range []string{string(store.SourceRepo), "s0", "s3", "s" + strconv.Itoa(n-1)} {
				name, err := value.TextOf("prj-" + s)
				if err != nil {
					t.Fatal(err)
				}
				if _, err = a.Create(background(), store.Source(s), schema.TableProjects, map[string]value.Value{"name": name}); err != nil {
					t.Fatal(err)
				}
				to := s
				if s == string(store.SourceRepo) {
					to = ""
				}
				histAddMemory(t, a, to, "quokka marker "+s)
				want["marker "+s] = to
			}
			q, err := json.Marshal(opssearch.RunIn{Query: "quokka"})
			if err != nil {
				t.Fatal(err)
			}
			out, err := run(t, a, opssearch.OpRun, string(q), false)
			if err != nil {
				t.Fatal(err)
			}
			var res opssearch.RunOut
			if err = json.Unmarshal([]byte(out), &res); err != nil {
				t.Fatal(err)
			}
			if len(res.Data.Hits) != len(want) {
				t.Fatalf("hits %d, want %d: %s", len(res.Data.Hits), len(want), out)
			}
			for _, h := range res.Data.Hits {
				text := strings.NewReplacer("<b>", "", "</b>", "").Replace(h.Body + " " + h.Snippet)
				found := false
				for word, src := range want {
					if !strings.Contains(text, word) {
						continue
					}
					found = true
					if h.Source.Store != src {
						t.Errorf("hit %q: _source store %q, want %q", text, h.Source.Store, src)
					}
				}
				if !found {
					t.Errorf("hit %q matches no marker", text)
				}
			}
			notes := 0
			for _, r := range a.DoctorChecks(background(), false) {
				if r.Check == string(DoctorCheckCombinedCache) {
					notes += len(r.Findings)
				}
			}
			if combined != (notes == 1) || notes > 1 {
				t.Fatalf("combined %v: %d combined_cache findings", combined, notes)
			}
		})
	}
}
