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

// TestSiblingFolderPushIsUpToDate (devco N-15): a folder that sent nothing
// while a sibling folder's rows rode the shared clone's push says "up to
// date", not "pushed (sent 0, received 0, merged 0, refused 0)".
func TestSiblingFolderPushIsUpToDate(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name            string
		pushed          bool
		sent, cloneSent int
		want            string
	}{
		{"sibling's rows pushed", true, 0, 2, "store s: up to date"},
		{"own rows pushed", true, 2, 2, "store s: pushed (sent 2, received 0, merged 0, refused 0)"},
		{"push with no rows at all", true, 0, 0, "store s: pushed (sent 0, received 0, merged 0, refused 0)"},
		{"no push", false, 0, 0, "store s: up to date"},
	} {
		p := folderPushed(c.pushed, c.sent, c.cloneSent)
		if got := storeCountsLine("s", storeStatusOf(c.sent, 0, 0, 0, p), c.sent, 0, 0, 0); got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
}
