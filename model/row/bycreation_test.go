package row_test

import (
	"slices"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
)

// TestByCreation (devco:D-460): created_at first, the id breaks ties (and
// orders rows without a created_at), so a sort with it is the same on every
// run whatever the ids' digits.
func TestByCreation(t *testing.T) {
	at := time.Date(2026, 11, 2, 10, 0, 0, 0, time.UTC)
	mk := func(id string, created time.Time) row.Row {
		parsed, err := ids.ParseAny(id)
		if err != nil {
			t.Fatal(err)
		}
		return row.Row{ID: parsed, CreatedAt: created}
	}
	hashLow := mk("tsk_000ab1965c647ba083639f54d90a7f93", at.Add(time.Hour))
	fresh := mk("tsk_01a1a5aca7f57659be7320c5f4b70011", at)
	tieA := mk("tsk_01a1a5aca7f57659be7320c5f4b70022", at.Add(time.Minute))
	tieB := mk("tsk_01a1a5aca7f57659be7320c5f4b70033", at.Add(time.Minute))
	none := mk("tsk_ffffa5aca7f57659be7320c5f4b70044", time.Time{})
	for _, in := range [][]row.Row{{hashLow, tieB, fresh, none, tieA}, {tieA, none, hashLow, fresh, tieB}} {
		got := slices.Clone(in)
		slices.SortFunc(got, row.ByCreation)
		var gotIDs []string
		for _, r := range got {
			gotIDs = append(gotIDs, r.ID.String())
		}
		want := []string{none.ID.String(), fresh.ID.String(), tieA.ID.String(), tieB.ID.String(), hashLow.ID.String()}
		if !slices.Equal(gotIDs, want) {
			t.Errorf("sorted %v, want %v", gotIDs, want)
		}
	}
}
