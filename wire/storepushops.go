package wire

// storepushops.go — the App as opsstores.Pusher: `lore store set`,
// `lore store compact`, `lore store show <store>` and the per-store mode
// lines of `lore doctor` and `lore sync status` (storepushmode.go says
// where the settings live and who may change them).
//
//	store set me k=v       ─► config.yaml personal.<k> (this machine)
//	store set <org store>  ─► admin? ─► lore-store.yaml, one commit, pushed
//	store compact <store>  ─► personal, or admin of the org; no other clone
//	                          on this machine holds unpushed work ─► git
//	                          Backend.Compact (epoch +1, lease push)

import (
	"context"
	"fmt"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/thesatellite-ai/lore/adapters/backend/git"
	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/engine/attach"
	"github.com/thesatellite-ai/lore/logic/onboard"
	"github.com/thesatellite-ai/lore/logic/storepush"
	"github.com/thesatellite-ai/lore/model/errs"
	opsstores "github.com/thesatellite-ai/lore/ops/stores"
	"github.com/thesatellite-ai/lore/store"
)

var _ opsstores.Pusher = (*App)(nil)

// storeSetSubject starts the commit of an org store's settings.
const storeSetSubject = "lore: store set "

// openGitStore opens src's store as a git clone (the caller closes it).
func (a *App) openGitStore(ctx context.Context, src attach.Store) (*git.Backend, func(), error) {
	canon, err := canonical(attach.Store{URL: src.URL, Branch: src.Branch})
	if err != nil {
		return nil, nil, err
	}
	if _, perr := url.Parse(canon); perr != nil {
		return nil, nil, errs.New(errs.StoreScheme, "wire: the store url does not parse")
	}
	b, err := a.openStore(ctx, canon, src.URL)
	if err != nil {
		return nil, nil, err
	}
	gb := gitOf(b)
	if gb == nil || !gb.CloneMode() {
		_ = b.Close()
		return nil, nil, errs.New(errs.StoreScheme, "wire: the store has no git clone")
	}
	return gb, func() { _ = b.Close() }, nil
}

// StorePush implements opsstores.Pusher.
func (a *App) StorePush(ctx context.Context, name string) (opsstores.PushView, error) {
	if name == attach.ReservedMe {
		// The personal store: a folder store has no push settings, but
		// show still says where it is and which setups share it.
		src, err := a.findStore(ctx, "store show", name)
		if err != nil {
			return opsstores.PushView{}, err
		}
		if strings.HasPrefix(src.URL, folderURLScheme) {
			return opsstores.PushView{Store: name, Place: opsstores.PlacePersonal, Folder: true,
				URL: src.URL, SharedBy: a.personalSharedBy(src.URL)}, nil
		}
	}
	src, place, org, err := a.placeOf(ctx, "store show", name)
	if err != nil {
		return opsstores.PushView{}, err
	}
	gb, done, err := a.openGitStore(ctx, src)
	if err != nil {
		return opsstores.PushView{}, err
	}
	defer done()
	place, org = refinePlace(gb.Dir(), place, org)
	v := a.pushView(ctx, name, gb, place, org)
	if place == placePersonal {
		v.URL, v.SharedBy = src.URL, a.personalSharedBy(src.URL)
	}
	return v, nil
}

// personalSharedBy names the live setups whose personal store is url: their
// own personal_store, or the shared personal.store they inherit. Display
// only: a config that does not read names none.
func (a *App) personalSharedBy(url string) []string {
	cfg, err := ReadSetupsConfig(a.opts.Home)
	if err != nil {
		return nil
	}
	same := func(u string) bool {
		u = strings.TrimSpace(u)
		if u == "" {
			return false
		}
		if u == url {
			return true
		}
		x, xerr := canonical(attach.Store{URL: u})
		y, yerr := canonical(attach.Store{URL: url})
		return xerr == nil && yerr == nil && x == y
	}
	var out []string
	for _, e := range cfg.Setups {
		switch ps := strings.TrimSpace(e.PersonalStore); ps {
		case onboard.PersonalStoreNone:
		case "", onboard.PersonalStoreInherit:
			if same(cfg.SharedStore) {
				out = append(out, e.Name)
			}
		default:
			if same(ps) {
				out = append(out, e.Name)
			}
		}
	}
	return out
}

