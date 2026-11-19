package wire

// storehere.go — a command run inside a store's checkout (STORE layout
// 1.5, 2026-11-12). A store clone (~/.lore/stores/<owner>/<repo>, a
// person's own clone, or a folder store's folder) is recognised by
// lore-store.yaml at its top (rowfs.StoreTop). It is never a code repo:
//
//	open (full)      ─▶ openStoreHere: no repo store, the one store folder
//	                    the command runs in attached and written to
//	                    (openDetached), so `cd <store>/<folder> && lore task
//	                    list` lists that folder; at the store top, the root
//	repoTop          ─▶ errInStore: no .lore/ is made, no stores.yaml, no
//	                    .gitignore or hook edits (every repo-only path asks
//	                    repoTop or configState first)
//	setup resolution ─▶ no folder rule matches a store (setupactive.go)
//
// A git clone is used only when it is the clone lore uses for that store
// (git.CloneDirFor: lore's own under ~/.lore/stores, or one `lore store
// use` recorded); any other clone of a store is refused with the command
// that makes lore use it, so lore never writes a clone it does not track.

import (
	"os"
	"path/filepath"
	"strings"

	gitbackend "github.com/thesatellite-ai/lore/adapters/backend/git"
	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/engine/attach"
	"github.com/thesatellite-ai/lore/logic/fileuri"
	"github.com/thesatellite-ai/lore/logic/repourl"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/fspath"
	"github.com/thesatellite-ai/lore/store"
)

// storeCheckout is the store a command runs inside.
type storeCheckout struct {
	// top is the store's top folder (lore-store.yaml there), canonical:
	// stored and compared that way (model/fspath, devco D-413).
	top string
	// shown is top in the spelling of the folder the command ran in (on
	// macOS /var stays /var, never /private/var): what messages name.
	shown string
	// folder is the store folder the command runs in (rowfs.FolderRoot at
	// the top or outside every folder).
	folder string
	// isGit: a git clone (else a folder store's folder).
	isGit bool
}

// storeHere reports whether dir is inside a store's checkout.
func storeHere(dir string) (storeCheckout, bool) {
	top, isGit, ok := rowfs.StoreTop(dir)
	if !ok {
		return storeCheckout{}, false
	}
	return storeCheckout{top: top, shown: fspath.InTyped(dir, top), folder: rowfs.StoreFolderAt(top, dir), isGit: isGit}, true
}

// inStore reports whether dir is inside a store's checkout.
func inStore(dir string) bool {
	_, ok := storeHere(dir)
	return ok
}

// errInStore refuses a command that needs a code repo inside a store.
// Its hint is the repo-only class (repoonly.go, devco D-523): cd into the
// code repo, --store does not apply; before it said only "run it in a code
// repo", and a move (memory move --to <org>/<folder>) tried --store next.
func errInStore(sc storeCheckout) error {
	return errs.New(errs.NotAStore, "wire: "+sc.shown+" is a lore store ("+rowfs.MarkerFile+" at its top), not a code repo; this command works on a code repo's rows").
		WithHint("cd into the code repo it is for and run it there; --store does not apply (a store clone is no code repo); inside a store folder lore reads and writes that folder (`lore task list`, `lore task add …`)")
}

