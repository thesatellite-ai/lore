package wire

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"

	"gopkg.in/yaml.v3"

	"github.com/thesatellite-ai/lore/adapters/backend/folder"
	"github.com/thesatellite-ai/lore/adapters/backend/git"
	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/adapters/cache/sqlite"
	"github.com/thesatellite-ai/lore/adapters/identity"
	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/engine/attach"
	"github.com/thesatellite-ai/lore/logic/merge"
	"github.com/thesatellite-ai/lore/logic/orgresolve"
	"github.com/thesatellite-ai/lore/logic/origin"
	"github.com/thesatellite-ai/lore/logic/policy"
	"github.com/thesatellite-ai/lore/logic/refs"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// On-disk names wire chooses (spec 7.4 cache layout).
const (
	// RepoCacheFile is where the repo store's cache lived before it moved
	// under the lore home (RepoCachePath), relative to the repo top. lore
	// no longer creates it; a copy left in a checkout is untracked and
	// rebuildable: `lore doctor` names it as safe to delete and `lore fix`
	// removes it. The .gitignore block keeps its lines (teammates on
	// v0.1.17 expect the same block). It is not .lore/lore.db: the v0.1.17
	// code still owns that file.
	RepoCacheFile = ".lore/lore-cache.db"
	// repoCacheDir holds the repo stores' caches under cacheDir, one file
	// per checkout: <repo folder name>-<short hash of its path>.db. The
	// name is sqlite.RepoCacheRel's, so `lore cache prune` finds the cache
	// of a checkout that moved.
	repoCacheDir = sqlite.RepoDir
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
	// Hand edits are picked up before any store fetches or pushes
	// (handedit.go, App.state runs the pick-up once the open is done):
	// until then the stores read their last commits only.
	if !repoOnly {
		ctx = withDeferredFetch(ctx)
	}
	// STORE layout 1.5: inside a store's checkout there is no code repo;
	// the store folder the command runs in is the one store (storehere.go).
	if sc, ok := storeHere(opts.RepoDir); ok {
		if repoOnly {
			return nil, errInStore(sc)
		}
		one, err := storeHereEntry(opts.Home, sc)
		if err != nil {
			return nil, err
		}
		st, err := openDetachedHere(ctx, opts, &one, one.Name)
		if err != nil {
			return nil, err
		}
		st.here = one.Name
		return st, nil
	}
	st := &state{actorDone: map[store.Source]bool{}}
	if err := st.resolveActor(ctx, opts, !repoOnly); err != nil {
		return nil, err
	}
	reg, err := builtinRegistry()
	if err != nil {
		return nil, err
	}
	if opts.LockTimeout == 0 { // FA-AGENT-8: store.lock_timeout, a local setting
		if opts.LockTimeout, err = readLockTimeout(opts.Home); err != nil {
			return nil, err
		}
	}
	fail := func(err error) (*state, error) { return nil, errors.Join(err, st.close()) }

	repo, err := git.OpenRepo(ctx, opts.RepoDir, rowfs.FolderRoot, opts.openOptions())
	if err != nil {
		return nil, err
	}
	// A repo with no lore data that a store links by its remote (`lore org
	// link` in a client repo, never `lore init`) is never written to: its
	// repo store is read-only and its cache lives under ~/.lore. Decided
	// from the root OpenRepo found (no further git call), and only for a
	// repo with no .lore/, so a lore project pays nothing for it.
	remote := ""
	if !repoOnly && opts.RepoLinkedByRemote != nil && !loreFolderAt(repo.Dir()) {
		if remote = opts.RepoLinkedByRemote(ctx); remote != "" {
			repo.MarkReadOnly()
		}
	}
	st.linkedByRemote = remote
	st.backends = append(st.backends, repo)
	st.repoTop = repo.Dir()
	var repoBackend store.Backend = repo
	if opts.WrapBackend != nil {
		if repoBackend, err = wrapBackend(ctx, opts, store.SourceRepo, "", repo); err != nil {
			return fail(err)
		}
	} else {
		repoBackend = liveCursorKeeper{repo} // livecursor.go: a hook's read keeps a live run's cursor
	}
	// D13 (formatold.go): a project on lore 0.1.17's format is read, never
	// written in the newer format; the guard sits outside every wrapper
	// (the live overlay included), so no path reaches its rows
	if ProjectFormatOld(st.repoTop) {
		repoBackend = formatGuard{Backend: repoBackend, top: st.repoTop}
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
	// D16 (formatold.go): a shared store still on lore 0.1.17's layout is
	// read, never written in the new format, until lore migrate run
	for i := range attached {
		if storeOnOldLayout(attached[i].Backend) {
			attached[i].Backend = storeFormatGuard{Backend: attached[i].Backend, name: string(attached[i].Name)}
		}
	}

	st.caches = newCaches()
	// SQ3 / VARS ARE DATA: var, res and row references resolve from the row
	// files (wire/resolver.go); st.eng is read lazily, set below.
	rv := newResolver(st)
	exp := refs.Expander{Resolver: rv}
	// The repo store's cache lives under the lore home, never in the work
	// tree (owner item 16): a read then creates nothing in the repo, so it
	// has nothing to hide through .git/info/exclude.
	var repoCache string
	switch {
	case remote != "":
		if repoCache, err = remoteRepoCachePath(opts.Home, st.repoTop); err != nil {
			return fail(err)
		}
	case NoCreate(ctx) && !loreFolderAt(st.repoTop): // doctor_nocreate.go: a read (or a write that may not set up, S-2) never creates .lore/
		return fail(noProjectErr(ctx, st.repoTop))
	default:
		// Only a command that sets a repo up on purpose (init, setup,
		// link, store add, sync: host.setsUpRepo) or an in-process caller
		// with a plain ctx makes .lore/ here (a read reaches here only when
		// it exists: MkdirAll then changes nothing).
		if mkErr := os.MkdirAll(filepath.Join(st.repoTop, loreFolderName), cacheDirMode); mkErr != nil {
			return fail(errs.FileIO("create the .lore folder", filepath.Join(st.repoTop, loreFolderName), mkErr))
		}
		repoCache = RepoCachePath(opts.Home, st.repoTop)
		if mkErr := mkCacheDir(filepath.Dir(repoCache)); mkErr != nil {
			return fail(mkErr)
		}
	}
	if err = st.caches.open(ctx, store.SourceRepo, repoCache, reg, exp); err != nil {
		return fail(err)
	}
	// FLAG3: the cache use record (see after the caches open); committed
	// marks the stores.yaml entries so prune sees a `lore store remove`.
	cacheRoot := filepath.Join(writableHome(opts.Home), cacheDir)
	var uses []sqlite.Use
	committed := map[store.Source]bool{}
	if cfg, cfgErr := readStoresFile(st.repoTop); cfgErr == nil {
		for _, s := range cfg.Stores {
			committed[s.Name] = true
		}
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
		for _, n := range names {
			uses = append(uses, sqlite.UseOf(cacheRoot, path, string(n), committed[n]))
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
			uses = append(uses, sqlite.UseOf(cacheRoot, path, string(o.Name), committed[o.Name]))
		}
	}
	if !repoOnly {
		// FLAG3: every full open records which cache files this repo uses
		// (~/.lore/cache/uses/<hash>.json), so `lore cache prune` can
		// remove only caches no repo on this machine attaches any more:
		// caches always rebuild from row files and old client data must
		// not linger. A repoOnly open attaches nothing and must not wipe
		// the record. A failed write is ignored: it can only make prune
		// drop a cache that the next command rebuilds.
		_ = sqlite.RecordUse(cacheRoot, st.repoTop, uses)
	}

	backends := map[store.Source]store.Backend{store.SourceRepo: repoBackend}
	st.folders = map[store.Source]string{}
	for _, o := range attached {
		backends[o.Name] = o.Backend
		st.folders[o.Name] = o.Folder
	}
	st.byName = backends
	// AGENT_IDENTITY 6.1 / OQ9: a detected agent whose 6-hex key another
	// owner already holds uses 10 hex; settled now that the stores are
	// open, before the engine stamps anything with the actor.
	if err = st.settleAgentKey(ctx, backends); err != nil {
		return fail(err)
	}
	// read after settleAgentKey, which may lengthen an agent's key (OQ9)
	actor := st.actor
	// D9: team stores nearest-first; OV11 session ids; SECRETS 3.8: built on
	// the first write that scans, so list/show/search never build it (P3-SEC-5)
	scanner, err := newScanner(ctx, backends, withRepoGitleaks(st.repoTop), withFolders(st.folders), withToolSessions(opts.Audit), withLazyBuild())
	if err != nil {
		return fail(err)
	}
	repoAudit := opts.Audit
	if repoAudit == nil {
		repoAudit = &fileAudit{path: filepath.Join(st.repoTop, filepath.FromSlash(AuditFile))}
	}
	ra := newRoutedAudit(repoAudit, opts.Home, attached)
	ra.rowFile = st.rowFileOf
	st.audit = ra

	repoClient, note := repoIsClient(ctx, opts)
	st.repoClassNote = note
	// D8 / LORE_TEAM_SPEC 7.6: agent_details of the caller's org; read for
	// every caller of a client repo, not only an agent, because a person
	// editing a task an agent worked on must not write the agent's id into
	// the client either (P2B-OLD-2: engine Attached.AgentsInternal).
	details := AgentDetailsInternal
	if repoClient {
		if details, err = agentDetailsOf(opts.Home, st.who.Org); err != nil {
			return fail(err)
		}
	}
	engOpts := []engine.Option{
		engine.WithStore(engine.Attached{Name: store.SourceRepo, Backend: repoBackend, Client: repoClient, ActorAs: st.clientActorAs(repoClient),
			AgentsInternal: details == AgentDetailsInternal, Place: repoPlace(ctx, st.repoTop)}),
		engine.WithCache(reexpandCache{caches: st.caches, rv: rv}), engine.WithRegistry(reg), engine.WithScanner(scanner),
		engine.WithActor(actor), engine.WithAudit(st.audit), engine.WithWall(opts.Wall),
		engine.WithProvenance(newProvenance(opts, st.repoTop)), // _o place facts and the _w stamp (PROV4, PROV6, PROV8, OQ7)
	}
	for _, o := range attached {
		a := o.Attached
		a.KeyNamespace = keyNamespace(a)
		a.Place = placeOf(ctx, a) // FQ4: sto_ id (+ "/<folder prj_ id>") of a marked store; "" keeps the attached name
		engOpts = append(engOpts, engine.WithStore(a))
	}
	// FQ11 / FA-AGENT-6: this machine's stricter local policy values.
	machinePol, polNotes, err := machinePolicy(opts.Home)
	if err != nil {
		return fail(err)
	}
	if opts.Notice != nil && !repoOnly {
		for _, n := range polNotes {
			opts.Notice(n)
		}
	}
	engOpts = append(engOpts, engine.WithMachinePolicy(machinePol))
	// P3-LOCAL-2: this machine's machine store, when a table needs it.
	if engOpts, err = st.withMachine(ctx, opts.Home, reg, exp, engOpts); err != nil {
		return fail(err)
	}
	st.defaultTarget = store.SourceRepo
	if st.eng, err = engine.New(engOpts...); err != nil {
		return fail(err)
	}
	scanner.bindEngine(st.eng) // D8: secrets.use_repo_gitleaks decides whether the repo .gitleaks.toml allow applies
	if err = st.refreshAll(ctx); err != nil {
		return fail(err)
	}
	// TEAM_PROFILES 6.3 (TP-4): a second email or login of a person writes
	// as their one actor; the contacts are readable only now, after the
	// refresh, and nothing has been written yet.
	if err = st.canonicalActor(ctx, opts.Notice); err != nil {
		return fail(err)
	}
	actor = st.actor
	st.refuseWritesIfLeft(ctx, opts.Home, attached) // leftgate.go: a removed person writes only to their personal store (B2-12)
	if repoClient && st.who.Kind == identity.KindAgent {
		// D8 / LORE_TEAM_SPEC 7.6: by default (agent_details: internal) an
		// agent in a client repo is recorded as its owning person; an org
		// that sets agent_details: client shows the agent as itself, so no
		// owner is looked up and clientActorAs stamps the agent's id.
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

// resolveActor sets st.actor (and st.who for a resolved identity) from
// opts: an explicit Actor, the ResolveActor hook, or the identity chain.
// notice prints the 10.4 stale-marker line (once per command: the full
// open, never the repo-only one too).
func (st *state) resolveActor(ctx context.Context, opts Options, notice bool) error {
	switch {
	case !opts.Actor.IsZero():
		st.actor = opts.Actor
	case opts.ResolveActor != nil:
		a, err := opts.ResolveActor(ctx)
		if err != nil {
			return err
		}
		st.actor = a
	default:
		who, err := resolveIdentity(ctx, opts)
		if err != nil {
			return err
		}
		if st.actor, err = who.ActorID(); err != nil { // the first key's id after an email change
			return err
		}
		st.who = who
		if who.Detection != nil && who.Detection.Note != "" && opts.Notice != nil && notice {
			opts.Notice(who.Detection.Note)
		}
	}
	return nil
}

// dryClientNote is repoIsClient's note under a dry run that has not the
// org's stores to check against (B4-14).
const dryClientNote = "whether this is a client repo is checked by the next run that is not a dry run (its org's stores are not on this machine yet; a dry run fetches none)"

// repoIsClient asks opts.RepoIsClient whether the repo store is a client
// repo. No hook, or an error, answers client (fail closed: an internal
// row must never be pointed at, nor a teammate's email written, from a
// customer's repository), with the reason as note.
func repoIsClient(ctx context.Context, opts Options) (client bool, note string) {
	if opts.RepoIsClient == nil {
		return true, "the repo is treated as a client repo: no client-repo check is wired"
	}
	client, err := opts.RepoIsClient(ctx)
	if err != nil && git.NoClone(ctx) && (errs.Has(err, errs.LinkUnknown) || errs.Has(err, errs.StoreUnreachable)) {
		// B4-14: a dry run fetches and clones no store, so the org's links
		// are not here yet; it still fails closed, but says why instead of
		// calling an own repo a client repo
		return true, dryClientNote
	}
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
	if err = cleanStoresFileURLs(repoTop, cfg); err != nil {
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
	// ONBOARDING 3.4: the active setup's personal_store replaces it.
	// B4-22: a lore 0.1.17 local personal store already attached as `me`
	// (personal.store: local, localPersonalStores) is never attached a
	// second time from a setup's personal_store: the 0.1.17 one is read
	// until lore migrate run (or the setup step) moves it
	v017Me := slices.ContainsFunc(cfg.Stores, func(s attach.Store) bool { return s.Name == store.Source(attach.ReservedMe) })
	if p := effectivePersonalURL(ctx, home, optsProjectDir(opts), mc); p != "" && !v017Me {
		me := attach.Store{Name: store.Source(attach.ReservedMe), URL: p}
		dup := false
		if pc, perr := canonical(me); perr == nil {
			for _, s := range cfg.Stores {
				if c, cerr := canonical(attach.Store{URL: s.URL, Folder: s.Folder, Branch: s.Branch}); cerr == nil && sameRoot(c, pc) {
					dup = true
				}
			}
		}
		if !dup {
			cfg.Stores = append(cfg.Stores, me)
		}
		// D10: the personal store this setup stopped using, while it holds
		// notes (personal_previous.go)
		if pb, ok := previousPersonalStore(home, setupPersonalValue(ctx, home, optsProjectDir(opts))); ok && !slices.ContainsFunc(cfg.Stores, func(s attach.Store) bool { return s.Name == pb.Name }) {
			cfg.Stores = append(cfg.Stores, pb)
		}
		// D16: lore 0.1.17's everywhere/ notes, read-only, while the
		// personal store is on the 0.1.17 layout (lore migrate run moves
		// them to _store/)
		if ev, ok := personalEverywhereStore(home, p); ok && !slices.ContainsFunc(cfg.Stores, func(s attach.Store) bool { return s.Name == ev.Name }) {
			cfg.Stores = append(cfg.Stores, ev)
		}
		// D5: lore 0.1.17's notes for this repo's org keep their folder
		// (personal_orgs.go)
		if mo, ok := personalOrgStore(ctx, opts, p); ok && !slices.ContainsFunc(cfg.Stores, func(s attach.Store) bool { return s.Name == mo.Name }) {
			cfg.Stores = append(cfg.Stores, mo)
		}
	}
	raws := make([]string, 0, len(cfg.Stores))
	for i, s := range cfg.Stores {
		if cfg.Stores[i].URL, err = canonical(s); err != nil {
			return nil, nil, errs.Prefix("wire: store "+string(s.Name), err)
		}
		raws = append(raws, s.URL)
	}
	openers, err := storeOpeners(reg, originsOf(raws...), opts)
	if err != nil {
		return nil, nil, err
	}
	var strict, maybe attach.Config
	for _, s := range cfg.Stores {
		// a dry run clones nothing (git.NoClone): every store without a
		// clone here is left out as unreachable, the way an org store is
		if optional[s.Name] || git.NoClone(ctx) {
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
	afterAttach(ctx, home, repoTop, opened, urls) // orgsource.go, storeaddr.go: SOURCE_OF_TRUTH rows 7 and 9
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
// cannot parse it), a credential is cleaned on read (attach.CleanURL:
// password, token user; SECRETS 14.2 row 16, OV4; never echoed; the commit
// hook still refuses the committed file), and the entry's folder becomes the `?folder=` the backends read
// (rowfs.FolderParam), its branch the `?branch=` (git.BranchParam).
func canonical(s attach.Store) (string, error) {
	raw, err := attach.CleanURL(s.URL)
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

// sameRoot reports whether two canonical urls name the same store place.
// canonical leaves a file:// path as written, so "file:///x/" and
// "file:///x" (one folder) would load the personal store twice under two
// names; a trailing slash on the path is not a different place.
func sameRoot(a, b string) bool {
	if a == b {
		return true
	}
	ua, ea := url.Parse(a)
	ub, eb := url.Parse(b)
	if ea != nil || eb != nil || ua.Scheme != folder.Scheme || ub.Scheme != folder.Scheme {
		return false
	}
	trim := func(u *url.URL) string {
		c := *u
		if p := strings.TrimRight(c.Path, "/"); p != "" {
			c.Path = p
		}
		c.RawPath = ""
		return c.String()
	}
	return trim(ua) == trim(ub)
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
// git clone signs commits with when git has none (FLAG4, storeOpeners).
// owner, when not nil, names the org identity that signs every commit of
// a store url it answers for, whatever git knows (Q-R2-1). notice, when not
// nil, hears the merge report of a store sync (storeMergeReport).
// identityFor, when set, is the person's signature for one store url
// (storeSigner.identityFor): it replaces who's fallback identity for that
// store, so a personal store signs as the setup that owns it (item 39).
func newOpeners(reg *schema.Registry, origins map[string]string, who git.Option, owner func(context.Context, string) (git.Signature, bool), identityFor func(context.Context, string) git.Signature, notice func(string)) (*store.Openers, error) {
	o := store.NewOpeners()
	if err := o.Register(folder.Scheme, folder.NewOpener()); err != nil {
		return nil, err
	}
	base := []git.Option{git.WithOrigins(origins), who}
	gitOpen := func(ctx context.Context, u *url.URL, oo store.OpenOptions) (store.Backend, error) {
		// One merger per opened store, so its report names the store.
		opts := append(slices.Clone(base), git.WithMerger(merge.Driver{Registry: reg, Report: storeMergeReport(u, notice)}))
		// Q-R2-1: a store of an org with an identity is always signed by
		// that identity, never by the person's own git email (spec 6.5).
		if owner != nil {
			if sig, ok := owner(ctx, u.String()); ok {
				opts = append(opts, git.WithSigner(sig))
			}
		}
		if identityFor != nil {
			remote := u.String()
			opts = append(opts, git.WithIdentity(func(ctx context.Context) git.Signature { return identityFor(ctx, remote) }))
		}
		b, err := git.NewOpener(opts...)(ctx, u, oo)
		if err == nil {
			sayCloneIdentity(ctx, b, notice) // the clone got its git identity (storeidentity.go)
		}
		return b, err
	}
	// store.PathScheme: a plain path is a git repository on this machine
	// (wire/storelocalpath.go); a folder store is always file://.
	for _, s := range append(slices.Clone(git.Schemes), store.PathScheme) {
		if err := o.Register(s, gitOpen); err != nil {
			return nil, err
		}
	}
	return o, nil
}

// storeMergeReportPrefix starts every line of a store sync's merge report.
const storeMergeReportPrefix = "lore sync: store "

// storeMergeReport is the merge report of one git store's sync, the same
// outcomes `lore merge-driver` prints for a repo row (TE-F-1, TE-F-2): a
// field both sides changed that the newer edit settled, and a soft delete
// that lost to an edit or two deletes that met (DELETION 3.7). It names
// the store (its url without credentials or query), the row and the
// outcome; `lore trash list --overruled` lists the rows kept alive. A row
// whose merge left conflict markers is refused by the sync, so nothing is
// said for it. nil notice drops the report.
func storeMergeReport(u *url.URL, notice func(string)) func(merge.Result) {
	if notice == nil {
		return nil
	}
	shown := *u
	shown.User, shown.RawQuery, shown.Fragment = nil, "", ""
	where := shown.String()
	return func(res merge.Result) {
		if !res.Clean() {
			return
		}
		at := storeMergeReportPrefix + where + ": " + string(res.Row.Table) + "/" + res.Row.ID.String() + ": "
		if len(res.NewerWins) > 0 {
			// TE-F-2: a field both sides changed is settled newer-wins;
			// the teammate's older value never vanishes silently.
			notice(at + "both sides changed " + strings.Join(res.NewerWins, ", ") + "; kept the newer edit")
		}
		if res.Trash != "" {
			notice(at + string(res.Trash))
		}
	}
}

// markerOf is implemented by both 0.1.18 backends: the store's
// lore-store.yaml.
type markerOf interface{ Marker() rowfs.Marker }

// storeDirOf is implemented by the git and folder backends: the folder
// that holds lore-store.yaml.
type storeDirOf interface{ Dir() string }

// currentMarker is b's lore-store.yaml: the one read at open, or, when
// that one had no sto_ id (a v0.1.17 marker), the file on disk now. A
// `lore fix` in this same process may have given the store its id since
// (migrate writes marker v2), and the private render and the store audit
// file must use that id, not a name- stem for one run and the id after
// (B4-17: two names for one store). ok is false for a backend without a
// marker.
func currentMarker(b store.Backend) (rowfs.Marker, bool) {
	c := concrete(b)
	m, ok := c.(markerOf)
	if !ok {
		return rowfs.Marker{}, false
	}
	mk := m.Marker()
	if mk.ID.IsZero() {
		if d, dok := c.(storeDirOf); dok && d.Dir() != "" {
			if fresh, err := rowfs.ReadMarker(d.Dir()); err == nil && !fresh.ID.IsZero() {
				mk = fresh
			}
		}
	}
	return mk, true
}

// branchOf is implemented by the git backend.
type branchOf interface{ Branch() string }

// cachePath is <home>/cache/stores/<store id>/<branch>/<folder>.db (CFG3
// refined + CFG fixups: named by permanent ids so every repo that attaches
// the same store folder shares one file, whatever it calls the store). A
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
	dir := filepath.Join(writableHome(home), cacheDir, cacheStoresDir, id, filepath.FromSlash(branch))
	path := filepath.Join(dir, filepath.FromSlash(name)+cacheExt)
	if err := mkCacheDir(filepath.Dir(path)); err != nil {
		return "", err
	}
	return path, nil
}

// mkCacheDir creates a cache folder under the lore home. A home lore may
// not write (permissions, a read-only file system) is refused naming the
// folder and the fix: the caches of code repos live there too, so even a
// read needs it (the repo cache left the work tree).
func mkCacheDir(dir string) error {
	err := os.MkdirAll(dir, cacheDirMode)
	if err == nil {
		return nil
	}
	if errors.Is(err, fs.ErrPermission) || errors.Is(err, syscall.EROFS) {
		return errs.Wrap(errs.ReadOnlyFS, "lore cannot create its cache folder "+dir+": the lore home (~/.lore) is not writable", err).
			WithHint("make the lore home writable (chmod u+w on it and on its cache folder), then run the command again")
	}
	return errs.Wrap(errs.Internal, "wire: create the cache folder "+dir, err)
}

// cacheOpen is the sqlite cache constructor with the expander it needs.
func cacheOpen(ctx context.Context, path string, reg *schema.Registry, exp sqlite.Expander) (*sqlite.Cache, error) {
	return sqlite.Open(ctx, path, reg, exp)
}

// combinedCachePath is <home>/cache/combined/<sha256 of the repo top>.db:
// one file per code repo, outside the repo so it never shows in git status.
func combinedCachePath(home, repoTop string) (string, error) {
	if err := mkCacheDir(filepath.Join(writableHome(home), cacheDir, combinedDir)); err != nil {
		return "", err
	}
	return hashedCacheFile(home, combinedDir, repoTop), nil
}

// hashedCacheFile is <home>/cache/<dir>/<sha256 of repoTop>.db, the name of
// the combined and the remote-linked repo caches; nothing is created.
func hashedCacheFile(home, dir, repoTop string) string {
	sum := sha256.Sum256([]byte(repoTop))
	return filepath.Join(writableHome(home), cacheDir, dir, hex.EncodeToString(sum[:])+cacheExt)
}

// RepoCachePath is the repo store's cache of the checkout at repoTop:
// <home>/cache/repo/<folder name>-<12 hex of sha256(path)>.db. The folder
// name makes the file readable (`lore where` and ~/.lore/README.md list
// it); the hash of the (symlink-resolved) path gives two checkouts of one
// name two files. Nothing is created.
func RepoCachePath(home, repoTop string) string {
	return filepath.Join(writableHome(home), cacheDir, filepath.FromSlash(sqlite.RepoCacheRel(repoTop)))
}

// repoCacheOf is the cache file st's repo store was opened at ("" before
// the caches opened).
func (st *state) repoCacheOf() string {
	if st.caches == nil {
		return ""
	}
	st.caches.mu.Lock()
	defer st.caches.mu.Unlock()
	return st.caches.paths[store.SourceRepo]
}

// remoteRepoDir holds the repo caches of repos that hold no lore data
// (<home>/cache/remote/<sha256 of the repo top>.db).
const remoteRepoDir = "remote"

// remoteRepoCachePath is the repo store's cache of a repo linked by its
// remote with no .lore/ of its own: outside the repo, one file per
// checkout, so nothing is ever created in the client's repository.
func remoteRepoCachePath(home, repoTop string) (string, error) {
	if err := mkCacheDir(filepath.Join(writableHome(home), cacheDir, remoteRepoDir)); err != nil {
		return "", err
	}
	return hashedCacheFile(home, remoteRepoDir, repoTop), nil
}

// linkedByRemote is the remote a store link names the repo of opts by
// when the repo holds no lore data (no .lore/ at its top); "" when it has
// .lore/, is not a git repo, or no store links it by its remote.
func linkedByRemote(ctx context.Context, opts Options) string {
	if opts.RepoLinkedByRemote == nil {
		return ""
	}
	remote := opts.RepoLinkedByRemote(ctx) // "" for a repo with its own project id: no git call below
	if remote == "" {
		return ""
	}
	top, err := git.RepoRoot(ctx, opts.RepoDir, opts.Home)
	if err != nil || loreFolderAt(top) {
		return ""
	}
	return remote
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

// repoPlace is the repo store's place id (PROVENANCE 5.4, PROV-MOVED-ALL):
// the checkout's rep_ id from .lore/repo.json, so a Lore-Moved-To trailer,
// a `moves` entry and a kind's `copies` item name the repo by its
// permanent id, never by the local name "repo". "" when the checkout has
// no repo id yet: the engine then records the attached name "repo". It is
// read once at open, so a `lore repo fork` in the same process is seen by
// the next open.
func repoPlace(ctx context.Context, top string) string {
	if id, ok := repoIDAt(ctx, top); ok {
		return id
	}
	return ""
}

// placeOf is an attached store's place id for `_o.moves` (PROVENANCE 5.4,
// FQ4): its marker's sto_ id, plus "/<folder prj_ id>" when the backend
// is a folder of the store (the folder's _meta.json project_id, stable
// before its first write: rowfs.Folder.ProjectID), so a folder rename
// never changes a recorded place. Unlike keyNamespace it is not hashed
// into row ids, so it may differ from it. An unreadable _meta.json falls
// back to keyNamespace's "<sto_ id>/<folder name>" rather than failing the
// open; "" when the store has no sto_ id.
func placeOf(ctx context.Context, a engine.Attached) string {
	c := concrete(a.Backend)
	mb, ok := c.(markerBackend)
	if !ok || mb.Marker().ID.IsZero() {
		return ""
	}
	fb, ok := c.(folderIDOf)
	if !ok {
		return keyNamespace(a)
	}
	pid, err := fb.FolderProjectID(ctx)
	if err != nil {
		return keyNamespace(a)
	}
	if pid.IsZero() {
		return mb.Marker().ID.String() // the store root has no folder id
	}
	return mb.Marker().ID.String() + "/" + pid.String()
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

// cleanStoresFileURLs rewrites .lore/stores.yaml when a url in it holds a
// credential (a password or a token-shaped user, attach.NormalizeURL): the file
// keeps everything else, the url loses the credential, and one line on
// stderr says so, naming the store and never the removed text (DECISION
// P2-CTX-6: SECRETS 14.2 row 16 cleans on read and writes back "on the
// next write"; for stores.yaml the safest next write is now, so the token
// does not stay on disk or reach a commit). A file with nothing to clean
// is never touched.
func cleanStoresFileURLs(top string, cfg attach.Config) error {
	dirty := map[string]string{} // raw url → clean url
	var names []string
	for _, s := range cfg.Stores {
		// NormalizeURL is the add-time judge (password or token user, in
		// https, ssh:// and scp form); CleanURL is the url without it. A
		// scp url with a token user becomes ssh:// (the scp form has no
		// way to drop its user); one Strip cannot read stays as written
		// and Open refuses it.
		if _, err := attach.NormalizeURL(s.URL); !errs.Has(err, errs.URLUserinfo) {
			continue
		}
		if clean, err := attach.CleanURL(s.URL); err == nil {
			dirty[s.URL] = clean
			names = append(names, string(s.Name))
		}
	}
	if len(dirty) == 0 {
		return nil
	}
	err := editStoresFile(top, func(list *yaml.Node) error {
		for _, n := range list.Content {
			if u := mapGet(n, keyURL); u != nil {
				if clean, ok := dirty[u.Value]; ok {
					u.Value = clean
				}
			}
		}
		return nil
	})
	if err != nil {
		return errs.Prefix("wire: remove a credential from "+attach.FileName, err)
	}
	// The composition root is the one layer that may talk to the terminal.
	fmt.Fprintf(os.Stderr, "lore: %s: a credential was in the url of store %s; lore removed it from %s (use your git credential helper; rotate it if it was ever committed)\n",
		engine.WarnURLUserinfo, strings.Join(names, ", "), attach.FileName)
	return nil
}

// The store-only open (storeOnlyApp → openDetached) builds the core with no
// repo store: only one store folder is opened (BUG-T3, FA-AGENT-9). It
// serves the roster commands run outside any repo (`lore actor register`,
// `lore org import`'s register step; StoreOnlyRoster) and a write naming
// its store with --to there (targetoutside.go). Each place open uses the repo gets an explicit
// answer here:
//
//   - repo store: none; the engine's only store is target, which is also
//     the default target, so a write can land nowhere else.
//   - stores.yaml, `--to` extras, the personal store `me`: not read or
//     attached (they are the repo's or the person's, not the roster's).
//   - repo cache (.lore/lore-cache.db): none; target keeps its usual cache
//     under ~/.lore/cache/stores, and no cache use record is written
//     (FLAG3 records a repo's caches; there is no repo).
//   - scanner: no repo .gitleaks.toml allow (withRepoGitleaks("")), and no
//     tool-session source (it lives in the repo's lore.db).
//   - audit: target's machine-local store trail (routedAudit); the repo
//     sink is storeOnlyAudit, which refuses any event naming the repo.
//   - provenance: no repo id, dir or git facts (GitNone); _w (version,
//     machine) as usual.
//   - client-repo check, agent owner (D8): no repo, so not a client; an org
//     store is internal.
//   - format gate (O6): the repo's never asks; target's v0.1.17 question
//     still applies (readStoreFormats).

// orgStoreNamed is the org store folder target of Options.OrgStores, or
// E_NOT_FOUND when the setup configures none by that name.
func orgStoreNamed(ctx context.Context, opts Options, target store.Source) (attach.Store, error) {
	org := rawOrgStores(ctx, opts)
	idx := slices.IndexFunc(org, func(s attach.Store) bool { return s.Name == target })
	if idx < 0 {
		return attach.Store{}, errs.New(errs.NotFound, "roster: no org store named "+string(target)+" is configured").
			WithHint("the org entry in ~/.lore/config.yaml names the store and its org_folder")
	}
	return org[idx], nil
}

// openDetached is the store-only open: the core with no repo store and
// only one (when one is not nil) attached, as its default target; with
// one nil it is the machine-only open (openMachineOnly): the machine
// store alone, the default target. Every answer listed on the store-only open
// holds for both.
func openDetached(ctx context.Context, opts Options, one *attach.Store) (*state, error) {
	return openDetachedHere(ctx, opts, one, "")
}

// openDetachedHere is openDetached; a non-empty here names the attached
// store that answers for the repo store (engine.WithRepoAlias, storehere.go).
func openDetachedHere(ctx context.Context, opts Options, one *attach.Store, here store.Source) (*state, error) {
	st := &state{actorDone: map[store.Source]bool{}}
	if err := st.resolveActor(ctx, opts, true); err != nil {
		return nil, err
	}
	reg, err := builtinRegistry()
	if err != nil {
		return nil, err
	}
	if opts.LockTimeout == 0 { // FA-AGENT-8
		if opts.LockTimeout, err = readLockTimeout(opts.Home); err != nil {
			return nil, err
		}
	}
	target := store.SourceMachine
	var attached []attach.Opened
	if one != nil {
		target = one.Name
		raw := one.URL
		if one.URL, err = canonical(*one); err != nil {
			return nil, errs.Prefix("wire: store "+string(one.Name), err)
		}
		openers, oerr := storeOpeners(reg, originsOf(raw), opts)
		if oerr != nil {
			return nil, oerr
		}
		if attached, err = attach.Open(ctx, attach.Config{Stores: []attach.Store{*one}}, openers, opts.openOptions()); err != nil {
			return nil, err
		}
	}
	fail := func(err error) (*state, error) { return nil, errors.Join(err, st.close()) }
	for i := range attached {
		st.backends = append(st.backends, attached[i].Backend)
		attached[i].Level = levelOf(attached[i])
		attached[i].Location = location(one.URL, attached[i].Folder)
		if opts.WrapBackend != nil {
			if attached[i].Backend, err = wrapBackend(ctx, opts, attached[i].Name, attached[i].Folder, attached[i].Backend); err != nil {
				return fail(err)
			}
		}
	}
	st.caches = newCaches()
	rv := newResolver(st)
	exp := refs.Expander{Resolver: rv}
	st.byName = map[store.Source]store.Backend{}
	st.folders = map[store.Source]string{}
	for _, o := range attached {
		path, pathErr := cachePath(opts.Home, o)
		if pathErr != nil {
			return fail(pathErr)
		}
		if err = st.caches.open(ctx, o.Name, path, reg, exp); err != nil {
			return fail(err)
		}
		st.byName[o.Name] = o.Backend
		st.folders[o.Name] = o.Folder
	}
	if err = st.settleAgentKey(ctx, st.byName); err != nil {
		return fail(err)
	}
	scanner, err := newScanner(ctx, st.byName, withRepoGitleaks(""), withFolders(st.folders), withLazyBuild()) // SECRETS 3.8, P3-SEC-5
	if err != nil {
		return fail(err)
	}
	ra := newRoutedAudit(storeOnlyAudit{}, opts.Home, attached)
	ra.rowFile = st.rowFileOf
	st.audit = ra
	machinePol, polNotes, err := machinePolicy(opts.Home)
	if err != nil {
		return fail(err)
	}
	if opts.Notice != nil {
		for _, n := range polNotes {
			opts.Notice(n)
		}
	}
	engOpts := []engine.Option{
		engine.WithCache(reexpandCache{caches: st.caches, rv: rv}), engine.WithRegistry(reg), engine.WithScanner(scanner),
		engine.WithActor(st.actor), engine.WithAudit(st.audit), engine.WithWall(opts.Wall),
		engine.WithProvenance(storeOnlyProvenance{inner: newProvenance(opts, "")}),
		engine.WithMachinePolicy(machinePol),
	}
	for _, o := range attached {
		a := o.Attached
		a.KeyNamespace = keyNamespace(a)
		a.Place = placeOf(ctx, a)
		engOpts = append(engOpts, engine.WithStore(a))
	}
	if engOpts, err = st.withMachine(ctx, opts.Home, reg, exp, engOpts); err != nil { // P3-LOCAL-2
		return fail(err)
	}
	st.defaultTarget = target
	if here != "" {
		engOpts = append(engOpts, engine.WithRepoAlias(here))
	}
	if one == nil {
		// The machine store is never one of Stores(), so the engine's
		// own default (the first store) has nothing to pick.
		engOpts = append(engOpts, engine.WithDefaultTarget(store.SourceMachine))
	}
	if st.eng, err = engine.New(engOpts...); err != nil {
		return fail(err)
	}
	scanner.bindEngine(st.eng)
	if err = st.refreshAll(ctx); err != nil {
		return fail(err)
	}
	if err = st.canonicalActor(ctx, opts.Notice); err != nil { // TEAM_PROFILES 6.3, as in open
		return fail(err)
	}
	st.refuseWritesIfLeft(ctx, opts.Home, attached) // leftgate.go: inside a store clone too (B2-25)
	if st.storeFormat, err = readStoreFormats(ctx, opts, "", st.byName, st.actor); err != nil {
		return fail(err)
	}
	return st, nil
}

// storeOnlyAudit is the repo audit sink of a store-only open: there is no
// repo, so an event naming it is refused (fail closed: it would mean a
// write reached a store that is not attached); every other event already
// went to its store's machine-local trail (routedAudit).
type storeOnlyAudit struct{}

// Record implements engine.AuditSink.
func (storeOnlyAudit) Record(_ context.Context, ev engine.Event) error {
	if ev.Store == store.SourceRepo {
		return errs.New(errs.NotAStore, "wire: no repo is open (a roster command run outside a repo); nothing is recorded for it")
	}
	return nil
}

// storeOnlyProvenance is the provenance of a store-only open: the command
// runs in no checkout, so there is no repo id, dir or git state to
// record (PROV6: never an absolute path); the writer facts (_w) stay.
type storeOnlyProvenance struct{ inner *provenance }

// Place implements engine.Provenance: no git facts.
func (storeOnlyProvenance) Place(context.Context) origin.Facts {
	return origin.Facts{Git: origin.Git{State: origin.GitNone}}
}

// Upstream implements engine.Provenance: no repo, no upstream.
func (storeOnlyProvenance) Upstream(context.Context, string) bool { return false }

// Writer implements engine.Provenance.
func (p storeOnlyProvenance) Writer() (string, ids.MachineID) { return p.inner.Writer() }

// LinkedRemoteProject is the project id an org store link gives the repo
// of a by its remote (orgresolve.RemoteProjectID(remote), the only place
// that id lives), for `lore init` in a repo that held no lore files when
// the process started (B1-18): the project then takes it, so the link
// keeps naming the repo and its store folder's rows keep reaching it.
// The caller checks that: init's own RootAt has made .lore/ by now, so the
// "no .lore/" test of linkedByRemote cannot be used here.
func (a *App) LinkedRemoteProject(ctx context.Context) (ids.ID[ids.Project], string, bool) {
	if a.opts.RepoLinkedByRemote == nil {
		return ids.ID[ids.Project]{}, "", false
	}
	remote := a.opts.RepoLinkedByRemote(ctx)
	if remote == "" {
		return ids.ID[ids.Project]{}, "", false
	}
	raw, err := orgresolve.RemoteProjectID(remote)
	if err != nil {
		return ids.ID[ids.Project]{}, "", false
	}
	pid, err := ids.Parse[ids.Project](raw)
	if err != nil {
		return ids.ID[ids.Project]{}, "", false
	}
	return pid, remote, true
}
