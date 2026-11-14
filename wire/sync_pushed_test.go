package wire

import (
	"testing"

	opsstores "github.com/thesatellite-ai/lore/ops/stores"
)

// TestPushedNoRowsCountsTheWholeClone: the folders of one store share one
// clone and one push, so a folder that sent no rows did not push "commits
// with no note rows" when a sibling folder's rows rode that push; a store
// whose push carried no row of any folder still says so (devco J-4).
func TestPushedNoRowsCountsTheWholeClone(t *testing.T) {
	urls := map[string]string{"agency/client": "u-agency", "agency/org": "u-agency", "solo": "u-solo", "nourl": ""}
	reps := []opsstores.Report{
		{Store: "agency/client", Pushed: true},
		{Store: "agency/org", Sent: 2, Pushed: true},
		{Store: "solo", Pushed: true},
		{Store: "nourl", Pushed: true},
	}
	sent := storeCloneSent(reps, func(n string) string { return urls[n] })
	for _, c := range []struct {
		store string
		want  int
	}{{"agency/client", 2}, {"agency/org", 2}, {"solo", 0}, {"nourl", 0}} {
		if got := sent[storeCloneKey(c.store, urls[c.store])]; got != c.want {
			t.Errorf("%s: clone sent %d, want %d", c.store, got, c.want)
		}
	}
}