// storeHereEntry is the attach entry of the store folder sc names: the
// folder store at its top, or the git store its clone's origin names,
// pinned to the clone's branch when the clone is that store's pinned
// clone. Its name is the store's name on this machine when one is known
// (an org entry, me), else the clone folder's name.
func storeHereEntry(home string, sc storeCheckout) (attach.Store, error) {
	one := attach.Store{Folder: sc.folder}
	if sc.folder == rowfs.FolderRoot {
		one.Folder = ""
	}
	name := filepath.Base(sc.top)
	if !sc.isGit {
		one.URL = fileuri.URL(sc.top, "")
		if p := personalFolderOf(home); p != "" && fspath.Same(p, sc.top) {
			name = attach.ReservedMe
		}
	} else {
		origin := gitbackend.OriginAt(sc.top)
		r, err := gitbackend.RemoteOfRaw(origin)
		if err != nil {
			return attach.Store{}, errs.New(errs.NotAStore, "wire: "+sc.shown+" is a store clone whose origin is not a store url lore can read").
				WithHint("point its origin at the store (`git -C " + sc.shown + " remote set-url origin <store url>`), or run lore in a code repo")
		}
		one.URL = origin
		used, _, err := gitbackend.CloneDirFor(home, r, "")
		if err != nil {
			return attach.Store{}, err
		}
		if !fspath.Same(used, sc.top) {
			branch := headBranch(sc.top)
			pinned, _, perr := gitbackend.CloneDirFor(home, r, branch)
			if perr != nil || branch == "" || !fspath.Same(pinned, sc.top) {
				return attach.Store{}, errs.New(errs.Refused, "wire: "+sc.shown+" is a clone of store "+repourl.ShownStore(r.Norm)+" that lore does not use (lore's clone of it is "+used+"), so lore will not read or write it").
					WithHint("make lore use this clone: lore store use --path " + sc.shown + " (or work in " + used + ")")
			}
			one.Branch = branch
		}
		if org := orgOfURL(home, origin); org != "" {
			name = org
		} else if p := personalURLOf(home); p != "" && sameStoreURL(p, origin) {
			name = attach.ReservedMe
		}
	}
	if one.Folder != "" {
		name += targetSep + one.Folder
	}
	one.Name = store.Source(name)
	return one, nil
}

// headBranch is the branch the clone at dir has checked out ("" when
// detached or unreadable), read from .git/HEAD.
func headBranch(dir string) string {
	b, err := os.ReadFile(filepath.Join(dir, ".git", "HEAD"))
	if err != nil {
		return ""
	}
	br, ok := strings.CutPrefix(strings.TrimSpace(string(b)), headRefPrefix)
	if !ok {
		return ""
	}
	return br
}

// sameStoreURL reports whether two git urls name one store (host/path,
// an ssh alias resolved or routed by insteadOf: sameStoreAs, D-627).
func sameStoreURL(a, b string) bool {
	if _, err := gitbackend.RemoteOfRaw(a); err != nil {
		return false
	}
	if _, err := gitbackend.RemoteOfRaw(b); err != nil {
		return false
	}
	same := sameStoreAs(a)
	return same != nil && same(b)
}

// storeIdentity is the one identity of a store url for comparing two
// spellings of a store: a git url's host/path with an ssh alias host
// resolved to its HostName (gitbackend.Remote.Norm, D-493), so
// git@<alias>:org/repo.git, git@github.com:org/repo.git and
// https://github.com/org/repo are one store; any other url falls back to
// repourl.NormalizeStore.
func storeIdentity(raw string) (string, error) {
	if r, err := gitbackend.RemoteOfRaw(raw); err == nil {
		return r.Norm, nil
	}
	return repourl.NormalizeStore(raw)
}

// personalURLOf is personal.store of the machine config ("" when unset).
func personalURLOf(home string) string {
	mc, err := readMachineConfig(home)
	if err != nil {
		return ""
	}
	return mc.personalURL()
}

// personalFolderOf is the local folder of a file:// personal store ("").
func personalFolderOf(home string) string {
	p := personalURLOf(home)
	if !strings.HasPrefix(p, folderURLScheme) {
		return ""
	}
	return fileuri.LocalPath(p)
}

// storeCloneOrg is the org whose store the store checkout around dir is
// ("" outside a store checkout, or for a store of no org entry): the org a
// command run in the clone belongs to, so setup resolution (step 5) picks
// that org's setup and identity there as in the org's code repos (devco
// D-693).
func storeCloneOrg(home, dir string) string {
	sc, ok := storeHere(dir)
	if !ok {
		return ""
	}
	if !sc.isGit {
		return orgOfURL(home, fileuri.URL(sc.top, ""))
	}
	return orgOfURL(home, gitbackend.OriginAt(sc.top))
}
