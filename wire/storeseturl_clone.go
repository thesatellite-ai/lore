package wire

// storeseturl_clone.go — the clone half of `lore store set-url` (B3-15).
// A git store's clone folder is derived from its url, so a new address
// means a new folder: set-url's own check of the new address opens it
// there. Writes made while the old address was unreachable are commits in
// the OLD clone only; left there, they vanish from every list and no sync
// ever sends them. So set-url carries them:
//
//	planCloneCarry (before anything is written)
//	   └─ the clones of this store id whose origin is not the new address
//	      and that hold unpushed work ("stranded")
//	        none          → nothing to do (a fully pushed old clone is
//	                        left as it was)
//	        one, and the new address's clone holds nothing of its own
//	                      → carry: that clone becomes the store's clone
//	        otherwise     → E_REFUSED naming the commits, nothing written
//	applyCloneCarry (after the places moved)
//	   └─ under the old clone's lock: origin → the new address; the new
//	      address's own clone (fully pushed: everything in it is on the
//	      remote) is removed and the old clone moves into its folder (O-14:
//	      the folder serving the store carries the new address's name; a
//	      person's own clone keeps its folder); the clone record names it
//	      for the new address
//
// The next `lore sync` then pushes the waiting commits to the new address,
// as the write that made them promised. `lore doctor` (doctor_clones.go)
// reports a stranded clone left by an older lore, with this command as
// its fix: run again, set-url carries it the same way.

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"time"

	gitbackend "github.com/thesatellite-ai/lore/adapters/backend/git"
	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/logic/storeaddr"
	"github.com/thesatellite-ai/lore/model/errs"
)

// cloneCarry is what applyCloneCarry does: adopt the stranded clone for
// the new address and drop the new address's own clone.
type cloneCarry struct {
	from    string   // the stranded clone folder (kept)
	key     string   // the clone record key of the new address
	own     bool     // a person's own clone (`lore store use`): stays own
	oldKeys []string // record keys naming the stranded folder (dropped)
	drop    string   // the new address's own clone ("" when none or the same folder)
	dest    string   // the new address's clone folder: from moves there (O-14; "" to stay)
	pending []string // what the stranded clone holds (for the line set-url prints)
}

// strandedClone is a whole clone of a store at another address than the
// one the store is reached at now, holding work its remote lacks.
type strandedClone struct {
	found   gitbackend.CloneFound
	pending []string
}

// strandedClones lists the whole clones on this machine of store id whose
// origin is not the address addr and that hold unpushed commits or
// uncommitted changes. It only reads.
func strandedClones(ctx context.Context, home, id, addr string) []strandedClone {
	found, err := gitbackend.ScanClones(home)
	if err != nil {
		return nil
	}
	var out []strandedClone
	for _, c := range found {
		if c.State != gitbackend.CloneWhole || c.Origin == "" || storeaddr.Same(c.Origin, addr) {
			continue
		}
		if m, merr := rowfs.ReadMarker(c.Paths.Dir); merr != nil || m.ID.String() != id {
			continue
		}
		// every branch counts here: a clone holding any work is carried, never lost
		if p := unpushedIn(ctx, cloneDirs{git: c.Paths.Dir, work: c.Paths.Dir}); len(p) > 0 {
			for i := range p {
				p[i] = strings.TrimSuffix(p[i], " in "+c.Paths.Dir) // the folder is named once, by the caller
			}
			out = append(out, strandedClone{found: c, pending: p})
		}
	}
	return out
}

