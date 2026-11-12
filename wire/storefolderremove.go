package wire

// storefolderremove.go — opsstores.FolderRemover, `lore store folder
// remove <store> <folder> [--purge]` (ops/stores/folderremove.go has the
// flow and the questions).
//
//	FolderContents ─▶ findStore ─▶ org store? only its admin
//	               ─▶ the org's org_folder? refused
//	               ─▶ the folder's _meta.json (none: E_NOT_FOUND), its
//	                  links, its rows (live, trashed; actor copies aside)
//	RemoveFolder   ─▶ --purge with rows: the folder opened alone
//	                  (openFolderApp, no push wrap), every live row into
//	                  the trash under one bat_ (engine Delete: cascades,
//	                  restrict order, reason)
//	               ─▶ one store commit: an empty folder's files go; a
//	                  purged folder's _meta.json (its id and links) goes,
//	                  its trashed rows and actor copies stay for `lore
//	                  trash restore` once the folder is made again
//	               ─▶ pushed per the store's push mode (now: at once;
//	                  batch, manual: with the next push / `lore sync`)

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/thesatellite-ai/lore/adapters/backend/folder"
	"github.com/thesatellite-ai/lore/adapters/backend/git"
	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/adapters/orgcfg"
	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/engine/attach"
	"github.com/thesatellite-ai/lore/logic/fileuri"
	"github.com/thesatellite-ai/lore/logic/storepush"
	"github.com/thesatellite-ai/lore/logic/trash"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	opsstores "github.com/thesatellite-ai/lore/ops/stores"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

var _ opsstores.FolderRemover = (*App)(nil)

// Folder removal wording and names.
const (
	folderRemoveVerb    = "store folder remove"
	folderRemoveSubject = "lore: store folder remove "
	// folderRemoveReason is the delete reason the purged rows carry when
	// --reason names none (delete.require_reason is met; `lore trash list`
	// shows it).
	folderRemoveReason = "store folder remove --purge"
	// folderRemoveSource names the folder's store in the purge's own App.
	folderRemoveSource = store.Source("folder_remove")
	// metaLinksField is the links list of a folder's _meta.json (`lore org
	// link`, adapters/orgcfg).
	metaLinksField = "links"
)

// FolderContents implements opsstores.FolderRemover.
func (a *App) FolderContents(ctx context.Context, name, folderName string) (opsstores.FolderContents, error) {
	at, err := a.folderStore(ctx, folderRemoveVerb, name, folderName) // the org's store that holds the folder (foldertarget.go)
	if err != nil {
		return opsstores.FolderContents{}, err
	}
	name = at.Name
	src, err := a.findStore(ctx, folderRemoveVerb, name)
	if err != nil {
		return opsstores.FolderContents{}, err
	}
	label := at.Label
	dir, err := a.storeDir(ctx, src)
	if err != nil {
		return opsstores.FolderContents{}, errs.Prefix(folderRemoveVerb+": store "+label, err)
	}
	if _, _, cerr := orgcfg.LoadConfig(a.opts.Home); cerr != nil {
		return opsstores.FolderContents{}, cerr // who may remove is read from it: fail closed
	}
	place, org := a.removePlace(src, name, dir)
	if place == placeOrg && !isOrgAdmin(a.opts.Home, org) {
		return opsstores.FolderContents{}, errs.New(errs.Refused, folderRemoveVerb+": store "+label+" belongs to org "+org+" and only an admin of "+org+" removes its folders; this machine has no admin setup of "+org).
			WithHint("ask the org admin to run `lore store folder remove " + name + " " + folderName + "`")
	}
	if a.isOrgFolder(src, folderName) {
		return opsstores.FolderContents{}, errs.New(errs.Refused, folderRemoveVerb+": "+folderName+" is the org folder of "+label+" (org_folder): it holds the org's roster, teams and lore-org.yaml and is never removed")
	}
	c := opsstores.FolderContents{Store: label, Folder: folderName}
	if gb, done, gerr := a.openGitStore(ctx, src); gerr == nil {
		c.Push = string(a.storeSettings(gb, place).Push)
		done()
	}
	root, err := rowfs.Open(dir, a.opts.rowfsOptions()...)
	if err != nil {
		return opsstores.FolderContents{}, err
	}
	f, err := root.Folder(folderName)
	if err != nil {
		return opsstores.FolderContents{}, err
	}
	meta := filepath.Join(dir, filepath.FromSlash(rowfs.FolderDataPath(folderName)), rowfs.MetaFileName)
	raw, err := os.ReadFile(meta)
	if errors.Is(err, fs.ErrNotExist) {
		return opsstores.FolderContents{}, errs.New(errs.NotFound, folderRemoveVerb+": "+label+" has no folder "+folderName+a.folderMissDetail(ctx)).
			WithHint("`lore store folder list " + name + "` lists its folders")
	}
	if err != nil {
		return opsstores.FolderContents{}, errs.Wrap(errs.Internal, folderRemoveVerb+": read the folder's "+rowfs.MetaFileName, err)
	}
	if c.Links, err = metaLinkCount(raw); err != nil {
		return opsstores.FolderContents{}, errs.Wrap(errs.Internal, folderRemoveVerb+": "+folderName+"'s "+rowfs.MetaFileName, err)
	}
	id, err := f.ProjectID(ctx)
	if err != nil {
		return opsstores.FolderContents{}, err
	}
	c.ProjectID = id.String()
	rows, _, err := f.Read(ctx, "")
	if err != nil {
		return opsstores.FolderContents{}, err
	}
	for _, r := range rows {
		live, trashed, cerr := rowKind(r)
		if cerr != nil {
			return opsstores.FolderContents{}, cerr
		}
		if live {
			c.Rows++
		}
		if trashed {
			c.Trashed++
		}
	}
	return c, nil
}

