package wire

// syncsetup.go — `lore sync` run outside a code repo (owner run
// 2026-11-12: from ~ it answered E_NOT_A_STORE). With no code repo in the
// folder, sync exchanges every store the setup sees, the same set
// `lore store list` shows there; inside a store's checkout
// (`cd <store>/<folder>`) it syncs that store.
//
//	App.Run(sync.run) ─▶ setupSyncHere? ─ no ─▶ the repo sync (host pass + stores)
//	                  └▶ yes ─▶ runDirect ─▶ syncRows.SyncRun ─▶ App.SyncSetup
//	SyncSetup ─▶ in a store checkout: that store (storeHereEntry)
//	          └▶ else opsstores.ListEntries, one entry per clone (url + branch)
//	   each ─▶ syncOneStore: an App over that store alone (openStoreApp:
//	           the org's identity for an org store), the pick-up of hand
//	           edits, then opsstores.SyncAll for it; a failure is that
//	           store's line and the next store still runs
//	   ─▶ one line per store; any failed (an unreachable remote included)
//	      ─▶ E_* naming each failed store and its next step, exit non-zero

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	gitbackend "github.com/thesatellite-ai/lore/adapters/backend/git"
	"github.com/thesatellite-ai/lore/adapters/fsreason"
	"github.com/thesatellite-ai/lore/adapters/orgcfg"
	"github.com/thesatellite-ai/lore/engine/attach"
	"github.com/thesatellite-ai/lore/logic/gitfault"
	"github.com/thesatellite-ai/lore/logic/repourl"
	"github.com/thesatellite-ai/lore/model/errs"
	opsstores "github.com/thesatellite-ai/lore/ops/stores"
	opssync "github.com/thesatellite-ai/lore/ops/sync"
	"github.com/thesatellite-ai/lore/store"
)

// setupSyncHere reports whether `lore sync` here is the setup's sync (no
// lore project here): the folder is a store checkout, is in no git work
// tree, or is in a git repo with no .lore folder from it up to the repo
// top (a repo nobody ran lore init in, or one linked to an org folder by
// its remote only). The last case answered E_NOT_A_STORE "a read creates
// nothing there", though the person's stores (the org folder the repo is
// linked to among them) were there to sync (batch 10 gate: once `setup
// join` stopped creating .lore in the folder it ran in, a joiner's first
// `lore sync` in that repo failed).
func (a *App) setupSyncHere(ctx context.Context) bool {
	if inStore(a.opts.RepoDir) || !insideWorkTree(ctx, a.opts.RepoDir) {
		return true
	}
	return !loreFolderUpTo(a.opts.RepoDir, gitTop(ctx, a.opts.RepoDir))
}

// setupStatusHere reports whether `lore sync status` here reads the
// setup's stores alone: where `lore sync` syncs them (setupSyncHere),
// except in a store checkout, whose status the repo path already gives.
func (a *App) setupStatusHere(ctx context.Context) bool {
	return !inStore(a.opts.RepoDir) && a.setupSyncHere(ctx)
}

// setupStatusLine opens `lore sync status` run with no code repo here: it
// says why no repo facts follow, so the store lines are not read as a
// repo's.
const setupStatusLine = "no code repo here: the stores of this setup (`lore store list` names them; `lore sync` syncs them)"

// setupBareLine follows setupStatusLine (and opens `lore sync`) in a bare
// repository: the stores still answer, but the folder is a git repo with
// no checked-out files, so the person is told what it is and the way to
// its repo status (a clone), never "git init here" (machines ReposKinds).
const setupBareLine = "this folder is a bare repository (no work tree), so it has no repo status: clone it (git clone <this folder> <new folder>) and run lore in the clone"

// bareLines is setupBareLine when dir is a bare repository, else nothing.
func bareLines(ctx context.Context, dir string) []string {
	if gitBareRepo(ctx, dir) {
		return []string{setupBareLine}
	}
	return nil
}

