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
//	   ├─▶ taken: "forwarded N commits to the old address …" (teammates follow)
//	   ├─▶ a teammate pushed to the old address first: "took in N commits
//	   │   a teammate pushed to its old address …" and "forwarded N commits
//	   │   to the new address …", then the forward above (a fast-forward)
//	   ├─▶ no answer: kept, the next sync tries again
//	   └─▶ refused or gone: said once, with the step each teammate runs
//	       (a renamed repo redirects, so they follow without it)

import (
	"context"
	"fmt"
	"io"
	"slices"
	"sort"
	"strconv"
	"strings"

	gitbackend "github.com/thesatellite-ai/lore/adapters/backend/git"
	"github.com/thesatellite-ai/lore/engine/attach"
	"github.com/thesatellite-ai/lore/logic/gitfault"
	"github.com/thesatellite-ai/lore/logic/repourl"
	"github.com/thesatellite-ai/lore/logic/storeaddr"
	"github.com/thesatellite-ai/lore/store"
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
		if _, _, ok := pushedTip(ctx, a.opts.Home, e.URL, e.Branch); !ok {
			continue // the move has not reached the new address yet
		}
		name := OrgStoreName(a.opts.Home, e.URL)
		gb, closeStore := a.openMovedStore(ctx, name, e.URL, e.Branch)
		var keep []string
		for _, old := range e.Forward {
			res, lines := a.forwardTo(ctx, gb, name, e, old)
			for _, line := range lines {
				_, _ = fmt.Fprintln(w, line)
			}
			if res == forwardRetry {
				keep = append(keep, old)
			}
		}
		closeStore()
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

// openMovedStore opens the store at url on branch (the clone a set-url
// moved it to) as a git backend, for MergeFrom; nil when it is no git
// store or does not open (the forward then pushes without taking in what
// the old address holds, and a refusal says so). closeStore always runs.
func (a *App) openMovedStore(ctx context.Context, name, url, branch string) (gb *gitbackend.Backend, closeStore func()) {
	closeStore = func() {}
	canon, err := canonical(attach.Store{Name: store.Source(name), URL: url, Branch: branch})
	if err != nil {
		return nil, closeStore
	}
	b, err := a.openStore(ctx, canon, url)
	if err != nil {
		return nil, closeStore
	}
	gb, ok := concrete(b).(*gitbackend.Backend)
	if !ok {
		_ = b.Close()
		return nil, closeStore
	}
	return gb, func() { _ = b.Close() }
}

// forwardTo sends the move owed to the old address old: first it takes in
// what old holds that the clone lacks (a teammate who pushed there before
// following the move, gb.MergeFrom) and sends that to the new address,
// so the push to old is a fast-forward and the new address loses nothing;
// then it pushes the clone's tip to old (forwardOne). It returns the lines
// to print, none when the forward waits silently (commits still on their
// way to the new address). One wording for every forward (devco D-699
// follow-up, up5fwd2): each remote this moved has exactly one line naming
// it with the commits git counted there ("forwarded N commits to the new
// address <url>", "forwarded N commits to the old address <url>"), and the
// take-in, which moves no remote, says "took in N commits a teammate
// pushed to …". The store line's "sent N" counts neither: those commits
// went out after it.
func (a *App) forwardTo(ctx context.Context, gb *gitbackend.Backend, name string, e storeAddrRecordEntry, old string) (forwardResult, []string) {
	shown, shownNew := repourl.Mask(old), repourl.Mask(e.URL)
	var lines []string
	if gb != nil {
		mr, err := gb.MergeFrom(ctx, old)
		switch {
		case err != nil:
			return forwardRetry, []string{"lore: store " + name + ": the move to " + shownNew + " was not sent to its old address " + shown + ": what " + shown + " holds could not be taken in first (" + err.Error() + "); the next lore sync tries again"}
		case mr.Offline:
			res, line := forwardFailure(mr.Detail, name, shown, shownNew)
			return res, []string{line}
		case len(mr.Refused) > 0:
			return forwardRefused, []string{"lore: store " + name + ": the move to " + shownNew + " did not reach its old address: " + shown + " holds commits a teammate pushed there that clash with this clone's on rows lore cannot merge (" + refusalList(mr.Refused) + "), so they were not taken in and " + shownNew + " lacks them. A teammate whose lore still uses " + shown + " follows only if it redirects to " + shownNew + " (a renamed repo); otherwise each teammate runs: lore store set-url --name " + name + " --url " + shownNew}
		case mr.Commits > 0:
			rep, serr := gb.Sync(ctx)
			gb.TakePushes() // said by the forwarded line below, never as a second push line
			if serr != nil || rep.Offline || len(rep.Refused) > 0 || !rep.Pushed {
				return forwardRetry, []string{"lore: store " + name + ": " + tookInLine(mr.Commits, "its old address "+shown) + "; they wait in the clone until " + shownNew + " takes them, and the next lore sync forwards them there and then the move to " + shown}
			}
			sha, ref, ok := pushedTip(ctx, a.opts.Home, e.URL, e.Branch)
			if !ok {
				return forwardRetry, nil
			}
			lines = append(lines, forwardedLine(name, plural(rep.Commits, "commit"), "new", shownNew, ref, sha)+": "+tookInLine(mr.Commits, "its old address")+", taken in first, so the new address lacks none of them")
		}
	}
	sha, ref, ok := pushedTip(ctx, a.opts.Home, e.URL, e.Branch)
	if !ok {
		return forwardRetry, lines
	}
	res, line := forwardOne(ctx, sha, ref, name, e.URL, old, pushedDir(a.opts.Home, e.URL, e.Branch))
	return res, append(lines, line)
}

// tookInLine is the one wording of a take-in (set-url's and the forward's):
// n commits a teammate pushed to where (an old address) that this clone
// lacked were merged into it. A take-in moves no remote, so it never says
// "forwarded" or "sent".
func tookInLine(n int, where string) string {
	return "took in " + plural(n, "commit") + " a teammate pushed to " + where + " before following the move"
}

// forwardedLine is the one wording of a push the forward made to a remote
// other than the store line's push: "lore: store <s>: forwarded <count>
// to the <which> address <url> (branch <b>, now at commit <c>)". count is
// what git counted there ("3 commits"), or "the move" when it could not be
// read; the WW matrix compares each such line with its own remote.
func forwardedLine(name, count, which, shown, ref, sha string) string {
	return "lore: store " + name + ": forwarded " + count + " to the " + which + " address " + shown + " (branch " + strings.TrimPrefix(ref, "refs/heads/") + ", now at commit " + gitbackend.Pushed{Commit: sha}.Short() + ")"
}

// refusalList names the rows of refused (at most three, then a count).
func refusalList(refused []store.Refusal) string {
	const most = 3
	var names []string
	for i, r := range refused {
		if i == most {
			names = append(names, "and "+strconv.Itoa(len(refused)-most)+" more")
			break
		}
		n := r.ID.String()
		if r.ID.IsZero() {
			n = r.Reason
		}
		names = append(names, n)
	}
	return strings.Join(names, ", ")
}

// mergeOldAddresses is set-url's take-in (D-699 follow-up): before the
// move is committed, the clone of the store at raw takes in what each old
// address still holds that it lacks (a teammate pushed there and this
// machine never synced), so the new address gets every commit and the
// move sent to the old address later is a fast-forward. An old address
// that does not answer is left to the forward, which takes it in first;
// it returns the lines set-url prints.
func (a *App) mergeOldAddresses(ctx context.Context, name, raw, branch string, old []string) []string {
	var froms []string
	for _, o := range old {
		if o != "" && !storeaddr.Same(o, raw) {
			froms = append(froms, o)
		}
	}
	if len(froms) == 0 {
		return nil
	}
	gb, closeStore := a.openMovedStore(ctx, name, raw, branch)
	defer closeStore()
	if gb == nil {
		return nil
	}
	var lines []string
	for _, o := range froms {
		mr, err := gb.MergeFrom(ctx, o)
		switch {
		case err != nil:
			lines = append(lines, "what the old address "+repourl.Mask(o)+" holds was not taken in ("+err.Error()+"); the next lore sync takes it in before it sends the move there")
		case len(mr.Refused) > 0:
			lines = append(lines, "the old address "+repourl.Mask(o)+" holds commits a teammate pushed there that clash with this clone's on rows lore cannot merge ("+refusalList(mr.Refused)+"); they were not taken in, so "+repourl.Mask(raw)+" lacks them")
		case mr.Commits > 0:
			lines = append(lines, tookInLine(mr.Commits, "the old address "+repourl.Mask(o))+" into the clone of "+repourl.Mask(raw)+", so they go there with the move")
		}
	}
	return lines
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
// line that says what happened. The line carries its own commit count
// (devco D-699 follow-up): the sync or push summary's "sent N" counts the
// commits that reached the store's address only, so a forward that moved
// the old address too says how many commits went there, and one that
// moved nothing says so instead of claiming a push.
func forwardOne(ctx context.Context, sha, ref, name, raw, old, dir string) (forwardResult, string) {
	shown, shownNew := repourl.Mask(old), repourl.Mask(raw)
	branch := strings.TrimPrefix(ref, "refs/heads/")
	out, err := gitRun{dir: dir, args: []string{"push", "--porcelain", old, sha + ":" + ref}, outInErr: true}.output(ctx)
	if err != nil {
		return forwardFailure(err.Error(), name, shown, shownNew)
	}
	short := gitbackend.Pushed{Commit: sha}.Short()
	n, known := forwardedCommits(ctx, dir, string(out), sha)
	if known && n == 0 {
		return forwardTaken, "lore: store " + name + ": its old address " + shown + " already holds the move to " + shownNew + " (commit " + short + " on branch " + branch + "), so nothing was pushed there"
	}
	count := "the move"
	if known {
		count = plural(n, "commit")
	}
	return forwardTaken, forwardedLine(name, count, "old", shown, ref, sha) + ", so a teammate whose lore still uses it follows the move to " + shownNew + " at their next sync"
}

// forwardedCommits counts the commits a `git push --porcelain` of sha
// moved onto the remote, from its ref line: "old..new" (a fast-forward:
// old..sha), "[new branch]" (all of sha's history) or "[up to date]" (0).
// known is false when the line cannot be read, so the caller never prints
// a count it did not establish.
func forwardedCommits(ctx context.Context, dir, porcelain, sha string) (n int, known bool) {
	for _, l := range strings.Split(porcelain, "\n") {
		f := strings.Split(l, "\t")
		if len(f) < 3 || !strings.Contains(f[1], ":") {
			continue // "To <url>", "Done"
		}
		summary := strings.Fields(f[2])
		rng := ""
		switch {
		case strings.HasPrefix(f[2], "[up to date]"):
			return 0, true
		case strings.HasPrefix(f[2], "[new branch]"):
			rng = sha
		case len(summary) > 0 && strings.Contains(summary[0], ".."):
			rng = strings.SplitN(summary[0], "..", 2)[0] + ".." + sha
		default:
			return 0, false
		}
		cnt, err := gitRun{dir: dir, args: []string{"rev-list", "--count", rng}}.output(ctx)
		if err != nil {
			return 0, false
		}
		v, err := strconv.Atoi(strings.TrimSpace(string(cnt)))
		return v, err == nil
	}
	return 0, false
}

// forwardFailure is the result and line of a push to an old address that
// failed with git's stderr. No answer keeps the forward (the next sync
// tries again), and so does a push refused as not a fast-forward: what
// the old address held was taken in just before (forwardTo), so a
// teammate pushed there while this sync ran, and the next sync takes that
// in too. An address with no repository, or one that refuses for good
// (permission, protection), is said once, with what teammates do.
func forwardFailure(stderr, name, shown, shownNew string) (forwardResult, string) {
	low := strings.ToLower(stderr)
	rejected := strings.Contains(low, "[rejected]") || strings.Contains(low, "non-fast-forward") || strings.Contains(low, "fetch first")
	if rejected {
		return forwardRetry, "lore: store " + name + ": the move to " + shownNew + " was not sent to its old address " + shown + ": it got commits this sync has not taken in yet (a teammate pushed there just now); the next lore sync takes them in and sends the move again"
	}
	if gitfault.Classify(stderr) == gitfault.Unreachable {
		return forwardRetry, "lore: store " + name + ": the move to " + shownNew + " was not sent to its old address " + shown + " (" + gitfault.Reason(stderr) + "); the next lore sync tries again"
	}
	why := gitfault.Describe(stderr, shown, nil)
	return forwardRefused, "lore: store " + name + ": the move to " + shownNew + " did not reach its old address: " + why + ". A teammate whose lore still uses " + shown + " follows only if it redirects to " + shownNew + " (a renamed repo); otherwise each teammate runs: lore store set-url --name " + name + " --url " + shownNew
}
