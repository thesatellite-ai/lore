package wire

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/backend/git"
	opsorgcmd "github.com/thesatellite-ai/lore/ops/orgcmd"
	"github.com/thesatellite-ai/lore/wire/internal/isolate"
)

// TestFixPrepareFetchesEveryOrgStore (B1-22): on a new machine after org
// import, `lore fix --all` fetches every store the org entry lists before
// the repos run, cloning the ones this machine has none of yet; before,
// it fetched only whole clones, so the org's other stores stayed "never
// fetched" and every repo's migrate failed with E_LINK_UNKNOWN (the
// children do not fetch).
func TestFixPrepareFetchesEveryOrgStore(t *testing.T) {
	if isolate.Run(t) {
		return
	}
	w := newOrgWorld(t)
	ctx := context.Background()
	mira := w.home(t, "mira")
	if _, err := orgHostAt(t, w.repo, mira).Create(ctx, opsorgcmd.CreateIn{Org: "agency", Store: "agency", OrgFolder: "org",
		Key: "human:mira@agency.example", Name: "Mira", Handle: "mira"}, true); err != nil {
		t.Fatalf("create: %v", err)
	}
	// the team's file lists a second store
	clone := filepath.Join(w.root, "notes-edit")
	orgGit(t, w.root, "clone", "-q", orgTestStoreURL, clone)
	orgFile := filepath.Join(clone, "org", "lore-org.yaml")
	b, err := os.ReadFile(orgFile)
	if err != nil {
		t.Fatal(err)
	}
	two := strings.Replace(string(b), "org_folder: org\n", "org_folder: org\n    - url: https://github.com/agency/empty\n", 1)
	if two == string(b) {
		t.Fatalf("lore-org.yaml has no org_folder line to extend:\n%s", b)
	}
	if err = os.WriteFile(orgFile, []byte(two), 0o600); err != nil {
		t.Fatal(err)
	}
	orgGit(t, clone, "commit", "-q", "-am", "second store")
	orgGit(t, clone, "push", "-q", "origin", "main")

	bo := w.home(t, "bo")
	if _, err = orgHostAt(t, w.root, bo).Import(ctx, opsorgcmd.ImportIn{URL: orgTestStoreURL, Key: "human:bo@agency.example", Name: "Bo", Handle: "bo"}, true); err != nil {
		t.Fatalf("import: %v", err)
	}
	app, err := New(StandaloneOptions(w.root, bo, w.root, "test"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = app.Close() }()
	p := app.FixPrepare(ctx, false, 2)
	if len(p.Failed) > 0 || p.Fetched < 2 {
		t.Fatalf("FixPrepare = %+v, want both org stores fetched", p)
	}
	r, err := git.RemoteOfRaw("https://github.com/agency/empty")
	if err != nil {
		t.Fatal(err)
	}
	dir, _, err := git.CloneDirFor(bo, r, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(filepath.Join(git.StateDirOf(dir), "fetch-stamp")); err != nil {
		t.Fatalf("the org's second store was not fetched (%s): %v", dir, err)
	}
}