// setupSyncStatus is `lore sync status` with no code repo here (H-10):
// the stores' standing against their remotes (local copies only, no
// network) and the refused pushes, the parts of the repo status that do
// not need a repo.
func (a *App) setupSyncStatus(ctx context.Context) (opssync.StatusOut, error) {
	out := opssync.StatusOut{SchemaVersion: opssync.SchemaVersion, Kind: opssync.KindStatus, Lines: append([]string{setupStatusLine}, bareLines(ctx, a.opts.RepoDir)...)}
	a.syncStoreStates(ctx, &out)
	if len(out.Data.Stores) == 0 {
		out.Lines = append(out.Lines, "no stores: this folder is in no setup with a team or personal store (`lore setup list`)")
	}
	refused, err := a.StorePushRefusals(ctx)
	if err != nil {
		out.Lines = append(out.Lines, "store pushes:     unknown (local state: "+err.Error()+")")
		return out, nil
	}
	own, held := splitHeldRecords(refused)
	out.Lines = append(out.Lines, fmt.Sprintf("refused pushes:   %d", len(own)))
	for _, r := range own {
		out.Data.StoreRefusals = append(out.Data.StoreRefusals, opssync.StoreRefusal{Store: r.Store, Code: r.Code, Reason: r.Reason,
			Rows: r.Rows, At: r.At, Next: storeRefusalNextFor(r.Code)})
		out.Lines = append(out.Lines, "  store "+a.shownStoreName(ctx, r.Store)+": "+r.Code+" at "+r.At+": "+r.Reason, "    next: "+storeRefusalNextFor(r.Code))
	}
	for _, r := range held {
		on := heldOn(r, own)
		out.Data.StoreHeld = append(out.Data.StoreHeld, opssync.StoreHeld{Store: r.Store, HeldBy: opsstores.HeldNames(on), Code: r.Code,
			Rows: r.Rows, At: r.At, Next: heldNext(on)})
		out.Lines = append(out.Lines, "  store "+a.shownStoreName(ctx, r.Store)+": "+opsstores.HeldLines(on), "    next: "+heldNext(on))
	}
	return out, nil
}

// loreFolderUpTo reports whether dir or a folder above it, up to top,
// holds a .lore folder (a monorepo's project may live in a subfolder).
// An unknown top ("") answers true: nothing is changed on a guess.
func loreFolderUpTo(dir, top string) bool {
	if top == "" {
		return true
	}
	d, err := filepath.Abs(dir)
	if err != nil {
		return true
	}
	if r, rerr := filepath.EvalSymlinks(d); rerr == nil {
		d = r // git names the top by its real path (a symlinked temp folder)
	}
	t := filepath.Clean(top)
	if r, rerr := filepath.EvalSymlinks(t); rerr == nil {
		t = r
	}
	for {
		if loreFolderAt(d) {
			return true
		}
		if d == t {
			return false
		}
		up := filepath.Dir(d)
		if up == d || !strings.HasPrefix(d, t) {
			return false
		}
		d = up
	}
}

