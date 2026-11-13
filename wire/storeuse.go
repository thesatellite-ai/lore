package wire

// storeuse.go — `lore store use` (opsstores.CloneUser; owner decision D1
// "use my own clone", 2026-11-12). The clone a store uses on this machine
// is gitbackend.CloneDirFor: lore's own under ~/.lore/stores unless the
// machine-local record (~/.lore/state/store-clones.json, D-b) names
// another folder. store use writes that record; --default drops it.
//
//	UseClone(path)      ─▶ the clone's top (lore-store.yaml beside .git)
//	                    ─▶ its origin ─▶ the configured store it clones
//	                       (store list's entries; --store picks one)
//	                    ─▶ lore's own clone of it holds no unpushed work
//	                    ─▶ record {path, why: own}
//	UseClone(--default) ─▶ the store's own-clone record dropped; the
//	                       person's clone is never touched

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	gitbackend "github.com/thesatellite-ai/lore/adapters/backend/git"
	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/logic/repourl"
	"github.com/thesatellite-ai/lore/model/errs"
	opsstores "github.com/thesatellite-ai/lore/ops/stores"
)

var _ opsstores.CloneUser = (*App)(nil)

// useVerb names the command in errors.
const useVerb = "store use"

// UseClone implements opsstores.CloneUser.
func (a *App) UseClone(ctx context.Context, in opsstores.UseIn) (opsstores.UseOut, error) {
	if in.Default != "" {
		return a.useDefaultClone(ctx, in.Default)
	}
	top, err := useCloneTop(in.Path)
	if err != nil {
		return opsstores.UseOut{}, err
	}
	origin := gitbackend.OriginAt(top)
	if origin == "" {
		return opsstores.UseOut{}, errs.New(errs.InvalidInput, useVerb+": "+top+" has no origin remote, so lore cannot tell which store it is a clone of").
			WithHint("add it: git -C " + top + " remote add origin <store url>")
	}
	r, err := gitbackend.RemoteOfRaw(origin)
	if err != nil {
		return opsstores.UseOut{}, errs.Wrap(errs.InvalidInput, useVerb+": the origin of "+top+" is not a git store url lore can use", err).
			WithHint("point it at the store: git -C " + top + " remote set-url origin <store url> (ssh or https)")
	}
	e, err := a.useEntry(ctx, in.Store, r, headBranch(top), top)
	if err != nil {
		return opsstores.UseOut{}, err
	}
	if e.Branch != "" && headBranch(top) != e.Branch {
		return opsstores.UseOut{}, errs.New(errs.InvalidInput, useVerb+": store "+e.Name+" is pinned to branch "+e.Branch+", but "+top+" has "+branchOrNone(headBranch(top))+" checked out").
			WithHint("check it out first: git -C " + top + " switch " + e.Branch)
	}
	home := a.opts.Home
	key := gitbackend.CloneKey(r, e.Branch)
	out := opsstores.UseOut{Store: e.Name, URL: e.URL, Branch: e.Branch, Path: top, Own: true}
	prev, prevOwn, err := gitbackend.CloneDirFor(home, r, e.Branch)
	if err != nil {
		return opsstores.UseOut{}, err
	}
	out.Previous = prev
	def, err := gitbackend.DefaultCloneDir(home, r, e.Branch)
	if err != nil {
		return opsstores.UseOut{}, err
	}
	if sameFolder(top, def) || (!prevOwn && sameFolder(top, prev)) {
		// lore's own clone: nothing to record (a stale own record goes)
		out.Own, out.Path = false, prev
		if prevOwn {
			out.Changed, out.Path = true, top
			return out, gitbackend.ForgetCloneRecord(home, key)
		}
		return out, nil
	}
	if prevOwn && sameFolder(top, prev) {
		return out, nil
	}
	if !prevOwn && isDirPath(filepath.Join(prev, ".git")) {
		if pending := unpushedIn(ctx, cloneDirs{git: prev, work: prev}); len(pending) > 0 {
			return opsstores.UseOut{}, errs.New(errs.Refused, useVerb+": lore's clone of store "+e.Name+" holds work its remote does not have ("+strings.Join(pending, ", ")+"); switching clones now would leave it behind").
				WithHint("run `lore sync` when the store is reachable, then lore store use " + top + " again")
		}
		out.Unused = prev
	}
	if err = gitbackend.RecordClone(home, key, gitbackend.CloneEntry{Path: top, Why: gitbackend.WhyOwn}, time.Now()); err != nil {
		return opsstores.UseOut{}, err
	}
	out.Changed = true
	return out, nil
}

