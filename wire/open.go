package wire

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/thesatellite-ai/lore/adapters/backend/folder"
	"github.com/thesatellite-ai/lore/adapters/backend/git"
	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/adapters/cache/sqlite"
	"github.com/thesatellite-ai/lore/adapters/identity"
	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/engine/attach"
	"github.com/thesatellite-ai/lore/logic/merge"
	"github.com/thesatellite-ai/lore/logic/policy"
	"github.com/thesatellite-ai/lore/logic/refs"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// On-disk names wire chooses (spec 7.4 cache layout).
const (
	// RepoCacheFile is the repo store's cache, relative to the repo top.
	// It is not .lore/lore.db: the v0.1.17 code still owns that file.
	RepoCacheFile = ".lore/lore-cache.db"
	// cacheDir holds the other stores' caches under the lore home.
	cacheDir = "cache"
	// cacheStoresDir is the folder of per-store caches under cacheDir.
	cacheStoresDir = "stores"
	// cacheExt ends a cache file name.
	cacheExt = ".db"
	// rootFolderName names the cache file of a store's root folder (".").
	rootFolderName = "root"
	// localBranch is the branch segment of a store with no branches (a
	// folder store).
	localBranch = "local"
	// combinedDir holds the CFG3 combined caches under cacheDir.
	combinedDir = "combined"
	// MaxStoreCaches is how many attached store folders get their own
	// cache file; past it the repo uses one combined cache (CFG3, O10):
	// a lens attaches at most sqlite.MaxLensSources files, one of them the
	// repo's own.
	MaxStoreCaches = sqlite.MaxLensSources - 1
	// cacheDirMode is the mode of cache folders lore creates (owner only:
	// caches hold internal rows).
	cacheDirMode = 0o700
)

