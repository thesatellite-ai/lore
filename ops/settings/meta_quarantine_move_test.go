package settings_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/ops/settings"
)

// TestMoveRefusesQuarantinedMeta (METADATA 3.13, section 6, X19): a row
// whose metadata arrived broken (a hand edit: an entry without "v") is not
// moved: lore move --id answers E_META_QUARANTINED naming the row and pointing
// at lore meta show, and the row stays where it is; once the entry is fixed
// the same move goes through. meta show lists the problem and its fix as
// a line, not an exit.
func TestMoveRefusesQuarantinedMeta(t *testing.T) {
	t.Setenv("LORE_HUMAN", "1")
	h := multiStore(t)
	id := h.ID(h.MustYes(memAdd, `{"body":"to move","to":"kb","meta":["ticket=HUB-1"]}`))
	// the kb store is a sibling temp dir of the repo (newStore)
	var file string
	_ = filepath.WalkDir(filepath.Dir(h.Repo), func(p string, d os.DirEntry, werr error) error {
		if werr == nil && !d.IsDir() && strings.Contains(filepath.Base(p), id) && strings.HasSuffix(p, ".json") {
			file = p
		}
		return nil
	})
	if file == "" {
		t.Fatalf("no row file for %s", id)
	}
	// the row file is read and written through an os.Root on its folder
	dir, err := os.OpenRoot(filepath.Dir(file))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = dir.Close() })
	name := filepath.Base(file)
	good, err := dir.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(good), `"v": "HUB-1"`) {
		t.Fatalf("row file shape changed: %s", good)
	}
	if err = dir.WriteFile(name, []byte(strings.Replace(string(good), `"v": "HUB-1"`, `"x": "HUB-1"`, 1)), 0o600); err != nil {
		t.Fatal(err)
	}
	h = h.Reopen()
	show := opstest.Text(h.Must(settings.OpMetaShow, `{"id":"`+id+`"}`))
	if !strings.Contains(show, "E_META_QUARANTINED  the entry has no v; fix the row file, or lore meta unset --id "+id+" --key mky_") {
		t.Fatalf("meta show of a quarantined row (a line, not an exit):\n%s", show)
	}
	_, err = h.RunYes("row.move", `{"id":"`+id+`","to":"kb/web"}`)
	if !errs.Has(err, errs.MetaQuarantined) || !strings.Contains(errs.EnvelopeOf(err).Error.Hint, "lore meta show --id "+id) {
		t.Fatalf("move of a quarantined row: %v, want E_META_QUARANTINED", err)
	}
	if err = dir.WriteFile(name, good, 0o600); err != nil {
		t.Fatal(err)
	}
	h = h.Reopen()
	h.MustYes("row.move", `{"id":"`+id+`","to":"kb/web"}`)
}
