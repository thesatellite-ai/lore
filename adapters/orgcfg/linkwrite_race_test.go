package orgcfg

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/adapters/gitref"
	"github.com/thesatellite-ai/lore/logic/orgresolve"
)

// TestConcurrentLinksFromSeveralMachinesAllLand (F11 race): several
// machines (one lore home, so one bare clone, each) link different remotes
// into the same store folder at the same moment; every push that loses
// the race is rebuilt on the new tip, so every link is on the store's
// main afterwards and none is reported offline.
func TestConcurrentLinksFromSeveralMachinesAllLand(t *testing.T) {
	w := newLinkWorld(t)
	const machines = 4
	var wg sync.WaitGroup
	results := make([]LinkWriteResult, machines)
	errsOut := make([]error, machines)
	for i := range machines {
		wg.Add(1)
		go func() {
			defer wg.Done()
			g := &GitLinks{Home: filepath.Join(t.TempDir(), "home"), Config: w.links.Config}
			remote := fmt.Sprintf("github.com/acme/app%d", i)
			results[i], errsOut[i] = g.WriteLink(context.Background(), LinkWrite{
				Plan:            orgresolve.LinkPlan{Entry: "agency", StoreURL: lwStoreNorm, Branch: "main", Folder: "acme", ProjectID: lwProject, Remote: remote},
				Signature:       gitref.Signature{Name: "Mira", Email: "mira@agency.example", When: time.Unix(1_800_000_000, 0)},
				FolderProjectID: func(string, string, string) (string, error) { return lwFolderID, nil },
			})
		}()
	}
	wg.Wait()
	for i := range machines {
		if errsOut[i] != nil || !results[i].Pushed || results[i].Offline {
			t.Fatalf("machine %d: %+v %v (every link must be pushed, none offline)", i, results[i], errsOut[i])
		}
	}
	var links []linkRecord
	if err := json.Unmarshal(w.metaOf(t, "acme")[metaLinksKey], &links); err != nil {
		t.Fatal(err)
	}
	if len(links) != machines {
		t.Fatalf("store holds %d links, want %d: %+v", len(links), machines, links)
	}
}

// TestWriteOrgFileOnADecliningStore: a store whose hook refuses every push
// answers ErrStoreRefused with the store's reason for `org create`, never
// "offline" (the commit would sit in the local clone unseen).
func TestWriteOrgFileOnADecliningStore(t *testing.T) {
	w := newLinkWorld(t)
	// A pre-receive hook that always fails: a link to false(1), so no file
	// of ours needs to be executable.
	falseBin, err := exec.LookPath("false")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Symlink(falseBin, filepath.Join(w.origin, "hooks", "pre-receive")); err != nil {
		t.Fatal(err)
	}
	res, err := w.links.WriteOrgFile(context.Background(), OrgFileWrite{URL: lwStoreURL, OrgFolder: "org",
		Doc:       OrgFileDoc{Name: "agency", Stores: []OrgFileStore{{URL: lwStoreURL, OrgFolder: "org"}}},
		Signature: gitref.Signature{Name: "Mira", Email: "mira@agency.example", When: time.Unix(1_800_000_000, 0)}})
	if !errors.Is(err, ErrStoreRefused) || res.Offline || res.Pushed {
		t.Fatalf("declined org create: %+v %v, want ErrStoreRefused", res, err)
	}
}
