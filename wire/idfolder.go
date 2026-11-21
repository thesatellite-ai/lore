package wire

// idfolder.go — a row named by id that lives in a folder of the org's
// stores this repo does not attach (B2-21). `lore task add … --to
// acme/sync` writes there and attaches the folder for that one command
// only; `lore task show/edit <id>` from the same repo then answered
// E_NOT_FOUND with "it may have landed on main". Now an operation whose
// argument is a row id the attached stores do not hold looks for the row
// file in the folders of the org's store clones on this machine, and
// attaches the folder that holds it for this command, as the --to write
// did; not found there either, the answer says where lore looked.
//
//	op with <id> ─▶ attached stores hold it ─▶ nothing to do
//	             └▶ org store clones: <clone>/<folder>/<table dir>/<id>.json
//	                  ─▶ found: attach "<org>/<folder>" (unrecorded) + a note
//	                  └▶ none: E_NOT_FOUND names the places searched

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/engine/attach"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/op"
	"github.com/thesatellite-ai/lore/store"
)

// rowFileExt is a row file's extension.
const rowFileExt = ".json"

// idArg is the row id an operation's first positional argument holds, ""
// when it takes none or the value is no row id.
func idArg(e op.Entry, d op.Decoder) string {
	in, err := e.Decode(d)
	if err != nil {
		return ""
	}
	arg0 := ""
	for _, f := range e.InputFields() {
		if f.Arg == 0 {
			arg0 = f.Name
		}
	}
	if arg0 == "" {
		return ""
	}
	for _, tv := range in.Texts() {
		if tv.Field == arg0 {
			if _, perr := ids.ParseAny(tv.Text); perr == nil {
				return tv.Text
			}
			return ""
		}
	}
	return ""
}

// idSearch is what attachFolderOfID looked at for a missing id.
type idSearch struct {
	id       string
	attached []string // the stores attached here
	folders  []string // "<org>/<folder>" of the org's store clones read
	// gone is the tombstone an attached store holds for id (devco:D-590)
	gone *goneRow
}

// attachFolderOfID is the B2-21 step (see the file comment); the returned
// search is non-nil when the id was not found anywhere, for the hint.
func (a *App) attachFolderOfID(ctx context.Context, e op.Entry, d op.Decoder) (*idSearch, error) {
	id := idArg(e, d)
	if id == "" {
		return nil, nil
	}
	anyID, err := ids.ParseAny(id)
	if err != nil {
		return nil, nil
	}
	eng, err := a.Engine(ctx)
	if err != nil {
		return nil, nil // the operation reports the open error itself
	}
	src, got, gerr := eng.Get(ctx, engine.Ref{ID: anyID})
	if gerr == nil && got.Deleted {
		// devco:D-590: an attached store holds the row's tombstone (Get
		// returns tombstones); the read's not-found says where and when
		// it was deleted (a trashed row answers E_IN_TRASH, untouched).
		// The repo's own tombstone is left to the repo's read: its live
		// ref keeps its own purge list and dated answer (live_notfound.go
		// whyMissingCode), which a purge before a sync only reaches there.
		if src == store.SourceRepo {
			return nil, nil
		}
		day := ""
		if !got.HLC.Wall.IsZero() {
			day = got.HLC.Wall.UTC().Format(dayLayout)
		}
		// a purged id is gone for good: E_PURGED, never the trash list
		// (an unreadable list counts as purged, as purgedInFolderOf)
		purged, perr := eng.IsPurged(ctx, src, anyID)
		return &idSearch{id: id, gone: &goneRow{id: id, place: string(src), day: day, purged: purged || perr != nil}}, nil
	}
	if gerr == nil || !errs.Has(gerr, errs.NotFound) {
		return nil, nil
	}
	search := &idSearch{id: id}
	for _, s := range eng.Stores() {
		search.attached = append(search.attached, string(s))
	}
	scan := a.orgFolders(ctx)
	if sc, in := storeHere(a.opts.RepoDir); in {
		// D-458: in a store clone's folder the other folders of the clone
		// and of its org's stores (the setup's org knows nothing of a
		// clone, which is under no setup's folders)
		scan = a.cloneIDScan(ctx, sc)
	}
	// devco:D-755: a row in a folder of the personal store (me/<folder>)
	// is found by id from any folder, as an org folder's row is
	scan = a.withPersonalFolders(ctx, scan)
	var gone *goneRow
	for _, f := range scan.Folders {
		name := f.Org
		if name == "" {
			name = f.Label
		}
		target := name + targetSep + f.Folder
		search.folders = append(search.folders, target)
		dir := f.Dir
		if dir == "" {
			var derr error
			if dir, derr = a.storeDir(ctx, attach.Store{Name: store.Source(f.Label), URL: f.URL, Branch: f.Branch}); derr != nil {
				continue
			}
		}
		hits, _ := filepath.Glob(filepath.Join(dir, f.Folder, "*", id+rowFileExt))
		if len(hits) == 0 {
			continue
		}
		if live, g := liveHit(hits, id, target); !live {
			// devco:D-590: a tombstone is no row to read there; never
			// "it is in <folder>" for a deleted row
			if gone == nil {
				gone = g
			}
			continue
		}
		a.attachExtra(attach.Store{Name: store.Source(target), URL: f.URL, Folder: f.Folder, Branch: f.Branch})
		if name == attach.ReservedMe {
			// devco:D-755: `me` is no name store add takes, and an id finds
			// its personal folder from every folder: no step to print
			_, _ = fmt.Fprintf(os.Stderr, "lore: %s is in %s (your personal store); found there for this command\n", id, target)
			return nil, nil
		}
		_, _ = fmt.Fprintf(os.Stderr, "lore: %s is in %s, which is not attached here; read from there for this command (lore store add … --folder %s attaches it for good)\n", id, target, f.Folder)
		return nil, nil
	}
	if gone != nil {
		return nil, gone.err()
	}
	return search, nil
}