// open builds the whole core for opts (see the package doc for the order).
// extra are `--to <store>/<folder>` targets to attach beside stores.yaml;
// repoOnly opens just the repo store (the store operations' engine). On
// any failure everything opened so far is closed.
func open(ctx context.Context, opts Options, extra []attach.Store, repoOnly bool) (*state, error) {
	st := &state{actorDone: map[store.Source]bool{}}
	switch {
	case !opts.Actor.IsZero():
		st.actor = opts.Actor
	case opts.ResolveActor != nil:
		a, err := opts.ResolveActor(ctx)
		if err != nil {
			return nil, err
		}
		st.actor = a
	default:
		who, err := resolveIdentity(ctx, opts)
		if err != nil {
			return nil, err
		}
		if st.actor, err = identity.ActorID(who.StableKey); err != nil {
			return nil, err
		}
		st.who = who
	}
	actor := st.actor
	reg, err := builtinRegistry()
	if err != nil {
		return nil, err
	}
	fail := func(err error) (*state, error) { return nil, errors.Join(err, st.close()) }

	repo, err := git.OpenRepo(ctx, opts.RepoDir, rowfs.FolderRoot, opts.openOptions())
	if err != nil {
		return nil, err
	}
	st.backends = append(st.backends, repo)
	st.repoTop = repo.Dir()
	var repoBackend store.Backend = repo
	if opts.WrapBackend != nil {
		if repoBackend, err = wrapBackend(ctx, opts, store.SourceRepo, "", repo); err != nil {
			return fail(err)
		}
	}

	var attached []attach.Opened
	if !repoOnly {
		personal, perr := localPersonalStores(ctx, opts)
		if perr != nil {
			return fail(perr)
		}
		org := orgStores(ctx, opts, st.repoTop)
		optional := make(map[store.Source]bool, len(org))
		for _, s := range org {
			optional[s.Name] = true
		}
		if attached, st.unreachable, err = openAttached(ctx, st.repoTop, opts, reg, slices.Concat(extra, org, personal), optional); err != nil {
			return fail(err)
		}
	}
	for _, o := range attached {
		st.backends = append(st.backends, o.Backend)
	}
	if opts.WrapBackend != nil {
		for i := range attached {
			if attached[i].Backend, err = wrapBackend(ctx, opts, attached[i].Name, attached[i].Folder, attached[i].Backend); err != nil {
				return fail(err)
			}
		}
	}

	st.caches = newCaches()
	exp := refs.Expander{} // no resolver yet: vars/resources are not migrated, references stay as written
	repoCache := filepath.Join(st.repoTop, filepath.FromSlash(RepoCacheFile))
	if mkErr := os.MkdirAll(filepath.Dir(repoCache), cacheDirMode); mkErr != nil {
		return fail(errs.Wrap(errs.Internal, "wire: create the .lore folder", mkErr))
	}
	if err = st.caches.open(ctx, store.SourceRepo, repoCache, reg, exp); err != nil {
		return fail(err)
	}
	if len(attached) > MaxStoreCaches {
		// CFG3 fallback (O10): past the lens attach limit every attached
		// store shares one combined cache file for this repo, each applied
		// under its own source name.
		path, pathErr := combinedCachePath(opts.Home, st.repoTop)
		if pathErr != nil {
			return fail(pathErr)
		}
		names := make([]store.Source, len(attached))
		for i, o := range attached {
			names[i] = o.Name
		}
		if err = st.caches.openCombined(ctx, names, path, reg, exp); err != nil {
			return fail(err)
		}
	} else {
		for _, o := range attached {
			path, pathErr := cachePath(opts.Home, o)
			if pathErr != nil {
				return fail(pathErr)
			}
			if err = st.caches.open(ctx, o.Name, path, reg, exp); err != nil {
				return fail(err)
			}
		}
	}

	backends := map[store.Source]store.Backend{store.SourceRepo: repoBackend}
	st.folders = map[store.Source]string{}
	for _, o := range attached {
		backends[o.Name] = o.Backend
		st.folders[o.Name] = o.Folder
	}
	st.byName = backends
	scanner, err := newScanner(ctx, backends)
	if err != nil {
		return fail(err)
	}
	repoAudit := opts.Audit
	if repoAudit == nil {
		repoAudit = &fileAudit{path: filepath.Join(st.repoTop, filepath.FromSlash(AuditFile))}
	}
	st.audit = newRoutedAudit(repoAudit, opts.Home, attached)

	repoClient, note := repoIsClient(ctx, opts)
	st.repoClassNote = note
	engOpts := []engine.Option{
		engine.WithStore(engine.Attached{Name: store.SourceRepo, Backend: repoBackend, Client: repoClient, ActorAs: st.clientActorAs(repoClient)}),
		engine.WithCache(st.caches), engine.WithRegistry(reg), engine.WithScanner(scanner),
		engine.WithActor(actor), engine.WithAudit(st.audit), engine.WithWall(opts.Wall),
	}
	for _, o := range attached {
		a := o.Attached
		a.KeyNamespace = keyNamespace(a)
		engOpts = append(engOpts, engine.WithStore(a))
	}
	st.defaultTarget = store.SourceRepo
	if st.eng, err = engine.New(engOpts...); err != nil {
		return fail(err)
	}
	for _, name := range st.eng.Stores() {
		if _, refErr := st.eng.Refresh(ctx, name); refErr != nil {
			return fail(errs.Wrap(errs.CodeOf(refErr), "wire: refresh the cache of store "+string(name), refErr))
		}
	}
	if repoClient && st.who.Kind == identity.KindAgent {
		// D8 / LORE_TEAM_SPEC 7.6: by default (agent_details: internal) an
		// agent in a client repo is recorded as its owning person; an org
		// that sets agent_details: client shows the agent as itself, so no
		// owner is looked up and clientActorAs stamps the agent's id.
		details, derr := agentDetailsOf(opts.Home, st.who.Org)
		if derr != nil {
			return fail(derr)
		}
		if details == AgentDetailsInternal {
			if st.agentOwner, st.agentOwnerName, err = agentOwnerOf(ctx, st); err != nil {
				return fail(err)
			}
		}
	}
	if st.format, err = readFormat(ctx, repo, actor); err != nil {
		return fail(err)
	}
	if st.storeFormat, err = readStoreFormats(ctx, opts, st.repoTop, st.byName, actor); err != nil {
		return fail(err)
	}
	return st, nil
}

