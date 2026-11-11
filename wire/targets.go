package wire

import (
	"context"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/engine/attach"
	"github.com/thesatellite-ai/lore/op"
	"github.com/thesatellite-ai/lore/store"
)

// targetSep splits `--to <store>/<folder>` (STORE TARGETS).
const targetSep = "/"

// prepareTarget looks at the operation's `to` before the engine runs it:
// `--to <store>/<folder>` attaches that folder of an attached store (or of
// `me`) as its own target, named "<store>/<folder>" (O1: one backend per
// data folder; `--to agency/org` is the company folder). A folder the store
// does not have yet is created only with --create or a yes; otherwise the
// write is refused naming the folders the store has (E_NEEDS_CONFIRM,
// PL-CREATE-1, targets_create.go).
// `team` and `company` were already rewritten to the org store names
// (resolveAlias).
//
// `<store>/.` names the store root (rowfs.FolderRoot), which always exists
// (it holds the store marker), so it never asks. When <store> itself is
// attached at its root, `<store>/.` is that same place and the input is
// rewritten to plain `<store>` (the returned decoder), so one data folder
// keeps one name and nothing new is recorded in stores.yaml.
//
// A target named outside a `to` field (op.TargetNamer: mission apply's plan
// file) gets the same rewrite through the context (op.WithTargetAlias),
// which the operation reads with op.TargetAlias (BUG-T8: in a client repo
// `agency/client2` is the org store attached at its linked folder, named
// `agency`).
func (a *App) prepareTarget(ctx context.Context, e op.Entry, d op.Decoder, c op.Caller) (context.Context, op.Decoder, error) {
	if err := a.checkMeTarget(ctx, e, d); err != nil {
		return ctx, d, err
	}
	from, root, created, err := a.attachFolderTarget(ctx, e, d, c)
	if err != nil {
		return ctx, d, err
	}
	if created.target != "" {
		ctx = context.WithValue(ctx, newFolderKey{}, created)
	}
	if root != "" {
		return op.WithTargetAlias(ctx, from, root), aliasDecoder{inner: d, from: from, to: root}, nil
	}
	return ctx, d, nil
}

