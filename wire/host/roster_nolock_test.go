package host

import (
	"testing"

	"github.com/thesatellite-ai/lore/op"
	"github.com/thesatellite-ai/lore/ops/profilestore"
)

// TestRosterCommandsTakeNoLiveLock (devco:T-13): a roster command run in a
// code repo writes only the org store, yet it took the repo's live lock,
// fetched and integrated: 6-7 s idle at 2,500 open tasks and 33 s plus an
// E_STORE_LOCKED line while a task import held the lock. Its live session
// is now still (no lock, fetch or integrate: TestStillSessionLeavesGitAsItWas
// proves what a still session leaves alone); a row write is not.
func TestRosterCommandsTakeNoLiveLock(t *testing.T) {
	clearCIMarkers(t)
	roster := []string{
		profilestore.OpPeriodEdit, profilestore.OpPeriodAdd, profilestore.OpPeriodList,
		profilestore.OpActorOffboard, profilestore.OpActorStatus, profilestore.OpActorRejoin,
		profilestore.OpStaffAdd, profilestore.OpStaffRemove, profilestore.OpTeamAdd, profilestore.OpTeamRename,
	}
	body := op.JSONDecoder{Body: []byte(`{}`)}
	for _, name := range roster {
		run := Run{Entry: specEntry{spec: op.Spec{Name: name, Permission: op.PermWrite, Access: op.AccessWrite}}, Decoder: body}
		if !liveStill(&run) {
			t.Errorf("%s takes the repo's live lock; it writes only the org store", name)
		}
		if !leavesWorkRepo(name) {
			t.Errorf("%s wires the repo's git (hooks, .gitignore); it writes only the org store", name)
		}
	}
	for _, name := range []string{"memory.add", "task.add", "comment.add", "task.import"} {
		run := Run{Entry: specEntry{spec: op.Spec{Name: name, Permission: op.PermWrite, Access: op.AccessWrite}}, Decoder: body}
		if liveStill(&run) {
			t.Errorf("%s writes rows of the repo but runs without the live lock", name)
		}
	}
}