// repoIsClient asks opts.RepoIsClient whether the repo store is a client
// repo. No hook, or an error, answers client (fail closed: an internal
// row must never be pointed at, nor a teammate's email written, from a
// customer's repository), with the reason as note.
func repoIsClient(ctx context.Context, opts Options) (client bool, note string) {
	if opts.RepoIsClient == nil {
		return true, "the repo is treated as a client repo: no client-repo check is wired"
	}
	client, err := opts.RepoIsClient(ctx)
	if err != nil {
		return true, "the repo is treated as a client repo: whether it is one could not be checked (" + err.Error() + ")"
	}
	return client, ""
}

// orgStores asks opts.OrgStores for the folders the repo's org brings; no
// hook or an error gives none (fail closed: nothing internal is attached on
// a guess, and a write naming the org store then answers "no store named").
func orgStores(ctx context.Context, opts Options, top string) []attach.Store {
	return renameOrgClashes(rawOrgStores(ctx, opts), top)
}

// rawOrgStores is Options.OrgStores as given (nil on error: fail closed).
func rawOrgStores(ctx context.Context, opts Options) []attach.Store {
	if opts.OrgStores == nil {
		return nil
	}
	got, err := opts.OrgStores(ctx)
	if err != nil {
		return nil
	}
	return got
}

// renameOrgClashes gives the org's linked-folder entry (bare org name, a
// folder) the name "<org>/<folder>" when the repo's stores.yaml already
// uses the bare name for another place (typically the store root, made by
// `lore store init <org> …` in this repo). Without it the committed entry
// wins the clash, the linked folder is never attached, and `--to team` or
// `--to <org>` would write into the store root instead (r4 repro: policies
// landing at store_root). An entry naming the same url and folder as the
// committed one is left as is (it is the same place; the committed entry
// attaches it). top is the repo top; an unreadable stores.yaml renames
// nothing.
func renameOrgClashes(org []attach.Store, top string) []attach.Store {
	if len(org) == 0 || top == "" {
		return org
	}
	cfg, err := readStoresFile(top)
	if err != nil {
		return org
	}
	out := slices.Clone(org)
	for i, s := range out {
		f := strings.Trim(s.Folder, "/")
		if strings.Contains(string(s.Name), targetSep) || f == "" || f == rowfs.FolderRoot {
			continue
		}
		for _, c := range cfg.Stores {
			if c.Name != s.Name {
				continue
			}
			if strings.Trim(c.Folder, "/") == f && sameURL(c, s) {
				break
			}
			out[i].Name = store.Source(string(s.Name) + targetSep + f)
			break
		}
	}
	return out
}