// planCloneCarry decides, before set-url writes anything, what happens to
// the store's clones (the flow above). ok is false when there is nothing
// to carry (a folder store, or no clone holding unpushed work); an error
// refuses the whole set-url.
func (a *App) planCloneCarry(ctx context.Context, id, raw, branch string) (cloneCarry, bool, error) {
	const verb = "store set-url"
	r, err := gitbackend.RemoteOfRaw(raw)
	if err != nil {
		return cloneCarry{}, false, nil // not a git store: no clone to carry
	}
	home := a.opts.Home
	newDir, newOwn, err := gitbackend.CloneDirFor(home, r, branch)
	if err != nil {
		return cloneCarry{}, false, err
	}
	stranded := strandedClones(ctx, home, id, raw)
	if len(stranded) == 0 {
		// D-515: a fully pushed old clone moves too (before, it was left
		// with a `lore store forget` hint while a clone holding work moved:
		// one store moved, the other did not)
		return a.planCleanCarry(ctx, id, raw, branch, newDir, newOwn)
	}
	if len(stranded) > 1 {
		return cloneCarry{}, false, errs.New(errs.Refused, verb+": store "+id+" has unpushed work in more than one clone at an old address: "+strandedList(stranded)+"; set-url can carry only one to "+raw).
			WithHint("push all but one by hand (git -C <clone> push " + raw + " HEAD), then run this command again")
	}
	s := stranded[0]
	carry := newCloneCarry(home, s.found, gitbackend.CloneKey(r, branch), newDir, newOwn)
	carry.pending = s.pending
	if newDir == carry.from || !exists(filepath.Join(newDir, ".git")) {
		return carry, true, nil
	}
	mine := unpushedIn(ctx, cloneDirs{git: newDir, work: newDir})
	if newOwn || len(mine) > 0 {
		why := "it is your own clone (`lore store use`)"
		if len(mine) > 0 {
			why = "it holds work of its own (" + strings.Join(mine, ", ") + ")"
		}
		return cloneCarry{}, false, errs.New(errs.Refused, verb+": the clone "+carry.from+" of store "+id+" holds "+strings.Join(s.pending, ", ")+" that "+raw+" lacks, and the clone of the new address "+newDir+" cannot be replaced: "+why+"; nothing was changed").
			WithHint("push the old clone's commits to the new address by hand (git -C " + carry.from + " push " + raw + " HEAD), run `lore sync`, then run this command again")
	}
	carry.drop = newDir
	return carry, true, nil
}

// applyCloneCarry carries the stranded clone to the new address (the flow
// above) and returns the line set-url prints. origin is the new address in
// the spelling lore writes as a clone's origin.
func (a *App) applyCloneCarry(ctx context.Context, c cloneCarry, raw string) (tail, at string, err error) {
	const verb = "store set-url"
	home := a.opts.Home
	origin := ""
	if c.drop != "" {
		origin = gitbackend.OriginAt(c.drop) // the spelling the open just wrote
	}
	if origin == "" {
		o, err := gitbackend.OriginOf(raw)
		if err != nil {
			return "", "", err
		}
		origin = o
	}
	lctx, unlock, lerr := gitbackend.LockClone(ctx, c.from)
	if lerr != nil {
		return "", "", lerr
	}
	// D-699: what the clone knew of the old repository's branch goes with
	// the move, so a new empty repository is filled by the next sync
	// instead of being taken for the old one reset
	err = gitbackend.RepointOrigin(lctx, c.from, origin)
	unlock()
	if err != nil {
		return "", "", errs.Prefix(verb+": ", err)
	}
	if c.drop != "" {
		// The new address's clone was fully pushed when planned; check
		// again under its lock (a writer may have committed since) before
		// removing it.
		tail = dropReplacedClone(ctx, c.drop)
	}
	at = c.from
	if c.dest != "" && tail == "" && !exists(c.dest) {
		if merr := moveClone(ctx, c.from, c.dest); merr != nil {
			tail = "; it keeps its folder name (moving it to " + c.dest + " failed: " + merr.Error() + ")"
		} else {
			at = c.dest
			// D-565: the folder the clone left (stores/_local after a move
			// to a path and back) goes when nothing else is in it
			_ = os.Remove(filepath.Dir(c.from)) // fails, harmlessly, while it holds another clone
		}
	}
	why := gitbackend.WhyMoved
	if c.own {
		why = gitbackend.WhyOwn
	}
	if err := gitbackend.RecordClone(home, c.key, gitbackend.CloneEntry{Path: at, Why: why}, time.Now()); err != nil {
		return "", "", err
	}
	if idLine := a.carriedCloneIdentity(ctx, at, raw); idLine != "" {
		tail += "; " + idLine
	}
	for _, k := range c.oldKeys {
		if err := gitbackend.ForgetCloneRecord(home, k); err != nil {
			return "", "", err
		}
	}
	return tail, at, nil
}

// carryLine is set-url's line for the clone c carried to at (tail: what
// applyCloneCarry adds), with pending what the clone holds now: counted
// after every set-url commit (D-562: counted before the org file commit,
// it said "1 unpushed commit(s)" where git counted 2).
func carryLine(c cloneCarry, at, raw, tail string, pending []string) string {
	if len(pending) == 0 {
		// D-515: a fully pushed clone: nothing waits to be sent
		head := "the clone " + c.from
		if at != c.from {
			head = "the clone " + c.from + " moved to " + at + "; it"
		}
		return head + " now fetches from and pushes to " + raw + tail
	}
	head := "the clone " + c.from + " " + pendingText(pending)
	if at != c.from {
		head = "the clone " + c.from + " moved to " + at + " " + pendingText(pending) + "; it"
	}
	return head + " now fetches from and pushes to " + raw + "; `lore sync` sends its waiting commits" + tail
}

