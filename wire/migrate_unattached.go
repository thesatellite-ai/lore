package wire

// migrate_unattached.go — the folders of an attached store that this repo
// does not attach (devco P-9).
//
// A 0.1.17 `--to <org>/<folder> --create` made a folder in the org store
// that the repo never attached; after the switch to 0.1.18, migrate run
// rewrote only the attached folders, so that folder's rows stayed `_v: 1`
// for good while migrate status, doctor and lore fix all said current:
// they read only attached stores too. A store clone is converted as a
// whole (its marker and its layout commit), so its rows are too:
//
//	attached backends ─▶ store clones / folder stores (repo store skipped)
//	                  ─▶ every folder of each (rowfs.StoreFolders + root)
//	                  ─▶ the ones no entry attaches here
//	status:  count their v1 rows offline (rowfs read), one pending line
//	         per folder naming `lore migrate run`
//	run:     open each as a store folder and rewrite its v1 rows (one
//	         commit per folder in the clone; Finish pushes the clone)
//
// The rows of an unattached folder are not shown by reads here whatever
// their format: attaching decides that (`lore store add --folder`, which
// the write path names). What this file guarantees is that no v1 row of a
// store the run converts is left behind, and that every one left is named.

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/thesatellite-ai/lore/adapters/backend/folder"
	gitbackend "github.com/thesatellite-ai/lore/adapters/backend/git"
	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/engine/attach"
	"github.com/thesatellite-ai/lore/logic/fileuri"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/fspath"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	opsmigrate "github.com/thesatellite-ai/lore/ops/migrate"
	"github.com/thesatellite-ai/lore/store"
)

// unattachedFolder is one folder of an attached store that no entry of
// this repo attaches.
type unattachedFolder struct {
	// place names it as status and the run print it: "<store>/<folder>"
	// (the store's attached name without its folder part).
	place string
	// dir is the store's local checkout; src addresses the folder (url,
	// branch, folder) for the open that rewrites it.
	dir string
	src attach.Store
	// readOnly: every entry of the store is read_only here; only its
	// owner converts it.
	readOnly bool
}

// unattachedTag marks a pending line of an unattached folder: doctor
// tells such lines apart from the repo's own (doctor_rowformat.go).
const unattachedTag = "a folder of the store this repo does not attach"

// unattachedV1Line is the pending line of one unattached folder holding
// v1 rows (status, doctor, lore fix).
const unattachedV1Line = "%s (" + unattachedTag + "): %s at v1; lore migrate run upgrades them (to read them here: lore store add --name <name> --url %s --folder %s --yes)"

// unattachedV1ReadOnlyLine is unattachedV1Line for a store attached read_only.
const unattachedV1ReadOnlyLine = "%s (a folder of a read_only store this repo does not attach): %s at v1; only the store's owner converts them (lore migrate run where it is attached writable)"

// storePlaceOf is the url, branch and local checkout of an attached store
// backend that is a store clone or a folder store; ok is false for any
// other backend (the repo store, a memory store in tests). A clone's
// branch is "" when the entry pins none: Branch() names the resolved
// default branch, and a url pinned to it is another clone (<url>@main),
// so the spelling whose clone folder is this checkout is the one kept
// (home is the lore home the clone record lives in).
func storePlaceOf(home string, b store.Backend) (rawURL, branch, dir string, ok bool) {
	switch c := concrete(b).(type) {
	case *gitbackend.Backend:
		if !c.CloneMode() || c.RemoteURL() == "" {
			return "", "", "", false
		}
		for _, br := range []string{"", c.Branch()} {
			if d, _, err := gitbackend.CloneDirOf(home, c.RemoteURL(), br); err == nil && fspath.Same(d, c.Dir()) {
				return c.RemoteURL(), br, c.Dir(), true
			}
		}
		return "", "", "", false // a clone lore cannot address again: not swept
	case *folder.Backend:
		return fileuri.URL(c.Dir(), ""), "", c.Dir(), true
	}
	return "", "", "", false
}

// unattachedFolders lists the folders of every store st attaches (the
// repo store excepted) that no entry attaches here, by place. The
// v0.1.17 local personal store's folders are left out: its flatten step
// (personal_store_flatten) moves them.
func (a *App) unattachedFolders(st *state) ([]unattachedFolder, error) {
	skipMe := false
	if mc, err := readMachineConfig(a.opts.Home); err == nil {
		skipMe = mc.localPersonalV017(a.opts.Home) != ""
	}
	type clone struct {
		name, url, branch, dir string
		readOnly               bool
	}
	attached := map[string]bool{} // dir NUL folder
	byDir := map[string]*clone{}
	var order []string
	names := st.eng.Stores()
	for i, b := range st.backends {
		name := names[i]
		base, _, _ := strings.Cut(string(name), targetSep)
		if name == store.SourceRepo || (skipMe && base == attach.ReservedMe) {
			continue
		}
		u, branch, dir, ok := storePlaceOf(a.opts.Home, b)
		if !ok {
			continue
		}
		f := rootFolder(st.folders[name])
		if f == "" {
			f = rowfs.FolderRoot
		}
		attached[dir+"\x00"+f] = true
		ro := st.eng.ReadOnly(name)
		if c, seen := byDir[dir]; seen {
			c.readOnly = c.readOnly && ro // one writable entry makes it writable
			continue
		}
		byDir[dir] = &clone{name: base, url: u, branch: branch, dir: dir, readOnly: ro}
		order = append(order, dir)
	}
	var out []unattachedFolder
	for _, dir := range order {
		c := byDir[dir]
		folders, err := rowfs.StoreFolders(dir)
		if err != nil {
			return nil, errs.Wrap(errs.Internal, "migrate: list the folders of store "+c.name, err)
		}
		for _, f := range append([]string{rowfs.FolderRoot}, folders...) {
			if attached[dir+"\x00"+f] {
				continue
			}
			place := c.name + targetSep + f
			if f == rowfs.FolderRoot {
				place = c.name + " (store root)"
			}
			out = append(out, unattachedFolder{place: place, dir: dir, readOnly: c.readOnly,
				src: attach.Store{Name: store.Source(place), URL: c.url, Branch: c.branch, Folder: f}})
		}
	}
	return out, nil
}