// openAttached reads <repo>/.lore/stores.yaml (absent = no stores), adds
// the `--to <store>/<folder>` targets and the personal store `me` (last,
// BATCH 15; skipped when stores.yaml already attaches the same url's root,
// so it loads once), and opens them all.
//
// optional names the stores the org brings (Options.OrgStores): one of
// them that has no local copy and cannot be reached (E_STORE_UNREACHABLE)
// is left out and returned in unreachable instead of failing the open, so
// reads keep working offline (spec 5.4) and each write decides how to
// fail closed (App.unreachableGuard). Any other failure, and any failure
// of a committed or personal store, still fails the open.
func openAttached(ctx context.Context, repoTop string, opts Options, reg *schema.Registry, extra []attach.Store, optional map[store.Source]bool) (opened []attach.Opened, unreachable map[store.Source]error, err error) {
	home := opts.Home
	cfg, err := readStoresFile(repoTop)
	if err != nil {
		return nil, nil, err
	}
	for _, x := range extra {
		// The committed file wins a name clash (an org store the repo
		// also attaches by hand, or a recorded folder target).
		if !slices.ContainsFunc(cfg.Stores, func(s attach.Store) bool { return s.Name == x.Name }) {
			cfg.Stores = append(cfg.Stores, x)
		}
	}
	mc, err := readMachineConfig(home)
	if err != nil {
		return nil, nil, err
	}
	if p := mc.personalURL(); p != "" {
		me := attach.Store{Name: store.Source(attach.ReservedMe), URL: p}
		dup := false
		if pc, perr := canonical(me); perr == nil {
			for _, s := range cfg.Stores {
				if c, cerr := canonical(attach.Store{URL: s.URL, Folder: s.Folder, Branch: s.Branch}); cerr == nil && c == pc {
					dup = true
				}
			}
		}
		if !dup {
			cfg.Stores = append(cfg.Stores, me)
		}
	}
	raws := make([]string, 0, len(cfg.Stores))
	for i, s := range cfg.Stores {
		if cfg.Stores[i].URL, err = canonical(s); err != nil {
			return nil, nil, errs.Wrap(errs.CodeOf(err), "wire: store "+string(s.Name), err)
		}
		raws = append(raws, s.URL)
	}
	openers, err := newOpeners(reg, originsOf(raws...), storeIdentity(opts))
	if err != nil {
		return nil, nil, err
	}
	var strict, maybe attach.Config
	for _, s := range cfg.Stores {
		if optional[s.Name] {
			maybe.Stores = append(maybe.Stores, s)
		} else {
			strict.Stores = append(strict.Stores, s)
		}
	}
	if opened, err = attach.Open(ctx, strict, openers, opts.openOptions()); err != nil {
		return nil, nil, err
	}
	for _, s := range maybe.Stores {
		one, oerr := attach.Open(ctx, attach.Config{Stores: []attach.Store{s}}, openers, opts.openOptions())
		switch {
		case errs.Has(oerr, errs.StoreUnreachable):
			if unreachable == nil {
				unreachable = map[store.Source]error{}
			}
			unreachable[s.Name] = oerr
			continue
		case oerr != nil:
			for _, o := range opened {
				_ = o.Backend.Close() // best effort: the open error is what the caller needs
			}
			return nil, nil, oerr
		}
		opened = append(opened, one...)
	}
	urls := make(map[store.Source]string, len(cfg.Stores))
	for _, s := range cfg.Stores {
		urls[s.Name] = s.URL
	}
	for i := range opened {
		opened[i].Level = levelOf(opened[i])
		opened[i].Location = location(urls[opened[i].Name], opened[i].Folder)
	}
	return opened, unreachable, nil
}

// location is engine.Attached.Location for a store opened from the
// canonical url u (which may already carry its `?folder=`) attached at
// folder: u with the folder set explicitly, the root spelled "." whether
// it was written as "", "." or not at all, so every spelling of one store
// folder gives one string. A url that does not parse is its own place.
func location(u, folder string) string {
	p, err := url.Parse(u)
	if err != nil {
		return u
	}
	q := p.Query()
	f := q.Get(rowfs.FolderParam)
	if folder != "" {
		f = folder
	}
	if f == "" {
		f = rowfs.FolderRoot
	}
	q.Set(rowfs.FolderParam, f)
	p.RawQuery = q.Encode()
	return p.String()
}

// levelOf is the settings level of one attached store folder (O1, spec
// 4.3): the personal store, a store's org/ folder, a project folder, or
// the store root.
func levelOf(o attach.Opened) policy.Level {
	switch {
	case o.Name == store.Source(attach.ReservedMe) || strings.HasPrefix(string(o.Name), attach.ReservedMe+targetSep):
		return policy.LevelPersonal
	case o.Folder == defaultOrgFolder:
		return policy.LevelOrg
	case o.Folder != "" && o.Folder != rowfs.FolderRoot:
		return policy.LevelFolder
	}
	return policy.LevelStoreRoot
}

