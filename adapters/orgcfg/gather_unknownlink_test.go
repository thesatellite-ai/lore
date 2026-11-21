package orgcfg

import (
	"context"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/logic/orgconf"
	"github.com/thesatellite-ai/lore/logic/orgresolve"
)

// oldRemoteLinks is remoteLinks offline past link_index.max_age: the link
// is only in a copy too old to decide by (LinkResult.Old, status unknown).
type oldRemoteLinks struct{ remoteLinks }

func (r *oldRemoteLinks) Links(ctx context.Context, pid string, remotes []string) (orgresolve.LinkResult, error) {
	res, err := r.remoteLinks.Links(ctx, pid, remotes)
	if err != nil || len(res.Links) == 0 {
		return res, err
	}
	return orgresolve.LinkResult{Status: orgresolve.LinkStatusUnknown, Old: res.Links, Detail: "acme: github.com/acme/notes-two (last fetched 2h0m0s ago)"}, nil
}

func (r *oldRemoteLinks) EntryLinks(context.Context, *orgconf.Org, string, []string) ([]orgresolve.Link, bool, error) {
	return nil, false, nil
}

// TestGatherKeepsUnknownLinkOfARepoWithNoLoreData (devco D-860, hunt
// H-128): offline past link_index.max_age, a repo with no lore data whose
// only link is in a too-old copy is not linked (fail closed: no project
// id, no LinkedByRemote), but Gather keeps the link as UnknownLinks with
// the copy's last good fetch and the max age, so every read can say the
// folder was not searched. Before, the link was dropped without a trace.
// Break-verified by dropping the appendUnknown call in byRemote.
func TestGatherKeepsUnknownLinkOfARepoWithNoLoreData(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	writeFile(t, ConfigPath(home), "orgs:\n  - name: acme\n    identity: {key: human:a@acme.com}\n    stores:\n      - url: https://github.com/acme/notes-two\n")
	const remote = "github.com/client/hub"
	pid, err := orgresolve.RemoteProjectID(remote)
	if err != nil {
		t.Fatal(err)
	}
	fetched := time.Date(2026, 1, 5, 9, 0, 0, 0, time.UTC)
	dir, _ := cloneDir(home, "github.com/acme/notes-two")
	writeState(dir, fetchStampFile, fetched.Format(time.RFC3339Nano))

	src := &oldRemoteLinks{remoteLinks{pid: pid, remote: remote, folder: "client_a"}}
	s, err := Gather(ctx, In{Home: home, Dir: gitRepo(t, "git@github.com:client/hub.git", ""), Links: src})
	if err != nil {
		t.Fatalf("Gather: %v", err)
	}
	if s.ProjectID != "" || s.LinkedByRemote != "" {
		t.Fatalf("a too-old copy linked the repo (project %q by %q): the link must stay unknown", s.ProjectID, s.LinkedByRemote)
	}
	if len(s.UnknownLinks) != 1 {
		t.Fatalf("UnknownLinks = %+v, want the one link of the old copy", s.UnknownLinks)
	}
	u := s.UnknownLinks[0]
	if u.Entry != "acme" || u.Folder != "client_a" || !u.Since.Equal(fetched) || u.MaxAge != orgconf.DefaultLinkIndexMaxAge {
		t.Fatalf("unknown link %+v, want acme/client_a since %v, max age %v", u, fetched, orgconf.DefaultLinkIndexMaxAge)
	}

	// online again: the fresh link links the repo, and nothing is unknown
	fresh, err := Gather(ctx, In{Home: home, Dir: gitRepo(t, "git@github.com:client/hub.git", ""), Links: &src.remoteLinks})
	if err != nil || fresh.ProjectID != pid || len(fresh.UnknownLinks) != 0 {
		t.Fatalf("online: project %q unknown %+v err %v, want %q and none", fresh.ProjectID, fresh.UnknownLinks, err, pid)
	}
}
