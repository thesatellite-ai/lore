package stores

import (
	"context"
	"strings"
	"testing"
)

// fakeNamedShow is a Manager that also finds a store's clone and lists its
// folders (store show --store <name>).
type fakeNamedShow struct {
	*fakeEnv
	clone   CloneView
	folders []string
}

func (f fakeNamedShow) StoreClone(context.Context, string) (CloneView, bool) { return f.clone, true }

func (f fakeNamedShow) ListFolders(_ context.Context, name string) (FolderListOut, error) {
	out := FolderListOut{Stores: []string{name}}
	for _, d := range f.folders {
		out.Folders = append(out.Folders, FolderRow{Store: name, Folder: d})
	}
	return out, nil
}

// TestStoreShowNamedPrintsCloneAndFolders guards devco D-571: `store show
// --store acme` printed only "push manual, compact off, epoch 0"; it now
// also prints the url, the clone and its last fetch, the folders and the
// unpushed count, as store show without a name does.
func TestStoreShowNamedPrintsCloneAndFolders(t *testing.T) {
	m := fakeNamedShow{fakeEnv: &fakeEnv{},
		clone: CloneView{URL: "git@github.com:acme-co/lore-team.git", Shown: "git@github.com:acme-co/lore-team.git", Cloned: true,
			Clone: "/home/ada/.lore/stores/acme-co/lore-team", DefaultBranch: "main", LastFetch: "2026-11-17T10:00:00+05:30",
			Unpushed: []string{"refs/heads/main (2 commits)"}},
		folders: []string{"org", "web"}}
	v := PushView{Store: "acme", Place: PlaceOrg, Org: "acme", Push: "manual", Summary: "push manual, compact off", Unpushed: 2}
	out := namedStoreShow(context.Background(), m, "acme", v).Text()
	for _, want := range []string{
		"acme: push manual, compact off",
		"  at        git@github.com:acme-co/lore-team.git",
		"  folders   org, web",
		"  waiting   2 local commit(s) not pushed yet",
		"  clone     /home/ada/.lore/stores/acme-co/lore-team   default branch main   last fetch 2026-11-17T10:00:00+05:30",
		"  unpushed  refs/heads/main (2 commits)",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("store show --store acme lacks %q:\n%s", want, out)
		}
	}
	v.Unpushed, m.clone.Unpushed = 0, nil
	if out := namedStoreShow(context.Background(), m, "acme", v).Text(); !strings.Contains(out, "waiting   nothing") {
		t.Errorf("no unpushed count when nothing waits:\n%s", out)
	}
}