// canonical prepares one stores.yaml url for store.Openers: the scp form
// (git@host:owner/repo) becomes ssh:// through git.CanonicalURL (net/url
// cannot parse it), a password is refused (attach.NormalizeURL, never
// echoed), and the entry's folder becomes the `?folder=` the backends read
// (rowfs.FolderParam), its branch the `?branch=` (git.BranchParam).
func canonical(s attach.Store) (string, error) {
	raw, err := attach.NormalizeURL(s.URL)
	if err != nil {
		return "", err
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", errs.New(errs.StoreScheme, "wire: the store url does not parse")
	}
	if u.Scheme != folder.Scheme {
		if raw, err = git.CanonicalURL(raw); err != nil {
			return "", err
		}
		if u, err = url.Parse(raw); err != nil {
			return "", errs.New(errs.StoreScheme, "wire: the store url does not parse")
		}
	}
	if s.Folder != "" || s.Branch != "" {
		q := u.Query()
		if s.Folder != "" {
			q.Set(rowfs.FolderParam, s.Folder)
		}
		if s.Branch != "" {
			q.Set(git.BranchParam, s.Branch) // a file:// store refuses it (E_STORE_SCHEME): no branches
		}
		u.RawQuery = q.Encode()
	}
	return u.String(), nil
}

// originsOf maps each git store url in raws (as written: stores.yaml, the
// command line) to the origin its clone gets: keyed by the canonical
// credential-free remote url the git opener receives (canonical() minus its
// query), valued by git.OriginOf, the person's own spelling without
// credentials. canonical() stays the identity everywhere else (clone
// folders, locations, duplicate checks); only the clone's origin follows
// the spelling, so an insteadOf rule keyed on it applies and an scp-like
// home-relative path is not turned into an absolute one. A file:// url, or
// one that does not canonicalise (canonical reports that), is skipped; when
// two spellings share one remote url the first wins.
func originsOf(raws ...string) map[string]string {
	out := map[string]string{}
	for _, raw := range raws {
		canon, err := canonical(attach.Store{URL: raw})
		if err != nil || strings.HasPrefix(canon, folder.Scheme+":") {
			continue
		}
		key, kerr := git.OriginOf(canon)
		origin, oerr := git.OriginOf(raw)
		if kerr != nil || oerr != nil {
			continue
		}
		if _, seen := out[key]; !seen {
			out[key] = origin
		}
	}
	return out
}

// newOpeners registers the 0.1.18 backends: folder for file://, git for
// ssh, https and http with logic/merge as the row merger (spec 11 rule 5:
// the same merge the engine runs). origins is originsOf the store urls as
// written: each git clone's origin spelling. who is the lore identity a
// git clone signs commits with when git has none (FLAG4, storeIdentity).
func newOpeners(reg *schema.Registry, origins map[string]string, who git.Option) (*store.Openers, error) {
	o := store.NewOpeners()
	if err := o.Register(folder.Scheme, folder.NewOpener()); err != nil {
		return nil, err
	}
	gitOpen := git.NewOpener(git.WithMerger(merge.Driver{Registry: reg}), git.WithOrigins(origins), who)
	for _, s := range git.Schemes {
		if err := o.Register(s, gitOpen); err != nil {
			return nil, err
		}
	}
	return o, nil
}

// markerOf is implemented by both 0.1.18 backends: the store's
// lore-store.yaml.
type markerOf interface{ Marker() rowfs.Marker }

// branchOf is implemented by the git backend.
type branchOf interface{ Branch() string }

