package wire

// orgread.go — a read naming an org store anywhere (devco D-471, D-472).
// `lore memory list --store acme` in a code repo under the setup's folders
// said "no store named acme is attached" (an unattached repo) or read only
// the folder the repo links (a linked repo), while the same read from the
// plain parent folder read every folder of the org. A read naming the org
// now resolves as from a plain folder wherever it runs (runOutsideRepo).
// A read naming a folder the store does not have said E_NEEDS_CONFIRM
// "nothing written … rerun with --create": write wording on a read.
//
//	read --store <org>[/<folder>] in a repo ─▶ namesOrgRead ─▶ runStoreOnly
//	read --store <store>/<missing folder>   ─▶ readMissingFolder (E_NOT_FOUND)

import (
	"context"
	"strings"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/adapters/orgcfg"
	"github.com/thesatellite-ai/lore/engine/attach"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/op"
	"github.com/thesatellite-ai/lore/store"
)

// namesOrgRead reports whether the row read e names an org's store as its
// scope (`--store <org>` or `--store <org>/<folder>`, or --to): the org is
// one the home's setup has, or one whose stores the org hook brings here.
// A name this repo's own .lore/stores.yaml gives a store of no org keeps
// the repo's meaning (the repo path reads it).
func (a *App) namesOrgRead(ctx context.Context, e op.Entry, d op.Decoder) bool {
	if !orgReadOp(e.Spec()) {
		return false
	}
	in, err := e.Decode(d)
	if err != nil {
		return false
	}
	n := scopeOf(in)
	name := n.to
	if name == "" {
		name = n.store
	}
	base, _, _ := strings.Cut(name, targetSep)
	if base == "" || base == string(store.SourceRepo) || base == attach.ReservedMe {
		return false
	}
	orgURLs, isOrg := a.orgNamed(ctx, base)
	if !isOrg {
		return false
	}
	top, err := a.storeRepoTop(ctx)
	if err != nil || top == "" {
		return true
	}
	cfg, err := readStoresFile(top)
	if err != nil {
		return true
	}
	for _, s := range cfg.Stores {
		if string(s.Name) != base && !strings.HasPrefix(string(s.Name), base+targetSep) {
			continue
		}
		if !orgURLs[urlKey(s.URL)] {
			return false // the repo names another store so
		}
	}
	return true
}

// orgNamed reports whether base is an org's name here and the places
// (urlKey) of its stores: from the home's org config,
// and from the org stores the hook brings whose name is base or
// base/<folder>.
func (a *App) orgNamed(ctx context.Context, base string) (map[string]bool, bool) {
	urls := map[string]bool{}
	found := false
	if cfg, _, err := orgcfg.LoadConfig(a.opts.Home); err == nil && cfg != nil {
		for i := range cfg.Orgs {
			if cfg.Orgs[i].Name != base {
				continue
			}
			found = true
			for _, s := range cfg.Orgs[i].Stores {
				urls[urlKey(s.URL)] = true
			}
		}
	}
	for _, s := range rawOrgStores(ctx, a.opts) {
		if string(s.Name) == base || strings.HasPrefix(string(s.Name), base+targetSep) {
			found = true
			urls[urlKey(s.URL)] = true
		}
	}
	return urls, found
}

// readMissingFolder is a read's refusal of `--store <base>/<folder>` when
// the store has no such folder: nothing to read, so it says the folder
// does not exist yet and how to list the store's folders. Never the
// write's E_NEEDS_CONFIRM "nothing written … rerun with --create"
// (missingFolder): a read creates nothing.
func (a *App) readMissingFolder(ctx context.Context, src attach.Store, base, folderName string) error {
	msg := "store " + base + " has no folder " + folderName + " yet, so there is nothing to read"
	if dir, err := a.storeDir(ctx, src); err == nil {
		if have := dataFolders(dir); len(have) > 0 {
			msg += " (its folders: " + strings.Join(have, ", ") + ")"
		}
	}
	msg += a.folderMissDetail(ctx) // the org's other stores, when it has some
	return errs.New(errs.NotFound, msg).
		WithHint("lore store folder list --store " + base + " lists its folders; read one with --store " + base + "/<folder>")
}

// urlKey is the store place of raw for comparing urls (storeKey; the raw
// text when it does not parse, so two unparsable urls match only when equal).
func urlKey(raw string) string {
	if k, ok := storeKey(raw); ok {
		return k
	}
	return raw
}