// rowKind sorts one row file of a folder: live (counted, trashed by
// --purge), trashed (counted, kept), or neither (a tombstone, or a copy
// of a roster actor: actors are never deleted and go with the folder).
func rowKind(r store.Row) (live, trashed bool, err error) {
	if r.Deleted || r.Table == schema.TableActors {
		return false, false, nil
	}
	m, err := r.Decode()
	if err != nil {
		return false, false, err
	}
	if _, in := trash.Read(m); in {
		return false, true, nil
	}
	return true, false, nil
}

// metaLinkCount counts the repo links in a folder's _meta.json.
func metaLinkCount(raw []byte) (int, error) {
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(raw, &doc); err != nil {
		return 0, err
	}
	l, ok := doc[metaLinksField]
	if !ok || string(l) == "null" {
		return 0, nil
	}
	var links []json.RawMessage
	if err := json.Unmarshal(l, &links); err != nil {
		return 0, err
	}
	return len(links), nil
}

// removePlace is where the store's settings and rights live: the personal
// store, an org's store (by the org entry listing its url, else the
// lore-org.yaml its worktree holds), or a store of no org.
func (a *App) removePlace(src attach.Store, name, dir string) (pushPlace, string) {
	if name == attach.ReservedMe {
		return placePersonal, ""
	}
	if org := orgOfURL(a.opts.Home, src.URL); org != "" {
		return placeOrg, org
	}
	return refinePlace(dir, placeStore, "")
}

// isOrgFolder reports whether folderName is the org folder an org entry
// sets on this store (org_folder: the roster, teams and lore-org.yaml).
func (a *App) isOrgFolder(src attach.Store, folderName string) bool {
	cfg, _, err := orgcfg.LoadConfig(a.opts.Home)
	if err != nil || cfg == nil {
		return folderName == rowfs.FolderOrg // unreadable config: the default name stays guarded
	}
	want, ok := storeKey(src.URL)
	if !ok {
		return false
	}
	for _, o := range cfg.Orgs {
		for _, sc := range o.Stores {
			if k, kok := storeKey(sc.URL); kok && k == want && strings.Trim(sc.OrgFolder, "/") == folderName {
				return true
			}
		}
	}
	return false
}

