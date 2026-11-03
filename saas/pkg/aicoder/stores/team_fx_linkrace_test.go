package stores

import (
	"testing"
	"time"
)

// TestWriteLinkReportsItsOwnChangeAfterALostRace is trial F1: dev1's clone
// fetched a moment ago (inside fetch_ttl), a teammate then wrote to the
// store, so dev1's first push loses the race. The retry builds on a merge
// that already holds dev1's own link commit; the result must still say
// that this run created the folder and wrote the link, never "already so".
func TestWriteLinkReportsItsOwnChangeAfterALostRace(t *testing.T) {
	e := newEnv(t)
	st0 := initStore(t, e, e.manager(t.TempDir(), nil), "o/store")
	dev1 := e.manager(t.TempDir(), nil)
	dev1.FetchTTL = time.Hour
	st, err := dev1.Open(ctx(), st0.URL)
	mustOK(t, err)
	_, err = st.Fetch(ctx(), true)
	mustOK(t, err)
	// The teammate's write moves the store branch after dev1's fetch.
	e.seed("o/store", "main", map[string]string{"org/.lore/data/rules/r1.json": `{"id":"r1"}`})

	link := LinkRecord{ProjectID: "prj_web", Remote: "github.com/client/website"}
	res, err := st.WriteLink(ctx(), LinkChange{Branch: "main", Folder: "client", Link: link, NewProject: newProjectFiles("prj_client"), Signature: sig})
	mustOK(t, err)
	if res.Write.Attempts < 2 {
		t.Fatalf("test premise: the first push must lose the race: %+v", res.Write)
	}
	if !res.Changed || !res.CreatedFolder || !res.Write.Pushed {
		t.Fatalf("this run wrote the link and created the folder: %+v", res)
	}
	if _, ok := e.originFile("o/store", "refs/heads/main", "client/.lore/data/_meta.json"); !ok {
		t.Fatal("the link reached the store")
	}
	// A real re-run is the no-op case.
	again, err := st.WriteLink(ctx(), LinkChange{Branch: "main", Folder: "client", Link: link, NewProject: newProjectFiles("prj_other"), Signature: sig})
	mustOK(t, err)
	if again.Changed || again.CreatedFolder || again.Write.Changed {
		t.Fatalf("a re-run changes nothing: %+v", again)
	}
}