// unattachedV1 counts, per table, the v1 rows of one unattached folder,
// reading its files offline (no open, no fetch). Rows that do not decode
// are not counted: they are no v1 row lore can rewrite. Only a file that
// declares an older format is decoded (rowfs OldRows, devco:D-1016: lore
// doctor decoded every row file of every unattached folder each run).
func unattachedV1(ctx context.Context, u unattachedFolder) (map[row.Table]int, int, error) {
	root, err := rowfs.Open(u.dir)
	if err != nil {
		return nil, 0, err
	}
	f, err := root.Folder(u.src.Folder)
	if err != nil {
		return nil, 0, err
	}
	v1, err := f.OldRows(ctx, row.FormatVersion)
	if err != nil {
		return nil, 0, errs.Prefix("migrate: read "+u.place, err)
	}
	n := 0
	for _, c := range v1 {
		n += c
	}
	return v1, n, nil
}

// unattachedPending is the pending line of an unattached folder with v1
// rows (n > 0), per table in name order.
func unattachedPending(u unattachedFolder, v1 map[row.Table]int) string {
	var parts []string
	for _, t := range slices.Sorted(maps.Keys(v1)) {
		parts = append(parts, t.String()+" "+plural(v1[t], "row"))
	}
	what := strings.Join(parts, ", ")
	if u.readOnly {
		return fmt.Sprintf(unattachedV1ReadOnlyLine, u.place, what)
	}
	return fmt.Sprintf(unattachedV1Line, u.place, what, u.src.URL, u.src.Folder)
}

// rewriteUnattached rewrites the v1 rows of tables in every writable
// unattached folder (dryRun: only lists them) and returns their ids per
// table. Each folder is opened as a store folder through the same openers
// as an attached one, so its rewrite is one commit in the clone (the run's
// Finish pushes the clone with its attached place); the open must land
// in the same checkout, or the folder is refused rather than cloned anew.
func (a *App) rewriteUnattached(ctx context.Context, st *state, tables []row.Table, dryRun bool) (map[row.Table][]ids.AnyID, error) {
	folders, err := a.unattachedFolders(st)
	if err != nil {
		return nil, err
	}
	found := map[row.Table][]ids.AnyID{}
	for _, u := range folders {
		if u.readOnly {
			continue
		}
		if _, n, cerr := unattachedV1(ctx, u); cerr != nil || n == 0 {
			continue // nothing to rewrite (an unreadable folder is status's to name)
		}
		got, err := a.rewriteFolder(ctx, u, tables, dryRun)
		if err != nil {
			return nil, err
		}
		for t, list := range got {
			found[t] = append(found[t], list...)
		}
	}
	return found, nil
}

// rewriteFolder opens one unattached folder and rewrites its v1 rows.
func (a *App) rewriteFolder(ctx context.Context, u unattachedFolder, tables []row.Table, dryRun bool) (map[row.Table][]ids.AnyID, error) {
	canon, err := canonical(u.src)
	if err != nil {
		return nil, errs.Prefix("migrate: store folder "+u.place, err)
	}
	b, err := a.openStore(ctx, canon, u.src.URL)
	if err != nil {
		return nil, errs.Prefix("migrate: open store folder "+u.place, err)
	}
	defer func() { _ = b.Close() }()
	if _, _, dir, ok := storePlaceOf(a.opts.Home, b); !ok || !fspath.Same(dir, u.dir) {
		return nil, errs.New(errs.Internal, "migrate: store folder "+u.place+" opened outside its checkout "+u.dir+"; nothing rewritten there").
			WithHint("lore store add --name <name> --url " + u.src.URL + " --folder " + u.src.Folder + " --yes attaches it, then lore migrate run upgrades it")
	}
	got, err := rewriteStoreTables(ctx, b, tables, dryRun)
	if err != nil {
		return nil, errs.Prefix("migrate: store folder "+u.place, err)
	}
	return got, nil
}

// statusUnattached adds a place and a pending line (a note when the store
// is read_only here) for every unattached folder that holds v1 rows. A
// folder with none adds nothing: status lists what this repo reads, plus
// what a run would still convert.
func (a *App) statusUnattached(ctx context.Context, st *state, out *opsmigrate.StatusOut) error {
	folders, err := a.unattachedFolders(st)
	if err != nil {
		return err
	}
	for _, u := range folders {
		v1, n, cerr := unattachedV1(ctx, u)
		if cerr != nil {
			out.Notes = append(out.Notes, cerr.Error())
			continue
		}
		if n == 0 {
			continue
		}
		out.Places = append(out.Places, opsmigrate.PlaceStatus{Place: u.place, V1: n, Total: n})
		line := unattachedPending(u, v1)
		if u.readOnly {
			out.Notes = append(out.Notes, line)
			continue
		}
		out.Pending = append(out.Pending, line)
	}
	return nil
}