// RemoveFolder implements opsstores.FolderRemover.
func (a *App) RemoveFolder(ctx context.Context, in opsstores.FolderRemoveIn, c opsstores.FolderContents) (opsstores.FolderRemoveOut, error) {
	at, err := a.folderStore(ctx, folderRemoveVerb, in.Store, c.Folder)
	if err != nil {
		return opsstores.FolderRemoveOut{}, err
	}
	in.Store = at.Name
	src, err := a.findStore(ctx, folderRemoveVerb, in.Store)
	if err != nil {
		return opsstores.FolderRemoveOut{}, err
	}
	out := opsstores.RemovedOut(c)
	if c.Rows > 0 {
		reason := strings.TrimSpace(in.Reason)
		if reason == "" {
			reason = folderRemoveReason
		}
		if out.Batch, err = a.trashFolderRows(ctx, src, in.Store, c.Folder, reason); err != nil {
			return opsstores.FolderRemoveOut{}, err
		}
	}
	keepRows := !c.Empty()
	pushed, note, err := a.commitStoreEdit(ctx, src, in.Store, folderRemoveSubject+c.Folder, func(dir string) ([]string, error) {
		return removeFolderFiles(dir, c.Folder, keepRows)
	})
	if err != nil {
		if out.Batch != "" {
			return opsstores.FolderRemoveOut{}, errs.Prefix(folderRemoveVerb+": the rows are in the trash under "+out.Batch+" but the folder is still there", err).
				WithHint("rerun `lore store folder remove " + in.Store + " " + c.Folder + " --purge --yes`, or `lore trash restore " + out.Batch + "`")
		}
		return opsstores.FolderRemoveOut{}, err
	}
	out.Pushed, out.Note = pushed, note
	return out, nil
}

// removeFolderFiles deletes the folder's files under dir and returns the
// slash path to commit: the whole folder, or with keepRows only its
// _meta.json (the trashed row files and actor copies stay, so the rows
// come back with `lore trash restore` once the folder is made again).
func removeFolderFiles(dir, folderName string, keepRows bool) ([]string, error) {
	if keepRows {
		rel := path.Join(rowfs.FolderDataPath(folderName), rowfs.MetaFileName)
		if err := os.Remove(filepath.Join(dir, filepath.FromSlash(rel))); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return nil, errs.Wrap(errs.Internal, folderRemoveVerb+": remove "+rel, err)
		}
		return []string{rel}, nil
	}
	if err := os.RemoveAll(filepath.Join(dir, filepath.FromSlash(folderName))); err != nil {
		return nil, errs.Wrap(errs.Internal, folderRemoveVerb+": remove "+folderName, err)
	}
	return []string{folderName}, nil
}

// commitStoreEdit runs edit on the store's local folder and saves it: a
// folder store is the folder itself; a git store's edit is committed as
// one commit (the org identity signs an org store's) and pushed per the
// store's push mode: now at once; batch and manual leave it for the next
// push. pushed is true when it reached the remote now; note says
// otherwise why not.
func (a *App) commitStoreEdit(ctx context.Context, src attach.Store, name, subject string, edit func(dir string) ([]string, error)) (pushed bool, note string, err error) {
	canon, err := canonical(attach.Store{URL: src.URL, Branch: src.Branch})
	if err != nil {
		return false, "", err
	}
	u, err := url.Parse(canon)
	if err != nil {
		return false, "", errs.New(errs.StoreScheme, "wire: the store url does not parse")
	}
	if u.Scheme == folder.Scheme {
		_, err = edit(fileuri.OSPath(u.Path))
		return false, "", err
	}
	gb, done, err := a.openGitStore(ctx, src)
	if err != nil {
		return false, "", err
	}
	defer done()
	paths, err := edit(gb.Dir())
	if err != nil || len(paths) == 0 {
		return false, "", err
	}
	if err = gitCommitFilesAs(ctx, gb.Dir(), a.opts.Wall(), storeIdentityFunc(a.opts), subject, paths...); err != nil {
		return false, "", err
	}
	place, _ := a.removePlace(src, name, gb.Dir())
	if mode := a.storeSettings(gb, place).Push; mode != storepush.ModeNow {
		return false, "committed in this machine's copy of the store; push " + string(mode) + " sends it with the store's next push (`lore sync` sends it now)", nil
	}
	return a.pushStoreClone(ctx, gb, subject)
}

