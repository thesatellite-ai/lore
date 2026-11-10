package engine

import (
	"context"
	"strings"
	"testing"
)

// TestMoveRecordsPlaceIDs pins FQ4: a move records each attachment's Place
// (sto_ id plus folder prj_ id) in `_o.moves`, and the attached name where
// no Place is known.
func TestMoveRecordsPlaceIDs(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	w := newProvWorld(t, fakeProv{})
	const agencyPlace = "sto_0000000000000000000000000000a001/prj_0000000000000000000000000000f001"
	a := w.eng.stores[agency]
	a.Place = agencyPlace
	w.eng.stores[agency] = a
	prj := w.project(t, agency, "p")
	m := w.memIn(t, agency, prj, "moving")
	got, err := w.eng.Move(ctx, Ref{ID: m.ID}, personal)
	if err != nil {
		t.Fatal(err)
	}
	if o := got.Row.Origin.String(); !strings.Contains(o, `"moved_from":"`+agencyPlace+`"`) || !strings.Contains(o, `"moved_to":"`+string(personal)+`"`) {
		t.Fatalf("_o.moves = %s", o)
	}
}