// SyncSetup is `lore sync` outside a code repo (see the file comment).
func (a *App) SyncSetup(ctx context.Context) (opssync.RunOut, error) {
	out := opssync.RunOut{SchemaVersion: opssync.SchemaVersion, Kind: opssync.KindReport, Data: opssync.RunData{Pass: opssync.PassSetup}}
	var stores []attach.Store
	if sc, in := storeHere(a.opts.RepoDir); in {
		one, err := storeHereEntry(a.opts.Home, sc)
		if err != nil {
			return out, err
		}
		out.Data.Pass = opssync.PassStore
		one.Name = store.Source(storeNameOnly(string(one.Name), one.Folder))
		one.Folder = ""
		// named as `lore store list` names it: an org's second store by its
		// short name, not by the org's name
		if n := orgStoreLabel(a.opts.Home, one.URL); n != "" {
			one.Name = store.Source(n)
		}
		stores = []attach.Store{one}
	} else {
		var err error
		if stores, err = a.setupStores(ctx); err != nil {
			return out, err
		}
		// B-9: one clone per store, each named as `lore store list` names
		// it from any repo (a client repo's linked folder is attached under
		// the org's name, so two clones were both "acme")
		for i := range stores {
			if n := orgStoreLabel(a.opts.Home, stores[i].URL); n != "" {
				stores[i].Name = store.Source(n)
			}
		}
	}
	out.Lines = bareLines(ctx, a.opts.RepoDir)
	if len(stores) == 0 {
		out.Lines = append(out.Lines, "no stores to sync: this folder is in no setup with a team or personal store (`lore setup list`, `lore store list`)")
		return out, nil
	}
	var failed []opssync.StoreReport
	var codes []errs.Code
	for _, one := range stores {
		r, code := a.syncOneStore(ctx, one)
		out.Data.Stores = append(out.Data.Stores, r)
		out.Lines = append(out.Lines, syncStoreLines(r)...)
		if r.Status == opssync.StoreStatusFailed || r.Status == opssync.StoreStatusOffline {
			failed = append(failed, r)
			codes = append(codes, code)
		}
	}
	if len(failed) == 0 {
		return out, nil
	}
	// a failed run prints no stdout: the lines go to stderr first, then
	// the error names every failed store and its next step
	for _, l := range out.Lines {
		a.notice(l)
	}
	names := make([]string, 0, len(failed))
	nexts := make([]string, 0, len(failed))
	for _, f := range failed {
		names = append(names, f.Store)
		nexts = append(nexts, f.Store+": "+f.Next)
	}
	msg := fmt.Sprintf("sync: %d of %d stores failed: %s (the other stores synced)", len(failed), len(stores), strings.Join(names, ", "))
	return out, errs.New(sameCode(codes), msg).WithHint(strings.Join(nexts, "; "))
}

// sameCode is the one code every failure shares, else E_STORE_UNREACHABLE
// when a store could not be reached, else E_STALE.
func sameCode(codes []errs.Code) errs.Code {
	for _, c := range codes[1:] {
		if c != codes[0] {
			for _, x := range codes {
				if x == errs.StoreUnreachable || x == errs.Offline {
					return errs.StoreUnreachable
				}
			}
			return errs.Stale
		}
	}
	return codes[0]
}

// storeNameOnly drops "/<folder>" from an attached store name.
func storeNameOnly(name, folder string) string {
	if folder != "" {
		if n, ok := strings.CutSuffix(name, targetSep+folder); ok {
			return n
		}
	}
	return name
}

// setupStores is the setup's store list (opsstores.ListEntries, what
// `lore store list` shows here) with one entry per clone: the folders of
// one store share its clone, so it is synced once, under its store name.
func (a *App) setupStores(ctx context.Context) ([]attach.Store, error) {
	entries, err := opsstores.ListEntries(ctx, a)
	if err != nil {
		return nil, err
	}
	var out []attach.Store
	at := map[string]int{}
	for _, e := range entries {
		key := storePlaceKey(attach.Store{URL: e.URL, Branch: e.Branch})
		name := storeNameOnly(e.Name, e.Folder)
		if i, seen := at[key]; seen {
			if e.Folder == "" { // the store's own name beats a folder's
				out[i].Name = store.Source(name)
			}
			continue
		}
		at[key] = len(out)
		out = append(out, attach.Store{Name: store.Source(name), URL: e.URL, Branch: e.Branch, ReadOnly: e.ReadOnly})
	}
	return out, nil
}

