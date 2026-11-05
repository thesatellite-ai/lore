package wire

import (
	"context"
	"slices"
	"strings"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/engine/attach"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/op"
	"github.com/thesatellite-ai/lore/store"
)

// targetSep splits `--to <store>/<folder>` (STORE TARGETS).
const targetSep = "/"

// prepareTarget looks at the operation's `to` before the engine runs it:
// `--to <store>/<folder>` attaches that folder of an attached store (or of
// `me`) as its own target, named "<store>/<folder>" (O1: one backend per
// data folder; `--to agency/org` is the company folder). A folder the store
// does not have yet is created only after a yes (E_NEEDS_CONFIRM).
// `team` and `company` were already rewritten to the org store names
// (resolveAlias).
//
// `<store>/.` names the store root (rowfs.FolderRoot), which always exists
// (it holds the store marker), so it never asks. When <store> itself is
// attached at its root, `<store>/.` is that same place and the input is
// rewritten to plain `<store>` (the returned decoder), so one data folder
// keeps one name and nothing new is recorded in stores.yaml.
func (a *App) prepareTarget(ctx context.Context, e op.Entry, d op.Decoder, c op.Caller) (op.Decoder, error) {
	from, root, err := a.attachFolderTarget(ctx, e, d, c)
	if err != nil {
		return d, err
	}
	if root != "" {
		return aliasDecoder{inner: d, from: from, to: root}, nil
	}
	return d, nil
}

