package wire

import (
	"context"
	"os"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/orgcfg"
	"github.com/thesatellite-ai/lore/logic/orgconf"
	"github.com/thesatellite-ai/lore/logic/orgresolve"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// remoteLinks is an orgcfg.LinkSource over fixed links (orgresolve.StaticLinks).
type remoteLinks struct{ orgresolve.StaticLinks }

func (l remoteLinks) EntryLinks(ctx context.Context, _ *orgconf.Org, projectID string, remotes []string) ([]orgresolve.Link, bool, error) {
	res, err := l.Links(ctx, projectID, remotes)
	return res.Links, true, err
}
func (remoteLinks) DefaultBranch(context.Context, string) string { return orgconf.DefaultStoreBranch }
func (remoteLinks) ClientRepoDefaults(context.Context, *orgconf.Org) (map[string]string, error) {
	return nil, nil
}

var _ orgcfg.LinkSource = remoteLinks{}

// TestDefaultTargetLinkedByRemote (H-2): in an own repo with no lore files
// that a store link names by its remote, a plain create goes to the linked
// folder's store, not to the repo store (a read-only cache there); a repo
// with its own lore files still writes to itself.
func TestDefaultTargetLinkedByRemote(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	const remote = "github.com/acme/svc"
	config := "orgs:\n  - name: acme\n    identity: {key: human:a@acme.com, name: A, handle: a}\n    own: [\"github.com/acme/*\"]\n    stores:\n      - url: https://github.com/acme/notes\n"
	home, dir := orgTestRepo(t, "https://"+remote, config)
	if err := os.RemoveAll(dir + "/.lore"); err != nil { // a repo with no lore files
		t.Fatal(err)
	}
	pid, err := orgresolve.RemoteProjectID(remote)
	if err != nil {
		t.Fatal(err)
	}
	links := remoteLinks{orgresolve.StaticLinks{Status: orgresolve.LinkStatusKnown, All: map[string][]orgresolve.Link{
		pid: {{Entry: "acme", StoreURL: "github.com/acme/notes", Folder: "svc", Remote: remote, Own: true}},
	}}}
	r := NewOrgResolution(home, dir, links)
	q := WriteQuery{Table: schema.TableMemories, Text: "a note"}
	got, err := r.DefaultTarget(ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	if got != store.Source("acme") {
		t.Fatalf("DefaultTarget = %q, want the linked folder's store acme", got)
	}
	if r.Routed() == "" {
		t.Error("the routed write does not say why it went to the folder")
	}

	// the same repo with its own lore files: the repo store, as before
	home2, dir2 := orgTestRepo(t, "https://"+remote, config)
	r2 := NewOrgResolution(home2, dir2, links)
	if got, err := r2.DefaultTarget(ctx, q); err != nil || got != "" {
		t.Fatalf("a repo with its own lore files: DefaultTarget = %q, %v; want the repo store", got, err)
	}
}
