//go:build journeys && !windows

package journeys

// solo_localseed_test.go — a solo dev's local personal store moves to a
// private git repo with the one command lore's own note names (devco
// N-13): the folder is no git repo, so "push the folder" never worked.
//
//	day 1    Kit keeps personal notes in the local folder store
//	day 2    Kit makes an empty private repo and runs
//	         `lore store set-url --name me --url <repo>`: the folder's notes
//	         are copied into lore's clone, nothing is pushed; `lore sync`
//	         pushes them and the next note
//
// Named TestJourneySolo… so the solo persona's gate step runs it.

import "testing"

// Placeholders of this journey.
const (
	jlsName  = "Kit Local"
	jlsEmail = "kit@solo.example"
	jlsRepo  = "github.com/kit-dev/site"
	jlsStore = "github.com/kit-dev/kit-notes"
)

func TestJourneySoloLocalStoreToEmptyRepo(t *testing.T) {
	t.Parallel()
	w := NewWorld(t)
	kit := w.Persona(jlsName, jlsEmail)
	m := kit.Laptop()
	app := m.Clone(w.Remote(jlsRepo), "site")

	w.Step("day1/local-personal-store-and-a-note", func() {
		m.LoreIn(app, "init").OK(t)
		m.LoreIn(app, "store", "init", "--name", "me").OK(t)
		m.LoreIn(app, "memory", "add", "--body", "kept on this laptop only", "--to", "me").OK(t)
	})

	w.Step("day2/set-url-to-an-empty-repo-copies-the-folder", func() {
		repo := w.Remote(jlsStore)
		r := m.LoreIn(app, "store", "set-url", "--name", "me", "--url", repo.SSHURL()).OK(t)
		r.Says(t, "is now at "+repo.SSHURL(), "copied the folder store", "`lore sync` pushes it")
		if jsuRemoteHas(w, jlsStore, "kept on this laptop only") {
			t.Fatalf("set-url pushed to the private repo on its own")
		}
		m.LoreIn(app, "memory", "add", "--body", "written after the move", "--to", "me").OK(t)
		m.LoreIn(app, "sync").OK(t)
		for _, body := range []string{"kept on this laptop only", "written after the move"} {
			if !jsuRemoteHas(w, jlsStore, body) {
				t.Fatalf("after lore sync the private repo lacks %q", body)
			}
		}
	})
}