// cachePath is <home>/cache/stores/<store id>/<branch>/<folder>.db. A
// v0.1.17 marker has no store id; the store's name stands in, so the path
// is still stable for this repo.
func cachePath(home string, o attach.Opened) (string, error) {
	id := string(o.Name)
	if m, ok := concrete(o.Backend).(markerOf); ok && !m.Marker().ID.IsZero() {
		id = m.Marker().ID.String()
	}
	branch := localBranch
	if b, ok := concrete(o.Backend).(branchOf); ok && b.Branch() != "" {
		branch = b.Branch()
	}
	name := rootFolderName
	if o.Folder != "" && o.Folder != rowfs.FolderRoot {
		clean, err := rowfs.CleanFolder(o.Folder)
		if err != nil {
			return "", err
		}
		name = clean
	}
	dir := filepath.Join(home, cacheDir, cacheStoresDir, id, filepath.FromSlash(branch))
	path := filepath.Join(dir, filepath.FromSlash(name)+cacheExt)
	if err := os.MkdirAll(filepath.Dir(path), cacheDirMode); err != nil {
		return "", errs.Wrap(errs.Internal, "wire: create the cache folder", err)
	}
	return path, nil
}

// cacheOpen is the sqlite cache constructor with the expander it needs.
func cacheOpen(ctx context.Context, path string, reg *schema.Registry, exp sqlite.Expander) (*sqlite.Cache, error) {
	return sqlite.Open(ctx, path, reg, exp)
}

// combinedCachePath is <home>/cache/combined/<sha256 of the repo top>.db:
// one file per code repo, outside the repo so it never shows in git status.
func combinedCachePath(home, repoTop string) (string, error) {
	sum := sha256.Sum256([]byte(repoTop))
	dir := filepath.Join(home, cacheDir, combinedDir)
	if err := os.MkdirAll(dir, cacheDirMode); err != nil {
		return "", errs.Wrap(errs.Internal, "wire: create the cache folder", err)
	}
	return filepath.Join(dir, hex.EncodeToString(sum[:])+cacheExt), nil
}

// markerBackend is a backend that knows its store marker (folder and git
// backends).
type markerBackend interface{ Marker() rowfs.Marker }

// keyNamespace is an attached store's natural-id namespace: its marker's
// sto_ id, plus "/<folder>" for a `<store>/<folder>` target ("" when the
// backend has no marker id: such a store keeps v0.1.17's ids).
func keyNamespace(a engine.Attached) string {
	mb, ok := concrete(a.Backend).(markerBackend)
	if !ok || mb.Marker().ID.IsZero() {
		return ""
	}
	ns := mb.Marker().ID.String()
	if _, folder, has := strings.Cut(string(a.Name), "/"); has {
		ns += "/" + folder
	}
	return ns
}

// sameURL reports whether two store entries name the same store url and
// branch (canonical forms; an unparsable url is never the same).
func sameURL(a, b attach.Store) bool {
	ca, err := canonical(attach.Store{URL: a.URL, Branch: a.Branch})
	if err != nil {
		return false
	}
	cb, err := canonical(attach.Store{URL: b.URL, Branch: b.Branch})
	return err == nil && ca == cb
}

// Unwrapper is what a backend returned by Options.WrapBackend implements:
// the backend it wraps. wire reads a store's marker, branch and clone
// through the concrete backend (natural-id namespace, cache path, store
// identity), so a wrapper that hid it would silently change row ids.
type Unwrapper interface {
	Unwrap() store.Backend
}

// concrete is b with every WrapBackend layer removed.
func concrete(b store.Backend) store.Backend {
	for {
		u, ok := b.(Unwrapper)
		if !ok {
			return b
		}
		b = u.Unwrap()
	}
}

// wrapBackend applies Options.WrapBackend to one store's backend. A
// wrapper that returns a different backend must implement Unwrapper
// (fail closed: E_CONFIG_INVALID), and nil is refused.
func wrapBackend(ctx context.Context, opts Options, name store.Source, folder string, b store.Backend) (store.Backend, error) {
	w := opts.WrapBackend(ctx, name, folder, b)
	switch w {
	case nil:
		return nil, errs.New(errs.ConfigInvalid, "wire: WrapBackend returned no backend for store "+string(name))
	case b:
		return b, nil
	}
	if _, ok := w.(Unwrapper); !ok {
		return nil, errs.New(errs.ConfigInvalid, "wire: the WrapBackend wrapper of store "+string(name)+" does not implement wire.Unwrapper")
	}
	return w, nil
}
