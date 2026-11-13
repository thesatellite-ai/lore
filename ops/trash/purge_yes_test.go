package trash_test

import (
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/ops/trash"
	"github.com/thesatellite-ai/lore/schema"
)

// TestPurgeYesSaysConfirm: A-18, `trash purge --yes` stays refused (DQ2:
// --yes never purges) and its hint says why, naming --confirm; restore
// and purge take the row id as --id like trash show (--target is an
// alias the CLI keeps).
func TestPurgeYesSaysConfirm(t *testing.T) {
	h := opstest.New(t)
	id := h.Seed(schema.TableMemories, map[string]string{"body": `"old note"`})
	h.MustYes("memory.delete", `{"id":"`+id+`"}`)
	_, err := h.RunYes(trash.OpPurge, `{"target":"`+id+`"}`)
	if !errs.Has(err, errs.NeedsConfirm) || !strings.Contains(errs.EnvelopeOf(err).Error.Hint, "--yes does not purge") {
		t.Fatalf("purge --yes: %v", err)
	}
	_, err = h.Run(trash.OpPurge, `{"target":"`+id+`"}`)
	if !errs.Has(err, errs.NeedsConfirm) || strings.Contains(errs.EnvelopeOf(err).Error.Hint, "--yes") {
		t.Fatalf("purge without --yes: %v", err)
	}
	for _, name := range []string{trash.OpRestore, trash.OpPurge, trash.OpShow} {
		e, _ := h.App.Registry().Lookup(name)
		var flag string
		for _, f := range e.InputFields() {
			if f.Arg == 0 {
				flag = f.Flag
			}
		}
		if flag != "id" {
			t.Errorf("%s: the row id flag is --%s, want --id", name, flag)
		}
	}
}