// attachFolderTarget is prepareTarget's attach step (see there). A
// non-empty root means the input's to (from) is `<root>/.` and store root
// is attached at its root: the caller rewrites from to root.
func (a *App) attachFolderTarget(ctx context.Context, e op.Entry, d op.Decoder, c op.Caller) (from, root string, err error) {
	in, err := e.Decode(d)
	if err != nil {
		return "", "", nil // the engine's Run reports the decode error itself
	}
	to := ""
	for _, tv := range in.Texts() {
		if tv.Field == engine.InputTarget {
			to = tv.Text
		}
	}
	named := false
	if to == "" {
		// mission apply names its target in the plan file (BUG-T8)
		to, named = in.NamedTarget()
	}
	base, folderName, ok := strings.Cut(to, targetSep)
	if !ok || base == "" || folderName == "" {
		return "", "", nil
	}
	if named && folderName == rowfs.FolderRoot {
		return "", "", nil // `<store>/.` is rewritten in a to field only; the run answers it
	}
	name := store.Source(to)
	a.mu.Lock()
	for _, x := range a.extra {
		if x.Name == name {
			a.mu.Unlock()
			return "", "", nil
		}
	}
	a.mu.Unlock()
	recorded, err := a.recordedTarget(ctx, name)
	if err != nil || recorded {
		return "", "", err // a recorded target is attached from stores.yaml
	}
	for _, s := range a.orgStoresHere(ctx) {
		if s.Name == name {
			return "", "", nil // the org brings it (the company folder): attached at open
		}
	}
	src, found, fromOrg, err := a.baseStore(ctx, base)
	if err != nil || !found {
		return "", "", err // not found: the engine answers "no store named … is attached"
	}
	isRoot := folderName == rowfs.FolderRoot
	if isRoot && (src.Folder == "" || src.Folder == rowfs.FolderRoot) {
		return to, base, nil
	}
	if same, ok, cerr := a.coveringAttachment(ctx, src, folderName); cerr != nil || ok {
		// One store folder keeps one name: an org store attached at its
		// linked folder ("northwind" at globex) is what
		// `northwind/globex` names, so the input is rewritten to it and
		// nothing is attached twice (a second name over the same folder
		// made every row of it E_AMBIGUOUS_ID).
		return to, string(same), cerr
	}
	exists := isRoot // the root holds the marker: it always exists
	if !exists {
		if exists, err = a.folderExists(ctx, src, folderName); err != nil {
			return "", "", err
		}
	}
	if !exists && !c.Yes {
		return "", "", errs.New(errs.NeedsConfirm, "store "+base+" has no folder "+folderName+" yet").
			WithQuestion("create the folder " + folderName + " in store " + base + "?").
			WithHint("rerun with --yes to create it with this write")
	}
	target := attach.Store{Name: name, URL: src.URL, Folder: folderName, ReadOnly: src.ReadOnly, Branch: src.Branch}
	// `me/<folder>` and an org store's folder are not recorded: the
	// personal url is machine-local, and an org store reached through the
	// org link must never have its url written into the repo (CFG7).
	persist := base != attach.ReservedMe && !fromOrg
	if persist {
		// A confirmed folder target is recorded in stores.yaml (spec 7.1
		// `folder:` key, entry named "<store>/<folder>"), so later reads
		// attach it and list its rows. `me/<folder>` is not: the personal
		// store's url is machine-local and stores.yaml is shared.
		top, terr := a.repoTop(ctx)
		if terr != nil {
			return "", "", terr
		}
		if err := addStoresEntry(top, target); err != nil {
			return "", "", err
		}
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if !persist {
		a.extra = append(a.extra, target)
	}
	if a.st != nil { // already open (a long-lived `lore api`): reopen with the new target
		cerr := a.st.close()
		a.st, a.opened, a.err = nil, false, nil
		if cerr != nil {
			return "", "", cerr
		}
	} else if a.opened {
		a.opened, a.err = false, nil
	}
	return "", "", nil
}

// coveringAttachment finds an attachment that already covers folder
// folderName of src's store (same canonical url and branch, same folder):
// a stores.yaml entry, a store the org brings, or an earlier unrecorded
// folder target. ok is false when none does.
func (a *App) coveringAttachment(ctx context.Context, src attach.Store, folderName string) (store.Source, bool, error) {
	want, err := attachLocation(src, folderName)
	if err != nil {
		return "", false, nil // an unparsable url covers nothing; the open reports it
	}
	top, err := a.repoTop(ctx)
	if err != nil {
		return "", false, err
	}
	cfg, err := readStoresFile(top)
	if err != nil {
		return "", false, err
	}
	a.mu.Lock()
	extra := slices.Clone(a.extra)
	a.mu.Unlock()
	for _, s := range slices.Concat(cfg.Stores, a.orgStoresHere(ctx), extra) {
		if got, lerr := attachLocation(s, s.Folder); lerr == nil && got == want {
			return s.Name, true, nil
		}
	}
	return "", false, nil
}

// attachLocation is the engine.Attached.Location store s would have when
// attached at folder f.
func attachLocation(s attach.Store, f string) (string, error) {
	u, err := canonical(attach.Store{URL: s.URL, Branch: s.Branch})
	if err != nil {
		return "", err
	}
	return location(u, f), nil
}

// baseStore finds the stores.yaml entry (or `me`, or a store the repo's
// org brings: Options.OrgStores) a folder target names; fromOrg marks the
// last kind, whose url must never be written into the repo.
func (a *App) baseStore(ctx context.Context, name string) (src attach.Store, found, fromOrg bool, err error) {
	if name == attach.ReservedMe {
		mc, cerr := readMachineConfig(a.opts.Home)
		if cerr != nil {
			return attach.Store{}, false, false, cerr
		}
		p := mc.personalURL()
		return attach.Store{Name: store.Source(name), URL: p}, p != "", false, nil
	}
	top, err := a.repoTop(ctx)
	if err != nil {
		return attach.Store{}, false, false, err
	}
	cfg, err := readStoresFile(top)
	if err != nil {
		return attach.Store{}, false, false, err
	}
	for _, s := range cfg.Stores {
		if string(s.Name) == name {
			return s, true, false, nil
		}
	}
	for _, s := range a.orgStoresHere(ctx) {
		if string(s.Name) == name {
			return s, true, true, nil
		}
	}
	return attach.Store{}, false, false, nil
}

// recordedTarget reports whether stores.yaml already lists the folder
// target name (recorded by an earlier confirmed write).
func (a *App) recordedTarget(ctx context.Context, name store.Source) (bool, error) {
	top, err := a.repoTop(ctx)
	if err != nil {
		return false, err
	}
	cfg, err := readStoresFile(top)
	if err != nil {
		return false, err
	}
	for _, s := range cfg.Stores {
		if s.Name == name {
			return true, nil
		}
	}
	return false, nil
}

// orgStoresHere is orgStores for this App's repo (an unknown top renames
// nothing).
func (a *App) orgStoresHere(ctx context.Context) []attach.Store {
	top, err := a.repoTop(ctx)
	if err != nil {
		top = ""
	}
	return orgStores(ctx, a.opts, top)
}