// orgStoreLabel is the name `lore store list` gives the org store at raw:
// the org's name for the store that holds its org folder (or its only
// store), else the store's short name; "" when raw is no org's store.
func orgStoreLabel(home, raw string) string {
	cfg, _, err := orgcfg.LoadConfig(home)
	if err != nil || cfg == nil {
		return ""
	}
	want, ok := storeKey(raw)
	if !ok {
		return ""
	}
	for i := range cfg.Orgs {
		o := &cfg.Orgs[i]
		main := o.OrgFolderStore()
		if main == nil && len(o.Stores) > 0 {
			main = &o.Stores[0]
		}
		for _, sc := range o.Stores {
			if k, ok := storeKey(sc.URL); !ok || k != want {
				continue
			}
			if main != nil && main.URL == sc.URL {
				return o.Name
			}
			return sc.ShortName()
		}
	}
	return ""
}

// storePlaceKey is the clone a store entry uses: its canonical url and
// branch (the folders of one store share it).
func storePlaceKey(s attach.Store) string {
	key := s.URL
	if k, ok := storeKey(s.URL); ok {
		key = k
	}
	return key + "#" + s.Branch
}

// syncOneStore syncs one store on an App over that store alone. A store
// that cannot be opened, refreshed or synced is its report's failure with
// the cause and the next step (code is its error code), never the run's.
func (a *App) syncOneStore(ctx context.Context, one attach.Store) (opssync.StoreReport, errs.Code) {
	r := opssync.StoreReport{Store: string(one.Name), URL: a.urlLabel(one.URL)}
	fail := func(err error) (opssync.StoreReport, errs.Code) {
		r.Status = opssync.StoreStatusFailed
		r.Error, r.Next = syncFailure(err, one)
		return r, errs.CodeOf(err)
	}
	x, err := openStoreApp(ctx, a.opts, orgOfURL(a.opts.Home, one.URL), one)
	if err != nil {
		return fail(err)
	}
	defer func() { _ = x.Close() }()
	x.mu.Lock()
	st := x.st
	x.mu.Unlock()
	if err = st.pickUpThenSync(ctx, x.opts); err != nil {
		return fail(err)
	}
	res, err := opsstores.SyncAll(ctx, x, string(one.Name))
	a.forgetStoreChecks(ctx, res.Stores)
	detail := "" // git's text of the call that did not reach the remote
	// this pass syncs the whole store: every folder's rows count for it,
	// the folder reports SyncAll splits off (Unattached, W-5) included;
	// the store's own report gives its state
	for _, s := range res.Stores {
		r.Sent, r.Received, r.Merged = r.Sent+s.Sent, r.Received+s.Received, r.Merged+s.Merged
		if s.Unattached {
			continue
		}
		r.Refused, r.Problems, r.Offline, r.HeldBy, r.Held = s.Refused, s.Problems, s.Offline, s.HeldBy, s.Held
		if s.Offline && detail == "" {
			detail = s.OfflineDetail
		}
	}
	if err != nil {
		return fail(err)
	}
	switch {
	case r.Offline:
		if cause, next, gone := storeGone(detail, one.URL, string(one.Name)); gone {
			// H-11: the host answered "no repository" (or refused this
			// machine): waiting or the network fixes nothing
			r.Status, r.Error, r.Next = opssync.StoreStatusFailed, cause, next
			return r, errs.StoreUnreachable
		}
		r.Status = opssync.StoreStatusOffline
		r.Error = "its remote could not be reached; local changes are kept and sent on the next sync"
		r.Next = "check the network and your access to " + r.URL + ", then run `lore sync` again (`lore doctor` explains a store it cannot reach)"
		return r, errs.StoreUnreachable
	case r.Sent > 0 && r.Received > 0:
		r.Status = opssync.StoreStatusBoth
	case r.Sent > 0:
		r.Status = opssync.StoreStatusPushed
	case r.Received > 0:
		r.Status = opssync.StoreStatusPulled
	default:
		r.Status = opssync.StoreStatusUpToDate
	}
	return r, ""
}

