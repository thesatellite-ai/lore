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
//	   └─ under the old clone's lock: origin → the new address; the clone
//	      record names it for the new address; the new address's own
//	      clone (fully pushed: everything in it is on the remote) is removed
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
	stranded := strandedClones(ctx, home, id, raw)
	if len(stranded) == 0 {
		return cloneCarry{}, false, nil
	}
	newDir, newOwn, err := gitbackend.CloneDirFor(home, r, branch)
	if err != nil {
		return cloneCarry{}, false, err
	}
	if len(stranded) > 1 {
		return cloneCarry{}, false, errs.New(errs.Refused, verb+": store "+id+" has unpushed work in more than one clone at an old address: "+strandedList(stranded)+"; set-url can carry only one to "+raw).
			WithHint("push all but one by hand (git -C <clone> push " + raw + " HEAD), then run this command again")
	}
	s := stranded[0]
	carry := cloneCarry{from: s.found.Paths.Dir, key: gitbackend.CloneKey(r, branch), own: s.found.Own, pending: s.pending}
	for k, e := range gitbackend.ReadCloneRecord(home).Clones {
		if e.Path == carry.from && k != carry.key {
			carry.oldKeys = append(carry.oldKeys, k)
		}
	}
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
func (a *App) applyCloneCarry(ctx context.Context, c cloneCarry, raw string) (string, error) {
	const verb = "store set-url"
	home := a.opts.Home
	origin := ""
	if c.drop != "" {
		origin = gitbackend.OriginAt(c.drop) // the spelling the open just wrote
	}
	if origin == "" {
		o, err := gitbackend.OriginOf(raw)
		if err != nil {
			return "", err
		}
		origin = o
	}
	lctx, unlock, err := gitbackend.LockClone(ctx, c.from)
	if err != nil {
		return "", err
	}
	_, err = gitRun{dir: c.from, args: []string{"remote", "set-url", "--", gitbackend.RemoteName, origin}}.output(lctx)
	unlock()
	if err != nil {
		return "", errs.Wrap(errs.Internal, verb+": point the clone "+c.from+" at "+raw, err)
	}
	why := gitbackend.WhyMoved
	if c.own {
		why = gitbackend.WhyOwn
	}
	if err := gitbackend.RecordClone(home, c.key, gitbackend.CloneEntry{Path: c.from, Why: why}, time.Now()); err != nil {
		return "", err
	}
	for _, k := range c.oldKeys {
		if err := gitbackend.ForgetCloneRecord(home, k); err != nil {
			return "", err
		}
	}
	line := "the clone " + c.from + " (" + strings.Join(c.pending, ", ") + ") now fetches from and pushes to " + raw + "; `lore sync` sends its waiting commits"
	if c.drop == "" {
		return line, nil
	}
	// The new address's clone was fully pushed when planned; check again
	// under its lock (a writer may have committed since) before removing it.
	dctx, dunlock, err := gitbackend.LockClone(ctx, c.drop)
	if err != nil {
		return line + "; the clone " + c.drop + " was left (" + err.Error() + "): remove it once nothing uses it", nil
	}
	mine := unpushedIn(dctx, cloneDirs{git: c.drop, work: c.drop})
	dunlock()
	if len(mine) > 0 {
		return line + "; the clone " + c.drop + " was left: it now holds " + strings.Join(mine, ", "), nil
	}
	if err := os.RemoveAll(c.drop); err != nil {
		return line + "; the unused clone " + c.drop + " could not be removed (" + err.Error() + ")", nil
	}
	return line, nil
}

// strandedList names each stranded clone and what it holds.
func strandedList(s []strandedClone) string {
	parts := make([]string, 0, len(s))
	for _, c := range s {
		parts = append(parts, c.found.Paths.Dir+" ("+strings.Join(c.pending, ", ")+")")
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