// pendingText is what a clone holds as set-url's line shows it:
// "(2 unpushed commit(s), 1 uncommitted change(s))".
func pendingText(pending []string) string {
	return "(" + strings.Join(pending, ", ") + ")"
}

// pendingIn is what the store clone at dir has to send, as set-url's line
// counts it: the commits of its store branch (the one checked out) that
// its remote's tracking branch lacks, as `git rev-list --count
// origin/<branch>..HEAD` counts them (D-562: a count over every branch
// took a branch made by hand in the clone for store work), then its
// uncommitted changes; the folder is left out (the line names it once).
func pendingIn(ctx context.Context, dir string) []string {
	var out []string
	branch := strings.TrimPrefix(cloneHeadRef(ctx, dir), "refs/heads/")
	spec := "HEAD"
	if branch != "" {
		track := "refs/remotes/" + gitbackend.RemoteName + "/" + branch
		if _, err := (gitRun{dir: dir, args: []string{"rev-parse", "--verify", "--quiet", track}}).output(ctx); err == nil {
			spec = track + "..HEAD"
		}
	}
	if n, err := (gitRun{dir: dir, args: []string{"rev-list", "--count", spec}}).output(ctx); err == nil {
		if c := strings.TrimSpace(string(n)); c != "0" && c != "" {
			out = append(out, c+" unpushed commit(s)")
		}
	}
	for _, p := range unpushedIn(ctx, cloneDirs{work: dir}) {
		out = append(out, strings.TrimSuffix(p, " in "+dir))
	}
	return out
}

// newCloneCarry is the carry of the clone c to the new address's clone
// record key and folder newDir (newOwn: that folder is a person's own
// clone): the record keys naming c's folder are dropped, and c moves into
// newDir unless either clone is a person's own.
func newCloneCarry(home string, c gitbackend.CloneFound, key, newDir string, newOwn bool) cloneCarry {
	carry := cloneCarry{from: c.Paths.Dir, key: key, own: c.Own}
	if !carry.own && !newOwn && newDir != carry.from {
		// O-14: the carried clone moves to the new address's folder, so
		// the folder serving the store carries its name (before, the old
		// name served forever, and the git user line named a folder set-url
		// then removed)
		carry.dest = newDir
	}
	for k, e := range gitbackend.ReadCloneRecord(home).Clones {
		if e.Path == carry.from && k != carry.key {
			carry.oldKeys = append(carry.oldKeys, k)
		}
	}
	return carry
}

// planCleanCarry plans the move of the store's fully pushed clone at an
// old address to the new one (D-515): the same carry as a clone holding
// work, so both of an org's stores end at the new address's folder. The
// new address's clone set-url's check just made holds only what the
// remote has, so it is dropped for the old one. Nothing to carry (no old
// clone, more than one, or one the new address's clone cannot give way
// to) leaves the old clones where they are; set-url then names each with
// the `lore store forget` that removes it (leftClonesLine).
func (a *App) planCleanCarry(ctx context.Context, id, raw, branch, newDir string, newOwn bool) (cloneCarry, bool, error) {
	r, err := gitbackend.RemoteOfRaw(raw)
	if err != nil {
		return cloneCarry{}, false, nil
	}
	left := leftClones(ctx, a.opts.Home, id, raw)
	if len(left) != 1 {
		return cloneCarry{}, false, nil // none, or several: which one serves is the person's call
	}
	carry := newCloneCarry(a.opts.Home, left[0], gitbackend.CloneKey(r, branch), newDir, newOwn)
	if newDir == carry.from || !exists(filepath.Join(newDir, ".git")) {
		return carry, true, nil
	}
	if newOwn || len(unpushedIn(ctx, cloneDirs{git: newDir, work: newDir})) > 0 {
		return cloneCarry{}, false, nil // the new address's clone is the person's or holds work: it serves
	}
	carry.drop = newDir
	return carry, true, nil
}

