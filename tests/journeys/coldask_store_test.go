//go:build journeys && !windows

package journeys

// coldask_store_test.go — devco:D-1029 (hunt H-291, follow-up of H-290):
// the first `lore ask` of a session still waited for one fetch after the
// others. A store attached without an org link (`lore store init` in the
// repo: stores.yaml) was fetched by its sync inside the operation, after
// the code repo's fetch had ended; and an org store a teammate had pushed
// to was fetched twice (the org's link index, then the store's sync). Now
// a read starts every due store fetch at once and the sync takes it.
//
// Same fake ssh as coldask_test.go: counts calls in flight, never wall
// times.
//
//	day 1  online: a team store attached by hand (no org) holds a note
//	day 2  over a slow link, after a pause, a teammate has pushed to the
//	       store: the first ask fetches the store and the code repo at the
//	       same time, once each, and merges the teammate's commit

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestJourneyColdFirstAskFetchesAStoreOnlyStoreAtOnce(t *testing.T) {
	t.Parallel()
	w := NewWorld(t)
	ivo := w.Persona("Ivo Brant", "ivo@acme-co.example")
	lap := ivo.Laptop()
	notes := w.Remote("github.com/acme-co/lore-notes")
	code := jPlainRepo(t, lap, w.Remote("github.com/acme-co/orbit"), filepath.Join(lap.Home, "work", "orbit"))
	sshDir := t.TempDir()
	var last string
	calls := func() (fetches, overlapped int) {
		fetches, overlapped, last = sshFetches(sshDir)
		return fetches, overlapped
	}

	w.Step("day1/a-note-in-a-store-of-no-org", func() {
		lap.LoreIn(code, "init").OK(t)
		lap.LoreIn(code, "store", "init", "--name", "notes", "--url", notes.SSHURL(), "--yes").OK(t)
		lap.LoreIn(code, "memory", "add", "--to", "notes", "--body", "The orbit release train leaves every second tuesday.", "--yes").OK(t)
		code.CommitAll("add lore")
		code.Git("push", "-q", "origin", "HEAD:main")
		lap.LoreIn(code, "sync").OK(t)
		slowLink(t, w, lap, sshDir)
		lap.LoreIn(code, "sync").OK(t)
		if fe, _ := calls(); fe == 0 {
			t.Fatal("the fake ssh logged no fetch: the counts below would prove nothing")
		}
	})

	w.Step("day2/first-ask-fetches-the-store-and-repo-at-once", func() {
		w.AdvanceDays(1) // long past live.fetch_ttl: every fetch is due
		tip := pushToStore(t, w, notes.Dir, "a teammate's commit")
		calls() // the invariants' own reads after the last step
		r := lap.LoreIn(code, "ask", "--query", "when does the orbit release train leave").OK(t).Says(t, "every second tuesday")
		fe, over := calls()
		if fe != 2 {
			t.Errorf("the first ask of the day made %d fetch(es), want 2 (the store and the code repo, once each):\n%s\n%s", fe, last, r.Out())
		}
		if over == 0 {
			t.Errorf("the first ask of the day fetched the store and the code repo one after the other, want both at once:\n%s", last)
		}
		clone := filepath.Join(lap.Home, ".lore", "stores", "acme-co", "lore-notes")
		if got := w.Git(clone, "rev-list", "HEAD"); !strings.Contains(got, tip) {
			t.Errorf("the teammate's commit %s fetched with the first ask was not merged into the store clone", tip)
		}
		lap.LoreIn(code, "ask", "--query", "when does the orbit release train leave").OK(t).Says(t, "every second tuesday")
		if fe, _ := calls(); fe != 0 {
			t.Errorf("an ask right after made %d fetch(es), want none:\n%s", fe, last)
		}
	})
}

// pushToStore commits a README into the store remote at bare as a
// teammate outside this machine would (the harness's own git) and returns
// the commit.
func pushToStore(t *testing.T, w *World, bare, subject string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "mate")
	w.Git(w.Root, "clone", "-q", bare, dir)
	jWrite(t, filepath.Join(dir, "README.md"), "Team notes.\n")
	w.Git(dir, "add", "README.md")
	w.Git(dir, "-c", "user.name=Bea Mate", "-c", "user.email=bea@acme-co.example", "commit", "-q", "-m", subject)
	w.Git(dir, "push", "-q", "origin", "HEAD:main")
	return strings.TrimSpace(w.Git(dir, "rev-parse", "HEAD"))
}
