package sqlite

import (
	"path/filepath"
	"testing"

	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/store"
)

// TestFileCites: another cache file is asked, read-only, whether a
// row_refs line references one of the ids a write applied (devco:D-1021):
// yes for a cited id among many, no for ids it does not cite, no for a
// file that does not exist.
func TestFileCites(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	c := f.open("archive.db")
	cited := must(ids.New[ids.Memory]()).Any()
	f.apply(c, store.SourceRepo, "m1",
		f.memory(newMem(), newPrj(), "see ${ref:"+cited.String()+"} first"),
		f.memory(newMem(), newPrj(), "no reference here"))
	path := filepath.Join(f.dir, "archive.db")
	other := must(ids.New[ids.Memory]()).String()
	if got, err := FileCites(f.ctx, path, []string{other, cited.String()}); err != nil || !got {
		t.Errorf("[D-1021] FileCites(a cited id among others) = %v, %v; want true", got, err)
	}
	if got, err := FileCites(f.ctx, path, []string{other}); err != nil || got {
		t.Errorf("[D-1021] FileCites(an id no line references) = %v, %v; want false", got, err)
	}
	if got, err := FileCites(f.ctx, filepath.Join(f.dir, "none.db"), []string{cited.String()}); err != nil || got {
		t.Errorf("[D-1021] FileCites(a missing file) = %v, %v; want false", got, err)
	}
}
