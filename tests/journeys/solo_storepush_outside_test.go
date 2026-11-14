//go:build journeys && !windows

package journeys

// solo_storepush_outside_test.go — `lore store push --store me` from the
// home folder: like `lore sync`, it needs only the setup, not a code repo,
// so a person who wrote to the personal store from ~ can send it from ~.

import "testing"

func TestJourneySoloStorePushOutsideRepo(t *testing.T) {
	t.Parallel()
	w := NewWorld(t)
	sam := w.Persona(soloName, soloEmail)
	lap := sam.Laptop()
	w.Remote(soloStore) // an empty repo Sam made on GitHub for the personal store

	w.Step("day1/install-and-setup", func() {
		lap.GitHubAccounts(t, GitHubAccount{Login: soloLogin, Key: "id_ed25519"})
		soloSetupStart(t, lap, soloName, soloEmail)
		soloSetupRest(t, lap, "create")
	})

	w.Step("day1/note-from-home-pushed-from-home", func() {
		lap.Lore("", "memory", "add", "--body", "sent with store push from home", "--to", "me").OK(t)
		lap.Lore("", "store", "push", "--store", "me").OK(t)
		if !soloRemoteHas(w, "sent with store push from home") {
			t.Errorf("store push from the home folder did not send the personal store's note")
		}
	})
}