// useCloneTop is the top folder of the person's clone at p (p itself or a
// folder inside it): a git clone whose top holds lore-store.yaml.
func useCloneTop(p string) (string, error) {
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", errs.Wrap(errs.BadPath, useVerb+": "+p+" is not a usable path", err)
	}
	st, err := os.Stat(abs)
	if err != nil || !st.IsDir() {
		return "", errs.New(errs.NotFound, useVerb+": "+abs+" is not a folder").
			WithHint("clone the store there first: git clone <store url> " + abs)
	}
	top, isGit, ok := rowfs.StoreTop(abs)
	switch {
	case !ok && insideGit(abs):
		return "", errs.New(errs.NotAStore, useVerb+": "+abs+" is a git clone with no "+rowfs.MarkerFile+" at its top, so it is not a lore store (or the store branch is not checked out)").
			WithHint("give the clone of a lore store (`lore store list` names the stores); check out the store's branch if another is checked out")
	case !ok:
		return "", errs.New(errs.NotAStore, useVerb+": "+abs+" is not a git clone of a store").
			WithHint("clone the store first: git clone <store url> " + abs + ", then lore store use " + abs)
	case !isGit:
		return "", errs.New(errs.InvalidInput, useVerb+": "+top+" is a folder store (no git), which lore already reads where it is; store use is for git clones").
			WithHint("attach a folder store by its file:// url: lore store add <name> file://" + top)
	}
	if fi, gerr := os.Lstat(filepath.Join(top, ".git")); gerr != nil || !fi.IsDir() {
		return "", errs.New(errs.BadPath, useVerb+": "+top+"/.git is a file (a linked worktree or a separate git dir); lore keeps its lock and state in a clone's .git folder").
			WithHint("make a plain clone: git clone <store url> <folder>, then lore store use <folder>")
	}
	return top, nil
}

// insideGit reports whether p or a folder above it holds .git.
func insideGit(p string) bool {
	for d := p; ; d = filepath.Dir(d) {
		if _, err := os.Lstat(filepath.Join(d, ".git")); err == nil {
			return true
		}
		if filepath.Dir(d) == d {
			return false
		}
	}
}

// useCandidates are the configured git stores (store list's entries:
// stores.yaml, me, the org's stores) whose url names store r.
func (a *App) useCandidates(ctx context.Context, r gitbackend.Remote) []opsstores.Entry {
	var all []opsstores.Entry
	if l, err := a.ListStores(ctx); err == nil {
		all = append(all, l...)
	}
	if o, err := a.OrgStoreEntries(ctx); err == nil {
		all = append(all, o...)
	}
	var out []opsstores.Entry
	for _, e := range all {
		if er, err := gitbackend.RemoteOfRaw(e.URL); err == nil && er.Norm == r.Norm {
			if e.Branch == "" {
				e.Branch, _ = opsstores.PinnedBranch(e.URL, "")
			}
			out = append(out, e)
		}
	}
	return out
}

