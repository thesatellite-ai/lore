package host

import (
	"strconv"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
)

// TestBrokenLoreFileMetaMatrix (METADATA 3.12: the pre-commit check
// refuses a nested conflict): a row whose meta holds a structured conflict
// entry, or a marker block at any depth, is refused and the reason names
// it; clean meta, a marker-like single line and a non-row lore file pass.
func TestBrokenLoreFileMetaMatrix(t *testing.T) {
	q := strconv.Quote("<<<<<<< 0001\nmine\n=======\ntheirs\n>>>>>>> 0002")
	k1, k2 := mdMust(ids.New[ids.MetaKey]()), mdMust(ids.New[ids.MetaKey]())
	withMeta := func(entries map[ids.ID[ids.MetaKey]]string) []byte {
		t.Helper()
		r, err := row.Decode(memRow(t, 1, mdActorA, "episodic", "clean body"))
		if err != nil {
			t.Fatal(err)
		}
		r.Meta = map[ids.ID[ids.MetaKey]]value.JSON{}
		for k, e := range entries {
			r.Meta[k] = mdMust(value.JSONOf([]byte(e)))
		}
		b, err := row.Encode(r)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	cases := []struct {
		name string
		doc  []byte
		want string // "" = accepted; else a substring of the reason
	}{
		{"clean row", memRow(t, 1, mdActorA, "episodic", "x"), ""},
		{"clean meta", withMeta(map[ids.ID[ids.MetaKey]]string{k1: `{"name":"cost","v":3.5}`, k2: `{"name":"labels","v":["a"]}`}), ""},
		{"marker-like line in meta", withMeta(map[ids.ID[ids.MetaKey]]string{k1: `{"name":"note","v":"<<<<<<< not a block"}`}), ""},
		{"folder meta file (not a row)", []byte(`{"project_id":"prj_1","name":"acme"}`), ""},
		{"structured conflict entry", withMeta(map[ids.ID[ids.MetaKey]]string{k1: `{"name":"cost","strict":true,"conflict":{"base":3.5,"ours":4,"theirs":5}}`}), brokenMetaConflict + k1.String()},
		{"conflict next to a clean entry", withMeta(map[ids.ID[ids.MetaKey]]string{k1: `{"name":"a","v":"1"}`, k2: `{"name":"cost","conflict":{"base":null,"ours":4,"theirs":null}}`}), brokenMetaConflict + k2.String()},
		{"marker block in a nested meta value", withMeta(map[ids.ID[ids.MetaKey]]string{k1: `{"name":"deploy","v":{"env":{"region":` + q + `}}}`}), brokenConflictMarkers},
		{"not JSON", []byte(`{"id":`), brokenInvalidJSON},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := brokenLoreFile(c.doc)
			if c.want == "" {
				if got != "" {
					t.Fatalf("refused: %q", got)
				}
				return
			}
			if !strings.Contains(got, c.want) {
				t.Fatalf("reason %q, want it to contain %q", got, c.want)
			}
		})
	}
}
