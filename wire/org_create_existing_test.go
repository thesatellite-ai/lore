package wire

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/orgcfg"
	"github.com/thesatellite-ai/lore/logic/orgconf"
	opsorgcmd "github.com/thesatellite-ai/lore/ops/orgcmd"
	"github.com/thesatellite-ai/lore/wire/internal/isolate"
)

// TestOrgCreateOnExistingOrgFileMatchesImport (B2-19): an admin who
// starts a team (org create, the setup page's team step) on a store whose
// org/lore-org.yaml already exists gets the org entry a member's join (org
// import) builds from that file: every store it lists, its own and repos
// patterns. Before, the entry held only the one store and the patterns
// typed (none), so the org's own repos were client repos for the admin.
func TestOrgCreateOnExistingOrgFileMatchesImport(t *testing.T) {
	if isolate.Run(t) {
		return
	}
	w := newOrgWorld(t)
	ctx := context.Background()
	mira := w.home(t, "mira")
	if _, err := orgHostAt(t, w.repo, mira).Create(ctx, opsorgcmd.CreateIn{Org: "agency", Store: "agency", OrgFolder: "org", Own: []string{"github.com/agency/*"},
		Key: "human:mira@agency.example", Name: "Mira", Handle: "mira"}, true); err != nil {
		t.Fatalf("create: %v", err)
	}
	// the team's file gains client repos after it was made
	clone := filepath.Join(w.root, "notes-edit")
	orgGit(t, w.root, "clone", "-q", orgTestStoreURL, clone)
	orgFile := filepath.Join(clone, "org", "lore-org.yaml")
	base, err := os.ReadFile(orgFile)
	if err != nil {
		t.Fatal(err)
	}
	extra := "repos:\n  - github.com/clientco/*\n"
	if err = os.WriteFile(orgFile, append(append([]byte{}, base...), extra...), 0o600); err != nil {
		t.Fatal(err)
	}
	orgGit(t, clone, "commit", "-q", "-am", "client repos")
	orgGit(t, clone, "push", "-q", "origin", "main")

	entry := func(home string) *orgconf.Org {
		t.Helper()
		cfg, _, err := orgcfg.LoadConfig(home)
		if err != nil {
			t.Fatal(err)
		}
		o := cfg.FindOrg("agency")
		if o == nil {
			t.Fatalf("no org entry agency in %s", home)
		}
		return o
	}
	// the admin's team step: org create with the store url, no patterns
	ana := w.home(t, "ana")
	if _, err = orgHostAt(t, w.root, ana).Create(ctx, opsorgcmd.CreateIn{Org: "agency", Store: orgTestStoreURL, OrgFolder: "org",
		Key: "human:ana@agency.example", Name: "Ana", Handle: "ana"}, true); err != nil {
		t.Fatalf("admin create on the existing org: %v", err)
	}
	admin := entry(ana)
	// a member's join: org import of the same store
	bo := w.home(t, "bo")
	if _, err = orgHostAt(t, w.root, bo).Import(ctx, opsorgcmd.ImportIn{URL: orgTestStoreURL, Key: "human:bo@agency.example", Name: "Bo", Handle: "bo"}, true); err != nil {
		t.Fatalf("member import: %v", err)
	}
	member := entry(bo)
	if !slices.Equal(admin.Own, []string{"github.com/agency/*"}) || !slices.Equal(admin.Repos, []string{"github.com/clientco/*"}) {
		t.Fatalf("admin entry own %v repos %v, want the file's patterns\n%s", admin.Own, admin.Repos, readConfig(t, ana))
	}
	if !slices.Equal(admin.Own, member.Own) || !slices.Equal(admin.Repos, member.Repos) || len(admin.Stores) != len(member.Stores) {
		t.Fatalf("admin and member entries differ:\nadmin:\n%s\nmember:\n%s", readConfig(t, ana), readConfig(t, bo))
	}
	for i := range admin.Stores {
		if admin.Stores[i].URL != member.Stores[i].URL || admin.Stores[i].OrgFolder != member.Stores[i].OrgFolder {
			t.Fatalf("store %d: admin %+v, member %+v", i, admin.Stores[i], member.Stores[i])
		}
	}
}
