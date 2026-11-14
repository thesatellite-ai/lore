package wire

import "testing"

// TestTaskInRepoCoversAClientReposOwnTask (D-14): a claim on a client
// repo's own task sits on the linked store's signals ref, but its row is in
// the client repo, so the held record says InRepo and the commit-msg hook
// adds the Lore-Task trailer; a store-only task stays out of the repo.
func TestTaskInRepoCoversAClientReposOwnTask(t *testing.T) {
	for _, c := range []struct {
		name   string
		w      coordWorld
		inRepo bool
	}{
		{"own repo, own signals ref", coordWorld{}, true},
		{"client repo's own task, claim on the store", coordWorld{spot: sigSpot{store: true}, client: true}, true},
		{"store-only task", coordWorld{spot: sigSpot{store: true}}, false},
	} {
		if got := c.w.taskInRepo(); got != c.inRepo {
			t.Errorf("%s: taskInRepo = %v, want %v", c.name, got, c.inRepo)
		}
	}
}