// useEntry is the configured store the clone at top (origin r, head
// branch head) is for: --store names it (by name or url), else the one
// store its origin names; several stores on different branches are told
// apart by the branch the clone has checked out, else refused.
func (a *App) useEntry(ctx context.Context, named string, r gitbackend.Remote, head, top string) (opsstores.Entry, error) {
	cands := a.useCandidates(ctx, r)
	if named != "" {
		for _, e := range cands {
			if e.Name == named || e.URL == named {
				return e, nil
			}
		}
		if src, err := a.lookupStore(ctx, useVerb, named); err == nil {
			return opsstores.Entry{}, errs.New(errs.InvalidInput, useVerb+": the origin of "+top+" is "+repourl.ShownStore(r.Norm)+", but store "+named+" is "+src.URL).
				WithHint("give the clone of " + named + ", or the store this clone belongs to (`lore store list`)")
		}
		return opsstores.Entry{}, errs.New(errs.NotFound, useVerb+": no store named "+named+" is configured here").
			WithHint("`lore store list` names the stores; attach a store first with lore store add <name> <url>")
	}
	if len(cands) == 0 {
		return opsstores.Entry{}, errs.New(errs.NotFound, useVerb+": "+top+" is a clone of "+repourl.ShownStore(r.Norm)+", which is not one of the stores lore knows here").
			WithHint("attach it first (lore store add <name> <url>, or the org's setup), then run lore store use " + top + " again; `lore store list` names the stores")
	}
	var branches []string
	for _, e := range cands {
		if !slices.Contains(branches, e.Branch) {
			branches = append(branches, e.Branch)
		}
	}
	if len(branches) == 1 {
		return cands[0], nil
	}
	for _, e := range cands {
		if e.Branch != "" && e.Branch == head {
			return e, nil
		}
	}
	var names []string
	for _, e := range cands {
		names = append(names, e.Name)
	}
	return opsstores.Entry{}, errs.New(errs.InvalidInput, useVerb+": "+repourl.ShownStore(r.Norm)+" is configured on several branches ("+strings.Join(names, ", ")+"), and the branch "+top+" has checked out names none of them").
		WithHint("name the store: lore store use " + top + " --store <name>")
}

// useDefaultClone drops the own-clone record of store (a name or url).
func (a *App) useDefaultClone(ctx context.Context, named string) (opsstores.UseOut, error) {
	home := a.opts.Home
	var e opsstores.Entry
	r, err := gitbackend.RemoteOfRaw(named)
	if err == nil {
		e = opsstores.Entry{Name: repourl.ShownStore(r.Norm), URL: named}
		e.Branch, _ = opsstores.PinnedBranch(named, "")
		if cs := a.useCandidates(ctx, r); len(cs) > 0 && e.Branch == "" {
			e.Name = cs[0].Name
		}
	} else {
		src, lerr := a.lookupStore(ctx, useVerb, named)
		if lerr != nil {
			return opsstores.UseOut{}, lerr
		}
		if r, err = gitbackend.RemoteOfRaw(src.URL); err != nil {
			return opsstores.UseOut{}, errs.New(errs.InvalidInput, useVerb+": store "+named+" is not a git store; it has no clone to choose").
				WithHint("store use applies to git stores (`lore store list` shows each store's url)")
		}
		e = opsstores.Entry{Name: named, URL: src.URL, Branch: src.Branch}
	}
	key := gitbackend.CloneKey(r, e.Branch)
	rec := gitbackend.ReadCloneRecord(home).Clones
	got, ok := rec[key]
	if !ok && e.Branch == "" {
		// one pinned own clone of the store and no branch named: that one
		for k, v := range rec {
			if strings.HasPrefix(k, r.Norm+"@") && v.Why == gitbackend.WhyOwn {
				if ok {
					return opsstores.UseOut{}, errs.New(errs.InvalidInput, useVerb+": "+repourl.ShownStore(r.Norm)+" has own clones on several branches").
						WithHint("name the store by its configured name: lore store use --default <name>")
				}
				key, got, ok = k, v, true
				_, e.Branch, _ = strings.Cut(k, "@")
			}
		}
	}
	out := opsstores.UseOut{Store: e.Name, URL: e.URL, Branch: e.Branch}
	if !ok || got.Why != gitbackend.WhyOwn {
		dir, _, derr := gitbackend.CloneDirFor(home, r, e.Branch)
		if derr != nil {
			return opsstores.UseOut{}, derr
		}
		out.Path = dir
		return out, nil
	}
	if err = gitbackend.ForgetCloneRecord(home, key); err != nil {
		return opsstores.UseOut{}, err
	}
	dir, _, err := gitbackend.CloneDirFor(home, r, e.Branch)
	if err != nil {
		return opsstores.UseOut{}, err
	}
	out.Path, out.Previous, out.Changed = dir, got.Path, true
	return out, nil
}

// branchOrNone names a checked-out branch, or none.
func branchOrNone(b string) string {
	if b == "" {
		return "no branch"
	}
	return "branch " + b
}