// pushStoreClone syncs a store clone after a commit lore made (as
// withStoreDir does): a refusal by the remote is an error; offline keeps
// the commit for the next push.
func (a *App) pushStoreClone(ctx context.Context, gb *git.Backend, subject string) (bool, string, error) {
	rep, err := gb.Sync(ctx)
	if err != nil {
		return false, "", err
	}
	if len(rep.Refused) > 0 {
		return false, "", errs.New(errs.PolicyDenied, subject+": the remote refused the commit: "+rep.Refused[0].Reason)
	}
	if rep.Offline {
		return false, "committed in this machine's copy of the store; the remote is unreachable, so the next command or `lore sync` pushes it", nil
	}
	writeStamp(filepath.Dir(gb.Dir()), pushStampFile, a.opts.Wall())
	return true, "", nil
}

// trashFolderRows moves every live row of the folder into the store's
// trash under one new bat_ (each a soft delete through the engine: its
// cascade dependents join the batch, a restrict dependent is deleted
// first). The folder is opened alone, without the push wrap, so the
// deletes are local commits that the removal's commit pushes once.
func (a *App) trashFolderRows(ctx context.Context, src attach.Store, name, folderName, reason string) (string, error) {
	_, org := a.removePlace(src, name, "")
	x, err := a.openFolderApp(ctx, org, attach.Store{Name: folderRemoveSource, URL: src.URL, Branch: src.Branch, Folder: folderName})
	if err != nil {
		return "", err
	}
	defer func() { _ = x.Close() }()
	st, err := x.state(ctx)
	if err != nil {
		return "", err
	}
	bid, err := ids.New[ids.Batch]()
	if err != nil {
		return "", errs.Wrap(errs.Internal, folderRemoveVerb+": new batch id", err)
	}
	// the person confirmed the purge naming every row it moves (the op's
	// --confirm), so a cascade's group question is not asked again
	dctx := engine.WithBatch(engine.WithDeleteReason(engine.WithConfirmed(ctx), reason), engine.Batch{ID: bid, Kind: engine.BatchKindDelete})
	pending, err := liveRowsOf(ctx, st.byName[folderRemoveSource])
	if err != nil {
		return "", err
	}
	// every row as ONE delete: one plan (a row of the folder never blocks
	// another: they all go), one store commit (owner report 2026-11-12: a
	// delete per row took over five minutes for 1,000 rows)
	refs := make([]engine.Ref, 0, len(pending))
	for _, r := range pending {
		refs = append(refs, engine.Ref{Store: folderRemoveSource, Table: r.Table, ID: r.ID})
	}
	if _, err := st.eng.TrashMany(store.WithCommitTitle(dctx, folderRemoveVerb+" --purge: the folder's rows to the trash"), refs); err != nil {
		return "", errs.Prefix(folderRemoveVerb+": the folder's rows could not go to the trash (nothing was moved)", err)
	}
	return bid.String(), nil
}

// liveRowsOf is every live row of b (rowKind), in the order read.
func liveRowsOf(ctx context.Context, b store.Backend) ([]store.Row, error) {
	if b == nil {
		return nil, errs.New(errs.Internal, folderRemoveVerb+": the folder is not open")
	}
	rows, _, err := b.Read(ctx, "")
	if err != nil {
		return nil, err
	}
	var out []store.Row
	for _, r := range rows {
		live, _, kerr := rowKind(r)
		if kerr != nil {
			return nil, kerr
		}
		if live {
			out = append(out, r)
		}
	}
	return out, nil
}

// openFolderApp is an App over one store folder and the machine store
// (openDetached), with no push wrap: an org store's writes are signed as
// the org (openOrgFolderApp), any other store's as this machine's
// identity. The caller closes it.
func (a *App) openFolderApp(ctx context.Context, org string, one attach.Store) (*App, error) {
	opts := a.opts
	opts.WrapBackend = nil
	if org != "" {
		return openOrgFolderApp(ctx, opts, org, one)
	}
	x, err := New(detachedOpts(opts))
	if err != nil {
		return nil, err
	}
	st, err := openDetached(ctx, x.opts, &one)
	if err != nil {
		return nil, err
	}
	st.eng.SetRedactHook(x.redactTask)
	x.mu.Lock()
	x.opened, x.st = true, st
	x.cfgOpened = true
	x.cfgErr = errs.New(errs.NotAStore, "wire: only one store folder is open here").WithHint("run it inside a git repository")
	x.mu.Unlock()
	return x, nil
}