// pushView is one store's settings and state.
func (a *App) pushView(ctx context.Context, name string, gb *git.Backend, place pushPlace, org string) opsstores.PushView {
	v := opsstores.PushView{Store: name, Place: opsstores.PlacePersonal}
	switch place {
	case placeOrg:
		v.Place, v.Org, v.Admin = opsstores.PlaceOrg, org, isOrgAdmin(a.opts.Home, org)
	case placeStore:
		v.Place = opsstores.PlaceStore
	}
	s, err := storepush.FromRaw(a.rawSettings(gb, place))
	if err != nil {
		v.Problem, s = err.Error(), storepush.Default()
	}
	v.Push, v.PushEvery = string(s.Push), storepush.FormatAge(s.PushEvery)
	v.Compact = storepush.CompactOff
	if s.Compact {
		v.Compact = storepush.CompactOn
	}
	if s.CompactAfter > 0 {
		v.CompactAfter = storepush.FormatAge(s.CompactAfter)
	}
	v.Summary = s.Describe()
	v.Epoch = gb.Epoch(ctx)
	v.Unpushed, _ = gb.Unpushed(ctx) // display only
	if oldest, oerr := gb.OldestUnpushed(ctx); oerr == nil && !oldest.IsZero() {
		now := a.opts.Wall()
		v.WaitingSince = oldest.Local().Format(time.RFC3339)
		v.WaitingFor = storepush.FormatAge(now.Sub(oldest).Truncate(time.Minute))
		if now.Sub(oldest) < time.Minute {
			v.WaitingFor = "under 1m"
		}
		v.Stale = storepush.Stale(oldest, now)
	}
	if t := readStamp(git.StateDirOf(gb.Dir()), pushStampFile); !t.IsZero() {
		v.LastPush = t.Local().Format(time.RFC3339)
	}
	return v
}

// normalized is set's values spelled the way the files keep them, and the
// raw settings after applying them over cur; a whole that does not hold
// together (compact=on without compact_after) is E_INVALID_INPUT.
func normalized(cur storepush.Raw, set []storepush.Assignment) (storepush.Raw, error) {
	next := cur
	for _, s := range set {
		if _, err := storepush.Default().Apply(s.Key, s.Value); err != nil {
			return cur, errs.New(errs.InvalidInput, "store set: "+err.Error()).
				WithHint("push=now|batch|manual, push_every=<interval ≥ 1m>, compact=on|off, compact_after=<age ≥ 1d>")
		}
		v := strings.ToLower(strings.TrimSpace(s.Value))
		if s.Key == storepush.KeyPushEvery || s.Key == storepush.KeyCompactAfter {
			d, err := storepush.ParseAge(s.Value)
			if err != nil {
				return cur, errs.New(errs.InvalidInput, "store set: "+s.Key+": "+err.Error())
			}
			v = storepush.FormatAge(d)
		}
		switch s.Key {
		case storepush.KeyPush:
			next.Push = v
		case storepush.KeyPushEvery:
			next.PushEvery = v
		case storepush.KeyCompact:
			next.Compact = v
		case storepush.KeyCompactAfter:
			next.CompactAfter = v
		}
	}
	if _, err := storepush.FromRaw(next); err != nil {
		return cur, errs.New(errs.InvalidInput, "store set: "+err.Error()).
			WithHint("e.g. compact=on compact_after=30d; push_every at least 1m, compact_after at least 1d")
	}
	return next, nil
}

// SetStorePush implements opsstores.Pusher.
func (a *App) SetStorePush(ctx context.Context, name string, set []storepush.Assignment) (opsstores.PushView, error) {
	src, place, org, err := a.placeOf(ctx, "store set", name)
	if err != nil {
		return opsstores.PushView{}, err
	}
	if place == placeStore {
		// A stores.yaml store may be an org's store this machine has not
		// imported: its worktree's lore-org.yaml says so.
		gb, done, oerr := a.openGitStore(ctx, src)
		if oerr != nil {
			return opsstores.PushView{}, oerr
		}
		place, org = refinePlace(gb.Dir(), place, org)
		done()
	}
	changed := false
	switch place {
	case placePersonal:
		cur, rerr := personalPushRaw(a.opts.Home)
		if rerr != nil {
			return opsstores.PushView{}, rerr
		}
		next, nerr := normalized(cur, set)
		if nerr != nil {
			return opsstores.PushView{}, nerr
		}
		if changed = next != cur; changed {
			if werr := writePersonalPush(a.opts.Home, next); werr != nil {
				return opsstores.PushView{}, werr
			}
		}
	case placeOrg, placeStore:
		if place == placeOrg && !isOrgAdmin(a.opts.Home, org) {
			return opsstores.PushView{}, errNotAdmin("store set", name, org)
		}
		c, werr := a.setOrgStorePushAt(ctx, src, name, set)
		if werr != nil {
			return opsstores.PushView{}, werr
		}
		changed = c
	}
	gb, done, err := a.openGitStore(ctx, src)
	if err != nil {
		return opsstores.PushView{}, err
	}
	defer done()
	v := a.pushView(ctx, name, gb, place, org)
	v.Changed = changed
	return v, nil
}

