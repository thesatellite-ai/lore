package wire

// storeseturl_seed.go — `lore store set-url` to an EMPTY git repo (devco
// N-13). The hint for a personal folder store used to be "push the folder
// to a private git repo, then lore store set-url": the folder is no git
// repository, set-url to the still-empty repo was refused with a wrong
// next step, and the empty clone that refusal left was opened again
// without a fetch, so the retry failed even after the push.
//
//	set-url --url <git>
//	  ├─ a clone of <git> lore made earlier with no commit and no file:
//	  │  removed first (opened again it would not fetch; it holds nothing)
//	  ├─ open <git>: a store with an id → set-url as before
//	  ├─ empty, and the store set-url moves is a folder store:
//	  │     copy the folder into the clone and commit it (seedFromFolder;
//	  │     the next `lore sync` pushes it, as after any write);
//	  │     the store keeps its id, the folder stays as it was
//	  └─ anything else refused: the hint names the step that fits
//	     (empty: push the store there; not empty: check the url), and a
//	     clone this run made that holds nothing is removed again

import (
	"context"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/thesatellite-ai/lore/adapters/backend/folder"
	gitbackend "github.com/thesatellite-ai/lore/adapters/backend/git"
	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/engine/attach"
	"github.com/thesatellite-ai/lore/logic/fileuri"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
)

// seedSubject is the commit subject of a folder store copied to git.
const seedSubject = "lore: store copied here from a folder store"

// setURLClone is lore's clone folder of the git url raw on branch and
// whether it exists now; "" when raw is no git url.
func (a *App) setURLClone(raw, branch string) (string, bool) {
	r, err := gitbackend.RemoteOfRaw(raw)
	if err != nil {
		return "", false
	}
	dir, own, err := gitbackend.CloneDirFor(a.opts.Home, r, branch)
	if err != nil || own {
		return "", false // a person's own clone is never removed or filled
	}
	return dir, exists(filepath.Join(dir, ".git"))
}

// emptyClone reports whether dir is a git clone with no commit on any
// branch and no file besides .git: what opening an empty remote leaves.
// Removing it loses nothing.
func emptyClone(ctx context.Context, dir string) bool {
	if dir == "" || !exists(filepath.Join(dir, ".git")) {
		return false
	}
	refs, err := gitRun{dir: dir, args: []string{"for-each-ref", "--count=1", "--format=%(refname)", "refs/heads/", "refs/remotes/"}}.output(ctx)
	if err != nil || strings.TrimSpace(string(refs)) != "" {
		return false
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if e.Name() != ".git" {
			return false
		}
	}
	return true
}

// dropEmptyClone removes dir when it is an empty clone (emptyClone) and
// reports whether it did.
func dropEmptyClone(ctx context.Context, dir string) bool {
	if !emptyClone(ctx, dir) {
		return false
	}
	return os.RemoveAll(dir) == nil
}

// dropNewClone removes dir, a clone this set-url made before it was
// refused, unless it holds work of its own (it never does: set-url only
// opened it): a refused set-url leaves no clone that the next try would
// open without a fetch.
func dropNewClone(ctx context.Context, dir string) {
	if dir == "" || !exists(filepath.Join(dir, ".git")) {
		return
	}
	if !emptyClone(ctx, dir) && len(unpushedIn(ctx, cloneDirs{git: dir, work: dir})) > 0 {
		return
	}
	_ = os.RemoveAll(dir) // best effort: a clone left is opened again, as before
}

// folderStoreDir is the folder of a folder store src names ("" when src
// is no folder store with a marker).
func folderStoreDir(src attach.Store) string {
	canon, err := canonical(attach.Store{URL: src.URL})
	if err != nil {
		return ""
	}
	u, err := url.Parse(canon)
	if err != nil || u.Scheme != folder.Scheme {
		return ""
	}
	dir := fileuri.OSPath(u.Path)
	if _, err := rowfs.ReadMarker(dir); err != nil {
		return ""
	}
	return dir
}

// seedFromFolder fills lore's clone of the empty git store at raw with the
// folder store src names and commits it (never pushes: `lore sync` does); it returns the store's id (the folder's) and
// the line set-url prints. ok is false when src is no folder store, or
// the clone at raw is not empty: nothing was done.
func (a *App) seedFromFolder(ctx context.Context, src attach.Store, raw, cloneDir string) (ids.ID[ids.Store], string, bool, error) {
	dir := folderStoreDir(src)
	if dir == "" || !emptyClone(ctx, cloneDir) {
		return ids.ID[ids.Store]{}, "", false, nil
	}
	canon, err := canonical(attach.Store{Name: src.Name, URL: raw, Branch: src.Branch})
	if err != nil {
		return ids.ID[ids.Store]{}, "", false, err
	}
	b, err := a.openStore(ctx, canon, raw)
	if err != nil {
		return ids.ID[ids.Store]{}, "", false, err
	}
	defer func() { _ = b.Close() }() // the commit below is the last use
	gb, isGit := b.(*gitbackend.Backend)
	if !isGit {
		return ids.ID[ids.Store]{}, "", false, nil
	}
	if err = gb.SeedFrom(ctx, dir, seedSubject); err != nil {
		return ids.ID[ids.Store]{}, "", false, errs.Prefix("store set-url: copy "+underHome(dir)+" into "+raw, err)
	}
	// Owner rule 1: nothing reaches a remote without an explicit push or
	// sync by a person, so the copy waits in the clone like any write.
	line := "copied the folder store " + underHome(dir) + " into the clone " + underHome(gb.Dir()) + " of " + raw + " (committed, not pushed: `lore sync` pushes it); lore now reads and writes that clone, and " + underHome(dir) + " is left as it was (a copy you may keep or delete)"
	return gb.Marker().ID, line, true, nil
}

// notAStoreAt is set-url's refusal for a url that holds no store: an
// empty repo gets the step that fills it, anything else the url check.
func notAStoreAt(name, raw string, empty bool) error {
	const verb = "store set-url"
	if empty {
		return errs.New(errs.NotAStore, verb+": "+raw+" is an empty repository, and "+name+" is not a folder store set-url could copy into it").
			WithHint("push the store there first (git -C <the store's clone> push " + raw + " HEAD:main), then run lore store set-url --name " + name + " --url " + raw + " again")
	}
	return errs.New(errs.NotAStore, verb+": "+raw+" holds no lore store with an id ("+rowfs.MarkerFile+")").
		WithHint("check the url (lore store set-url --name " + name + " --url <url>); a v0.1.17 store needs lore fix first")
}
