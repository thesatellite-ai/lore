package orgcfg

import (
	"context"
	"errors"
	"testing"

	"github.com/thesatellite-ai/lore/logic/orgconf"
	"github.com/thesatellite-ai/lore/logic/orgresolve"
)

// remoteLinks answers the links of one (project id, remote) pair, as a
// store holding one `lore org link` made in a repo with no lore data.
type remoteLinks struct {
	pid, remote, folder string
	asked               []string
	// fail answers every lookup of a project id with errLinks
	fail bool
}

func (r *remoteLinks) EntryLinks(ctx context.Context, _ *orgconf.Org, pid string, remotes []string) ([]orgresolve.Link, bool, error) {
	res, err := r.Links(ctx, pid, remotes)
	return res.Links, true, err
}

func (r *remoteLinks) DefaultBranch(context.Context, string) string {
	return orgconf.DefaultStoreBranch
}

func (r *remoteLinks) ClientRepoDefaults(context.Context, *orgconf.Org) (map[string]string, error) {
	return nil, nil
}

// errLinks is a link source whose stores cannot be read as stores.
var errLinks = errors.New("a code repo is listed under stores")

func (r *remoteLinks) Links(_ context.Context, pid string, remotes []string) (orgresolve.LinkResult, error) {
	r.asked = append(r.asked, pid)
	if r.fail && pid != "" {
		return orgresolve.LinkResult{}, errLinks
	}
	out := orgresolve.LinkResult{Status: orgresolve.LinkStatusKnown}
	for _, rm := range remotes {
		if pid == r.pid && rm == r.remote {
			out.Links = append(out.Links, orgresolve.Link{Entry: "acme", StoreURL: "github.com/acme/notes-two", Folder: r.folder, Remote: rm})
		}
	}
	return out, nil
}

// TestGatherLinksARepoWithNoLoreData: a client repo with no .lore/ at all
// resolves the link a store holds for its remote (the project id derived
// from the remote, orgresolve.RemoteProjectID), as a client repo of the
// org, and LinkedByRemote names that remote; a repo with its own project
// id never looks one up by remote.
func TestGatherLinksARepoWithNoLoreData(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	writeFile(t, ConfigPath(home), "orgs:\n  - name: acme\n    identity: {key: human:a@acme.com}\n    stores:\n      - url: https://github.com/acme/notes\n        org_folder: org\n      - url: https://github.com/acme/notes-two\n")
	const remote = "github.com/client/hub"
	pid, err := orgresolve.RemoteProjectID(remote)
	if err != nil {
		t.Fatal(err)
	}
	src := &remoteLinks{pid: pid, remote: remote, folder: "client_a"}

	s, err := Gather(ctx, In{Home: home, Dir: gitRepo(t, "git@github.com:client/hub.git", ""), Links: src})
	if err != nil || s.ResolveErr != nil {
		t.Fatalf("Gather: %v %v", err, s.ResolveErr)
	}
	if s.ProjectID != pid || s.LinkedByRemote != remote {
		t.Fatalf("project %q by remote %q, want %q by %q", s.ProjectID, s.LinkedByRemote, pid, remote)
	}
	if s.Result.OrgName() != "acme" || s.Result.Role != orgresolve.RoleClient {
		t.Fatalf("org %q role %q, want acme client", s.Result.OrgName(), s.Result.Role)
	}

	// another remote with no link: no project id, nothing linked
	other, err := Gather(ctx, In{Home: home, Dir: gitRepo(t, "git@github.com:client/else.git", ""), Links: src})
	if err != nil || other.ProjectID != "" || other.LinkedByRemote != "" {
		t.Fatalf("an unlinked repo: project %q remote %q err %v", other.ProjectID, other.LinkedByRemote, err)
	}

	// a lore project keeps its own id
	src.asked = nil
	own, err := Gather(ctx, In{Home: home, Dir: gitRepo(t, "git@github.com:client/hub.git", "prj_1"), Links: src})
	if err != nil || own.ProjectID != "prj_1" || own.LinkedByRemote != "" {
		t.Fatalf("a lore project: %q %q %v", own.ProjectID, own.LinkedByRemote, err)
	}
	for _, a := range src.asked {
		if a == pid {
			t.Fatal("a repo with its own project id looked its link up by remote")
		}
	}
}

// TestGatherRemoteLookupIsBestEffort: in a repo with no project id (before
// `lore init`), a store that cannot be read as one fails only the lookup
// by remote, never Gather: `lore init` still runs there, and the lookup of
// the project's own id reports the store later (TestE10).
func TestGatherRemoteLookupIsBestEffort(t *testing.T) {
	home := t.TempDir()
	writeFile(t, ConfigPath(home), "orgs:\n  - name: acme\n    identity: {key: human:a@acme.com}\n    stores:\n      - url: https://github.com/acme/notes\n        org_folder: org\n")
	src := &remoteLinks{fail: true}
	s, err := Gather(context.Background(), In{Home: home, Dir: gitRepo(t, "git@github.com:acme/tools.git", ""), Links: src})
	if err != nil || s.ProjectID != "" || s.LinkedByRemote != "" {
		t.Fatalf("Gather with an unreadable store and no project id: %+v %v, want no error and nothing linked", s, err)
	}
	if _, err = Gather(context.Background(), In{Home: home, Dir: gitRepo(t, "git@github.com:acme/tools.git", "prj_1"), Links: src}); !errors.Is(err, errLinks) {
		t.Fatalf("a project's own lookup must still report the store: %v", err)
	}
}
