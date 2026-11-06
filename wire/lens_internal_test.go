package wire

// lens_internal_test.go — later 22 (FA-META-1, CFG4): a client repo's
// `lore sql` lens is the repo plus its shareable stores; --internal adds
// the internal stores and names them (RunOut.Internal); the `trash` view
// lists only the trashed rows of the stores the lens attached, so a client
// repo never sees an internal store's titles. An internal repo reads every
// store and names none as internal-only.

import (
	"context"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	opsmemory "github.com/thesatellite-ai/lore/ops/memory"
	opssql "github.com/thesatellite-ai/lore/ops/sql"
)

func TestLensClientShareableInternal(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name         string
		client       bool
		internal     bool
		wantSources  []string
		wantInternal []string
	}{
		{"client repo", true, false, []string{"agency", "repo"}, nil},
		{"client repo --internal", true, true, []string{"agency", "golang", "repo"}, []string{"golang"}},
		{"internal repo", false, false, []string{"agency", "golang", "repo"}, nil},
		{"internal repo --internal", false, true, []string{"agency", "golang", "repo"}, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			repo := gitRepo(t)
			writeStoresYAML(t, repo, "stores:\n"+
				"  - name: agency\n    url: "+folderStoreAt(t, folderStore, rowfs.VisibilityShareable)+"\n"+
				"  - name: golang\n    url: "+folderStoreAt(t, secondStore, rowfs.VisibilityInternal)+"\n")
			client := c.client
			a := openWith(t, repo, Options{RepoIsClient: func(context.Context) (bool, error) { return client, nil }})
			withProject(t, a)
			for _, s := range []string{"repo", "agency", "golang"} {
				for _, body := range []string{"keep in " + s, "trash in " + s} {
					out, err := run(t, a, opsmemory.OpAdd, `{"body":"`+body+`","to":"`+s+`"}`, true)
					if err != nil {
						t.Fatalf("%s: %v", body, err)
					}
					if strings.HasPrefix(body, "trash") {
						if _, err = run(t, a, opsmemory.OpDelete, `{"id":"`+idOf(t, out)+`"}`, true); err != nil {
							t.Fatal(err)
						}
					}
				}
			}
			query := func(q string) opssql.Result {
				t.Helper()
				res, err := a.SQL(background(), opssql.Request{Query: q, Internal: c.internal})
				if err != nil {
					t.Fatalf("%s: %v", q, err)
				}
				return res
			}
			res := query(`SELECT DISTINCT source FROM memories_all ORDER BY source`)
			if got := cells(res); !slices.Equal(got, c.wantSources) {
				t.Fatalf("memories_all sources = %v, want %v", got, c.wantSources)
			}
			if !slices.Equal(res.Internal, c.wantInternal) {
				t.Fatalf("internal = %v, want %v", res.Internal, c.wantInternal)
			}
			if got := cells(query(`SELECT source FROM trash WHERE table_name = 'memories' ORDER BY source`)); !slices.Equal(got, c.wantSources) {
				t.Fatalf("trash sources = %v, want %v", got, c.wantSources)
			}
			// the live views hold only the kept rows
			if got := cells(query(`SELECT count(*) FROM memories_all`)); !slices.Equal(got, []string{strconv.Itoa(len(c.wantSources))}) {
				t.Fatalf("memories_all rows = %v", got)
			}
		})
	}
}
