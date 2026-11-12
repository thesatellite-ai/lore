package wire

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	opsmemory "github.com/thesatellite-ai/lore/ops/memory"
	"github.com/thesatellite-ai/lore/ops/settings"
	"github.com/thesatellite-ai/lore/schema"
)

// quarProbe is the metadata value of the quarantine test: it must never
// reach a problem text (problems name keys and rules, never values).
const quarProbe = "acme-quarantine-probe"

// TestMetaQuarantinedAcrossCacheFiles (METADATA 3.13 "counted", later
// 278): App.MetaQuarantined lists the quarantined rows of every cache
// file — the repo's and an attached folder store's — each once, with its
// source, table and id; problems name the key, never the value; fixing a
// row's file clears only that row.
func TestMetaQuarantinedAcrossCacheFiles(t *testing.T) {
	t.Parallel()
	repo, storeDir, _, _ := folderStoreRepo(t)
	a := newTestApp(t, repo)
	withProject(t, a)
	add := func(input string) string {
		t.Helper()
		out, err := run(t, a, opsmemory.OpAdd, input, true)
		if err != nil {
			t.Fatal(err)
		}
		var m struct {
			ID string `json:"id"`
		}
		if err = json.Unmarshal([]byte(out), &m); err != nil || m.ID == "" {
			t.Fatalf("memory add %s: %s %v", input, out, err)
		}
		if _, err = run(t, a, settings.OpMetaSet, `{"id":"`+m.ID+`","pair":"client=`+quarProbe+`"}`, true); err != nil {
			t.Fatal(err)
		}
		return m.ID
	}
	inRepo := add(`{"body":"repo row"}`)
	inStore := add(`{"body":"store row","to":"northwind"}`)
	clean := add(`{"body":"clean row"}`)
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}

	// drop the value of the metadata entry of each row file (an entry
	// without v is quarantined), saving the bytes to put one back later;
	// the files are read and written through an os.Root on their folder
	saved := map[string][]byte{}
	for _, f := range []struct{ dir, id string }{{repo, inRepo}, {storeDir, inStore}} {
		dir, name := memRowRoot(t, f.dir, f.dir == repo), f.id+".json"
		b, err := dir.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		saved[f.id] = b
		bad := strings.ReplaceAll(string(b), `"v": "`+quarProbe+`"`, `"x": 1`)
		bad = strings.ReplaceAll(bad, `"v":"`+quarProbe+`"`, `"x":1`)
		if !strings.Contains(string(b), quarProbe) || strings.Contains(bad, quarProbe) {
			t.Fatalf("no metadata value to drop in %s:\n%s", name, b)
		}
		if err = dir.WriteFile(name, []byte(bad), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	list := func() ([]string, int) {
		t.Helper()
		b := newTestApp(t, repo)
		if _, err := run(t, b, opsmemory.OpList, `{}`, true); err != nil { // reads (and refreshes) every cache
			t.Fatal(err)
		}
		st, err := b.state(background())
		if err != nil {
			t.Fatal(err)
		}
		files := len(cacheFiles(st.caches))
		q, err := b.MetaQuarantined(background())
		if err != nil {
			t.Fatal(err)
		}
		var got []string
		for _, x := range q {
			got = append(got, string(x.Source)+"|"+x.Table+"|"+x.ID)
			for _, p := range x.Problems {
				if strings.Contains(p, quarProbe) {
					t.Errorf("problem %q echoes the value", p)
				}
				if !strings.Contains(p, "key mky_") {
					t.Errorf("problem %q does not name the key id", p)
				}
			}
		}
		slices.Sort(got)
		if err = b.Close(); err != nil {
			t.Fatal(err)
		}
		return got, files
	}
	got, files := list()
	want := []string{"northwind|memories|" + inStore, "repo|memories|" + inRepo}
	slices.Sort(want)
	if files < 2 {
		t.Fatalf("%d cache files, the test needs the repo's and the store's", files)
	}
	if !slices.Equal(got, want) {
		t.Fatalf("quarantined %v, want %v (clean row %s never)", got, want, clean)
	}

	if err := memRowRoot(t, repo, true).WriteFile(inRepo+".json", saved[inRepo], 0o600); err != nil {
		t.Fatal(err)
	}
	if got, _ = list(); !slices.Equal(got, []string{"northwind|memories|" + inStore}) {
		t.Fatalf("after fixing the repo row: %v", got)
	}
}

// memRowRoot is an os.Root on the memories data folder at the root of
// dir (the repo's .lore/data, or the store attached at its root, whose
// root folder's tables sit at its top), closed at cleanup.
func memRowRoot(t *testing.T, dir string, isRepo bool) *os.Root {
	t.Helper()
	data := filepath.Join(dir, rowfs.StoreDataDir)
	if isRepo {
		data = filepath.Join(dir, ".lore", "data")
	}
	r, err := os.OpenRoot(filepath.Join(data, string(schema.TableMemories)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Close() })
	return r
}
