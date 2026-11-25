package wire

// foldercarry.go — `lore move --from <org>/<folder> --to <store>/<folder>`
// carries a whole folder into another store of its org under the same
// name (devco D-992, bug hunt H-260: a client folder could not move into
// the client's own store. The target resolved by folder name to the
// folder it was leaving, "same store; nothing to move", and `store folder
// add` on the new store refused the name, since folder names are unique
// across an org's stores).
//
//	--to <store>/<folder>, the store lacks the folder, another store of
//	the org holds it, and --from names that same folder
//	  ─▶ the target stays <store>/<folder> (no redirect to the folder it
//	     leaves) and may be created: the name moves, so it stays unique
//	  ─▶ the rows and the folder's own bookkeeping move (opsrow)
//	  ─▶ FinishFolderCarry: the folder made in <store>, the repo links of
//	     the old _meta.json added to the new one, the old folder removed
//	     as `store folder remove --purge` removes it (what is left goes
//	     to the trash under one batch)
//
// Any other move is unchanged (foldertarget.go still sends a
// `<store>/<folder>` write to the org store holding the folder).

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/engine/attach"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/op"
	opsrow "github.com/thesatellite-ai/lore/ops/row"
	opsstores "github.com/thesatellite-ai/lore/ops/stores"
	"github.com/thesatellite-ai/lore/store"
)

var _ opsrow.FolderCarrier = (*App)(nil)

// folderCarrySubject is the commit subject of the folder made in the new
// store.
const folderCarrySubject = "lore: move folder "

// folderCarry is a bulk move that carries a whole folder (see the file
// comment): the folder, the org store holding it now, and the target
// store and the name the move writes to.
type folderCarry struct {
	folder string
	from   orgFolder
	to     attach.Store
	toName store.Source
}

// folderCarryKey carries a folderCarry through the move's context.
type folderCarryKey struct{}

// carryOf is the folderCarry ctx carries for target name ("" any).
func carryOf(ctx context.Context, name store.Source) (folderCarry, bool) {
	fc, ok := ctx.Value(folderCarryKey{}).(folderCarry)
	if !ok || (name != "" && fc.toName != name) {
		return folderCarry{}, false
	}
	return fc, true
}

// folderCarryOf reports whether the op is a bulk move that carries a
// folder into another store of its org (see the file comment).
func (a *App) folderCarryOf(ctx context.Context, e op.Entry, d op.Decoder) (folderCarry, bool) {
	from := bulkMoveSource(e, d)
	if from == "" {
		return folderCarry{}, false
	}
	in, err := e.Decode(d)
	if err != nil {
		return folderCarry{}, false
	}
	_, to := typedTarget(in)
	base, folderName, ok := strings.Cut(strings.TrimSpace(to), targetSep)
	if !ok || base == "" || folderName == "" || folderName == rowfs.FolderRoot || base == attach.ReservedMe {
		return folderCarry{}, false
	}
	fromBase, fromFolder, cut := strings.Cut(from, targetSep)
	if !cut {
		fromBase, fromFolder = "", from // a folder named alone
	}
	if fromFolder != folderName {
		return folderCarry{}, false
	}
	src, found, _, err := a.baseStore(ctx, base)
	if err != nil || !found {
		return folderCarry{}, false
	}
	f, ok := a.otherOrgStoreFolder(ctx, src, folderName)
	if !ok || !a.namesOrgFolder(ctx, fromBase, f) {
		return folderCarry{}, false
	}
	return folderCarry{folder: folderName, from: f, to: src, toName: store.Source(base + targetSep + folderName)}, true
}

// namesOrgFolder reports whether base (a --from store, "" for a folder
// named alone) reaches folder f: the store holding it, or a store of the
// same org that foldertarget.go sends on to it.
func (a *App) namesOrgFolder(ctx context.Context, base string, f orgFolder) bool {
	if base == "" {
		return true // a bare folder name is the one folder of the org
	}
	src, found, _, err := a.baseStore(ctx, base)
	if err != nil || !found {
		return false
	}
	if k, ok := storeKey(src.URL); ok {
		if fk, fok := storeKey(f.URL); fok && k == fk {
			return true
		}
	}
	g, ok := a.otherOrgStoreFolder(ctx, src, f.Folder)
	return ok && g.URL == f.URL
}

// FolderCarry implements opsrow.FolderCarrier: the carry this move's
// context holds, as the move prints it.
func (a *App) FolderCarry(ctx context.Context) (opsrow.FolderCarry, bool) {
	fc, ok := carryOf(ctx, "")
	if !ok {
		return opsrow.FolderCarry{}, false
	}
	out := opsrow.FolderCarry{Folder: fc.folder, From: fc.from.Label + targetSep + fc.folder, To: string(fc.toName)}
	if dir, err := a.storeDir(ctx, a.carrySource(fc)); err == nil {
		if links, lerr := folderMetaLinks(dir, fc.folder); lerr == nil {
			out.Links = len(links)
		}
	}
	return out, true
}

// carrySource is the org store the carried folder leaves, as the store
// commands address it (by its url).
func (a *App) carrySource(fc folderCarry) attach.Store {
	return attach.Store{Name: store.Source(fc.from.URL), URL: fc.from.URL, Branch: fc.from.Branch}
}