// setOrgStorePushAt writes set into the lore-store.yaml of the store src
// (an org's store, or a stores.yaml store; one commit, pushed); the caller
// has checked the admin of an org store. changed is false when the store
// already reads that way.
func (a *App) setOrgStorePushAt(ctx context.Context, src attach.Store, name string, set []storepush.Assignment) (bool, error) {
	changed := false
	err := a.withStoreDir(ctx, src, storeSetSubject+name, func(dir string) ([]string, error) {
		m, rerr := rowfs.ReadMarker(dir)
		if rerr != nil {
			return nil, rerr
		}
		if m.ID.IsZero() {
			return nil, errs.New(errs.OldLore, "store set: "+name+" still has a v0.1.17 "+rowfs.MarkerFile+" (no store id)").
				WithHint("run lore fix first")
		}
		next, nerr := normalized(m.Push, set)
		if nerr != nil {
			return nil, nerr
		}
		if next == m.Push {
			return nil, nil
		}
		m.Push, changed = next, true
		if werr := rowfs.WriteMarker(dir, m); werr != nil {
			return nil, werr
		}
		return []string{rowfs.MarkerFile}, nil
	})
	return changed, err
}

// writePersonalPush writes the personal: block's push keys (an unset one
// is removed), keeping the rest of config.yaml as it is.
func writePersonalPush(home string, r storepush.Raw) error {
	return editMachineConfig(home, func(root *yaml.Node) error {
		p := mapChild(root, configKeyPersonal)
		for _, kv := range [][2]string{{storepush.KeyPush, r.Push}, {storepush.KeyPushEvery, r.PushEvery}, {storepush.KeyCompact, r.Compact}, {storepush.KeyCompactAfter, r.CompactAfter}} {
			if kv[1] == "" {
				mapDelete(p, kv[0])
				continue
			}
			mapSet(p, kv[0], scalar(kv[1]))
		}
		return nil
	})
}

// CompactStore implements opsstores.Pusher.
func (a *App) CompactStore(ctx context.Context, in opsstores.CompactIn) (opsstores.CompactOut, error) {
	src, place, org, err := a.placeOf(ctx, "store compact", in.Store)
	if err != nil {
		return opsstores.CompactOut{}, err
	}
	gb, done, err := a.openGitStore(ctx, src)
	if err != nil {
		return opsstores.CompactOut{}, err
	}
	defer done()
	place, org = refinePlace(gb.Dir(), place, org)
	if place == placeOrg && !isOrgAdmin(a.opts.Home, org) {
		return opsstores.CompactOut{}, errNotAdmin("store compact", in.Store, org).WithHint("ask the org admin to run `lore store compact " + in.Store + "`; only an admin rewrites an org store's history")
	}
	s := a.storeSettings(gb, place)
	before := in.Before
	switch {
	case before != "":
	case s.CompactAfter > 0:
		before = storepush.FormatAge(s.CompactAfter)
	default:
		before = opsstores.DefaultCompactBefore
	}
	age, err := storepush.ParseAge(before)
	if err != nil {
		return opsstores.CompactOut{}, errs.New(errs.InvalidInput, "store compact: --before "+err.Error())
	}
	cutoff := a.opts.Wall().Add(-age)
	if !in.DryRun && place != placePersonal { // a shared store: other clones on this machine first
		if pending := otherClonesUnpushed(ctx, a.opts.Home, gb.Dir()); len(pending) > 0 {
			return opsstores.CompactOut{}, errs.New(errs.Refused, "store compact: another clone of "+in.Store+" on this machine holds unpushed work ("+strings.Join(pending, ", ")+"); compacting now would make it replay onto a rewritten history").
				WithHint("run `lore sync` in the repos that use that clone (or `lore store show` to find it), then compact again")
		}
	}
	res, err := gb.Compact(ctx, git.CompactRequest{Cutoff: cutoff, DryRun: in.DryRun, Squash: s.Push != storepush.ModeNow})
	if err != nil {
		return opsstores.CompactOut{}, err
	}
	if res.Nothing {
		in.DryRun = false // nothing to do is an answer, not a plan
	}
	if !in.DryRun && !res.Nothing {
		writeStamp(git.StateDirOf(gb.Dir()), compactStampFile, a.opts.Wall())
		writeStamp(git.StateDirOf(gb.Dir()), pushStampFile, a.opts.Wall())
	}
	out := opsstores.CompactOut{Store: in.Store, DryRun: in.DryRun, Before: cutoff.UTC().Format(time.DateOnly), Nothing: res.Nothing,
		Squashed: res.Squashed, Kept: res.Kept, Rows: res.Rows, FromEpoch: res.FromEpoch, ToEpoch: res.ToEpoch, Backup: res.Backup}
	if pushes := gb.TakePushes(); len(pushes) > 0 { // the rewrite's push, named in the output
		last := pushes[len(pushes)-1]
		out.Commit, out.Branch = last.Short(), last.Branch
	}
	return out, nil
}