// dropReplacedClone removes the new address's own clone (fully pushed when
// planned) so the carried clone can take its folder; "" when removed,
// else the "; …" tail saying why it was left.
func dropReplacedClone(ctx context.Context, drop string) string {
	dctx, dunlock, err := gitbackend.LockClone(ctx, drop)
	if err != nil {
		return "; the clone " + drop + " was left (" + err.Error() + "): remove it once nothing uses it"
	}
	mine := unpushedIn(dctx, cloneDirs{git: drop, work: drop})
	dunlock()
	if len(mine) > 0 {
		return "; the clone " + drop + " was left: it now holds " + strings.Join(mine, ", ")
	}
	if err := os.RemoveAll(drop); err != nil {
		return "; the unused clone " + drop + " could not be removed (" + err.Error() + ")"
	}
	return ""
}

// moveClone renames the clone folder from to dest (its parent created).
// The clone lock lives inside the folder, so it is taken and released
// first (no lore writer is inside it now) rather than held across the
// rename, which would carry a held lock file to dest.
func moveClone(ctx context.Context, from, dest string) error {
	if err := os.MkdirAll(filepath.Dir(dest), 0o750); err != nil {
		return err
	}
	_, unlock, err := gitbackend.LockClone(ctx, from)
	if err != nil {
		return err
	}
	unlock()
	return os.Rename(from, dest)
}

// strandedList names each stranded clone and what it holds.
func strandedList(s []strandedClone) string {
	parts := make([]string, 0, len(s))
	for _, c := range s {
		parts = append(parts, c.found.Paths.Dir+" "+pendingText(c.pending))
	}
	return strings.Join(parts, "; ")
}

// leftClones lists the whole clones on this machine of store id whose
// origin is not the address addr and that hold no unpushed work: after
// set-url nothing reads or writes them, and nothing said so (H-12: each
// person kept ~/.lore/stores/<org>/<old-name> beside the new clone). It
// only reads; set-url names them with the command that removes each.
func leftClones(ctx context.Context, home, id, addr string) []gitbackend.CloneFound {
	found, err := gitbackend.ScanClones(home)
	if err != nil {
		return nil
	}
	var out []gitbackend.CloneFound
	for _, c := range found {
		if c.State != gitbackend.CloneWhole || c.Origin == "" || storeaddr.Same(c.Origin, addr) {
			continue
		}
		if m, merr := rowfs.ReadMarker(c.Paths.Dir); merr != nil || m.ID.String() != id {
			continue
		}
		if len(unpushedIn(ctx, cloneDirs{git: c.Paths.Dir, work: c.Paths.Dir})) == 0 {
			out = append(out, c)
		}
	}
	return out
}

// leftClonesLine is set-url's line for leftClones ("" when none): each
// old clone, that it is no longer used, and how to remove it.
func leftClonesLine(left []gitbackend.CloneFound, old []string) string {
	if len(left) == 0 {
		return ""
	}
	parts := make([]string, 0, len(left))
	for _, c := range left {
		u := c.Origin
		for _, o := range old {
			if storeaddr.Same(o, c.Origin) {
				u = o // the spelling the person knows
				break
			}
		}
		parts = append(parts, "the old clone "+c.Paths.Dir+" is no longer used (it holds nothing the store lacks); `lore store forget --url "+u+" --yes` removes it")
	}
	return strings.Join(parts, "; ")
}

// carriedCloneIdentity gives the carried clone at dir the git user of the
// store at raw (D-514): the same identity (storeSigner.cloneIdentity, the
// org's for an org store) the open of a new clone writes, so the folder
// serving the store after set-url commits as its org whichever clone ended
// there. A user.email the person set by hand stays (as on open); "" when
// nothing changed, else the line naming what was set.
func (a *App) carriedCloneIdentity(ctx context.Context, dir, raw string) string {
	id := storeIdentityFunc(a.opts).cloneIdentity(ctx, raw)
	if strings.TrimSpace(id.Email) == "" || strings.TrimSpace(id.Name) == "" {
		return ""
	}
	cur, err := gitbackend.LocalConfig(ctx, dir, gitbackend.ConfigUserEmail)
	if err != nil {
		return ""
	}
	if cur != "" {
		rec, _ := gitbackend.RecordedIdentity(dir) // unreadable: treated as the person's own
		if !strings.EqualFold(rec, cur) {
			return "" // the person's own identity for this clone stays
		}
		name, _ := gitbackend.LocalConfig(ctx, dir, gitbackend.ConfigUserName)
		if strings.EqualFold(cur, id.Email) && name == id.Name {
			return ""
		}
		id.Was = cur
	}
	if err := gitbackend.SetLocalIdentity(ctx, dir, id.Signature); err != nil {
		return "its git user was not set (" + err.Error() + "); `lore doctor --fix` sets it"
	}
	return strings.TrimPrefix(cloneIdentityLine(ctx, dir, id), CommandPrefix(ctx))
}
