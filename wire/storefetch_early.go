package wire

// storefetch_early.go — a read starts the fetch of every store attached
// without an org link at once (devco:D-1029, follow-up of D-1026).
//
// Why: the first read of a session (`lore ask` after a break, every fetch
// due) waited for the repo's refs/lore/* fetch and the org lookup, and only
// then, when the operation first read a store attached by stores.yaml (or
// the personal store), did that store's sync fetch it: one more network
// round trip, after the others. An org's stores are fetched by the org
// lookup itself (its link index); every other store clone the read will
// sync is fetched now, in parallel, and its sync takes the result
// (gitbackend.StartPrefetch):
//
//	read starts ─┬─ StartStoreFetches: due clones ── go: fetch ─┐
//	             ├─ repo fetch (live, D-1026)                    │
//	             └─ org lookup (link index fetch)                │
//	op ── store's first read ── refresh ── sync ◀── takes it ───┘
//
// The decision is the store's own (storePush.refreshOnce) with the home
// config's keys: a clone whose fetch is not due, that backs off after a
// failed fetch, that a dry run or offline run would not fetch, or that has
// no clone yet starts nothing; one lore may not write never gets the clone
// lock, so it fetches nothing either. A fetch that
// failed or that no sync took changes nothing but the tracking refs.

import (
	"context"
	"net/url"
	"os"
	"time"

	"github.com/thesatellite-ai/lore/adapters/backend/folder"
	gitbackend "github.com/thesatellite-ai/lore/adapters/backend/git"
	"github.com/thesatellite-ai/lore/adapters/live"
	"github.com/thesatellite-ai/lore/adapters/orgcfg"
	"github.com/thesatellite-ai/lore/engine/attach"
	"github.com/thesatellite-ai/lore/logic/orgconf"
)

// StartStoreFetches starts the due fetch of every store clone the read in
// the project at root will sync that no org lookup fetches (stores.yaml,
// the personal store), and returns ctx carrying them for the syncs and a
// wait that the caller runs when the operation ends (no fetch outlives
// the run). root is the project folder holding .lore/ ("" = none: only the
// personal store); now is the run's clock. A write never calls it: its
// fetch decision is the write TTL's (M-4).
func (a *App) StartStoreFetches(ctx context.Context, root string, now time.Time) (context.Context, func()) {
	// the set also takes the org lookup's fetches of its store clones
	// (gitbackend.NoteFetched), so their syncs do not fetch them again
	set := gitbackend.NewPrefetches()
	ctx = gitbackend.WithPrefetches(ctx, set)
	if os.Getenv(envAutopushSwitch) == autopushOff || gitbackend.NoClone(ctx) {
		set.Started()
		return ctx, set.Wait // lore fetches nothing on its own, or a dry run
	}
	// the decision (config and stamp files) runs beside the read too: a
	// read whose stores are fresh pays nothing for it (the recall gate's
	// warm calls); a sync waits for it (Started) before it looks
	go func() {
		defer set.Started()
		a.startStoreFetches(ctx, set, root, now)
	}()
	return ctx, set.Wait
}

// startStoreFetches adds to set the early fetch of every due clone
// (StartStoreFetches).
func (a *App) startStoreFetches(ctx context.Context, set *gitbackend.Prefetches, root string, now time.Time) {
	home, project := a.opts.Home, a.projectDir()
	cfg, _, err := orgcfg.LoadConfig(home)
	if err != nil || cfg == nil {
		return // the store's own refresh reports a bad config
	}
	ttl := cfg.FetchTTL(nil)
	for _, c := range earlyFetchClones(ctx, home, root, project, cfg) {
		state := gitbackend.StateDirOf(c)
		in := live.FetchInputs{Now: now, TTL: ttl, Kind: live.FetchCached, Autopush: true, HasRemote: true,
			LastFetch: live.FetchStamp(state), LastFailure: live.FetchFailed(state), Backoff: ttl}
		if live.BackingOff(in) || !live.ShouldFetch(in) || gitbackend.NoAccessSkips(c) {
			continue
		}
		set.Add(c, gitbackend.StartPrefetch(ctx, c, gitbackend.WithRoute(func(ctx context.Context, remote string) ([]string, []string) {
			return cloneRoute(ctx, home, project, remote)
		})))
	}
}

// earlyFetchClones is the clone folder of every git store the project at
// root attaches (stores.yaml) plus the personal store, without the clones
// of any org store in config.yaml (the org lookup fetches those) and
// without duplicates. A url that does not parse is left to the open,
// which reports it.
func earlyFetchClones(ctx context.Context, home, root, project string, cfg *orgconf.Config) []string {
	var stores []attach.Store
	if root != "" {
		if cfg, err := readStoresFile(root); err == nil {
			stores = cfg.Stores
		}
	}
	if mc, err := readMachineConfig(home); err == nil {
		if p := effectivePersonalURL(ctx, home, project, mc); p != "" {
			stores = append(stores, attach.Store{URL: p})
		}
	}
	orgDirs := map[string]bool{}
	for _, o := range cfg.Orgs {
		for _, sc := range o.Stores {
			// the org lookup's clone (no branch) and a pinned branch's
			orgDirs[cloneDirOf(home, attach.Store{URL: sc.URL})] = true
			orgDirs[cloneDirOf(home, attach.Store{URL: sc.URL, Branch: sc.Branch})] = true
		}
	}
	var out []string
	seen := map[string]bool{}
	for _, s := range stores {
		d := cloneDirOf(home, s)
		if d == "" || orgDirs[d] || seen[d] {
			continue
		}
		seen[d] = true
		out = append(out, d)
	}
	return out
}

// cloneDirOf is the clone folder the git opener uses for s ("" for a
// folder store or a url that does not parse).
func cloneDirOf(home string, s attach.Store) string {
	raw, err := canonical(attach.Store{URL: s.URL, Branch: s.Branch})
	if err != nil {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == folder.Scheme {
		return ""
	}
	r, err := gitbackend.RemoteOf(u)
	if err != nil {
		return ""
	}
	dir, _, err := gitbackend.CloneDirFor(home, r, s.Branch)
	if err != nil {
		return ""
	}
	return dir
}