// rawSettings is a store's settings as written: the personal: block for
// the personal store, the store's lore-store.yaml for an org's store or a
// stores.yaml store.
func (a *App) rawSettings(gb *git.Backend, place pushPlace) storepush.Raw {
	if place == placePersonal {
		r, _ := personalPushRaw(a.opts.Home) // unreadable: the defaults
		return r
	}
	m, err := rowfs.ReadMarker(gb.Dir())
	if err != nil {
		return storepush.Raw{}
	}
	return m.Push
}

// storeSettings is the effective settings of a store (default on a bad
// value).
func (a *App) storeSettings(gb *git.Backend, place pushPlace) storepush.Settings {
	s, err := storepush.FromRaw(a.rawSettings(gb, place))
	if err != nil {
		return storepush.Default()
	}
	return s
}

// PushViews implements opsstores.Pusher: the personal store, every org
// store this repo sees and every .lore/stores.yaml store, when it is a git
// store (doctor and sync status lines). A store that cannot be opened is left out.
func (a *App) PushViews(ctx context.Context) ([]opsstores.PushView, error) {
	var out []opsstores.PushView
	for _, n := range a.pushStoreNames(ctx) {
		v, err := a.StorePush(ctx, n)
		if err != nil {
			continue // the code repo, folder stores: no push mode
		}
		out = append(out, v)
	}
	return out, nil
}

// cloned reports whether this machine already holds a clone of src.
func cloned(home string, src attach.Store) bool {
	dir, _, err := git.CloneDirOf(home, src.URL, src.Branch)
	if err != nil {
		return false
	}
	return exists(filepath.Join(dir, ".git"))
}

// pushStoreNames lists the stores that may have a push mode, once each:
// me, the stores the repo's org brings, and .lore/stores.yaml's.
func (a *App) pushStoreNames(ctx context.Context) []string {
	names := []string{attach.ReservedMe}
	for _, s := range a.orgStoresHere(ctx) {
		names = append(names, string(s.Name))
	}
	if top, err := a.repoTop(ctx); err == nil {
		if cfg, rerr := readStoresFile(top); rerr == nil {
			for _, s := range cfg.Stores {
				names = append(names, string(s.Name))
			}
		}
	}
	seen := map[string]bool{}
	var out []string
	for _, n := range names {
		if !seen[n] {
			seen[n] = true
			out = append(out, n)
		}
	}
	return out
}