// FinishFolderCarry implements opsrow.FolderCarrier: after the rows moved,
// the folder is made in the new store (if no row write made it), the old
// folder's repo links are added to its _meta.json, and the old folder is
// removed (what is left of it to the trash under one batch).
func (a *App) FinishFolderCarry(ctx context.Context) (opsrow.FolderCarry, error) {
	out, ok := a.FolderCarry(ctx)
	fc, _ := carryOf(ctx, "")
	if !ok {
		return opsrow.FolderCarry{}, errs.New(errs.Internal, "move: no folder to carry")
	}
	from := a.carrySource(fc)
	fromDir, err := a.storeDir(ctx, from)
	if err != nil {
		return out, err
	}
	links, err := folderMetaLinks(fromDir, fc.folder)
	if err != nil {
		return out, err
	}
	to := attach.Store{Name: fc.to.Name, URL: fc.to.URL, Branch: fc.to.Branch}
	err = a.withStoreDir(ctx, to, folderCarrySubject+fc.folder, func(dir string) (paths []string, ferr error) {
		root, oerr := rowfs.Open(dir, a.opts.storeRowfsOptions(dir)...)
		if oerr != nil {
			return nil, oerr
		}
		f, oerr := root.Folder(fc.folder)
		if oerr != nil {
			return nil, oerr
		}
		f.SetFloorSource(func(context.Context) string { return orgFloorForStore(a.opts.Home, to.URL) })
		lock, oerr := root.Lock(ctx)
		if oerr != nil {
			return nil, oerr
		}
		defer func() { ferr = errors.Join(ferr, lock.Release()) }()
		if paths, ferr = f.Create(ctx); ferr != nil {
			return nil, ferr
		}
		rel := f.Layout().StorePath(fc.folder, rowfs.MetaFileName)
		added, aerr := addMetaLinks(filepath.Join(dir, filepath.FromSlash(rel)), links)
		if aerr != nil {
			return nil, aerr
		}
		if added && !slices.Contains(paths, rel) {
			paths = append(paths, rel)
		}
		return paths, nil
	})
	if err != nil {
		return out, errs.Prefix("move: make folder "+fc.folder+" in "+string(fc.toName), err)
	}
	name := fc.from.URL
	c, err := a.FolderContents(ctx, name, fc.folder)
	if err != nil {
		return out, errs.Prefix("move: the rows are in "+string(fc.toName)+" but "+out.From+" is still there", err)
	}
	rm, err := a.RemoveFolder(ctx, opsstores.FolderRemoveIn{Store: name, Folder: fc.folder, Purge: true, Reason: "moved to " + string(fc.toName)}, c)
	if err != nil {
		return out, errs.Prefix("move: the rows are in "+string(fc.toName)+" but "+out.From+" is still there", err).
			WithHint("remove it with `lore store folder remove --store " + name + " --folder " + fc.folder + " --purge --confirm`")
	}
	out.Removed, out.Batch = true, rm.Batch
	return out, nil
}

// folderMetaLinks reads the repo links of a folder's _meta.json in a store
// checkout (none when the folder or the file is missing).
func folderMetaLinks(dir, folderName string) ([]json.RawMessage, error) {
	for _, l := range []rowfs.Layout{rowfs.LayoutStore, rowfs.LayoutRepo} {
		raw, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(l.StorePath(folderName, rowfs.MetaFileName))))
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, errs.Wrap(errs.Internal, "move: read "+folderName+"'s "+rowfs.MetaFileName, err)
		}
		var doc map[string]json.RawMessage
		if err = json.Unmarshal(raw, &doc); err != nil {
			return nil, errs.Wrap(errs.Internal, "move: "+folderName+"'s "+rowfs.MetaFileName, err)
		}
		var links []json.RawMessage
		if l, ok := doc[metaLinksField]; ok && string(l) != "null" {
			if err = json.Unmarshal(l, &links); err != nil {
				return nil, errs.Wrap(errs.Internal, "move: "+folderName+"'s "+rowfs.MetaFileName+" links", err)
			}
		}
		return links, nil
	}
	return nil, nil
}

// addMetaLinks adds links to the _meta.json at path (each once, by its
// json text); added is false when every link was there already.
func addMetaLinks(path string, links []json.RawMessage) (bool, error) {
	if len(links) == 0 {
		return false, nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return false, errs.Wrap(errs.Internal, "move: read the new folder's "+rowfs.MetaFileName, err)
	}
	var doc map[string]json.RawMessage
	if err = json.Unmarshal(raw, &doc); err != nil {
		return false, errs.Wrap(errs.Internal, "move: the new folder's "+rowfs.MetaFileName, err)
	}
	var have []json.RawMessage
	if l, ok := doc[metaLinksField]; ok && string(l) != "null" {
		if err = json.Unmarshal(l, &have); err != nil {
			return false, errs.Wrap(errs.Internal, "move: the new folder's "+rowfs.MetaFileName+" links", err)
		}
	}
	seen := map[string]bool{}
	for _, l := range have {
		seen[compactJSON(l)] = true
	}
	added := false
	for _, l := range links {
		if k := compactJSON(l); !seen[k] {
			seen[k], added = true, true
			have = append(have, l)
		}
	}
	if !added {
		return false, nil
	}
	if doc[metaLinksField], err = json.Marshal(have); err != nil {
		return false, errs.Wrap(errs.Internal, "move: links", err)
	}
	b, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return false, errs.Wrap(errs.Internal, "move: "+rowfs.MetaFileName, err)
	}
	if err = os.WriteFile(path, append(b, '\n'), 0o644); err != nil {
		return false, errs.Wrap(errs.Internal, "move: write the new folder's "+rowfs.MetaFileName, err)
	}
	return true, nil
}

// compactJSON is raw without insignificant white space (raw itself when it
// does not parse), so one link written twice compares equal.
func compactJSON(raw json.RawMessage) string {
	var b bytes.Buffer
	if err := json.Compact(&b, raw); err != nil {
		return string(raw)
	}
	return b.String()
}
