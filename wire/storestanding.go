package wire

// storestanding.go — every store this machine and repo use (the repo's
// stores.yaml, the org's stores, the personal store `me`), each with where
// its local copy stands against its remote, read from the clone alone (no
// network). `lore sync status` prints one line per store; `lore store
// doctor` adds a reachability probe and the clone's halves.
//
//	ListStores + OrgStoreEntries (or the personal store alone outside a
//	repo) ─▶ localStoreDir ─▶ git: ReadPushState(work) | folder: no remote

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"time"

	gitbackend "github.com/thesatellite-ai/lore/adapters/backend/git"
	"github.com/thesatellite-ai/lore/logic/storepush"
	opsstores "github.com/thesatellite-ai/lore/ops/stores"
	opssync "github.com/thesatellite-ai/lore/ops/sync"
)

// storeStanding is one store as this machine holds it.
type storeStanding struct {
	entry opsstores.Entry
	// dir is the clone worktree (git) or the store folder.
	dir string
	git bool
	// cloned: a git store has a clone on this machine.
	cloned bool
	state  gitbackend.PushState
	// err is why the standing could not be read ("" when it was).
	err string
}

// storeStandings lists every store this repo and machine use, the repo's
// stores.yaml first, then the org's, then the personal store; one entry
// per name. Outside a repo only the personal store is listed.
func (a *App) storeStandings(ctx context.Context) []storeStanding {
	var entries []opsstores.Entry
	if list, err := a.ListStores(ctx); err == nil {
		entries = list
	} else if mc, merr := readMachineConfig(a.opts.Home); merr == nil {
		if p := effectivePersonalURL(ctx, a.opts.Home, a.projectDir(), mc); p != "" {
			entries = []opsstores.Entry{{Name: opsstores.PersonalName, URL: p, Personal: true, Source: opsstores.SourcePersonal}}
		}
	}
	if orgs, err := a.OrgStoreEntries(ctx); err == nil {
		entries = append(entries, orgs...)
	}
	seen := map[string]bool{}
	var out []storeStanding
	for _, e := range entries {
		if seen[e.Name] {
			continue
		}
		seen[e.Name] = true
		out = append(out, a.standingOf(ctx, e))
	}
	return out
}

// standingOf reads one store's standing.
func (a *App) standingOf(ctx context.Context, e opsstores.Entry) storeStanding {
	s := storeStanding{entry: e}
	dir, isGit, err := a.localStoreDir(e)
	s.dir, s.git = dir, isGit
	if err != nil {
		s.err = reasonOf(err)
		return s
	}
	if !isGit {
		return s
	}
	if st, serr := os.Stat(filepath.Join(dir, ".git")); serr != nil || !st.IsDir() {
		return s // not cloned on this machine
	}
	s.cloned = true
	ps, err := gitbackend.ReadPushState(ctx, "", dir)
	if err != nil {
		s.err = reasonOf(err)
		return s
	}
	s.state = ps
	return s
}

// syncStoreStates adds one line and one data entry per store to sync
// status (item 7: which store has commits its remote has not confirmed).
func (a *App) syncStoreStates(ctx context.Context, out *opssync.StatusOut) {
	list := a.storeStandings(ctx)
	if len(list) == 0 {
		return
	}
	now := a.Now()
	modes := map[string]opsstores.PushView{}
	if views, err := a.PushViews(ctx); err == nil {
		for _, v := range views {
			modes[v.Store] = v
		}
	}
	out.Lines = append(out.Lines, "stores:")
	for _, s := range list {
		st, line := stateOf(s), "  "+s.entry.Name+": "+standingText(s, now)
		if v, ok := modes[s.entry.Name]; ok { // store push modes, 2026-11-09
			st.Push, st.PushSummary, st.Epoch, st.PushProblem = v.Push, v.Summary, v.Epoch, v.Problem
			line += "; " + v.Summary
			if v.Problem != "" {
				line += " (problem: " + v.Problem + "; pushing as now)"
			} else if st.Ahead > 0 && v.Push != string(storepush.ModeNow) {
				line += " (`lore sync` pushes the waiting commits)" // a held mode: nothing else may push them soon
			}
		}
		out.Data.Stores = append(out.Data.Stores, st)
		out.Lines = append(out.Lines, line)
	}
}

// stateOf is s as sync status's data.
func stateOf(s storeStanding) opssync.StoreState {
	st := opssync.StoreState{Store: s.entry.Name, Kind: opssync.StoreKindFolder, Error: s.err}
	if !s.git {
		return st
	}
	st.Kind, st.Cloned = opssync.StoreKindGit, s.cloned
	if !s.cloned || s.err != "" {
		return st
	}
	ps := s.state
	st.Branch, st.Ahead, st.Behind = ps.Branch, ps.Ahead, ps.Behind
	if !ps.OldestUnpushed.IsZero() {
		st.OldestUnpushed = ps.OldestUnpushed.UTC().Format(time.RFC3339)
	}
	if !ps.LastPush.IsZero() {
		st.LastPush = ps.LastPush.UTC().Format(time.RFC3339)
	}
	return st
}

// standingText is a git store's standing in words:
//
//	ahead 2 (oldest 3h ago), behind 0 of origin/main as of the last fetch; last push 2026-11-09 07:19 UTC (5m ago)
//
// A zero now leaves the relative ages out, so the text of an unchanged
// store is the same on every run (store doctor --json).
func standingText(s storeStanding, now time.Time) string {
	switch {
	case !s.git:
		return "folder store (no remote; every write is already in the folder)"
	case s.err != "":
		return "unknown (" + s.err + ")"
	case !s.cloned:
		return "not cloned on this machine (the next command that reads it clones it)"
	}
	st := s.state
	ahead := "ahead " + strconv.Itoa(st.Ahead)
	if st.Ahead > 0 && !st.OldestUnpushed.IsZero() && !now.IsZero() {
		ahead += " (oldest " + storeAge(now.Sub(st.OldestUnpushed)) + " ago)"
	}
	behind := "behind " + strconv.Itoa(st.Behind) + " of " + gitbackend.RemoteName + "/" + st.Branch + " as of the last fetch"
	if !st.Tracked {
		behind = "never fetched"
	}
	push := "no push from this machine on record"
	if !st.LastPush.IsZero() {
		push = "last push " + st.LastPush.UTC().Format("2006-01-02 15:04 UTC")
		if !now.IsZero() {
			push += " (" + storeAge(now.Sub(st.LastPush)) + " ago)"
		}
	}
	return ahead + ", " + behind + "; " + push
}

// storeAge is d rounded to its largest unit: 45s, 12m, 3h, 2d.
func storeAge(d time.Duration) string {
	switch {
	case d < 0:
		d = 0
		fallthrough
	case d < time.Minute:
		return strconv.Itoa(int(d/time.Second)) + "s"
	case d < time.Hour:
		return strconv.Itoa(int(d/time.Minute)) + "m"
	case d < 48*time.Hour:
		return strconv.Itoa(int(d/time.Hour)) + "h"
	}
	return strconv.Itoa(int(d/(24*time.Hour))) + "d"
}