// autoCompact runs compact=on's compaction at most once a day per store,
// after a push that landed: the personal store, an org store from an org
// admin's machine, or a stores.yaml store of no org from any machine; a
// shared store only when this machine's other clones hold no unpushed work. Failures
// are silent (the next day tries again); a compaction is one note.
func (b *storePush) autoCompact(ctx context.Context) {
	if b.compacted || b.res == nil || b.stateDir == "" {
		return
	}
	b.compacted = true
	s := b.pushSettings()
	gb := gitOf(b.inner)
	if !s.Compact || gb == nil {
		return
	}
	now := b.res.now()
	if !storepush.AutoCompactDue(readStamp(b.stateDir, compactStampFile), now) {
		return
	}
	if b.name != store.Source(attach.ReservedMe) {
		if org := cloneOrg(b.res.home, gb.Dir()); org != "" && !isOrgAdmin(b.res.home, org) {
			return
		}
		if len(otherClonesUnpushed(ctx, b.res.home, gb.Dir())) > 0 {
			return
		}
	}
	writeStamp(b.stateDir, compactStampFile, now) // once a day, whatever the outcome
	// Inside the person's command, so bounded; a compaction cut short moved
	// nothing (the branch moves only after the remote took the rewrite).
	cctx, cancel := context.WithTimeout(ctx, 2*piggybackTimeout)
	defer cancel()
	res, err := gb.Compact(cctx, git.CompactRequest{Cutoff: s.Cutoff(now), Squash: s.Push != storepush.ModeNow})
	b.reportPushes()
	if err != nil || res.Nothing {
		return
	}
	_, _ = fmt.Fprintln(b.notes, "lore: store "+string(b.name)+": compacted its history before "+s.Cutoff(now).UTC().Format(time.DateOnly)+
		" into one snapshot ("+strconv.Itoa(res.Squashed)+" commits; every row kept; epoch "+strconv.Itoa(res.ToEpoch)+")")
}

// FlushBatches pushes every batch-mode store that holds unpushed commits
// (one batch commit each), whatever push_every says, each push bounded by
// piggybackTimeout: cmd/lore runs it after the hooks lore already has
// (the agent's session-start hook, the repo's post-commit and pre-push git
// hooks) and as the whole of the agent's session-end hook (under
// SessionEndBudget for every store together). lore runs no daemon or timer; when nothing runs
// lore the batch stays committed locally. Best effort: a store that
// cannot be pushed keeps its commits for `lore sync`.
func (a *App) FlushBatches(ctx context.Context) {
	for _, name := range a.pushStoreNames(ctx) {
		src, place, _, perr := a.placeOf(ctx, "flush", name)
		if perr != nil {
			continue
		}
		if !cloned(a.opts.Home, src) {
			continue // never clone (go to the network) from a hook: nothing can wait in a clone that does not exist
		}
		gb, done, oerr := a.openGitStore(ctx, src)
		if oerr != nil {
			continue
		}
		if a.storeSettings(gb, place).Push != storepush.ModeBatch {
			done()
			continue
		}
		if n, uerr := gb.Unpushed(ctx); uerr != nil || n == 0 {
			done()
			continue
		}
		if oerr != nil {
			continue
		}
		pctx, cancel := context.WithTimeout(ctx, piggybackTimeout) // never hold the hook up
		rep, serr := gb.SyncWith(pctx, git.SyncOptions{Squash: true})
		cancel()
		printPushes(a.pushNotes(), name, gb.TakePushes())
		if serr == nil && !rep.Offline && len(rep.Refused) == 0 {
			writeStamp(git.StateDirOf(gb.Dir()), pushStampFile, a.opts.Wall())
		}
		done()
	}
}

// SessionEndBudget bounds the whole session-end hook (`lore agent end`,
// cmd/lore sessionend.go): every store's push together gets the piggyback
// limit one push gets, so closing a session never waits longer.
const SessionEndBudget = piggybackTimeout

// pushModeLine is one store's line in `lore doctor` and `lore sync status`.
func pushModeLine(v opsstores.PushView) string {
	s := "store " + v.Store + ": " + v.Summary
	if v.Unpushed > 0 {
		age := ""
		if v.WaitingFor != "" {
			age = ", the oldest " + v.WaitingFor + " old"
		}
		s += " (" + strconv.Itoa(v.Unpushed) + " local commit(s) waiting" + age + "; `lore sync` pushes them)"
	}
	if v.Problem != "" {
		s += "; problem: " + v.Problem + " (pushing as now)"
	}
	return s
}

// doctorPushModes adds each store's push mode to `lore doctor`: a note, or
// a warning when a setting does not read.
func (a *App) doctorPushModes(ctx context.Context, warn, note func(string)) {
	views, err := a.PushViews(ctx)
	if err != nil {
		return
	}
	for _, v := range views {
		switch {
		case v.Problem != "":
			warn(pushModeLine(v))
		case v.Stale:
			warn(pushModeLine(v) + "; nothing has pushed it for over a day: run `lore sync`")
		default:
			note(pushModeLine(v))
		}
	}
}
