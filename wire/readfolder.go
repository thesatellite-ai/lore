package wire

// readfolder.go — a read in a code repo naming a store folder the repo
// does not attach (devco D-458). `lore task list --store acme/ops` in a
// repo linked to acme/sync said "no store named acme/ops is attached",
// while the same read from a plain folder or a store clone worked: the
// folder is read from the store's clone on disk for this one command
// (read-only, never recorded in .lore/stores.yaml), as a write's
// `--to acme/ops` already attaches it (targets.go).
//
//	read --store <base>/<folder> ─▶ attached here ─▶ nothing to do
//	                              └▶ <base> a store here or of the org, the
//	                                 folder in it (or in another store of
//	                                 the org) ─▶ attach read-only + a note

import (
	"context"
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/engine/attach"
	"github.com/thesatellite-ai/lore/op"
	"github.com/thesatellite-ai/lore/store"
)

// attachReadFolder is the D-458 step (see the file comment). It does
// nothing for a write, outside a code repo (the store-only opens resolve
// --store themselves, scope.go and targetoutside.go) or when the name is
// no <base>/<folder> of a store this repo or its org knows: the engine's
// own "no store named …" answers then.
func (a *App) attachReadFolder(ctx context.Context, e op.Entry, d op.Decoder) error {
	spec := e.Spec()
	if spec.Access == op.AccessWrite || !rowRead(spec.Name) {
		return nil
	}
	if _, in := storeHere(a.opts.RepoDir); in {
		return nil
	}
	in, err := e.Decode(d)
	if err != nil {
		return nil // the engine's Run reports the decode error itself
	}
	name := scopeOf(in).store
	base, folderName, ok := strings.Cut(name, targetSep)
	if !ok || base == "" || folderName == "" || folderName == rowfs.FolderRoot || base == attach.ReservedMe {
		return nil
	}
	top, err := a.storeRepoTop(ctx)
	if err != nil || top == "" {
		return nil // outside a repo: targetoutside.go
	}
	a.mu.Lock()
	known := slices.ContainsFunc(a.extra, func(s attach.Store) bool { return string(s.Name) == name })
	a.mu.Unlock()
	if known {
		return nil
	}
	if recorded, rerr := a.recordedTarget(ctx, store.Source(name)); rerr != nil || recorded {
		return rerr
	}
	if slices.ContainsFunc(a.orgStoresHere(ctx), func(s attach.Store) bool { return string(s.Name) == name }) {
		return nil
	}
	src, found, _, err := a.baseStore(ctx, base)
	if err != nil || !found {
		return err
	}
	if f, other := a.otherOrgStoreFolder(ctx, src, folderName); other {
		src = attach.Store{Name: src.Name, URL: f.URL, Branch: f.Branch}
	}
	if _, covered, cerr := a.coveringAttachment(ctx, src, folderName); cerr != nil || covered {
		return cerr // attached under another name: that name reads it
	}
	exists, err := a.folderExists(ctx, src, folderName)
	if err != nil {
		return err
	}
	if !exists {
		return a.readMissingFolder(ctx, src, base, folderName) // a read never creates a folder (D-472)
	}
	a.attachExtra(attach.Store{Name: store.Source(name), URL: src.URL, Folder: folderName, Branch: src.Branch, ReadOnly: true})
	_, _ = fmt.Fprintf(os.Stderr, "lore: %s is not attached in this repo; read from the store's clone for this command (lore store add … --folder %s attaches it for good)\n", name, folderName)
	return nil
}
