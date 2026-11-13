package host

import (
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// TestLiveShowUnknownIDIsNotFound is the host half of the lookup parity
// matrix (ops/internal/lookup TestLookupNotFoundMatrix, G-HOST-1): where
// live rows apply, `lore live show --id <id>` answers E_NOT_FOUND for every
// kind of id the live folder lacks (malformed, wrong case, huge, unicode,
// all zeros, a well-formed unknown id of a live table and of another
// table), with an empty live ref and with one holding rows; the id the
// live ref holds is printed.
func TestLiveShowUnknownIDIsNotFound(t *testing.T) {
	bad := []string{
		"not-an-id", "tsk_zz", strings.ToUpper(ltTaskA), strings.Repeat("z", 3000), "ünïcode_id",
		"tsk_" + strings.Repeat("0", 32), ltTaskB, ltMemory, ltActorID,
	}
	for _, withRows := range []bool{false, true} {
		name := "empty live ref"
		if withRows {
			name = "live ref with a row"
		}
		t.Run(name, func(t *testing.T) {
			f := newLiveFixture(t)
			if withRows {
				if err := f.b.Write(f.ctx, []store.Change{{Row: ltRow(t, schema.TableTasks, ltTaskA, 1, "live work", false)}}); err != nil {
					t.Fatal(err)
				}
			}
			ref, err := f.s.h.liveRef(f.ctx)
			if err != nil {
				t.Fatal(err)
			}
			for _, id := range bad {
				if _, ferr := ref.File(f.ctx, id); !errs.Has(ferr, errs.NotFound) {
					t.Errorf("%.40q: %v, want E_NOT_FOUND", id, ferr)
				}
			}
			b, ferr := ref.File(f.ctx, ltTaskA)
			switch {
			case withRows && (ferr != nil || !strings.Contains(string(b), ltTaskA)):
				t.Errorf("held id: %q %v, want its file", b, ferr)
			case !withRows && !errs.Has(ferr, errs.NotFound):
				t.Errorf("held id on an empty ref: %v, want E_NOT_FOUND", ferr)
			}
		})
	}
}
