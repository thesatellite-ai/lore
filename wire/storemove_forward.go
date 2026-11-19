package wire

// storemove_forward.go — set-url's promise to teammates (devco D-699,
// owner use pass 5 weak 5). An admin's `lore store set-url` commits the
// store's new address to the org's lore-org.yaml in the store, and a
// teammate's lore follows that file at their next sync (refreshOrgSources).
// The move commit went only to the new address, so a teammate whose clone
// still points at an old repo that still exists pulled from it and stayed
// there. Now the old address gets the move too, on an explicit push only:
//
//	store set-url (admin, org file moved) ─▶ address record: forward = the
//	   old addresses (recordStoreForward)
//	lore sync / lore store push ─▶ forwardStoreMoves: once the clone at the
//	   new address has nothing waiting, push its tip to each old address
//	   ├─▶ taken: "pushed … to its old address … too" (teammates follow)
//	   ├─▶ no answer: kept, the next sync tries again
//	   └─▶ refused or gone: said once, with the step each teammate runs
//	       (a renamed repo redirects, so they follow without it)

import (
	"context"
	"fmt"
	"io"
	"slices"
	"sort"
	"strings"

	gitbackend "github.com/thesatellite-ai/lore/adapters/backend/git"
	"github.com/thesatellite-ai/lore/logic/gitfault"
	"github.com/thesatellite-ai/lore/logic/repourl"
	"github.com/thesatellite-ai/lore/logic/storeaddr"
)

// recordStoreForward adds the old addresses a set-url moved store id away
// from to its entry in the address record (branch is the store branch the
// move was committed on), so the next explicit push sends the move there
// too. The new address itself is never a forward.
func recordStoreForward(home, id, raw, branch string, old []string) error {
	r := readStoreAddrRecord(home)
	e, ok := r.Stores[id]
	if !ok {
		return nil // recordStoreAddress writes the entry first
	}
	for _, o := range old {
		if o == "" || storeaddr.Same(o, raw) || slices.ContainsFunc(e.Forward, func(f string) bool { return storeaddr.Same(f, o) }) {
			continue
		}
		e.Forward = append(e.Forward, o)
	}
	e.Branch = branch
	r.Stores[id] = e
	return writeStoreAddrRecord(home, r)
}

// forwardResult is what one push of a move to an old address did.
type forwardResult int

const (
	forwardTaken   forwardResult = iota // the old address holds the move now
	forwardRetry                        // it did not answer: the next sync tries again
	forwardRefused                      // it refused the push or holds no repository: said once
)

// forwardStoreMoves pushes each set-url move still owed to an old address
// (recordStoreForward) and prints one line per old address to w. Only an
// explicit `lore sync` or `lore store push` calls it, never a read or a
// hook. A move waits while the clone at the new address still holds
// commits the new address lacks: the old address gets what the new one
// has, never more.
func (a *App) forwardStoreMoves(ctx context.Context, w io.Writer) {
	if a.opts.Home == "" || w == nil {
		return
	}
	r := readStoreAddrRecord(a.opts.Home)
	ids := make([]string, 0, len(r.Stores))
	for id, e := range r.Stores {
		if len(e.Forward) > 0 {
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return
	}
	sort.Strings(ids)
	changed := false
	for _, id := range ids {
		e := r.Stores[id]
		sha, ref, ok := pushedTip(ctx, a.opts.Home, e.URL, e.Branch)
		if !ok {
			continue // the move has not reached the new address yet
		}
		name := OrgStoreName(a.opts.Home, e.URL)
		var keep []string
		for _, old := range e.Forward {
			res, line := forwardOne(ctx, sha, ref, name, e.URL, old, pushedDir(a.opts.Home, e.URL, e.Branch))
			_, _ = fmt.Fprintln(w, line)
			if res == forwardRetry {
				keep = append(keep, old)
			}
		}
		if len(keep) != len(e.Forward) {
			e.Forward = keep
			r.Stores[id] = e
			changed = true
		}
	}
	if changed {
		_ = writeStoreAddrRecord(a.opts.Home, r) // best effort: a forward kept by mistake is pushed again, which git answers "up to date"
	}
}

// pushedDir is the clone folder of the store at url on branch.
func pushedDir(home, url, branch string) string {
	dir, _, err := gitbackend.CloneDirOf(home, url, branch)
	if err != nil {
		return ""
	}
	return dir
}

// pushedTip is the commit the clone of url holds on its store branch when
// the remote has all of it (the branch equals its tracking ref): sha and
// the branch ref; ok is false when there is no clone or commits still wait.
func pushedTip(ctx context.Context, home, url, branch string) (sha, ref string, ok bool) {
	dir := pushedDir(home, url, branch)
	if dir == "" {
		return "", "", false
	}
	ref = cloneHeadRef(ctx, dir)
	if ref == "" {
		return "", "", false
	}
	head, err := gitRun{dir: dir, args: []string{"rev-parse", "--verify", "--quiet", ref}}.output(ctx)
	if err != nil {
		return "", "", false
	}
	track, err := gitRun{dir: dir, args: []string{"rev-parse", "--verify", "--quiet", "refs/remotes/origin/" + strings.TrimPrefix(ref, "refs/heads/")}}.output(ctx)
	if err != nil || strings.TrimSpace(string(track)) != strings.TrimSpace(string(head)) {
		return "", "", false
	}
	return strings.TrimSpace(string(head)), ref, true
}

// forwardOne pushes sha to ref at the old address (a plain push: a
// teammate's later commits there are never overwritten) and returns the
// line that says what happened.
func forwardOne(ctx context.Context, sha, ref, name, raw, old, dir string) (forwardResult, string) {
	shown, shownNew := repourl.Mask(old), repourl.Mask(raw)
	branch := strings.TrimPrefix(ref, "refs/heads/")
	_, err := gitRun{dir: dir, args: []string{"push", "--quiet", old, sha + ":" + ref}}.output(ctx)
	if err == nil {
		return forwardTaken, "lore: pushed store " + name + ": commit " + gitbackend.Pushed{Commit: sha}.Short() + " to branch " + branch + " at its old address " + shown + " too, so a teammate whose lore still uses it follows the move to " + shownNew + " at their next sync"
	}
	return forwardFailure(err.Error(), name, shown, shownNew)
}

// forwardFailure is the result and line of a push to an old address that
// failed with git's stderr. No answer keeps the forward (the next sync
// tries again); a refused push (a teammate pushed there after the move)
// or an address with no repository is said once, with what teammates do.
func forwardFailure(stderr, name, shown, shownNew string) (forwardResult, string) {
	low := strings.ToLower(stderr)
	rejected := strings.Contains(low, "[rejected]") || strings.Contains(low, "non-fast-forward") || strings.Contains(low, "fetch first")
	if !rejected && gitfault.Classify(stderr) == gitfault.Unreachable {
		return forwardRetry, "lore: store " + name + ": the move to " + shownNew + " was not sent to its old address " + shown + " (" + gitfault.Reason(stderr) + "); the next lore sync tries again"
	}
	why := gitfault.Describe(stderr, shown, nil)
	if rejected {
		why = shown + " refused the move (a teammate pushed there after it)"
	}
	return forwardRefused, "lore: store " + name + ": the move to " + shownNew + " did not reach its old address: " + why + ". A teammate whose lore still uses " + shown + " follows only if it redirects to " + shownNew + " (a renamed repo); otherwise each teammate runs: lore store set-url --name " + name + " --url " + shownNew
}