// attachFolderTarget is prepareTarget's attach step (see there). A
// non-empty root means the input's to (from) is `<root>/.` and store root
// is attached at its root: the caller rewrites from to root.
func (a *App) attachFolderTarget(ctx context.Context, e op.Entry, d op.Decoder, c op.Caller) (from, root string, created newFolder, err error) {
	in, err := e.Decode(d)
	if err != nil {
		return "", "", newFolder{}, nil // the engine's Run reports the decode error itself
	}
	to := ""
	for _, tv := range in.Texts() {
		if tv.Field == engine.InputTarget {
			to = tv.Text
		}
	}
	if to == "" {
		// mission apply names its target in the plan file (BUG-T8)
		to, _ = in.NamedTarget()
	}
	base, folderName, ok := strings.Cut(to, targetSep)
	if !ok || base == "" || folderName == "" {
		return "", "", newFolder{}, nil
	}
	name := store.Source(to)
	// Every write to a store folder makes sure the folder has its project
	// row (ensureFolderProject; a folder an older lore or `store folder
	// add` made has none until its next write). The store root is no
	// project folder.
	this := newFolder{}
	if folderName != rowfs.FolderRoot {
		this = newFolder{target: name, folder: folderName}
	}
	a.mu.Lock()
	for _, x := range a.extra {
		if x.Name == name {
			a.mu.Unlock()
			return "", "", this, nil
		}
	}
	a.mu.Unlock()
	recorded, err := a.recordedTarget(ctx, name)
	if err != nil {
		return "", "", newFolder{}, err
	}
	if recorded {
		return "", "", this, nil // a recorded target is attached from stores.yaml
	}
	for _, s := range a.orgStoresHere(ctx) {
		if s.Name == name {
			return "", "", this, nil // the org brings it (the company folder): attached at open
		}
	}
	src, found, fromOrg, err := a.baseStore(ctx, base)
	if err != nil || !found {
		return "", "", newFolder{}, err // not found: the engine answers "no store named … is attached"
	}
	isRoot := folderName == rowfs.FolderRoot
	if isRoot && (src.Folder == "" || src.Folder == rowfs.FolderRoot) {
		return to, base, newFolder{}, nil
	}
	if same, ok, cerr := a.coveringAttachment(ctx, src, folderName); cerr != nil || ok {
		// One store folder keeps one name: an org store attached at its
		// linked folder ("northwind" at globex) is what
		// `northwind/globex` names, so the input is rewritten to it and
		// nothing is attached twice (a second name over the same folder
		// made every row of it E_AMBIGUOUS_ID).
		if cerr != nil {
			return "", "", newFolder{}, cerr
		}
		return to, string(same), newFolder{target: same, folder: folderName}, nil
	}
	exists := isRoot // the root holds the marker: it always exists
	if !exists {
		if exists, err = a.folderExists(ctx, src, folderName); err != nil {
			return "", "", newFolder{}, err
		}
	}
	if !exists && !c.Yes && !createAsked(in) {
		return "", "", newFolder{}, a.missingFolder(ctx, src, base, folderName)
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
			return "", "", newFolder{}, terr
		}
		if err := addStoresEntry(top, target); err != nil {
			return "", "", newFolder{}, err
		}
	}
	if !persist {
		unattachedNote(os.Stderr, to, folderName, fromOrg)
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
			return "", "", newFolder{}, cerr
		}
	} else if a.opened {
		a.opened, a.err = false, nil
	}
	return "", "", this, nil
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
		p := effectivePersonalURL(ctx, a.opts.Home, a.projectDir(), mc) // ONBOARDING 3.4
		return attach.Store{Name: store.Source(name), URL: p}, p != "", false, nil
	}
	top, err := a.storeRepoTop(ctx) // "" outside a repo: no stores.yaml, the org's stores still count
	if err != nil {
		return attach.Store{}, false, false, err
	}
	if top != "" {
		cfg, cerr := readStoresFile(top)
		if cerr != nil {
			return attach.Store{}, false, false, cerr
		}
		for _, s := range cfg.Stores {
			if string(s.Name) == name {
				return s, true, false, nil
			}
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

// unattachedNote tells the person that a write to `--to <store>/<folder>`
// lands in a folder this repo does not attach (me/<folder>, or an org
// store's folder, which is never recorded in stores.yaml), so this repo's
// lists will not show the row (STORE TARGETS: a folder is shown only in
// repos that attach it). INTEG-KNOWLEDGE-7: before it, the row seemed to
// vanish. It goes to stderr so --json output stays one JSON document.
//
// For an org store's folder (fromOrg) the command carries --yes and says
// why: store add asks before writing an org store's url into the committed
// stores.yaml (INTEG-CFG7-1), so the note names the confirmed form rather
// than a command that answers E_NEEDS_CONFIRM.
func unattachedNote(w io.Writer, to, folderName string, fromOrg bool) {
	if fromOrg {
		fmt.Fprintf(w, "lore: %s is not attached in this repo, so lists here will not show its rows; to see them: lore store add <name> <store url> --folder %s --yes (this commits the org store's url to .lore/stores.yaml, visible to everyone who can read this repo)\n", to, folderName)
		return
	}
	fmt.Fprintf(w, "lore: %s is not attached in this repo, so lists here will not show its rows; to see them: lore store add <name> <store url> --folder %s\n", to, folderName)
}

// newFolder is the store folder a `--to <store>/<folder>` write goes to
// (target is its attached name): after the write, Run makes sure the
// folder has its project row (ensureFolderProject), creating it on the
// folder's first write or on the next write to a folder made without one.
type newFolder struct {
	target store.Source
	folder string
}

// newFolderKey carries the newFolder of a write through its context.
type newFolderKey struct{}

// ensureFolderProject gives the folder a write just went to its project
// row when it has none (a list of the folder's projects, no write when the
// row is there), as the org folder gets one at its first roster write: the row
// every clone agrees on (id from the folder's _meta.json, name = the
// folder), so `lore project list` lists the folder and `--project
// <folder>` (team invite, memberships) finds it. A failure is a notice:
// the row the person wrote is already saved.
func (a *App) ensureFolderProject(ctx context.Context) {
	nf, ok := ctx.Value(newFolderKey{}).(newFolder)
	if !ok {
		return
	}
	st, err := a.state(ctx)
	if err == nil {
		_, err = a.rosterProject(ctx, st, nf.target, nf.folder)
	}
	if err != nil && a.opts.Notice != nil {
		a.opts.Notice("the folder " + string(nf.target) + " has no project row yet (" + err.Error() + "); its rows are saved")
	}
}
