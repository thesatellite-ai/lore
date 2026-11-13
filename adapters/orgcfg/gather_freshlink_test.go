package orgcfg

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/adapters/gitref"
	"github.com/thesatellite-ai/lore/logic/orgconf"
	"github.com/thesatellite-ai/lore/logic/orgresolve"
)

// TestGatherSeesALinkMadeSinceTheLastFetch (D-7): a member's copy of the
// org store was fetched a moment ago (well inside live.fetch_ttl); the
// admin then links the member's client repo from another machine. The
// member's next command must find the link: a repo the org's repos:
// pattern matches, with no project and no link, fetches the org stores
// once before deciding (the first write used to fail until the copy
// expired, and an identical retry went through).
func TestGatherSeesALinkMadeSinceTheLastFetch(t *testing.T) {
	ctx := context.Background()
	w := newLinkWorld(t)
	const yml = "orgs:\n  - name: agency\n    identity: {key: human:m@agency.example}\n    stores:\n      - url: " + lwStoreURL + "\n    repos:\n      - github.com/acme/*\n"
	cfg, err := orgconf.Parse([]byte(yml))
	if err != nil {
		t.Fatal(err)
	}
	memberHome := filepath.Join(t.TempDir(), "member")
	writeFile(t, ConfigPath(memberHome), yml)
	member := &GitLinks{Home: memberHome, Config: cfg}
	repo := gitRepo(t, "https://"+lwRemote+".git", "")

	s, err := Gather(ctx, In{Home: memberHome, Dir: repo, Links: member})
	if err != nil || s.LinkedByRemote != "" {
		t.Fatalf("before the link: remote %q err %v, want nothing linked", s.LinkedByRemote, err)
	}

	// the admin links the repo by its remote from another clone
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

	s, err = Gather(ctx, In{Home: memberHome, Dir: repo, Links: member})
	if err != nil || s.ResolveErr != nil {
		t.Fatalf("after the link: %v %v", err, s.ResolveErr)
	}
	if s.LinkedByRemote != lwRemote || s.ProjectID != pid {
		t.Fatalf("after the link: project %q by remote %q, want %q by %q (the fresh link)", s.ProjectID, s.LinkedByRemote, pid, lwRemote)
	}
	if len(s.Result.Links) != 1 || s.Result.Links[0].Folder != "client_a" {
		t.Fatalf("links %+v, want the one to client_a", s.Result.Links)
	}
}

// TestGatherPackageProjectUsesTheRepoLink (D-13): a package folder of a
// linked client repo, set up as its own project (its own .lore below the
// repo top), has no link of its own: the repo's (root project's) link
// covers it, so it resolves as a client repo with the root's folder.
func TestGatherPackageProjectUsesTheRepoLink(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	writeFile(t, ConfigPath(home), "orgs:\n  - name: acme\n    identity: {key: human:a@acme.com}\n    stores:\n      - url: https://github.com/acme/notes\n        org_folder: org\n      - url: https://github.com/acme/notes-two\n")
	const remote = "github.com/client/hub"
	src := &remoteLinks{pid: "prj_root", remote: remote, folder: "client_a"}
	top := gitRepo(t, "git@github.com:client/hub.git", "prj_root")
	pkg := filepath.Join(top, "workers", "extraction")
	writeFile(t, filepath.Join(pkg, ".lore", "data", "_meta.json"), `{"project_id":"prj_pkg","_v":2}`)

	s, err := Gather(ctx, In{Home: home, Dir: pkg, Links: src})
	if err != nil || s.ResolveErr != nil {
		t.Fatalf("Gather in the package: %v %v", err, s.ResolveErr)
	}
	if s.ProjectID != "prj_pkg" {
		t.Fatalf("project %q, want the package's own prj_pkg", s.ProjectID)
	}
	if s.Result.Role != orgresolve.RoleClient || len(s.Result.Links) != 1 || s.Result.Links[0].Folder != "client_a" {
		t.Fatalf("role %q links %+v, want client with the repo's link to client_a", s.Result.Role, s.Result.Links)
	}
}