// cloneIDScan is the folders a row id is looked for in from the store
// clone sc: every folder of cloneFolderScan but the one the read ran in
// (attached already: the read looked there).
func (a *App) cloneIDScan(ctx context.Context, sc storeCheckout) orgFolderScan {
	all := a.cloneFolderScan(ctx, sc)
	out := orgFolderScan{Unread: all.Unread, Searched: all.Searched, NoAccess: all.NoAccess}
	for _, f := range all.Folders {
		if f.Dir == sc.top && f.Folder == sc.folder {
			continue
		}
		out.Folders = append(out.Folders, f)
	}
	return out
}

// cloneFolderScan is every folder a store clone sc reaches: the clone's
// own folders, read from the clone on disk under the name its reads use
// (<store>/<folder>), then every folder of the other stores of the
// clone's org (scope.go's sources).
func (a *App) cloneFolderScan(ctx context.Context, sc storeCheckout) orgFolderScan {
	scan := orgFolderScan{Unread: map[string]error{}}
	root, err := storeHereEntry(a.opts.Home, storeCheckout{top: sc.top, shown: sc.shown, folder: rowfs.FolderRoot, isGit: sc.isGit})
	if err != nil {
		return scan
	}
	base := string(root.Name)
	scan.Searched = append(scan.Searched, base)
	own, err := storeFolders(ctx, sc.top, folderOf(ctx))
	if err != nil {
		scan.Unread[base] = err
	}
	for _, r := range own {
		id, perr := ids.Parse[ids.Project](r.ID)
		if perr != nil {
			continue
		}
		scan.Folders = append(scan.Folders, orgFolder{Label: base, Org: base, Folder: r.Folder, ID: id, URL: root.URL, Branch: root.Branch, Dir: sc.top, Rows: r.Rows})
	}
	rootKey := placeKey(root.URL, root.Branch)
	others := a.foldersOf(ctx, cloneEntries(cloneOrgEntry(a.opts.Home, root.URL), root))
	for _, f := range others.Folders {
		if placeKey(f.URL, f.Branch) != rootKey {
			scan.Folders = append(scan.Folders, f)
		}
	}
	for k, v := range others.Unread {
		scan.Unread[k] = v
	}
	scan.NoAccess = others.NoAccess
	return scan
}

// attachExtra attaches target for the rest of this process (not recorded
// in stores.yaml) and makes the next Engine call reopen with it.
func (a *App) attachExtra(target attach.Store) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.extra = append(a.extra, target)
	if a.st != nil {
		_ = a.st.close() // reopened below with the new target; a close error has nothing left to lose
		a.st, a.opened, a.err = nil, false, nil
	} else if a.opened {
		a.opened, a.err = false, nil
	}
}

// withSearchHint makes a not-found answer for s.id say where lore looked
// (the stores attached here and the org's store folders it read). It goes
// into the message, not the hint: the live hook still explains a deleted
// or purged id, and adds its own next step when nothing else did.
func (s *idSearch) withSearchHint(err error) error {
	var e *errs.Error
	if s == nil || !errors.As(err, &e) || e.Code != errs.NotFound || !strings.Contains(e.Message, s.id) {
		return err
	}
	if s.gone != nil {
		return s.gone.err()
	}
	sort.Strings(s.attached)
	sort.Strings(s.folders)
	where := "looked in the stores attached here (" + strings.Join(s.attached, ", ") + ")"
	if len(s.folders) > 0 {
		where += " and in the org's store folders on this machine (" + strings.Join(s.folders, ", ") + ")"
	}
	c := *e
	c.Message = e.Message + "; " + where
	return &c
}
