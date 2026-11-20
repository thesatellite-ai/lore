package orgcfg

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	gitbackend "github.com/thesatellite-ai/lore/adapters/backend/git"
	"github.com/thesatellite-ai/lore/adapters/gitref"
	"github.com/thesatellite-ai/lore/logic/gitfault"
	"github.com/thesatellite-ai/lore/logic/orgconf"
	"github.com/thesatellite-ai/lore/logic/orgresolve"
)

// TestNoAccessRecordNeverHidesAStoreWithACopy (devco D-741): one lore run
// whose fetch the forge refused (a pre-commit hook run with other
// credentials) wrote the no-access record into a clone this machine had
// fetched many times. Every later read skipped that store: its links
// vanished and reads by id looked only in the other org. A record on a
// clone holding a copy must never skip it; it is read from the copy, and
// whoami/doctor do not call it skipped.
func TestNoAccessRecordNeverHidesAStoreWithACopy(t *testing.T) {
	ctx := context.Background()
	w := newLinkWorld(t)
	const yml = "orgs:\n  - name: agency\n    identity: {key: human:m@agency.example}\n    stores:\n      - url: " + lwStoreURL + "\n    repos:\n      - github.com/acme/*\n"
	cfg, err := orgconf.Parse([]byte(yml))
	if err != nil {
		t.Fatal(err)
	}
	pid, err := orgresolve.RemoteProjectID(lwRemote)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = w.links.WriteLink(ctx, LinkWrite{
		Plan:            orgresolve.LinkPlan{Entry: "agency", StoreURL: lwStoreNorm, Branch: "main", Folder: "client_a", ProjectID: pid, Remote: lwRemote},
		Signature:       gitref.Signature{Name: "Admin", Email: "admin@agency.example", When: time.Unix(1_800_000_000, 0)},
		FolderProjectID: func(string, string, string) (string, error) { return lwFolderID, nil },
	}); err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(t.TempDir(), "member")
	writeFile(t, ConfigPath(home), yml)
	member := &GitLinks{Home: home, Config: cfg}
	repo := gitRepo(t, "https://"+lwRemote+".git", "")
	if s, gerr := Gather(ctx, In{Home: home, Dir: repo, Links: member}); gerr != nil || s.LinkedByRemote != lwRemote {
		t.Fatalf("first read: linked by %q, err %v", s.LinkedByRemote, gerr)
	}

	// another run's fetch was refused: the record lands in the fetched clone
	dir, _ := cloneDir(home, lwStoreNorm)
	markNoAccess(dir, gitfault.Missing, time.Now())
	if gitbackend.NoAccessSkips(dir) {
		t.Fatal("a clone with a copy is skipped for its no-access record")
	}
	s, err := Gather(ctx, In{Home: home, Dir: repo, Links: member})
	if err != nil || s.LinkedByRemote != lwRemote || len(s.Result.Links) != 1 || s.Result.Links[0].Folder != "client_a" {
		t.Fatalf("after a refused fetch elsewhere: linked by %q, links %+v, err %v; want the copy's link to client_a", s.LinkedByRemote, s.Result.Links, err)
	}
	if got := NoAccessStores(home, cfg, nil); len(got) != 0 {
		t.Errorf("NoAccessStores = %+v, want none (the store is read from its copy)", got)
	}

	// any fetch that works removes the record
	if err := fetchLocked(ctx, nil, dir, time.Now()); err != nil {
		t.Fatal(err)
	}
	if gitbackend.NoAccessRecorded(dir) {
		t.Error("a fetch that worked kept the no-access record")
	}
}