// storeGone reads git's text of a sync that did not reach the store at
// raw (named name): gone is false for no answer (the network, waiting
// fixes it). A host that answers "no repository" (the store moved, was
// renamed or deleted, or this person lost access) or refuses this
// machine's credentials is gone: cause says so with git's reason, next
// names the commands for each case (gitbackend.GoneSteps), never the
// network (H-11).
func storeGone(detail, raw, name string) (cause, next string, gone bool) {
	kind := gitfault.ClassifyAt(detail, fsreason.GitPathState)
	if detail == "" || kind == gitfault.Unreachable {
		return "", "", false
	}
	shown := repourl.Mask(raw)
	cause = gitfault.Describe(detail, shown, fsreason.GitPathState)
	switch kind {
	case gitfault.Missing:
		return cause + ": the host answers, but no store is there any more (it moved, was renamed or deleted, or your access was removed); local changes are kept", gitbackend.GoneSteps(name, shown), true
	case gitfault.Denied:
		return cause + "; local changes are kept", gitbackend.ProbeHint(kind, shown) + "; if your access was removed, ask an admin of the store", true
	}
	return cause + "; local changes are kept", gitbackend.ProbeHint(kind, shown), true
}

// storeURLOf is the url of the store attached here as name ("" when none
// is): the address a sync line names.
func (a *App) storeURLOf(ctx context.Context, name string) string {
	if list, err := a.ListStores(ctx); err == nil {
		for _, e := range list {
			if e.Name == name {
				return e.URL
			}
		}
	}
	for _, s := range a.orgStoresHere(ctx) {
		if string(s.Name) == name {
			return s.URL
		}
	}
	return ""
}

// syncFailure is the cause and the next step of a store that failed.
func syncFailure(err error, one attach.Store) (cause, next string) {
	cause = err.Error()
	var e *errs.Error
	if errors.As(err, &e) {
		cause = e.Message
		if h := chainHint(e); h != "" {
			return cause, h
		}
	}
	return cause, "check the store's url and your access to it (`git ls-remote " + one.URL + "`), then run `lore sync` again; `lore doctor` explains a store it cannot reach"
}

// chainHint is e's hint, or the first hint down its cause chain.
func chainHint(e *errs.Error) string {
	for c := e; c != nil; {
		if c.Hint != "" {
			return c.Hint
		}
		var next *errs.Error
		if !errors.As(c.Cause, &next) {
			return ""
		}
		c = next
	}
	return ""
}

// syncStoreLines is one store's line of the sync report, then its
// problems and, for a store that failed, its next step.
func syncStoreLines(r opssync.StoreReport) []string {
	head := "store " + r.Store + ": "
	var line string
	switch r.Status {
	case opssync.StoreStatusFailed:
		line = head + "FAILED: " + r.Error
	case opssync.StoreStatusOffline:
		line = head + "offline: " + r.Error
	case opssync.StoreStatusUpToDate:
		line = head + "up to date"
	default:
		line = fmt.Sprintf("%s%s (sent %d, received %d, merged %d)", head, r.Status, r.Sent, r.Received, r.Merged)
	}
	if r.Held != "" {
		line = head + r.Held
	}
	lines := []string{line}
	for _, p := range r.Problems {
		lines = append(lines, "  "+p)
	}
	if r.Next != "" {
		lines = append(lines, "  next: "+r.Next)
	}
	return lines
}

// openStoreApp is an App over one store and the machine store, nothing
// else: an org store writes as the org's identity (openOrgFolderApp), any
// other store as this machine's. Opening clones the store when this
// machine has no clone of it yet. The caller closes it.
func openStoreApp(ctx context.Context, opts Options, org string, one attach.Store) (*App, error) {
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
	st.eng.SetRedactHook(x.redactTask) // D21, as App.state does
	x.mu.Lock()
	x.opened, x.st = true, st
	x.cfgOpened = true
	x.cfgErr = errs.New(errs.NotAStore, "wire: only store "+string(one.Name)+" is open here").WithHint("run it inside a git repository")
	x.mu.Unlock()
	return x, nil
}
