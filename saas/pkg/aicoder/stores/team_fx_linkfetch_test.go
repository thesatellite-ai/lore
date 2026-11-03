package stores

import (
	"testing"
	"time"

	"saas/pkg/aicoder/lorecfg"
)

// TestLinkSourceForceFetchSeesANewLink is F11 + TEAM-025 on real git: a
// second machine that scanned the store a moment ago keeps its cached
// fetch (live.fetch_ttl) for ordinary commands, but a ForceFetch scan (what
// `lore sync` runs) fetches now and knows a link a teammate just pushed.
func TestLinkSourceForceFetchSeesANewLink(t *testing.T) {
	e := newEnv(t)
	dev1 := e.manager(t.TempDir(), nil)
	st := initStore(t, e, dev1, "o/store")
	cfg := &lorecfg.Config{Orgs: []lorecfg.Org{{Name: "agency", Stores: []lorecfg.Store{{URL: st.URL}}}}}
	remotes := []string{"github.com/client/website"}

	dev2 := e.manager(t.TempDir(), nil)
	dev2.FetchTTL = time.Minute
	scan := func(force bool) int {
		t.Helper()
		res, err := (&LinkSource{Manager: dev2, Config: cfg, ForceFetch: force}).Links(ctx(), "prj_web", remotes)
		mustOK(t, err)
		return len(res.Links)
	}
	if n := scan(false); n != 0 {
		t.Fatalf("before the link: %d links", n)
	}
	_, err := st.WriteLink(ctx(), LinkChange{Branch: "main", Folder: "client", Link: LinkRecord{ProjectID: "prj_web", Remote: remotes[0]}, NewProject: newProjectFiles("prj_client"), Signature: sig})
	mustOK(t, err)
	if n := scan(false); n != 0 {
		t.Fatalf("inside fetch_ttl an ordinary scan uses its last fetch (test premise): %d links", n)
	}
	if n := scan(true); n != 1 {
		t.Fatalf("a forced scan (lore sync) must fetch and find the new link: %d links", n)
	}
	if n := scan(false); n != 1 {
		t.Fatalf("the rebuilt index is what the next command reads: %d links", n)
	}
}