// orgConfigStores is every store of the org named base in the home's org
// config, the one holding the org folder first, each named base; none when
// the home has no such org. It lets a read name an org from a folder no
// setup covers (the home folder, a repo elsewhere).
func (a *App) orgConfigStores(base string) []attach.Store {
	cfg, _, err := orgcfg.LoadConfig(a.opts.Home)
	if err != nil || cfg == nil || base == "" || strings.Contains(base, targetSep) {
		return nil
	}
	var out []attach.Store
	for i := range cfg.Orgs {
		org := &cfg.Orgs[i]
		if org.Name != base {
			continue
		}
		for j := range org.Stores {
			s := attach.Store{Name: store.Source(base), URL: org.Stores[j].URL, Branch: attachBranch(&org.Stores[j]), Folder: rowfs.FolderRoot, ReadOnly: true}
			if org.Stores[j].OrgFolder != "" {
				out = append([]attach.Store{s}, out...)
			} else {
				out = append(out, s)
			}
		}
	}
	return out
}

// orgConfigTarget is the store a read names as `<org>` or `<org>/<folder>`
// when the org comes only from the home's org config: the org's first
// store's root for the whole org, else the store holding the folder (the
// read's refusal when none does). ok is false when base names no org.
func (a *App) orgConfigTarget(ctx context.Context, name store.Source, base, folderName string, hasFolder bool) (attach.Store, bool, error) {
	stores := a.orgConfigStores(base)
	if len(stores) == 0 {
		return attach.Store{}, false, nil
	}
	if !hasFolder || folderName == rowfs.FolderRoot {
		t := stores[0]
		t.Name = name
		return t, true, nil
	}
	for _, s := range stores {
		has, err := a.folderExists(ctx, s, folderName)
		if err != nil {
			return attach.Store{}, true, err
		}
		if has {
			return attach.Store{Name: name, URL: s.URL, Folder: folderName, Branch: s.Branch, ReadOnly: true}, true, nil
		}
	}
	return attach.Store{}, true, a.readMissingFolder(ctx, stores[0], base, folderName)
}

// uniqueBeside drops from also the entries that repeat target's name or an
// earlier entry's name: the company folder and the org-wide folders both
// name <org>/org, and two entries of one name refuse the open ("attached
// twice").
func uniqueBeside(target attach.Store, also []attach.Store) []attach.Store {
	seen := map[store.Source]bool{target.Name: true}
	out := also[:0:0]
	for _, s := range also {
		if seen[s.Name] {
			continue
		}
		seen[s.Name] = true
		out = append(out, s)
	}
	return out
}

// orgReadOp reports whether an operation may read an org by name
// (`--store <org>`): a row read, or a store folder listing. A store admin
// command (store set, store compact, store push) names a store this repo
// attaches by its name, and the repo path runs it (devco D-476).
func orgReadOp(spec op.Spec) bool {
	if !spec.OnlyReads() {
		return false
	}
	return !strings.HasPrefix(spec.Name, "store.") || strings.HasPrefix(spec.Name, "store.folder.")
}

// runOrgReadAside runs an org read (`--store <org>` inside a repo) on a
// store-only open and then puts the App's own open back: the App of a
// repo serves more commands than this one (lore serve, a test's App), and
// they must find the repo's stores again, not the org's (devco D-477: the
// next `--store repo` read refused "this command needs a repo").
func (a *App) runOrgReadAside(ctx context.Context, e op.Entry, d op.Decoder, c op.Caller) (op.Result, bool, error) {
	a.mu.Lock()
	opened, st, serr, cfgOpened, cfgErr, cfg := a.opened, a.st, a.err, a.cfgOpened, a.cfgErr, a.cfg
	a.opened, a.st, a.err = false, nil, nil // openStoreOnlyOn must not close the repo's state
	a.mu.Unlock()
	res, handled, err := a.runStoreOnly(ctx, e, d, c)
	a.mu.Lock()
	if a.st != nil && a.st != st {
		_ = a.st.close()
	}
	a.opened, a.st, a.err, a.cfgOpened, a.cfgErr, a.cfg = opened, st, serr, cfgOpened, cfgErr, cfg
	a.mu.Unlock()
	return res, handled, err
}
